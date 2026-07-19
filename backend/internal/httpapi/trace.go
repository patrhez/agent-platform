package httpapi

import (
	"net/http"

	"github.com/patrhez/agent-platform/backend/internal/logging"
)

func withTraceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		traceID := request.Header.Get(logging.HeaderTraceID)
		ctx := request.Context()
		if traceID == "" {
			ctx, traceID = logging.EnsureTraceID(ctx)
		} else {
			ctx = logging.WithTraceID(ctx, traceID)
		}
		responseWriter.Header().Set(logging.HeaderTraceID, traceID)
		next.ServeHTTP(responseWriter, request.WithContext(ctx))
	})
}
