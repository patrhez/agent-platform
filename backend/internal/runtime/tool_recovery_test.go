package runtime

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestResumePendingToolCallsCompletesCheckpointedAssistantCalls(t *testing.T) {
	executor := &recordingMCPExecutor{result: ToolResult{Content: "file contents", Summary: "read file"}}
	toolset, err := connectToolset(
		context.Background(),
		[]MCPServer{{
			Key:           "workspace",
			URL:           "http://example",
			AllowedTools:  []string{"file.read"},
			SafeArguments: map[string][]string{"file.read": {"repo", "path"}},
		}},
		func(context.Context, MCPServer) (mcpExecutor, error) { return executor, nil },
	)
	if err != nil {
		t.Fatalf("connectToolset() error = %v", err)
	}
	defer func() { _ = toolset.Close() }()

	state := checkpointState{
		Iteration: 1,
		Messages: []*schema.Message{schema.AssistantMessage("", []schema.ToolCall{{
			ID: "provider-call-id",
			Function: schema.FunctionCall{
				Name:      "file_read",
				Arguments: `{"repo":"demo","path":"README.md"}`,
			},
		}})},
	}
	var events []RuntimeEvent
	ordinal, err := (&EinoRunner{}).resumePendingToolCalls(
		context.Background(),
		"run-1",
		"conversation-1",
		&state,
		3,
		toolset,
		func(event RuntimeEvent) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("resumePendingToolCalls() error = %v", err)
	}
	if ordinal != 4 {
		t.Fatalf("ordinal = %d, want 4", ordinal)
	}
	if len(events) != 2 || events[0].ToolResult != nil || events[1].ToolResult == nil {
		t.Fatalf("events = %#v, want tool start then completion", events)
	}
	if events[1].Checkpoint == nil || events[1].Checkpoint.Ordinal != 4 {
		t.Fatalf("completion checkpoint = %#v, want ordinal 4", events[1].Checkpoint)
	}
	if len(state.Messages) != 2 || state.Messages[1].Role != schema.Tool {
		t.Fatalf("state messages = %#v, want assistant and tool", state.Messages)
	}
	if state.Messages[1].ToolCallID != "provider-call-id" {
		t.Fatalf("ToolMessage call ID = %q, want provider-call-id", state.Messages[1].ToolCallID)
	}
	if events[0].Tool.CallID != "provider-call-id" || events[0].Tool.ID == "" {
		t.Fatalf("tool request = %#v, want durable ID and provider call ID", events[0].Tool)
	}
	if executor.lastRequest.IdempotencyKey != "run-1:"+events[0].Tool.ID {
		t.Fatalf("IdempotencyKey = %q", executor.lastRequest.IdempotencyKey)
	}
	if platformToolCallID(&state.Messages[0].ToolCalls[0]) != events[0].Tool.ID {
		t.Fatalf(
			"checkpointed platform tool ID = %q, want %q",
			platformToolCallID(&state.Messages[0].ToolCalls[0]),
			events[0].Tool.ID,
		)
	}
}
