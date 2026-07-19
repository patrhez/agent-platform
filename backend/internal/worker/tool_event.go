package worker

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/runtime"
)

type safeToolEventPayload struct {
	ToolCallID string         `json:"toolCallId"`
	Tool       string         `json:"tool"`
	Status     string         `json:"status"`
	Arguments  map[string]any `json:"arguments"`
	Result     string         `json:"resultSummary,omitempty"`
	DurationMS int64          `json:"durationMs"`
}

func safeToolArguments(name string, raw json.RawMessage) map[string]any {
	values := make(map[string]any)
	if err := json.Unmarshal(raw, &values); err != nil {
		return map[string]any{}
	}
	var allowed []string
	switch name {
	case "code.search":
		allowed = []string{"repo", "query", "pathPrefix", "glob", "maxResults"}
	case "file.read":
		allowed = []string{"repo", "path", "startLine", "endLine"}
	case "workspace.list_repositories":
		return map[string]any{}
	default:
		return map[string]any{}
	}
	result := make(map[string]any, len(allowed))
	for _, key := range allowed {
		if value, found := values[key]; found {
			result[key] = value
		}
	}
	return result
}

func toolEventPayload(
	event runtime.RuntimeEvent,
	startedAt time.Time,
	finishedAt time.Time,
) (json.RawMessage, error) {
	if event.Tool == nil {
		return nil, fmt.Errorf("Tool event payload requires a Tool request")
	}
	status := "running"
	resultSummary := ""
	duration := int64(0)
	if event.ToolResult != nil {
		status = "completed"
		resultSummary = event.ToolResult.Summary
		duration = finishedAt.Sub(startedAt).Milliseconds()
		if duration < 0 {
			duration = 0
		}
	}
	payload, err := json.Marshal(safeToolEventPayload{
		ToolCallID: event.Tool.ID,
		Tool:       event.Tool.Name,
		Status:     status,
		Arguments:  safeToolArguments(event.Tool.Name, event.Tool.Arguments),
		Result:     resultSummary,
		DurationMS: duration,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Tool event payload: %w", err)
	}
	return payload, nil
}
