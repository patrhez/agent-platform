package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestWriteToolBoundaryLogExcludesArgumentsAndResults(t *testing.T) {
	logger, output := testLogger()
	startedAt := time.Date(2026, time.July, 18, 8, 0, 0, 0, time.UTC)
	request := &runtime.ToolRequest{
		ID:        "tool-call-1",
		Name:      "file.read",
		Arguments: json.RawMessage(`{"path":"private.go","secret":"do-not-log"}`),
	}

	writeToolBoundaryLog(context.Background(), logger, "run-1", runtime.RuntimeEvent{Tool: request}, startedAt, startedAt)
	writeToolBoundaryLog(context.Background(), logger, "run-1", runtime.RuntimeEvent{
		Tool:       request,
		ToolResult: &runtime.ToolResult{Summary: "private code result"},
	}, startedAt, startedAt.Add(34*time.Millisecond))

	contents := output.String()
	for _, expected := range []string{`"tool":"file.read"`, `"status":"running"`, `"status":"completed"`, `"run_id":"run-1"`} {
		if !strings.Contains(contents, expected) {
			t.Errorf("Tool log = %q, want substring %q", contents, expected)
		}
	}
	for _, rejected := range []string{"private.go", "do-not-log", "private code result"} {
		if strings.Contains(contents, rejected) {
			t.Errorf("Tool log contains rejected content %q: %q", rejected, contents)
		}
	}
}

func TestWriteRunFailureLogExcludesCauseDetails(t *testing.T) {
	logger, output := testLogger()

	writeRunFailureLog(
		context.Background(),
		logger,
		"run-1",
		domain.RunStatusFailed,
		15*time.Millisecond,
		errors.New("private provider response"),
	)

	contents := output.String()
	for _, expected := range []string{`"run_id":"run-1"`, `"status":"failed"`, `"error_code":"runtime_error"`} {
		if !strings.Contains(contents, expected) {
			t.Errorf("Run failure log = %q, want substring %q", contents, expected)
		}
	}
	if strings.Contains(contents, "private provider response") {
		t.Errorf("Run failure log contains cause details: %q", contents)
	}
}

func testLogger() (logging.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(&output),
		zapcore.InfoLevel,
	)
	return &bufferLogger{base: zap.New(core)}, &output
}

type bufferLogger struct {
	base *zap.Logger
}

func (logger *bufferLogger) Debug(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Debug(msg, fields...)
}
func (logger *bufferLogger) Info(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Info(msg, fields...)
}
func (logger *bufferLogger) Warn(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Warn(msg, fields...)
}
func (logger *bufferLogger) Error(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Error(msg, fields...)
}
func (logger *bufferLogger) With(fields ...zap.Field) logging.Logger {
	return &bufferLogger{base: logger.base.With(fields...)}
}
func (logger *bufferLogger) Sync() error { return logger.base.Sync() }
