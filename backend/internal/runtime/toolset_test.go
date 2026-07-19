package runtime

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeMCPExecutor struct {
	tools  map[string]toolMetadata
	closed bool
}

func (executor *fakeMCPExecutor) Call(context.Context, ToolRequest) (ToolResult, error) {
	return ToolResult{}, nil
}

func (executor *fakeMCPExecutor) Close() error {
	executor.closed = true
	return nil
}

func (executor *fakeMCPExecutor) AllowedTool(name string) (toolMetadata, bool) {
	metadata, found := executor.tools[name]
	return metadata, found
}

func TestConnectToolsetBuildsProviderCompatibleToolInfos(t *testing.T) {
	executor := &fakeMCPExecutor{tools: map[string]toolMetadata{
		"code.search": {
			Description: "search code",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo": map[string]any{"type": "string"},
				},
				"required": []any{"repo"},
			},
		},
		"workspace.list_repositories": {Description: "list repositories"},
	}}
	toolset, err := connectToolset(
		context.Background(),
		[]MCPServer{{
			Key:          "workspace",
			URL:          "http://example",
			AllowedTools: []string{"code.search", "workspace.list_repositories"},
		}},
		func(context.Context, MCPServer) (mcpExecutor, error) { return executor, nil },
	)
	if err != nil {
		t.Fatalf("connectToolset() error = %v", err)
	}
	if len(toolset.infos) != 2 {
		t.Fatalf("len(toolset.infos) = %d, want 2", len(toolset.infos))
	}
	for _, info := range toolset.infos {
		for _, character := range info.Name {
			valid := character >= 'a' && character <= 'z' ||
				character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || character == '_' || character == '-'
			if !valid {
				t.Errorf("Tool name %q contains unsupported character %q", info.Name, character)
			}
		}
	}
	search := toolset.infos[0]
	if search.Name != "code_search" || search.ParamsOneOf == nil {
		t.Fatalf("first ToolInfo = %q with schema %v", search.Name, search.ParamsOneOf)
	}
	converted, err := search.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema() error = %v", err)
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal converted schema: %v", err)
	}
	if string(encoded) == "" || !json.Valid(encoded) {
		t.Errorf("converted schema = %s", encoded)
	}
	if err := toolset.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !executor.closed {
		t.Error("toolset.Close() did not close the executor")
	}
}

func TestConnectToolsetRejectsMissingTool(t *testing.T) {
	executor := &fakeMCPExecutor{tools: map[string]toolMetadata{}}
	_, err := connectToolset(
		context.Background(),
		[]MCPServer{{Key: "workspace", URL: "http://example", AllowedTools: []string{"code.search"}}},
		func(context.Context, MCPServer) (mcpExecutor, error) { return executor, nil },
	)
	if err == nil {
		t.Fatal("connectToolset() error = nil, want missing Tool error")
	}
	if !executor.closed {
		t.Error("connectToolset() did not close the executor after failure")
	}
}

func TestModelToolNameReplacesUnsupportedCharacters(t *testing.T) {
	testCases := map[string]string{
		"code.search":                 "code_search",
		"file.read":                   "file_read",
		"workspace.list_repositories": "workspace_list_repositories",
		"my-tool":                     "my-tool",
		"weird name!":                 "weird_name_",
	}
	for input, want := range testCases {
		if got := ModelToolName(input); got != want {
			t.Errorf("ModelToolName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSafeArgumentsFiltersToAllowlistedKeys(t *testing.T) {
	raw := json.RawMessage(`{"repo":"agent-platform","query":"stream","apiKey":"secret"}`)
	got := safeArguments(raw, []string{"repo", "query"})
	if got["repo"] != "agent-platform" || got["query"] != "stream" {
		t.Errorf("safeArguments() = %#v, want allowlisted keys", got)
	}
	if _, leaked := got["apiKey"]; leaked {
		t.Error("safeArguments() leaked a non-allowlisted key")
	}
	if got := safeArguments(raw, nil); len(got) != 0 {
		t.Errorf("safeArguments() with no allowlist = %#v, want empty", got)
	}
}
