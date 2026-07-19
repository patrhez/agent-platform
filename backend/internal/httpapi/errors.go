package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/store"
	"go.uber.org/zap"
)

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(
	ctx context.Context,
	logger logging.Logger,
	responseWriter http.ResponseWriter,
	status int,
	value any,
) {
	responseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	responseWriter.WriteHeader(status)
	if err := json.NewEncoder(responseWriter).Encode(value); err != nil {
		logger.Error(ctx, "encode HTTP response failed", zap.Error(err))
	}
}

func writeError(
	ctx context.Context,
	logger logging.Logger,
	responseWriter http.ResponseWriter,
	status int,
	code string,
	err error,
) {
	writeJSON(ctx, logger, responseWriter, status, errorResponse{Code: code, Message: err.Error()})
}

func writeStoreError(
	ctx context.Context,
	logger logging.Logger,
	responseWriter http.ResponseWriter,
	err error,
) {
	switch {
	case errors.Is(err, store.ErrConversationNotFound),
		errors.Is(err, store.ErrRunNotFound),
		errors.Is(err, store.ErrArtifactNotFound):
		writeError(ctx, logger, responseWriter, http.StatusNotFound, "not_found", err)
	case errors.Is(err, store.ErrUnauthorized):
		writeError(ctx, logger, responseWriter, http.StatusForbidden, "forbidden", err)
	default:
		writeError(ctx, logger, responseWriter, http.StatusInternalServerError, "internal_error", err)
	}
}
