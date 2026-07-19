package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/runtime"
)

func TestToolEventPayloadIncludesSafeCompletionMetadata(t *testing.T) {
	startedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(34 * time.Millisecond)
	payload, err := toolEventPayload(runtime.RuntimeEvent{
		Tool: &runtime.ToolRequest{
			ID:            "tool-call-id",
			Name:          "code.search",
			Arguments:     json.RawMessage(`{"repo":"agent-platform","query":"stream","workspaceRoot":"/private"}`),
			SafeArguments: map[string]any{"repo": "agent-platform", "query": "stream"},
		},
		ToolResult: &runtime.ToolResult{Summary: "3 matches"},
	}, startedAt, finishedAt)
	if err != nil {
		t.Fatalf("toolEventPayload() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got["toolCallId"] != "tool-call-id" || got["tool"] != "code.search" ||
		got["status"] != "completed" || got["resultSummary"] != "3 matches" || got["durationMs"] != float64(34) {
		t.Errorf("payload metadata = %#v", got)
	}
	arguments, ok := got["arguments"].(map[string]any)
	if !ok || arguments["workspaceRoot"] != nil || arguments["repo"] != "agent-platform" {
		t.Errorf("payload arguments = %#v, want safe arguments", got["arguments"])
	}
}

func TestToolEventPayloadWithoutSafeArgumentsEmitsEmptyObject(t *testing.T) {
	payload, err := toolEventPayload(runtime.RuntimeEvent{
		Tool: &runtime.ToolRequest{
			ID:        "tool-call-id",
			Name:      "workspace.list_repositories",
			Arguments: json.RawMessage(`{"workspaceRoot":"/private"}`),
		},
	}, time.Now(), time.Now())
	if err != nil {
		t.Fatalf("toolEventPayload() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	arguments, ok := got["arguments"].(map[string]any)
	if !ok || len(arguments) != 0 {
		t.Errorf("payload arguments = %#v, want empty object", got["arguments"])
	}
}
