package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workspaceToolTimeout = 30 * time.Second
	maximumResultSummary = 500
)

// WorkspaceExecutor invokes the fixed read-only Tool allowlist over Streamable HTTP.
type WorkspaceExecutor struct {
	session *mcp.ClientSession
	allowed map[string]struct{}
}

// NewWorkspaceExecutor connects to the Workspace MCP server and validates its Tool list.
func NewWorkspaceExecutor(
	ctx context.Context,
	endpoint string,
	allowedTools []string,
) (*WorkspaceExecutor, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "agent-worker", Version: "0.1.0"}, nil)
	httpClient := &http.Client{Timeout: workspaceToolTimeout}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect Workspace MCP: %w", err)
	}
	executor := &WorkspaceExecutor{session: session, allowed: asSet(allowedTools)}
	if err := executor.validateTools(ctx); err != nil {
		if closeErr := session.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close Workspace MCP session: %w", closeErr))
		}
		return nil, err
	}
	return executor, nil
}

// Call invokes one allowlisted Workspace Tool with a bounded request deadline.
func (executor *WorkspaceExecutor) Call(ctx context.Context, request ToolRequest) (ToolResult, error) {
	if _, allowed := executor.allowed[request.Name]; !allowed {
		return ToolResult{}, fmt.Errorf("Tool %q is not allowlisted", request.Name)
	}
	arguments := map[string]any{}
	if err := json.Unmarshal(request.Arguments, &arguments); err != nil {
		return ToolResult{}, fmt.Errorf("decode arguments for Tool %s: %w", request.Name, err)
	}
	deadline, cancel := context.WithTimeout(ctx, workspaceToolTimeout)
	defer cancel()
	result, err := executor.session.CallTool(deadline, &mcp.CallToolParams{
		Name:      request.Name,
		Arguments: arguments,
	})
	if err != nil {
		return ToolResult{}, fmt.Errorf("call Workspace Tool %s: %w", request.Name, err)
	}
	content, err := marshalToolResult(result)
	if err != nil {
		return ToolResult{}, err
	}
	if result.IsError {
		return ToolResult{}, fmt.Errorf("Workspace Tool %s returned an error: %s", request.Name, content)
	}
	return ToolResult{Content: content, Summary: summarizeResult(content)}, nil
}

// Close releases the MCP session created for a Run.
func (executor *WorkspaceExecutor) Close() error {
	if err := executor.session.Close(); err != nil {
		return fmt.Errorf("close Workspace MCP session: %w", err)
	}
	return nil
}

func (executor *WorkspaceExecutor) validateTools(ctx context.Context) error {
	result, err := executor.session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list Workspace MCP Tools: %w", err)
	}
	available := make(map[string]struct{}, len(result.Tools))
	for _, tool := range result.Tools {
		available[tool.Name] = struct{}{}
	}
	for name := range executor.allowed {
		if _, found := available[name]; !found {
			return fmt.Errorf("Workspace MCP Tool %q is unavailable", name)
		}
	}
	return nil
}

func marshalToolResult(result *mcp.CallToolResult) (string, error) {
	if result.IsError && len(result.Content) > 0 {
		content, err := json.Marshal(result.Content)
		if err != nil {
			return "", fmt.Errorf("encode MCP Tool error content: %w", err)
		}
		return string(content), nil
	}
	if result.StructuredContent != nil {
		content, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return "", fmt.Errorf("encode structured MCP Tool result: %w", err)
		}
		return string(content), nil
	}
	content, err := json.Marshal(result.Content)
	if err != nil {
		return "", fmt.Errorf("encode MCP Tool result: %w", err)
	}
	return string(content), nil
}

func summarizeResult(content string) string {
	normalized := strings.ToValidUTF8(strings.Join(strings.Fields(content), " "), "�")
	if len(normalized) <= maximumResultSummary {
		return normalized
	}
	limit := maximumResultSummary
	for limit > 0 && !utf8.ValidString(normalized[:limit]) {
		limit--
	}
	return normalized[:limit] + "…"
}

func asSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
