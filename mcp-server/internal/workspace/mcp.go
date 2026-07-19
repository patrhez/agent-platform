package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverVersion = "0.1.0"

// NewMCPServer registers read-only Workspace tools on an MCP server.
func NewMCPServer(service *Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "workspace-mcp", Version: serverVersion}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "file.read",
		Description: "Read a bounded line range from a file in an allowed repository.",
	}, newReadHandler(service))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "code.search",
		Description: "Find bounded literal text matches in an allowed repository.",
	}, newSearchHandler(service))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "workspace.list_repositories",
		Description: "List safe repository aliases available in the Workspace.",
	}, newListRepositoriesHandler(service))
	return server
}

func newListRepositoriesHandler(service *Service) func(context.Context, *mcp.CallToolRequest, ListRepositoriesInput) (*mcp.CallToolResult, ListRepositoriesOutput, error) {
	return func(context context.Context, _ *mcp.CallToolRequest, input ListRepositoriesInput) (*mcp.CallToolResult, ListRepositoriesOutput, error) {
		startedAt := time.Now()
		output, err := service.ListRepositories(context, input)
		if err != nil {
			log.Printf("MCP Tool workspace.list_repositories status=failed duration=%s error=%v", time.Since(startedAt).Round(time.Millisecond), err)
			return errorResult(err), ListRepositoriesOutput{}, nil
		}
		log.Printf("MCP Tool workspace.list_repositories status=succeeded duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil, output, nil
	}
}

func newReadHandler(service *Service) func(context.Context, *mcp.CallToolRequest, ReadInput) (*mcp.CallToolResult, ReadOutput, error) {
	return func(context context.Context, _ *mcp.CallToolRequest, input ReadInput) (*mcp.CallToolResult, ReadOutput, error) {
		startedAt := time.Now()
		output, err := service.Read(context, input)
		if err != nil {
			log.Printf("MCP Tool file.read status=failed duration=%s error=%v", time.Since(startedAt).Round(time.Millisecond), err)
			return errorResult(err), ReadOutput{}, nil
		}
		log.Printf("MCP Tool file.read status=succeeded duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil, output, nil
	}
}

func newSearchHandler(service *Service) func(context.Context, *mcp.CallToolRequest, SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
	return func(context context.Context, _ *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		startedAt := time.Now()
		output, err := service.Search(context, input)
		if err != nil {
			log.Printf("MCP Tool code.search status=failed duration=%s error=%v", time.Since(startedAt).Round(time.Millisecond), err)
			return errorResult(err), SearchOutput{}, nil
		}
		log.Printf("MCP Tool code.search status=succeeded duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil, output, nil
	}
}

func errorResult(err error) *mcp.CallToolResult {
	payload, marshalErr := json.Marshal(map[string]string{
		"code":    errorCode(err),
		"message": err.Error(),
	})
	if marshalErr != nil {
		payload = []byte(`{"code":"INTERNAL","message":"serialize tool error"}`)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}},
		IsError: true,
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRepo):
		return "INVALID_REPO"
	case errors.Is(err, ErrInvalidPath):
		return "INVALID_PATH"
	case errors.Is(err, ErrPathOutsideWorkspace):
		return "PATH_OUTSIDE_WORKSPACE"
	case errors.Is(err, ErrRepositoryNotFound):
		return "REPOSITORY_NOT_FOUND"
	case errors.Is(err, ErrFileNotFound):
		return "NOT_FOUND"
	case errors.Is(err, ErrResultLimitExceeded):
		return "RESULT_LIMIT_EXCEEDED"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT"
	default:
		return "INTERNAL"
	}
}
