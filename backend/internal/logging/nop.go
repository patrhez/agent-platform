package logging

import (
	"context"

	"go.uber.org/zap"
)

type nopLogger struct{}

// Nop returns a Logger that discards all log records.
func Nop() Logger {
	return nopLogger{}
}

func (nopLogger) Debug(context.Context, string, ...zap.Field) {}
func (nopLogger) Info(context.Context, string, ...zap.Field)  {}
func (nopLogger) Warn(context.Context, string, ...zap.Field)  {}
func (nopLogger) Error(context.Context, string, ...zap.Field) {}
func (nopLogger) With(...zap.Field) Logger                    { return nopLogger{} }
func (nopLogger) Sync() error                                 { return nil }
