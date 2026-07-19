// Package async provides panic recovery helpers for goroutines.
package async

import (
	"context"
	"fmt"
	"runtime/debug"

	"go.uber.org/zap"
)

// PanicLogger records recovered panics. Callers typically inject logging.Logger.
type PanicLogger interface {
	Error(ctx context.Context, msg string, fields ...zap.Field)
}

// Recover recovers a panic in the current goroutine and logs it.
// Callers should invoke it with defer at the start of every goroutine body:
//
//	go func() {
//		defer async.Recover(ctx, logger)
//		// work
//	}()
func Recover(ctx context.Context, logger PanicLogger) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if logger == nil {
		return
	}
	logger.Error(
		ctx,
		"goroutine panic recovered",
		zap.String("panic", fmt.Sprint(recovered)),
		zap.String("stack", string(debug.Stack())),
	)
}
