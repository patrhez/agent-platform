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
	logger, output := testLogger(zapcore.InfoLevel)
	startedAt := time.Date(2026, time.July, 18, 8, 0, 0, 0, time.UTC)
	run := domain.Run{ID: "run-1", ConversationID: "conv-1"}
	request := &runtime.ToolRequest{
		ID:        "tool-call-1",
		Name:      "file.read",
		Arguments: json.RawMessage(`{"path":"private.go","secret":"do-not-log"}`),
	}

	writeToolBoundaryLog(context.Background(), logger, run, runtime.RuntimeEvent{Tool: request}, startedAt, startedAt)
	writeToolBoundaryLog(context.Background(), logger, run, runtime.RuntimeEvent{
		Tool:       request,
		ToolResult: &runtime.ToolResult{Summary: "private code result"},
	}, startedAt, startedAt.Add(34*time.Millisecond))

	contents := output.String()
	for _, expected := range []string{
		`"conversation_id":"conv-1"`, `"tool":"file.read"`, `"status":"running"`,
		`"status":"completed"`, `"run_id":"run-1"`,
	} {
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
	logger, output := testLogger(zapcore.InfoLevel)

	writeRunFailureLog(
		context.Background(),
		logger,
		domain.Run{ID: "run-1", ConversationID: "conv-1"},
		domain.RunStatusFailed,
		15*time.Millisecond,
		errors.New("private provider response"),
	)

	contents := output.String()
	for _, expected := range []string{
		`"conversation_id":"conv-1"`, `"run_id":"run-1"`, `"status":"failed"`, `"error_code":"runtime_error"`,
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("Run failure log = %q, want substring %q", contents, expected)
		}
	}
	if strings.Contains(contents, "private provider response") {
		t.Errorf("Run failure log contains cause details: %q", contents)
	}
}

func TestWriteRuntimeInputLogIncludesCorrelationAndPreview(t *testing.T) {
	logger, output := testLogger(zapcore.DebugLevel)
	run := domain.Run{ID: "run-1", ConversationID: "conv-1", Attempt: 2}
	writeRuntimeInputLog(context.Background(), logger, run, runtime.AgentInput{
		RunID:   "run-1",
		Attempt: 2,
		Messages: []runtime.ConversationMessage{
			{Role: "user", Content: "hello from the web"},
		},
	}, false)

	contents := output.String()
	for _, expected := range []string{
		`"msg":"runtime input"`, `"conversation_id":"conv-1"`, `"run_id":"run-1"`,
		`"message_count":1`, `"last_message_preview":"hello from the web"`,
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("runtime input log = %q, want substring %q", contents, expected)
		}
	}
}

func TestWritePersistedBoundaryLogExcludesRawArgumentsAndResultContent(t *testing.T) {
	logger, output := testLogger(zapcore.DebugLevel)
	run := domain.Run{ID: "run-1", ConversationID: "conv-1"}
	event := runtime.RuntimeEvent{
		StepNo:  3,
		Kind:    "tool",
		Summary: "Agent completed file.read: ok",
		Tool: &runtime.ToolRequest{
			Name:      "file.read",
			Arguments: json.RawMessage(`{"path":"secret.go","token":"do-not-log"}`),
			SafeArguments: map[string]any{
				"path": "secret.go",
			},
		},
		ToolResult: &runtime.ToolResult{
			Content: "FULL PRIVATE FILE BODY",
			Summary: "ok summary",
		},
		Checkpoint: &domain.Checkpoint{Ordinal: 4},
	}
	writePersistedBoundaryLog(context.Background(), logger, run, event, domain.ExecutionBoundary{
		StepNo: 3, Kind: "tool", SafeSummary: event.Summary, Checkpoint: event.Checkpoint,
	})

	contents := output.String()
	for _, expected := range []string{
		`"msg":"db persisted boundary"`, `"conversation_id":"conv-1"`, `"run_id":"run-1"`,
		`"tool":"file.read"`, `"checkpoint_ordinal":4`, `"result_summary_preview":"ok summary"`,
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("boundary log = %q, want substring %q", contents, expected)
		}
	}
	for _, rejected := range []string{"do-not-log", "FULL PRIVATE FILE BODY", `"token"`} {
		if strings.Contains(contents, rejected) {
			t.Errorf("boundary log contains rejected content %q: %q", rejected, contents)
		}
	}
}

func TestWriteCompleteRunLogIncludesPreview(t *testing.T) {
	logger, output := testLogger(zapcore.DebugLevel)
	writeCompleteRunLog(context.Background(), logger, domain.Run{ID: "run-1", ConversationID: "conv-1"}, domain.RunCompletion{
		Status:        domain.RunStatusSucceeded,
		AssistantText: "final answer",
	})
	contents := output.String()
	for _, expected := range []string{
		`"msg":"db complete run"`, `"conversation_id":"conv-1"`, `"run_id":"run-1"`,
		`"assistant_text_preview":"final answer"`, `"status":"succeeded"`,
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("complete run log = %q, want substring %q", contents, expected)
		}
	}
}

func testLogger(level zapcore.Level) (logging.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(&output),
		level,
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
