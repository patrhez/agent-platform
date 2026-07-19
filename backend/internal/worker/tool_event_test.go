package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/runtime"
)

func TestSafeToolArgumentsUsesToolSpecificWhitelist(t *testing.T) {
	testCases := []struct {
		name string
		tool string
		raw  string
		want map[string]any
	}{
		{
			name: "code search",
			tool: "code.search",
			raw:  `{"repo":"agent-platform","query":"streamRunEvents","pathPrefix":"backend","glob":"*.go","maxResults":5,"workspaceRoot":"/Users/private"}`,
			want: map[string]any{"repo": "agent-platform", "query": "streamRunEvents", "pathPrefix": "backend", "glob": "*.go", "maxResults": float64(5)},
		},
		{
			name: "file read",
			tool: "file.read",
			raw:  `{"repo":"agent-platform","path":"backend/main.go","startLine":1,"endLine":30,"apiKey":"secret"}`,
			want: map[string]any{"repo": "agent-platform", "path": "backend/main.go", "startLine": float64(1), "endLine": float64(30)},
		},
		{name: "repository list", tool: "workspace.list_repositories", raw: `{"workspaceRoot":"/private"}`, want: map[string]any{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := safeToolArguments(testCase.tool, json.RawMessage(testCase.raw))
			if !sameJSONMap(got, testCase.want) {
				t.Errorf("safeToolArguments() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestToolEventPayloadIncludesSafeCompletionMetadata(t *testing.T) {
	startedAt := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(34 * time.Millisecond)
	payload, err := toolEventPayload(runtime.RuntimeEvent{
		Tool: &runtime.ToolRequest{
			ID:        "tool-call-id",
			Name:      "code.search",
			Arguments: json.RawMessage(`{"repo":"agent-platform","query":"stream","workspaceRoot":"/private"}`),
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

func sameJSONMap(left map[string]any, right map[string]any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}
