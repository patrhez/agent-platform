package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"go.uber.org/zap"
)

const maxRequestBodyBytes = 1 << 20

type createConversationRequest struct {
	Title           string `json:"title"`
	Content         string `json:"content"`
	ClientMessageID string `json:"clientMessageId"`
}

type createConversationResponse struct {
	Conversation domain.Conversation `json:"conversation"`
	MessageID    string              `json:"messageId,omitempty"`
	RunID        string              `json:"runId,omitempty"`
	Status       string              `json:"status,omitempty"`
}

type createMessageRequest struct {
	Content         string `json:"content"`
	ClientMessageID string `json:"clientMessageId"`
	Mode            string `json:"mode"`
}

type createMessageResponse struct {
	MessageID string `json:"messageId"`
	RunID     string `json:"runId"`
	Status    string `json:"status"`
}

func (server *Server) listConversations(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	conversations, err := server.store.ListConversations(ctx, principal.UserID())
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	writeJSON(ctx, server.logger, responseWriter, http.StatusOK, map[string]any{"conversations": conversations})
}

func (server *Server) createConversation(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	body, ok := decodeJSON[createConversationRequest](server.logger, responseWriter, request)
	if !ok {
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		writeError(ctx, server.logger, responseWriter, http.StatusBadRequest, "invalid_content", fmt.Errorf("content is required"))
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = conversationTitle(content)
	}
	clientMessageID := strings.TrimSpace(body.ClientMessageID)
	if clientMessageID == "" {
		clientMessageID = ulid.Make().String()
	}
	if !validClientMessageID(clientMessageID) {
		writeError(
			ctx, server.logger, responseWriter, http.StatusBadRequest,
			"invalid_client_message_id", fmt.Errorf("clientMessageId must be a ULID"),
		)
		return
	}
	conversation, run, err := server.store.CreateConversationWithFirstMessage(
		ctx, principal.UserID(), title, clientMessageID, content, server.pins,
	)
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	writeJSON(ctx, server.logger, responseWriter, http.StatusAccepted, createConversationResponse{
		Conversation: conversation, MessageID: run.TriggerMessageID, RunID: run.ID, Status: string(run.Status),
	})
}

func (server *Server) getConversation(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	detail, err := server.store.GetConversation(ctx, principal.UserID(), request.PathValue("conversationID"))
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	writeJSON(ctx, server.logger, responseWriter, http.StatusOK, detail)
}

func (server *Server) deleteConversation(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	if err := server.store.DeleteConversation(ctx, principal.UserID(), request.PathValue("conversationID")); err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

func (server *Server) createMessage(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	body, ok := decodeJSON[createMessageRequest](server.logger, responseWriter, request)
	if !ok {
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		writeError(ctx, server.logger, responseWriter, http.StatusBadRequest, "invalid_content", fmt.Errorf("content is required"))
		return
	}
	clientMessageID := strings.TrimSpace(body.ClientMessageID)
	if !validClientMessageID(clientMessageID) {
		writeError(
			ctx, server.logger, responseWriter, http.StatusBadRequest,
			"invalid_client_message_id", fmt.Errorf("clientMessageId must be a ULID"),
		)
		return
	}
	mode, err := domain.ParseFollowUpMode(body.Mode)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusBadRequest, "invalid_mode", err)
		return
	}
	run, events, err := server.store.CreateUserMessageAndRunForUser(
		ctx, principal.UserID(), request.PathValue("conversationID"), clientMessageID, content, mode, server.pins,
	)
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	server.publishRunEvents(ctx, events)
	writeJSON(ctx, server.logger, responseWriter, http.StatusAccepted, createMessageResponse{
		MessageID: run.TriggerMessageID, RunID: run.ID, Status: string(run.Status),
	})
}

func (server *Server) cancelActiveRuns(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	events, err := server.store.CancelActiveRuns(ctx, principal.UserID(), request.PathValue("conversationID"))
	if err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	server.publishRunEvents(ctx, events)
	writeJSON(ctx, server.logger, responseWriter, http.StatusOK, map[string]any{"cancelled": true})
}

func (server *Server) publishRunEvents(ctx context.Context, events []domain.RunEvent) {
	for _, event := range events {
		if err := server.notifier.Publish(ctx, event.RunID, event.Seq); err != nil {
			server.logger.Warn(ctx, "publish Run event hint failed", zap.String("run_id", event.RunID), zap.Error(err))
		}
	}
}

func validClientMessageID(value string) bool {
	_, err := ulid.ParseStrict(value)
	return err == nil
}

func decodeJSON[T any](
	logger logging.Logger,
	responseWriter http.ResponseWriter,
	request *http.Request,
) (T, bool) {
	var value T
	request.Body = http.MaxBytesReader(responseWriter, request.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		writeError(request.Context(), logger, responseWriter, http.StatusBadRequest, "invalid_json", err)
		return value, false
	}
	return value, true
}

func conversationTitle(content string) string {
	const maxTitleRunes = 48
	if content == "" {
		return "New conversation"
	}
	if utf8.RuneCountInString(content) <= maxTitleRunes {
		return content
	}
	return string([]rune(content)[:maxTitleRunes]) + "…"
}
