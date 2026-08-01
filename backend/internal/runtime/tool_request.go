package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// buildToolRequest creates the platform representation of one model-selected tool call.
// durableID may be supplied by checkpoint recovery to preserve idempotency across retries.
func buildToolRequest(
	runID string,
	binding toolBinding,
	providerCallID string,
	argumentsInJSON string,
	durableID string,
) (ToolRequest, error) {
	if !json.Valid([]byte(argumentsInJSON)) {
		return ToolRequest{}, fmt.Errorf("model returned invalid JSON for Tool %s", binding.mcpName)
	}
	if durableID == "" {
		durableID = ulid.Make().String()
	}
	arguments := json.RawMessage(argumentsInJSON)
	return ToolRequest{
		ID:             durableID,
		CallID:         providerCallID,
		IdempotencyKey: runID + ":" + durableID,
		ServerKey:      binding.serverKey,
		Name:           binding.mcpName,
		Arguments:      arguments,
		SafeArguments:  safeArguments(arguments, binding.safeKeys),
	}, nil
}

func callWithRetry(ctx context.Context, executor ToolExecutor, request ToolRequest) (ToolResult, error) {
	var lastError error
	for attempt := 0; attempt < 3; attempt++ {
		result, err := executor.Call(ctx, request)
		if err == nil {
			return result, nil
		}
		lastError = err
		if ctx.Err() != nil || attempt == 2 {
			break
		}
		if err := waitForRetry(ctx, time.Duration(attempt+1)*200*time.Millisecond); err != nil {
			return ToolResult{}, err
		}
	}
	return ToolResult{}, fmt.Errorf("Tool %s failed after retries: %w", request.Name, lastError)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
