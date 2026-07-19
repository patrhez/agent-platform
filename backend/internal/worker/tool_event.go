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
	arguments := event.Tool.SafeArguments
	if arguments == nil {
		arguments = map[string]any{}
	}
	payload, err := json.Marshal(safeToolEventPayload{
		ToolCallID: event.Tool.ID,
		Tool:       event.Tool.Name,
		Status:     status,
		Arguments:  arguments,
		Result:     resultSummary,
		DurationMS: duration,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Tool event payload: %w", err)
	}
	return payload, nil
}
