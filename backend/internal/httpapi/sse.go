package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"go.uber.org/zap"
)

const heartbeatInterval = 15 * time.Second

func (server *Server) streamRunEvents(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	runID := request.PathValue("runID")
	after, err := eventCursor(request)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusBadRequest, "invalid_event_cursor", err)
		return
	}
	if _, err := server.store.GetRunSnapshot(ctx, principal.UserID(), runID); err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	responseWriter.Header().Set("Content-Type", "text/event-stream")
	responseWriter.Header().Set("Cache-Control", "no-cache")
	responseWriter.Header().Set("X-Accel-Buffering", "no")
	responseWriter.WriteHeader(http.StatusOK)
	flusher, _ := responseWriter.(http.Flusher)
	flush(flusher)

	subscription, err := server.notifier.Subscribe(ctx, runID)
	if err != nil {
		server.logger.Warn(ctx, "subscribe Run event hint failed", zap.String("run_id", runID), zap.Error(err))
		subscription = nil
	}
	if subscription != nil {
		defer func() {
			if err := subscription.Close(); err != nil {
				server.logger.Warn(ctx, "close Run event subscription failed", zap.String("run_id", runID), zap.Error(err))
			}
		}()
	}
	hints := notifications(subscription)

	cursor, terminal, err := server.replayEvents(responseWriter, request, principal.UserID(), runID, after, flusher)
	if err != nil || terminal {
		return
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(responseWriter, ": heartbeat\n\n"); err != nil {
				return
			}
			flush(flusher)
		case _, open := <-hints:
			if !open {
				hints = nil
				continue
			}
		}
		cursor, terminal, err = server.replayEvents(responseWriter, request, principal.UserID(), runID, cursor, flusher)
		if err != nil || terminal {
			return
		}
	}
}

func (server *Server) replayEvents(
	responseWriter http.ResponseWriter,
	request *http.Request,
	userID string,
	runID string,
	after int64,
	flusher http.Flusher,
) (int64, bool, error) {
	events, err := server.store.ListRunEvents(request.Context(), userID, runID, after)
	if err != nil {
		server.logger.Error(
			request.Context(),
			"replay Run events failed",
			zap.String("run_id", runID),
			zap.Error(err),
		)
		return after, false, err
	}
	cursor := after
	for _, event := range events {
		if err := writeSSEEvent(responseWriter, event); err != nil {
			return cursor, false, err
		}
		cursor = event.Seq
		flush(flusher)
		if terminalEventType(event.Type) {
			return cursor, true, nil
		}
	}
	return cursor, false, nil
}

func eventCursor(request *http.Request) (int64, error) {
	value := request.Header.Get("Last-Event-ID")
	if value == "" {
		value = request.URL.Query().Get("after")
	}
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, fmt.Errorf("event cursor must be a non-negative integer")
	}
	return cursor, nil
}

func notifications(subscription interface{ Notifications() <-chan int64 }) <-chan int64 {
	if subscription == nil {
		return nil
	}
	return subscription.Notifications()
}

func writeSSEEvent(responseWriter http.ResponseWriter, event domain.RunEvent) error {
	payload := strings.TrimSpace(string(event.SafePayload))
	if payload == "" {
		payload = "{}"
	}
	_, err := fmt.Fprintf(responseWriter, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, payload)
	return err
}

func terminalEventType(eventType string) bool {
	return eventType == "run.completed" || eventType == "run.failed" || eventType == "run.cancelled"
}

func flush(flusher http.Flusher) {
	if flusher != nil {
		flusher.Flush()
	}
}
