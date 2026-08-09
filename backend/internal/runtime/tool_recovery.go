package runtime

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"
)

// resumePendingToolCalls completes tool calls that were checkpointed after an
// assistant tool-call message but before any matching tool result was persisted.
func (runner *EinoRunner) resumePendingToolCalls(
	ctx context.Context,
	runID string,
	conversationID string,
	state *checkpointState,
	ordinal int,
	toolset *mcpToolset,
	emit func(RuntimeEvent) error,
) (int, error) {
	for hasPendingToolCalls(state.Messages) {
		nextOrdinal, err := runner.executePendingToolCalls(
			ctx, runID, conversationID, state, ordinal, toolset, emit,
		)
		if err != nil {
			return ordinal, err
		}
		ordinal = nextOrdinal
	}
	return ordinal, nil
}

func (runner *EinoRunner) executePendingToolCalls(
	ctx context.Context,
	runID string,
	conversationID string,
	state *checkpointState,
	ordinal int,
	toolset *mcpToolset,
	emit func(RuntimeEvent) error,
) (int, error) {
	lastMessage := state.Messages[len(state.Messages)-1]
	for index := range lastMessage.ToolCalls {
		call := &lastMessage.ToolCalls[index]
		request, binding, err := toolRequestFromCall(runID, call, toolset)
		if err != nil {
			return ordinal, err
		}
		if err := emit(RuntimeEvent{
			StepNo:  state.Iteration,
			Kind:    "tool",
			Summary: "Agent started " + request.Name,
			Tool:    &request,
		}); err != nil {
			return ordinal, err
		}
		result, err := callWithRetry(ctx, binding.executor, request)
		if err != nil {
			return ordinal, err
		}
		state.Messages = append(state.Messages, schema.ToolMessage(
			result.Content,
			request.ToolMessageCallID(),
			schema.WithToolName(call.Function.Name),
		))
		checkpoint, err := runner.newCheckpoint(runID, conversationID, ordinal+1, *state)
		if err != nil {
			return ordinal, err
		}
		ordinal++
		if err := emit(RuntimeEvent{
			StepNo:     state.Iteration,
			Kind:       "tool",
			Summary:    "Agent completed " + request.Name + ": " + result.Summary,
			Tool:       &request,
			ToolResult: &result,
			Checkpoint: checkpoint,
		}); err != nil {
			return ordinal, err
		}
	}
	return ordinal, nil
}

func toolRequestFromCall(runID string, call *schema.ToolCall, toolset *mcpToolset) (ToolRequest, toolBinding, error) {
	binding, found := toolset.binding(call.Function.Name)
	if !found {
		return ToolRequest{}, toolBinding{}, fmt.Errorf("model requested non-allowlisted Tool %q", call.Function.Name)
	}
	if call.ID == "" {
		return ToolRequest{}, toolBinding{}, fmt.Errorf("model returned a Tool call without an ID")
	}
	request, err := buildToolRequest(
		runID,
		binding,
		call.ID,
		call.Function.Arguments,
		platformToolCallID(call),
	)
	if err != nil {
		return ToolRequest{}, toolBinding{}, err
	}
	if platformToolCallID(call) == "" {
		if call.Extra == nil {
			call.Extra = map[string]any{}
		}
		call.Extra["platform_tool_call_id"] = request.ID
	}
	return request, binding, nil
}

func hasPendingToolCalls(messages []*schema.Message) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	return last.Role == schema.Assistant && len(last.ToolCalls) > 0
}

func platformToolCallID(call *schema.ToolCall) string {
	if call.Extra == nil {
		return ""
	}
	value, found := call.Extra["platform_tool_call_id"]
	if !found {
		return ""
	}
	id, _ := value.(string)
	return id
}
