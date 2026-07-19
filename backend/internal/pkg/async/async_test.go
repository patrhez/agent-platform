package async

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

type captureLogger struct {
	mu      sync.Mutex
	message string
	logged  chan struct{}
}

func newCaptureLogger() *captureLogger {
	return &captureLogger{logged: make(chan struct{}, 1)}
}

func (logger *captureLogger) Error(_ context.Context, msg string, _ ...zap.Field) {
	logger.mu.Lock()
	logger.message = msg
	logger.mu.Unlock()
	select {
	case logger.logged <- struct{}{}:
	default:
	}
}

func TestRecoverLogsPanic(t *testing.T) {
	logger := newCaptureLogger()
	go func() {
		defer Recover(context.Background(), logger)
		panic("boom")
	}()

	select {
	case <-logger.logged:
	case <-time.After(time.Second):
		t.Fatal("Recover did not log panic")
	}

	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.message != "goroutine panic recovered" {
		t.Fatalf("message = %q, want goroutine panic recovered", logger.message)
	}
}
