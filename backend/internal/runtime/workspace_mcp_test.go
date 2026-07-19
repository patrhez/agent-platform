package runtime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMarshalToolResultPrefersExplicitErrorContent(t *testing.T) {
	result := &mcp.CallToolResult{
		IsError:           true,
		StructuredContent: map[string]any{"repositories": nil},
		Content: []mcp.Content{&mcp.TextContent{
			Text: `{"code":"REPOSITORY_NOT_FOUND","message":"repository not found"}`,
		}},
	}

	content, err := marshalToolResult(result)
	if err != nil {
		t.Fatalf("marshalToolResult() error = %v", err)
	}
	if !strings.Contains(content, "REPOSITORY_NOT_FOUND") {
		t.Errorf("marshalToolResult() = %q, want explicit error content", content)
	}
}

func TestSummarizeResultPreservesValidUTF8AtLimit(t *testing.T) {
	content := strings.Repeat("€", maximumResultSummary)
	summary := summarizeResult(content)
	if !utf8.ValidString(summary) {
		t.Fatalf("summarizeResult() returned invalid UTF-8")
	}
	if !strings.HasSuffix(summary, "…") {
		t.Errorf("summarizeResult() = %q, want ellipsis suffix", summary)
	}
}
