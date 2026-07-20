package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

type recordingMCPExecutor struct {
	lastRequest ToolRequest
	result      ToolResult
}

func (executor *recordingMCPExecutor) Call(_ context.Context, request ToolRequest) (ToolResult, error) {
	executor.lastRequest = request
	return executor.result, nil
}

func (executor *recordingMCPExecutor) Close() error { return nil }

func (executor *recordingMCPExecutor) AllowedTool(name string) (toolMetadata, bool) {
	if name != "file.read" {
		return toolMetadata{}, false
	}
	return toolMetadata{
		Description: "read a file",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repo": map[string]any{"type": "string"},
				"path": map[string]any{"type": "string"},
			},
		},
	}, true
}

func TestMCPInvokableToolEmitsStartAndFinish(t *testing.T) {
	t.Parallel()

	executor := &recordingMCPExecutor{result: ToolResult{Content: "ok", Summary: "ok"}}
	toolset, err := connectToolset(
		context.Background(),
		[]MCPServer{{
			Key:          "workspace",
			URL:          "http://example",
			AllowedTools: []string{"file.read"},
			SafeArguments: map[string][]string{
				"file.read": {"repo", "path"},
			},
		}},
		func(context.Context, MCPServer) (mcpExecutor, error) { return executor, nil },
	)
	if err != nil {
		t.Fatalf("connectToolset() error = %v", err)
	}
	defer toolset.Close()

	var started []ToolRequest
	var finished []ToolRequest
	var finishedResults []ToolResult
	tools, err := buildInvokableTools(toolset, toolEmitHooks{
		RunID: "run-1",
		OnStart: func(request ToolRequest) error {
			started = append(started, request)
			return nil
		},
		OnFinish: func(request ToolRequest, result ToolResult) error {
			finished = append(finished, request)
			finishedResults = append(finishedResults, result)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("buildInvokableTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool type = %T, want InvokableTool", tools[0])
	}

	info, err := invokable.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "file_read" {
		t.Fatalf("Info().Name = %q, want file_read", info.Name)
	}

	arguments, err := json.Marshal(map[string]any{
		"repo":   "demo",
		"path":   "README.md",
		"secret": "redacted-value",
	})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	content, err := invokable.InvokableRun(context.Background(), string(arguments))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if content != "ok" {
		t.Fatalf("InvokableRun() = %q, want ok", content)
	}
	if len(started) != 1 || len(finished) != 1 || len(finishedResults) != 1 {
		t.Fatalf("hooks started=%d finished=%d results=%d", len(started), len(finished), len(finishedResults))
	}
	if started[0].Name != "file.read" || started[0].ServerKey != "workspace" {
		t.Fatalf("started request = %#v", started[0])
	}
	if started[0].ID == "" {
		t.Fatal("started request ID is empty")
	}
	if started[0].SafeArguments["repo"] != "demo" || started[0].SafeArguments["path"] != "README.md" {
		t.Fatalf("SafeArguments = %#v", started[0].SafeArguments)
	}
	if _, found := started[0].SafeArguments["secret"]; found {
		t.Fatalf("secret leaked into SafeArguments: %#v", started[0].SafeArguments)
	}
	if finished[0].ID != started[0].ID || finishedResults[0].Content != "ok" {
		t.Fatalf("finished request/result mismatch: %#v %#v", finished[0], finishedResults[0])
	}
	if executor.lastRequest.IdempotencyKey != "run-1:"+started[0].ID {
		t.Fatalf("IdempotencyKey = %q", executor.lastRequest.IdempotencyKey)
	}
}

func TestMCPInvokableToolKeepsProviderCallIDOutOfDurableID(t *testing.T) {
	t.Parallel()

	executor := &recordingMCPExecutor{result: ToolResult{Content: "ok", Summary: "ok"}}
	toolset, err := connectToolset(
		context.Background(),
		[]MCPServer{{
			Key:           "workspace",
			URL:           "http://example",
			AllowedTools:  []string{"file.read"},
			SafeArguments: map[string][]string{"file.read": {"repo"}},
		}},
		func(context.Context, MCPServer) (mcpExecutor, error) { return executor, nil },
	)
	if err != nil {
		t.Fatalf("connectToolset() error = %v", err)
	}
	defer toolset.Close()

	tools, err := buildInvokableTools(toolset, toolEmitHooks{RunID: "run-1"})
	if err != nil {
		t.Fatalf("buildInvokableTools() error = %v", err)
	}
	mcpTool := tools[0].(*mcpInvokableTool)
	// Observed failing provider id from conversation 01KY05CMCC6WHP4JFQBW88KJR7.
	providerCallID := "call_00_yS2dOj70Q8Z2pG2di0q85025"
	if len(providerCallID) <= 26 {
		t.Fatalf("fixture providerCallID len = %d, want > 26", len(providerCallID))
	}
	request, err := mcpTool.buildRequest(providerCallID, `{"repo":"demo"}`)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if len(request.ID) != 26 {
		t.Fatalf("request.ID len = %d (%q), want ULID char(26)", len(request.ID), request.ID)
	}
	if request.CallID != providerCallID {
		t.Fatalf("request.CallID = %q, want %q", request.CallID, providerCallID)
	}
	if request.ToolMessageCallID() != providerCallID {
		t.Fatalf("ToolMessageCallID() = %q, want provider id", request.ToolMessageCallID())
	}
	if request.IdempotencyKey != "run-1:"+request.ID {
		t.Fatalf("IdempotencyKey = %q", request.IdempotencyKey)
	}
}

func TestMCPInvokableToolRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	executor := &recordingMCPExecutor{result: ToolResult{Content: "ok"}}
	toolset, err := connectToolset(
		context.Background(),
		[]MCPServer{{
			Key:           "workspace",
			URL:           "http://example",
			AllowedTools:  []string{"file.read"},
			SafeArguments: map[string][]string{"file.read": {"repo"}},
		}},
		func(context.Context, MCPServer) (mcpExecutor, error) { return executor, nil },
	)
	if err != nil {
		t.Fatalf("connectToolset() error = %v", err)
	}
	defer toolset.Close()

	tools, err := buildInvokableTools(toolset, toolEmitHooks{RunID: "run-1"})
	if err != nil {
		t.Fatalf("buildInvokableTools() error = %v", err)
	}
	invokable := tools[0].(tool.InvokableTool)
	if _, err := invokable.InvokableRun(context.Background(), "{"); err == nil {
		t.Fatal("InvokableRun() error = nil, want invalid JSON error")
	}
}
