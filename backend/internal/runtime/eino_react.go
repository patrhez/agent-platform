package runtime

import (
	"context"
	"encoding/json"
	"fmt"
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
	// A checkpoint can contain an assistant tool-call message without a matching
	// tool result. Complete that recovery boundary before entering ADK.
	var err error
	ordinal, err = runner.resumePendingToolCalls(ctx, runID, &state, ordinal, toolset, emit)
	if err != nil {
		return Result{}, err
	}
	if state.Iteration >= runner.definition.Agent.Limits.MaxSteps {
		return Result{}, ErrStepLimit
	}
	return runner.runWithADK(ctx, runID, attempt, state, ordinal, toolset, emit)
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
