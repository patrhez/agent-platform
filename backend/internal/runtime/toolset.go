package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// toolBinding routes one model-visible Tool name to its MCP server and executor.
type toolBinding struct {
	serverKey string
	mcpName   string
	executor  ToolExecutor
	safeKeys  []string
}

// mcpToolset holds the per-Run executors and model Tool schemas for every configured MCP server.
type mcpToolset struct {
	infos     []*schema.ToolInfo
	bindings  map[string]toolBinding
	executors []ToolExecutor
}

// executorFactory creates one connected executor per MCP server; tests replace it.
type executorFactory func(ctx context.Context, server MCPServer) (mcpExecutor, error)

// mcpExecutor is a ToolExecutor that also exposes server-declared Tool metadata.
type mcpExecutor interface {
	ToolExecutor
	AllowedTool(name string) (toolMetadata, bool)
}

// toolMetadata is the provider-facing description of one MCP Tool.
type toolMetadata struct {
	Description string
	InputSchema any
}

// connectToolset connects every configured MCP server and assembles the model Tool schemas.
func connectToolset(
	ctx context.Context,
	servers []MCPServer,
	connect executorFactory,
) (*mcpToolset, error) {
	toolset := &mcpToolset{bindings: map[string]toolBinding{}}
	for _, server := range servers {
		executor, err := connect(ctx, server)
		if err != nil {
			return nil, closeToolsetOnError(toolset, fmt.Errorf("connect MCP server %q: %w", server.Key, err))
		}
		toolset.executors = append(toolset.executors, executor)
		for _, mcpName := range server.AllowedTools {
			metadata, found := executor.AllowedTool(mcpName)
			if !found {
				return nil, closeToolsetOnError(toolset, fmt.Errorf("MCP server %q does not expose Tool %q", server.Key, mcpName))
			}
			info, err := modelToolInfo(mcpName, metadata)
			if err != nil {
				return nil, closeToolsetOnError(toolset, err)
			}
			toolset.infos = append(toolset.infos, info)
			toolset.bindings[info.Name] = toolBinding{
				serverKey: server.Key,
				mcpName:   mcpName,
				executor:  executor,
				safeKeys:  server.SafeArguments[mcpName],
			}
		}
	}
	return toolset, nil
}

// binding resolves the model-visible Tool name chosen by the LLM.
func (toolset *mcpToolset) binding(modelToolName string) (toolBinding, bool) {
	binding, found := toolset.bindings[modelToolName]
	return binding, found
}

// Close releases every MCP session opened for the Run.
func (toolset *mcpToolset) Close() error {
	var joined error
	for _, executor := range toolset.executors {
		if err := executor.Close(); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func closeToolsetOnError(toolset *mcpToolset, cause error) error {
	if closeErr := toolset.Close(); closeErr != nil {
		return errors.Join(cause, closeErr)
	}
	return cause
}

// modelToolInfo converts MCP Tool metadata into an Eino ToolInfo with a provider-safe name.
func modelToolInfo(mcpName string, metadata toolMetadata) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{
		Name: ModelToolName(mcpName),
		Desc: metadata.Description,
	}
	if metadata.InputSchema == nil {
		return info, nil
	}
	encoded, err := json.Marshal(metadata.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("encode input schema for Tool %s: %w", mcpName, err)
	}
	parsed := &jsonschema.Schema{}
	if err := json.Unmarshal(encoded, parsed); err != nil {
		return nil, fmt.Errorf("decode input schema for Tool %s: %w", mcpName, err)
	}
	info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(parsed)
	return info, nil
}

// newStreamableExecutor is the production executorFactory backed by Streamable HTTP MCP.
func newStreamableExecutor(ctx context.Context, server MCPServer) (mcpExecutor, error) {
	executor, err := NewWorkspaceExecutor(ctx, server.URL, server.AllowedTools)
	if err != nil {
		return nil, err
	}
	return &streamableExecutor{WorkspaceExecutor: executor}, nil
}

// streamableExecutor adapts WorkspaceExecutor's MCP metadata to the toolMetadata shape.
type streamableExecutor struct {
	*WorkspaceExecutor
}

func (executor *streamableExecutor) AllowedTool(name string) (toolMetadata, bool) {
	tool, found := executor.WorkspaceExecutor.AllowedTool(name)
	if !found {
		return toolMetadata{}, false
	}
	return toolMetadata{Description: tool.Description, InputSchema: tool.InputSchema}, true
}

// safeArguments filters raw Tool arguments down to the configured allowlist.
func safeArguments(raw json.RawMessage, safeKeys []string) map[string]any {
	values := map[string]any{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(safeKeys))
	for _, key := range safeKeys {
		if value, found := values[key]; found {
			result[key] = value
		}
	}
	return result
}
