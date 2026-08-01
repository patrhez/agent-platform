package runtime

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// toolEmitHooks are platform callbacks around one MCP tool invocation.
type toolEmitHooks struct {
	RunID    string
	OnStart  func(ToolRequest) error
	OnFinish func(ToolRequest, ToolResult) error
}

type mcpInvokableTool struct {
	info    *schema.ToolInfo
	binding toolBinding
	hooks   toolEmitHooks
}

// buildInvokableTools converts the MCP toolset into Eino InvokableTools for ADK ToolsNode.
func buildInvokableTools(toolset *mcpToolset, hooks toolEmitHooks) ([]tool.BaseTool, error) {
	if toolset == nil {
		return nil, fmt.Errorf("toolset is required")
	}
	if hooks.RunID == "" {
		return nil, fmt.Errorf("tool emit hooks require RunID")
	}
	tools := make([]tool.BaseTool, 0, len(toolset.infos))
	for _, info := range toolset.infos {
		if info == nil {
			return nil, fmt.Errorf("toolset contains a nil ToolInfo")
		}
		binding, found := toolset.binding(info.Name)
		if !found {
			return nil, fmt.Errorf("missing binding for Tool %q", info.Name)
		}
		tools = append(tools, &mcpInvokableTool{info: info, binding: binding, hooks: hooks})
	}
	return tools, nil
}

func (mcpTool *mcpInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return mcpTool.info, nil
}

func (mcpTool *mcpInvokableTool) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	_ ...tool.Option,
) (string, error) {
	request, err := mcpTool.buildRequest(compose.GetToolCallID(ctx), argumentsInJSON)
	if err != nil {
		return "", err
	}
	if mcpTool.hooks.OnStart != nil {
		if err := mcpTool.hooks.OnStart(request); err != nil {
			return "", err
		}
	}
	result, err := callWithRetry(ctx, mcpTool.binding.executor, request)
	if err != nil {
		return "", err
	}
	if mcpTool.hooks.OnFinish != nil {
		if err := mcpTool.hooks.OnFinish(request, result); err != nil {
			return "", err
		}
	}
	return result.Content, nil
}

func (mcpTool *mcpInvokableTool) buildRequest(callID string, argumentsInJSON string) (ToolRequest, error) {
	return buildToolRequest(mcpTool.hooks.RunID, mcpTool.binding, callID, argumentsInJSON, "")
}
