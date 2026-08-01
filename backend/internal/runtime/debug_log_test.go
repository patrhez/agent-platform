package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestSummarizeSchemaMessages(t *testing.T) {
	summaries := summarizeSchemaMessages([]*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.UserMessage("what is paxos?"),
		{
			Role:    schema.Assistant,
			Content: "",
			ToolCalls: []schema.ToolCall{{
				Function: schema.FunctionCall{Name: "file_read"},
			}},
		},
	})
	if len(summaries) != 3 {
		t.Fatalf("summaries len = %d, want 3", len(summaries))
	}
	if summaries[1].Role != "user" || summaries[1].ContentPreview != "what is paxos?" {
		t.Fatalf("user summary = %#v", summaries[1])
	}
	if len(summaries[2].ToolCallNames) != 1 || summaries[2].ToolCallNames[0] != "file_read" {
		t.Fatalf("tool summary = %#v", summaries[2])
	}
}

func TestSummarizeBoundTools(t *testing.T) {
	toolset := &mcpToolset{
		infos: []*schema.ToolInfo{
			{Name: "file_read", Desc: "read a file from the workspace"},
			{Name: "code_search", Desc: "search", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"repo": {Type: "string"},
			})},
		},
		bindings: map[string]toolBinding{
			"file_read":   {serverKey: "workspace", mcpName: "file.read"},
			"code_search": {serverKey: "workspace", mcpName: "code.search"},
		},
	}
	summaries := summarizeBoundTools(toolset)
	if len(summaries) != 2 {
		t.Fatalf("summaries len = %d, want 2", len(summaries))
	}
	if summaries[0].ServerKey != "workspace" || summaries[0].MCPName != "file.read" || summaries[0].ModelName != "file_read" {
		t.Fatalf("first summary = %#v", summaries[0])
	}
	if summaries[0].HasInputSchema {
		t.Fatal("file_read should not report an input schema")
	}
	if !summaries[1].HasInputSchema {
		t.Fatal("code_search should report an input schema")
	}
}

func TestLogToolsetReadyAndEinoToolsBound(t *testing.T) {
	logger, output := testDebugLogger()
	runner := &EinoRunner{
		definition: Definition{Agent: AgentDefinition{
			ID: "issue-troubleshooter",
			MCPServers: []MCPServer{{
				Key:          "workspace",
				AllowedTools: []string{"file.read"},
			}},
		}},
		logger: logger,
	}
	toolset := &mcpToolset{
		infos: []*schema.ToolInfo{{Name: "file_read", Desc: "read a file"}},
		bindings: map[string]toolBinding{
			"file_read": {serverKey: "workspace", mcpName: "file.read"},
		},
	}
	runner.logToolsetReady(context.Background(), "run-1", toolset)

	session := &adkEmitSession{
		runID:   "run-1",
		attempt: 1,
		logger:  logger,
	}
	session.logEinoToolsBound(context.Background(), "issue-troubleshooter", []tool.BaseTool{
		&mcpInvokableTool{info: &schema.ToolInfo{Name: "file_read"}},
	})

	contents := output.String()
	for _, expected := range []string{
		`"msg":"mcp toolset ready"`, `"run_id":"run-1"`, `"server_key":"workspace"`,
		`"mcp_name":"file.read"`, `"model_name":"file_read"`,
		`"msg":"eino tools bound"`, `"agent_id":"issue-troubleshooter"`, `"model_tool_names":["file_read"]`,
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("setup logs = %q, want substring %q", contents, expected)
		}
	}
}

func TestLogEinoOutputExcludesRawToolArgumentsAndResultContent(t *testing.T) {
	logger, output := testDebugLogger()
	session := &adkEmitSession{
		ctx:    context.Background(),
		runID:  "run-1",
		logger: logger,
	}
	session.logEinoOutput(context.Background(), RuntimeEvent{
		StepNo:  2,
		Kind:    "tool",
		Summary: "Agent completed file.read: ok",
		Tool: &ToolRequest{
			Name:      "file.read",
			Arguments: json.RawMessage(`{"path":"secret.go","token":"do-not-log"}`),
			SafeArguments: map[string]any{
				"path": "secret.go",
			},
		},
		ToolResult: &ToolResult{
			Content: "FULL PRIVATE FILE BODY",
			Summary: "ok summary",
		},
	})

	contents := output.String()
	for _, expected := range []string{
		`"msg":"eino output"`, `"run_id":"run-1"`, `"tool":"file.read"`,
		`"result_summary_preview":"ok summary"`,
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("eino output log = %q, want substring %q", contents, expected)
		}
	}
	for _, rejected := range []string{"do-not-log", "FULL PRIVATE FILE BODY", `"token"`} {
		if strings.Contains(contents, rejected) {
			t.Errorf("eino output log contains rejected content %q: %q", rejected, contents)
		}
	}
}

func testDebugLogger() (logging.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(&output),
		zapcore.DebugLevel,
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
