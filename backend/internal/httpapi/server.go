// Package httpapi exposes the Agent Platform's browser-facing HTTP API.
package httpapi

import (
	"context"
	"net/http"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/events"
	"github.com/patrhez/agent-platform/backend/internal/logging"
)

// ConversationStore is the durable API dependency.
type ConversationStore interface {
	EnsureUser(context.Context, string, string, string) error
	CreateConversationWithFirstMessage(context.Context, string, string, string, string, domain.RunPins) (domain.Conversation, domain.Run, error)
	ListConversations(context.Context, string) ([]domain.Conversation, error)
	GetConversation(context.Context, string, string) (domain.ConversationDetail, error)
	DeleteConversation(context.Context, string, string) error
	CreateUserMessageAndRunForUser(context.Context, string, string, string, string, domain.FollowUpMode, domain.RunPins) (domain.Run, []domain.RunEvent, error)
	CancelActiveRuns(context.Context, string, string) ([]domain.RunEvent, error)
	GetRunSnapshot(context.Context, string, string) (domain.RunSnapshot, error)
	GetRunTrace(context.Context, string, string) (domain.RunTrace, error)
	ListRunEvents(context.Context, string, string, int64) ([]domain.RunEvent, error)
	GetArtifact(context.Context, string, string, string) (domain.Artifact, error)
	RequestRunCancellation(context.Context, string, string) (domain.RunSnapshot, error)
}

// Server owns request routing and its stateless API dependencies.
type Server struct {
	store    ConversationStore
	pins     domain.RunPins
	notifier events.Notifier
	logger   logging.Logger
}

// New returns the HTTP handler for the current API release.
func New(
	store ConversationStore,
	pins domain.RunPins,
	notifier events.Notifier,
	logger logging.Logger,
) http.Handler {
	if logger == nil {
		logger = logging.Nop()
	}
	server := &Server{store: store, pins: pins, notifier: notifier, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /api/v1/conversations", server.listConversations)
	mux.HandleFunc("POST /api/v1/conversations", server.createConversation)
	mux.HandleFunc("GET /api/v1/conversations/{conversationID}", server.getConversation)
	mux.HandleFunc("DELETE /api/v1/conversations/{conversationID}", server.deleteConversation)
	mux.HandleFunc("POST /api/v1/conversations/{conversationID}/messages", server.createMessage)
	mux.HandleFunc("POST /api/v1/conversations/{conversationID}/cancel-active", server.cancelActiveRuns)
	mux.HandleFunc("GET /api/v1/runs/{runID}", server.getRun)
	mux.HandleFunc("GET /api/v1/runs/{runID}/trace", server.getRunTrace)
	mux.HandleFunc("GET /api/v1/runs/{runID}/events", server.streamRunEvents)
	mux.HandleFunc("GET /api/v1/runs/{runID}/artifacts/{artifactID}", server.getArtifact)
	mux.HandleFunc("POST /api/v1/runs/{runID}/cancel", server.cancelRun)
	// Trace id must wrap access logging so http_request lines include trace_id.
	return withTraceID(accessLog(server.withDemoPrincipal(mux), logger))
}

func (server *Server) health(responseWriter http.ResponseWriter, _ *http.Request) {
	responseWriter.WriteHeader(http.StatusOK)
}
