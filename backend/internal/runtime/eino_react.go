package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/oklog/ulid/v2"
	"github.com/patrhez/agent-platform/backend/internal/domain"
)

// EinoRunner executes one Agent Run through Eino ADK behind the AgentRunner boundary.
type EinoRunner struct {
	definition Definition
	model      model.ToolCallingChatModel
	connect    executorFactory
}

type checkpointState struct {
	Iteration int               `json:"iteration"`
	Messages  []*schema.Message `json:"messages"`
}

// NewEinoRunner creates an Eino runtime for one release-bundled Agent definition.
func NewEinoRunner(definition Definition, baseURL string, apiKey string) (*EinoRunner, error) {
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	if baseURL == "" {
		return nil, fmt.Errorf("LLM_BASE_URL is required for the Eino runtime")
	}
	chatModel, err := newToolCallingModel(definition, baseURL, apiKey)
	if err != nil {
		return nil, fmt.Errorf("create Eino OpenAI model: %w", err)
	}
	return &EinoRunner{definition: definition, model: chatModel, connect: newStreamableExecutor}, nil
}

func newToolCallingModel(
	definition Definition,
	baseURL string,
	apiKey string,
) (model.ToolCallingChatModel, error) {
	if definition.Agent.Model.APIMode == "responses" {
		return newResponsesModel(definition.Agent.Model, baseURL, apiKey)
	}
	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       definition.Agent.Model.Model,
		Temperature: &definition.Agent.Model.Temperature,
		Timeout:     time.Duration(definition.Agent.Limits.RunTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create Eino OpenAI model: %w", err)
	}
	return chatModel, nil
}

// Run executes and checkpoints each model decision and Workspace Tool result.
func (runner *EinoRunner) Run(
	ctx context.Context,
	input AgentInput,
	checkpoint *domain.Checkpoint,
	emit func(RuntimeEvent) error,
) (Result, error) {
	runContext, cancel := context.WithTimeout(
		ctx,
		time.Duration(runner.definition.Agent.Limits.RunTimeoutSeconds)*time.Second,
	)
	defer cancel()
	toolset, err := connectToolset(runContext, runner.definition.Agent.MCPServers, runner.connect)
	if err != nil {
		return Result{}, err
	}
	state, ordinal, err := restoreState(input, checkpoint, runner.definition.SystemPrompt)
	if err != nil {
		return Result{}, closeToolsetOnError(toolset, err)
	}
	result, runErr := runner.execute(runContext, input.RunID, input.Attempt, state, ordinal, toolset, emit)
	if closeErr := toolset.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	return result, runErr
}

func (runner *EinoRunner) execute(
	ctx context.Context,
	runID string,
	attempt int,
	state checkpointState,
	ordinal int,
	toolset *mcpToolset,
	emit func(RuntimeEvent) error,
) (Result, error) {
	// Resume may land after an assistant tool_calls message and before tool results.
	// Drain those with the platform tool path before handing control to ADK.
	for hasPendingToolCalls(state.Messages) {
		nextOrdinal, err := runner.executeTools(ctx, runID, &state, ordinal, toolset, emit)
		if err != nil {
			return Result{}, err
		}
		ordinal = nextOrdinal
	}
	if state.Iteration >= runner.definition.Agent.Limits.MaxSteps {
		return Result{}, ErrStepLimit
	}
	return runner.runWithADK(ctx, runID, attempt, state, ordinal, toolset, emit)
}

func streamModelResponse(
	ctx context.Context,
	boundModel model.ToolCallingChatModel,
	messages []*schema.Message,
	streamID string,
	attempt int,
	stepNo int,
	emit func(RuntimeEvent) error,
) (*schema.Message, error) {
	stream, err := boundModel.Stream(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("stream Agent response: %w", err)
	}
	defer stream.Close()

	chunks := make([]*schema.Message, 0)
	started := false
	offset := 0
	for {
		chunk, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		if receiveErr != nil {
			return nil, fmt.Errorf("receive Agent response stream: %w", receiveErr)
		}
		if chunk == nil {
			continue
		}
		safeChunk := sanitizeAssistantMessage(chunk)
		chunks = append(chunks, safeChunk)
		if safeChunk.Content == "" {
			continue
		}
		if !started {
			if err := emitAssistantStreamEvent(emit, AssistantStreamEvent{
				StreamID: streamID,
				Phase:    "started",
				Attempt:  attempt,
				StepNo:   stepNo,
			}); err != nil {
				return nil, err
			}
			started = true
		}
		if err := emitAssistantStreamEvent(emit, AssistantStreamEvent{
			StreamID: streamID,
			Phase:    "delta",
			Attempt:  attempt,
			StepNo:   stepNo,
			Offset:   offset,
			Text:     safeChunk.Content,
		}); err != nil {
			return nil, err
		}
		offset++
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("receive Agent response stream: empty stream")
	}
	response, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("concatenate Agent response stream: %w", err)
	}
	return sanitizeAssistantMessage(response), nil
}

func emitAssistantStreamEvent(emit func(RuntimeEvent) error, assistant AssistantStreamEvent) error {
	if err := emit(RuntimeEvent{
		StepNo:    assistant.StepNo,
		Kind:      "model",
		Summary:   "Agent is generating a response",
		Assistant: &assistant,
	}); err != nil {
		return fmt.Errorf("emit assistant %s event: %w", assistant.Phase, err)
	}
	return nil
}

func (runner *EinoRunner) executeTools(
	ctx context.Context,
	runID string,
	state *checkpointState,
	ordinal int,
	toolset *mcpToolset,
	emit func(RuntimeEvent) error,
) (int, error) {
	lastMessage := state.Messages[len(state.Messages)-1]
	for index := range lastMessage.ToolCalls {
		call := &lastMessage.ToolCalls[index]
		request, binding, err := toolRequest(runID, call, toolset)
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
			call.ID,
			schema.WithToolName(call.Function.Name),
		))
		checkpoint, err := newCheckpoint(runID, ordinal+1, *state)
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

func toolRequest(runID string, call *schema.ToolCall, toolset *mcpToolset) (ToolRequest, toolBinding, error) {
	binding, found := toolset.binding(call.Function.Name)
	if !found {
		return ToolRequest{}, toolBinding{}, fmt.Errorf("model requested non-allowlisted Tool %q", call.Function.Name)
	}
	if !json.Valid([]byte(call.Function.Arguments)) {
		return ToolRequest{}, toolBinding{}, fmt.Errorf("model returned invalid JSON for Tool %s", call.Function.Name)
	}
	if call.ID == "" {
		return ToolRequest{}, toolBinding{}, fmt.Errorf("model returned a Tool call without an ID")
	}
	idempotencyKey := toolCallID(call)
	if idempotencyKey == "" {
		idempotencyKey = ulid.Make().String()
		if call.Extra == nil {
			call.Extra = map[string]any{}
		}
		call.Extra["platform_tool_call_id"] = idempotencyKey
	}
	arguments := json.RawMessage(call.Function.Arguments)
	return ToolRequest{
		ID:             idempotencyKey,
		IdempotencyKey: runID + ":" + idempotencyKey,
		ServerKey:      binding.serverKey,
		Name:           binding.mcpName,
		Arguments:      arguments,
		SafeArguments:  safeArguments(arguments, binding.safeKeys),
	}, binding, nil
}

func restoreState(input AgentInput, checkpoint *domain.Checkpoint, systemPrompt string) (checkpointState, int, error) {
	if checkpoint == nil {
		messages, err := initialMessages(input, systemPrompt)
		if err != nil {
			return checkpointState{}, 0, err
		}
		return checkpointState{Messages: messages}, 0, nil
	}
	if checkpoint.RuntimeName != Name || checkpoint.StateSchemaVersion != StateSchemaVersion {
		return checkpointState{}, 0, fmt.Errorf("unsupported Checkpoint schema for Run %s", input.RunID)
	}
	state := checkpointState{}
	if err := json.Unmarshal(checkpoint.Payload, &state); err != nil {
		return checkpointState{}, 0, fmt.Errorf("decode Checkpoint for Run %s: %w", input.RunID, err)
	}
	if len(state.Messages) == 0 {
		return checkpointState{}, 0, fmt.Errorf("Checkpoint for Run %s has no messages", input.RunID)
	}
	return state, checkpoint.Ordinal, nil
}

func initialMessages(input AgentInput, systemPrompt string) ([]*schema.Message, error) {
	if len(input.Messages) == 0 {
		return nil, fmt.Errorf("Run %s has no Conversation messages", input.RunID)
	}
	if systemPrompt == "" {
		return nil, fmt.Errorf("Run %s has no Skills-derived system prompt", input.RunID)
	}
	messages := make([]*schema.Message, 0, len(input.Messages)+1)
	messages = append(messages, schema.SystemMessage(systemPrompt))
	for index, message := range input.Messages {
		switch message.Role {
		case "user":
			messages = append(messages, schema.UserMessage(message.Content))
		case "assistant":
			messages = append(messages, schema.AssistantMessage(message.Content, nil))
		default:
			return nil, fmt.Errorf(
				"Run %s Conversation message %d has unsupported role %q",
				input.RunID,
				index,
				message.Role,
			)
		}
	}
	if messages[len(messages)-1].Role != schema.User {
		return nil, fmt.Errorf("Run %s Conversation history does not end with a user message", input.RunID)
	}
	return messages, nil
}

func newCheckpoint(runID string, ordinal int, state checkpointState) (*domain.Checkpoint, error) {
	payload, err := json.Marshal(sanitizeCheckpointState(state))
	if err != nil {
		return nil, fmt.Errorf("encode runtime Checkpoint: %w", err)
	}
	return &domain.Checkpoint{
		ID:                 ulid.Make().String(),
		RunID:              runID,
		Ordinal:            ordinal,
		RuntimeName:        Name,
		StateSchemaVersion: StateSchemaVersion,
		Payload:            payload,
		CreatedAt:          time.Now().UTC(),
	}, nil
}

func sanitizeCheckpointState(state checkpointState) checkpointState {
	result := state
	result.Messages = make([]*schema.Message, len(state.Messages))
	for index, message := range state.Messages {
		if message != nil && message.Role == schema.Assistant {
			result.Messages[index] = sanitizeAssistantMessage(message)
			continue
		}
		result.Messages[index] = message
	}
	return result
}

func hasPendingToolCalls(messages []*schema.Message) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	return last.Role == schema.Assistant && len(last.ToolCalls) > 0
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

func sanitizeAssistantMessage(message *schema.Message) *schema.Message {
	copy := *message
	copy.ReasoningContent = ""
	copy.Extra = sanitizeMessageExtra(message.Extra)
	if len(copy.ToolCalls) > 0 {
		copy.Content = ""
	}
	return &copy
}

func sanitizeMessageExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return extra
	}
	result := make(map[string]any, len(extra))
	for key, value := range extra {
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
		if normalized == "reasoning" || normalized == "reasoning-content" || normalized == "reasoningcontent" {
			continue
		}
		result[key] = value
	}
	return result
}

func toolCallID(call *schema.ToolCall) string {
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
