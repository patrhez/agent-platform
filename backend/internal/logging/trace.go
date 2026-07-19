package logging

import (
	"context"

	"github.com/oklog/ulid/v2"
)

type traceIDKey struct{}

// HeaderTraceID is the HTTP header used to accept or return a request trace id.
const HeaderTraceID = "X-Trace-Id"

// WithTraceID returns a child context carrying traceID for structured logs.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// TraceID returns the trace id stored in ctx, or an empty string when absent.
func TraceID(ctx context.Context) string {
	value, _ := ctx.Value(traceIDKey{}).(string)
	return value
}

// EnsureTraceID returns ctx unchanged when it already has a trace id.
// Otherwise it stores a newly generated ULID trace id.
func EnsureTraceID(ctx context.Context) (context.Context, string) {
	if traceID := TraceID(ctx); traceID != "" {
		return ctx, traceID
	}
	traceID := ulid.Make().String()
	return WithTraceID(ctx, traceID), traceID
}
