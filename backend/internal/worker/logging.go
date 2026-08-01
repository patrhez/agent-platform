package worker

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
	"go.uber.org/zap"
)

func writeToolBoundaryLog(
	ctx context.Context,
	logger logging.Logger,
	run domain.Run,
	event runtime.RuntimeEvent,
	startedAt time.Time,
	finishedAt time.Time,
) {
	if event.Tool == nil {
		return
	}
	if event.ToolResult == nil {
		logger.Info(
			ctx,
			"tool boundary",
			zap.String("conversation_id", run.ConversationID),
			zap.String("run_id", run.ID),
			zap.String("tool", event.Tool.Name),
			zap.String("status", "running"),
		)
		return
	}
	duration := finishedAt.Sub(startedAt).Round(time.Millisecond)
	if duration < 0 {
		duration = 0
	}
	logger.Info(
		ctx,
		"tool boundary",
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.String("tool", event.Tool.Name),
		zap.String("status", "completed"),
		zap.Duration("duration", duration),
	)
}

func writeRunFailureLog(
	ctx context.Context,
	logger logging.Logger,
	run domain.Run,
	status domain.RunStatus,
	duration time.Duration,
	cause error,
) {
	logger.Info(
		ctx,
		"run finished",
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.String("status", string(status)),
		zap.Duration("duration", duration.Round(time.Millisecond)),
		zap.String("error_code", runFailureCode(cause)),
	)
}

func runFailureCode(cause error) string {
	return runtime.ClassifyRunFailure(cause).Code
}

func writeRuntimeInputLog(
	ctx context.Context,
	logger logging.Logger,
	run domain.Run,
	input runtime.AgentInput,
	hasCheckpoint bool,
) {
	lastPreview := ""
	lastRole := ""
	lastLen := 0
	if count := len(input.Messages); count > 0 {
		last := input.Messages[count-1]
		lastRole = last.Role
		lastLen = utf8.RuneCountInString(last.Content)
		lastPreview = logging.Preview(last.Content, 0)
	}
	logger.Debug(
		ctx,
		"runtime input",
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.Int("attempt", run.Attempt),
		zap.Int("message_count", len(input.Messages)),
		zap.String("last_message_role", lastRole),
		zap.Int("last_message_len", lastLen),
		zap.String("last_message_preview", lastPreview),
		zap.Bool("has_checkpoint", hasCheckpoint),
	)
}

func writePersistedEventsLog(
	ctx context.Context,
	logger logging.Logger,
	run domain.Run,
	source string,
	events []domain.RunEvent,
) {
	if len(events) == 0 {
		return
	}
	types := make([]string, 0, len(events))
	seqs := make([]int64, 0, len(events))
	var textBytes int
	for _, event := range events {
		types = append(types, event.Type)
		seqs = append(seqs, event.Seq)
		if event.Type == "assistant.delta" {
			textBytes += assistantDeltaTextBytes(event.SafePayload)
		}
	}
	fields := []zap.Field{
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.String("source", source),
		zap.Int("event_count", len(events)),
		zap.Strings("event_types", types),
		zap.Int64s("event_seqs", seqs),
	}
	if textBytes > 0 {
		fields = append(fields, zap.Int("assistant_delta_bytes", textBytes))
	}
	if len(events) == 1 && events[0].Type != "assistant.delta" {
		fields = append(fields, zap.String("payload_preview", logging.Preview(string(events[0].SafePayload), 0)))
	}
	logger.Debug(ctx, "db persisted events", fields...)
}

func writePersistedBoundaryLog(
	ctx context.Context,
	logger logging.Logger,
	run domain.Run,
	event runtime.RuntimeEvent,
	boundary domain.ExecutionBoundary,
) {
	fields := []zap.Field{
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.Int("step_no", boundary.StepNo),
		zap.String("kind", boundary.Kind),
		zap.String("summary", boundary.SafeSummary),
	}
	if boundary.Checkpoint != nil {
		fields = append(fields, zap.Int("checkpoint_ordinal", boundary.Checkpoint.Ordinal))
	}
	if event.Tool != nil {
		status := "running"
		if event.ToolResult != nil {
			status = "completed"
		}
		fields = append(fields,
			zap.String("tool", event.Tool.Name),
			zap.String("tool_status", status),
		)
		if len(event.Tool.SafeArguments) > 0 {
			fields = append(fields, zap.Any("safe_arguments", event.Tool.SafeArguments))
		}
		if event.ToolResult != nil {
			fields = append(fields,
				zap.Int("result_summary_len", utf8.RuneCountInString(event.ToolResult.Summary)),
				zap.String("result_summary_preview", logging.Preview(event.ToolResult.Summary, 0)),
			)
		}
	}
	if event.Final != "" {
		fields = append(fields,
			zap.Int("final_len", utf8.RuneCountInString(event.Final)),
			zap.String("final_preview", logging.Preview(event.Final, 0)),
		)
	}
	logger.Debug(ctx, "db persisted boundary", fields...)
}

func writeCompleteRunLog(
	ctx context.Context,
	logger logging.Logger,
	run domain.Run,
	completion domain.RunCompletion,
) {
	fields := []zap.Field{
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.String("status", string(completion.Status)),
		zap.Int("assistant_text_len", utf8.RuneCountInString(completion.AssistantText)),
		zap.String("assistant_text_preview", logging.Preview(completion.AssistantText, 0)),
	}
	if completion.ErrorCode != "" {
		fields = append(fields, zap.String("error_code", completion.ErrorCode))
	}
	logger.Debug(ctx, "db complete run", fields...)
}

func assistantDeltaTextBytes(payload json.RawMessage) int {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return 0
	}
	return len(body.Text)
}
