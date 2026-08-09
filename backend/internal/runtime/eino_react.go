package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/oklog/ulid/v2"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/logging"
)

// EinoRunner executes one Agent Run through Eino ADK behind the AgentRunner boundary.
type EinoRunner struct {
	definition                Definition
	model                     model.ToolCallingChatModel
	connect                   executorFactory
	logger                    logging.Logger
	replay                    *replayCodec
	checkpointPause           time.Duration
	requireReasoningForHATest bool
}

type checkpointState struct {
	Iteration int               `json:"iteration"`
	Messages  []*schema.Message `json:"messages"`
}

// NewEinoRunner creates an Eino runtime for one release-bundled Agent definition.
func NewEinoRunner(
	definition Definition,
	baseURL string,
	apiKey string,
	replayKeys ReplayKeyringConfig,
	logger logging.Logger,
) (*EinoRunner, error) {
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
	if logger == nil {
		logger = logging.Nop()
	}
	checkpointPause, err := checkpointPauseFromEnv()
	if err != nil {
		return nil, err
	}
	var replay *replayCodec
	if definition.Agent.Model.ReasoningReplay == "required" {
		var replayErr error
		replay, replayErr = newReplayCodec(replayKeys)
		if replayErr != nil {
			return nil, replayErr
		}
	}
	return &EinoRunner{
		definition:                definition,
		model:                     chatModel,
		connect:                   newStreamableExecutor(logger),
		logger:                    logger,
		replay:                    replay,
		checkpointPause:           checkpointPause,
		requireReasoningForHATest: os.Getenv("HA_TEST_REQUIRE_REASONING_REPLAY") == "1",
	}, nil
}

func checkpointPauseFromEnv() (time.Duration, error) {
	raw := os.Getenv("HA_TEST_TOOL_CALL_CHECKPOINT_DELAY")
	if raw == "" {
		return 0, nil
	}
	delay, err := time.ParseDuration(raw)
	if err != nil || delay <= 0 {
		return 0, fmt.Errorf("HA_TEST_TOOL_CALL_CHECKPOINT_DELAY must be a positive duration")
	}
	return delay, nil
}

func (runner *EinoRunner) waitAfterToolCallCheckpoint(ctx context.Context) error {
	if runner.checkpointPause <= 0 {
		return nil
	}
	timer := time.NewTimer(runner.checkpointPause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
		ExtraFields: modelRequestFields(definition.Agent.Model),
	})
	if err != nil {
		return nil, fmt.Errorf("create Eino OpenAI model: %w", err)
	}
	return chatModel, nil
}

func modelRequestFields(definition ModelDefinition) map[string]any {
	if len(definition.RequestFields) == 0 {
		return nil
	}
	fields := make(map[string]any, len(definition.RequestFields))
	for key, value := range definition.RequestFields {
		fields[key] = value
	}
	return fields
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
	runner.logToolsetReady(runContext, input.RunID, toolset)
	state, ordinal, err := runner.restoreState(input, checkpoint)
	if err != nil {
		return Result{}, closeToolsetOnError(toolset, err)
	}
	result, runErr := runner.execute(
		runContext,
		input.RunID,
		input.ConversationID,
		input.Attempt,
		state,
		ordinal,
		toolset,
		emit,
	)
	if closeErr := toolset.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	return result, runErr
}

func (runner *EinoRunner) execute(
	ctx context.Context,
	runID string,
	conversationID string,
	attempt int,
	state checkpointState,
	ordinal int,
	toolset *mcpToolset,
	emit func(RuntimeEvent) error,
) (Result, error) {
	// A checkpoint can contain an assistant tool-call message without a matching
	// tool result. Complete that recovery boundary before entering ADK.
	var err error
	ordinal, err = runner.resumePendingToolCalls(
		ctx, runID, conversationID, &state, ordinal, toolset, emit,
	)
	if err != nil {
		return Result{}, err
	}
	if state.Iteration >= runner.definition.Agent.Limits.MaxSteps {
		return Result{}, ErrStepLimit
	}
	return runner.runWithADK(
		ctx, runID, conversationID, attempt, state, ordinal, toolset, emit,
	)
}

func (runner *EinoRunner) restoreState(
	input AgentInput,
	checkpoint *domain.Checkpoint,
) (checkpointState, int, error) {
	if checkpoint == nil {
		return runner.restoreNewRun(input)
	}
	return runner.restoreActiveRun(input, checkpoint)
}

func (runner *EinoRunner) restoreNewRun(input AgentInput) (checkpointState, int, error) {
	messages, err := initialMessages(input, runner.definition.SystemPrompt)
	if err != nil {
		return checkpointState{}, 0, err
	}
	model := runner.definition.Agent.Model
	if input.ProviderReplay == nil || model.ContinuationScope != "conversation" {
		return checkpointState{Messages: messages}, 0, nil
	}
	replayed, err := runner.restoreReplay(input, input.ProviderReplay)
	if err != nil {
		return checkpointState{Messages: messages}, 0, nil
	}
	replayed = append(replayed, schema.UserMessage(input.Messages[len(input.Messages)-1].Content))
	return checkpointState{Messages: replayed}, 0, nil
}

func (runner *EinoRunner) restoreActiveRun(
	input AgentInput,
	checkpoint *domain.Checkpoint,
) (checkpointState, int, error) {
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
	if checkpoint.ProviderReplay != nil {
		messages, err := runner.restoreReplay(input, checkpoint.ProviderReplay)
		if err != nil {
			return checkpointState{}, 0, fmt.Errorf("restore provider replay for Run %s: %w", input.RunID, err)
		}
		state.Messages = messages
	} else if runner.replay != nil {
		return checkpointState{}, 0, fmt.Errorf("Run %s: %w", input.RunID, ErrProviderReplayMissing)
	}
	return state, checkpoint.Ordinal, nil
}

func (runner *EinoRunner) restoreReplay(
	input AgentInput,
	replay *domain.ProviderReplay,
) ([]*schema.Message, error) {
	model := runner.definition.Agent.Model
	if runner.replay == nil || replay.Provider != model.Provider || replay.APIMode != model.APIMode ||
		replay.Model != model.Model || replay.ContinuationScope != model.ContinuationScope ||
		replay.AdapterVersion != model.AdapterVersion || replay.ConversationID != input.ConversationID {
		return nil, ErrProviderReplayIncompatible
	}
	return runner.replay.open(replay)
}

func (runner *EinoRunner) newCheckpoint(
	runID string,
	conversationID string,
	ordinal int,
	state checkpointState,
) (*domain.Checkpoint, error) {
	checkpoint, err := newCheckpoint(runID, ordinal, state)
	if err != nil || runner.replay == nil {
		return checkpoint, err
	}
	checkpoint.ProviderReplayRequired = true
	model := runner.definition.Agent.Model
	if runner.requireReasoningForHATest && toolCallBoundaryLacksReasoning(state.Messages) {
		return nil, fmt.Errorf("HA replay verification requires provider reasoning")
	}
	checkpoint.ProviderReplay, err = runner.replay.seal(replayContext{
		RunID: runID, CheckpointID: checkpoint.ID, ConversationID: conversationID,
		Provider: model.Provider, APIMode: model.APIMode, Model: model.Model,
		ContinuationScope: model.ContinuationScope, AdapterVersion: model.AdapterVersion,
	}, state.Messages)
	if err != nil {
		return nil, err
	}
	return checkpoint, nil
}

func toolCallBoundaryLacksReasoning(messages []*schema.Message) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	return last != nil && last.Role == schema.Assistant && len(last.ToolCalls) > 0 &&
		last.ReasoningContent == ""
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
		if normalized == "reasoning" || normalized == "reasoning-content" ||
			normalized == "reasoningcontent" || normalized == "openai-responses-output" {
			continue
		}
		result[key] = value
	}
	return result
}
