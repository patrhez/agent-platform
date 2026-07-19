package logging

import (
	"context"

	"go.uber.org/zap"
)

// Logger is the injectable, context-aware logging API used by service components.
// Zap does not extract fields from context.Context, so implementations must attach
// request-scoped values such as trace_id before forwarding to the underlying zap logger.
type Logger interface {
	Debug(ctx context.Context, msg string, fields ...zap.Field)
	Info(ctx context.Context, msg string, fields ...zap.Field)
	Warn(ctx context.Context, msg string, fields ...zap.Field)
	Error(ctx context.Context, msg string, fields ...zap.Field)
	With(fields ...zap.Field) Logger
	Sync() error
}
