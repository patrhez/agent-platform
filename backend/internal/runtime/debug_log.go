package runtime

import (
	"context"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"go.uber.org/zap"
)

type boundToolDebugSummary struct {
	ServerKey          string `json:"server_key"`
	MCPName            string `json:"mcp_name"`
	ModelName          string `json:"model_name"`
	DescriptionPreview string `json:"description_preview,omitempty"`
	HasInputSchema     bool   `json:"has_input_schema"`
}

type messageDebugSummary struct {
	Role           string   `json:"role"`
	ContentLen     int      `json:"content_len"`
	ContentPreview string   `json:"content_preview,omitempty"`
	ToolCallNames  []string `json:"tool_call_names,omitempty"`
}

func summarizeSchemaMessages(messages []*schema.Message) []messageDebugSummary {
	summaries := make([]messageDebugSummary, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		summary := messageDebugSummary{
			Role:           string(message.Role),
			ContentLen:     utf8.RuneCountInString(message.Content),
			ContentPreview: logging.Preview(message.Content, 0),
		}
		if len(message.ToolCalls) > 0 {
			names := make([]string, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				names = append(names, call.Function.Name)
			}
			summary.ToolCallNames = names
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func summarizeBoundTools(toolset *mcpToolset) []boundToolDebugSummary {
	if toolset == nil {
		return nil
	}
	summaries := make([]boundToolDebugSummary, 0, len(toolset.infos))
	for _, info := range toolset.infos {
		if info == nil {
			continue
		}
		summary := boundToolDebugSummary{
			ModelName:          info.Name,
			DescriptionPreview: logging.Preview(info.Desc, 160),
			HasInputSchema:     info.ParamsOneOf != nil,
		}
		if binding, found := toolset.binding(info.Name); found {
			summary.ServerKey = binding.serverKey
			summary.MCPName = binding.mcpName
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func (runner *EinoRunner) logToolsetReady(ctx context.Context, runID string, toolset *mcpToolset) {
	if runner == nil || runner.logger == nil || toolset == nil {
		return
	}
	servers := make([]string, 0, len(runner.definition.Agent.MCPServers))
	for _, server := range runner.definition.Agent.MCPServers {
		servers = append(servers, server.Key)
	}
	runner.logger.Debug(
		ctx,
		"mcp toolset ready",
		zap.String("run_id", runID),
		zap.Strings("server_keys", servers),
		zap.Int("server_count", len(servers)),
		zap.Int("tool_count", len(toolset.infos)),
		zap.Any("tools", summarizeBoundTools(toolset)),
	)
}

func (session *adkEmitSession) logEinoToolsBound(ctx context.Context, agentID string, tools []tool.BaseTool) {
	if session == nil || session.logger == nil {
		return
	}
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		names = append(names, info.Name)
	}
	session.logger.Debug(
		ctx,
		"eino tools bound",
		zap.String("run_id", session.runID),
		zap.Int("attempt", session.attempt),
		zap.String("agent_id", agentID),
		zap.Int("tool_count", len(names)),
		zap.Strings("model_tool_names", names),
	)
}

func (session *adkEmitSession) logEinoInput(ctx context.Context, resumed bool) {
	if session.logger == nil || session.state == nil {
		return
	}
	session.logger.Debug(
		ctx,
		"eino input",
		zap.String("run_id", session.runID),
		zap.Int("attempt", session.attempt),
		zap.Bool("resumed", resumed),
		zap.Int("message_count", len(session.state.Messages)),
		zap.Int("iteration", session.state.Iteration),
		zap.Any("messages", summarizeSchemaMessages(session.state.Messages)),
	)
}

func (session *adkEmitSession) logEinoOutput(ctx context.Context, event RuntimeEvent) {
	if session.logger == nil {
		return
	}
	fields := []zap.Field{
		zap.String("run_id", session.runID),
		zap.Int("attempt", session.attempt),
		zap.Int("step_no", event.StepNo),
		zap.String("kind", event.Kind),
		zap.String("summary", event.Summary),
	}
	if event.Final != "" {
		fields = append(fields,
			zap.Int("final_len", utf8.RuneCountInString(event.Final)),
			zap.String("final_preview", logging.Preview(event.Final, 0)),
		)
	}
	if event.Tool != nil {
		fields = append(fields, zap.String("tool", event.Tool.Name))
		if len(event.Tool.SafeArguments) > 0 {
			fields = append(fields, zap.Any("safe_arguments", event.Tool.SafeArguments))
		}
		if event.ToolResult != nil {
			fields = append(fields,
				zap.Int("result_summary_len", utf8.RuneCountInString(event.ToolResult.Summary)),
				zap.String("result_summary_preview", logging.Preview(event.ToolResult.Summary, 0)),
				zap.Int("result_content_len", utf8.RuneCountInString(event.ToolResult.Content)),
			)
		}
	}
	if event.Checkpoint != nil {
		fields = append(fields, zap.Int("checkpoint_ordinal", event.Checkpoint.Ordinal))
	}
	session.logger.Debug(ctx, "eino output", fields...)
}
