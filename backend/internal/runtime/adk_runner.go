package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/logging"
)

func (runner *EinoRunner) runWithADK(
	ctx context.Context,
	runID string,
	conversationID string,
	attempt int,
	state checkpointState,
	ordinal int,
	toolset *mcpToolset,
	emit func(RuntimeEvent) error,
) (Result, error) {
	session := &adkEmitSession{
		ctx:            ctx,
		runID:          runID,
		conversationID: conversationID,
		attempt:        attempt,
		state:          &state,
		ordinal:        &ordinal,
		emit:           emit,
		model:          runner.model,
		logger:         runner.logger,
		runner:         runner,
	}
	agent, err := runner.newADKAgent(ctx, session, toolset)
	if err != nil {
		return Result{}, err
	}
	session.logEinoInput(ctx, ordinal > 0)
	adkRunner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})
	return session.consumeADKEvents(ctx, adkRunner.Run(ctx, state.Messages))
}

func (runner *EinoRunner) newADKAgent(
	ctx context.Context,
	session *adkEmitSession,
	toolset *mcpToolset,
) (*adk.ChatModelAgent, error) {
	invokables, err := buildInvokableTools(toolset, toolEmitHooks{
		RunID: session.runID,
		OnStart: func(request ToolRequest) error {
			return session.emitToolStart(request)
		},
		OnFinish: func(request ToolRequest, result ToolResult) error {
			return session.emitToolFinish(request, result)
		},
	})
	if err != nil {
		return nil, err
	}
	session.logEinoToolsBound(ctx, runner.definition.Agent.ID, invokables)
	handlers, err := buildContextHandlers(ctx, runner.definition, session)
	if err != nil {
		return nil, err
	}
	// Messages already include the Skills-derived system prompt; GenModelInput
	// must not prepend Instruction or the model would see two system messages.
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        runner.definition.Agent.ID,
		Description: "platform Agent runtime",
		Model:       runner.model,
		GenModelInput: func(_ context.Context, _ string, input *adk.AgentInput) ([]*schema.Message, error) {
			return input.Messages, nil
		},
		MaxIterations: runner.definition.Agent.Limits.MaxSteps,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: invokables,
				// Tool completions must remain serialized so checkpoint ordinals and
				// worker persistence boundaries are deterministic.
				ExecuteSequentially: true,
			},
		},
		Handlers: handlers,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK ChatModelAgent: %w", err)
	}
	return agent, nil
}

// adkEmitSession maps ADK events into platform RuntimeEvents and Checkpoints.
// ordinal is shared with the resume preamble so Checkpoint ordinals stay monotonic.
type adkEmitSession struct {
	mu             sync.Mutex
	ctx            context.Context
	runID          string
	conversationID string
	attempt        int
	state          *checkpointState
	ordinal        *int
	emit           func(RuntimeEvent) error
	model          model.ToolCallingChatModel
	logger         logging.Logger
	runner         *EinoRunner
	// providerResponseCaptured means AfterModelRewriteState has already placed
	// the raw assistant response into state.Messages for this boundary.
	providerResponseCaptured bool
	final                    string
}

func (session *adkEmitSession) consumeADKEvents(
	ctx context.Context,
	iter *adk.AsyncIterator[*adk.AgentEvent],
) (Result, error) {
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if event.Err != nil {
			if errors.Is(event.Err, adk.ErrExceedMaxIterations) {
				return Result{}, ErrStepLimit
			}
			return Result{}, fmt.Errorf("ADK Agent event: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		if err := session.handleMessageOutput(ctx, event.Output.MessageOutput); err != nil {
			return Result{}, err
		}
	}
	if session.final == "" {
		return Result{}, fmt.Errorf("ADK Agent finished without a final assistant message")
	}
	return Result{Final: session.final}, nil
}

func (session *adkEmitSession) handleMessageOutput(
	ctx context.Context,
	output *adk.MessageVariant,
) error {
	switch output.Role {
	case schema.Assistant:
		message, err := session.collectAssistant(ctx, output)
		if err != nil {
			return err
		}
		return session.emitAssistantBoundary(message)
	case schema.Tool:
		// Tool PersistBoundary is emitted from InvokableTool OnFinish so the
		// Checkpoint includes the tool message at the same moment as today.
		return nil
	default:
		return nil
	}
}

func (session *adkEmitSession) collectAssistant(
	ctx context.Context,
	output *adk.MessageVariant,
) (*schema.Message, error) {
	if !output.IsStreaming {
		if output.Message == nil {
			return nil, fmt.Errorf("ADK assistant event has no message")
		}
		return sanitizeAssistantMessage(output.Message), nil
	}
	if output.MessageStream == nil {
		return nil, fmt.Errorf("ADK assistant stream event has no stream")
	}
	defer output.MessageStream.Close()
	return session.consumeAssistantStream(ctx, output.MessageStream)
}

func (session *adkEmitSession) consumeAssistantStream(
	ctx context.Context,
	stream *schema.StreamReader[*schema.Message],
) (*schema.Message, error) {
	stepNo := session.state.Iteration + 1
	streamID := fmt.Sprintf("%s:%d:%d", session.runID, session.attempt, stepNo)
	chunks := make([]*schema.Message, 0)
	started := false
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		if receiveErr != nil {
			return nil, fmt.Errorf("receive ADK assistant stream: %w", receiveErr)
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
			if err := emitAssistantStreamEvent(session.emit, AssistantStreamEvent{
				StreamID: streamID,
				Phase:    "started",
				Attempt:  session.attempt,
				StepNo:   stepNo,
			}); err != nil {
				return nil, err
			}
			started = true
		}
		if err := emitAssistantStreamEvent(session.emit, AssistantStreamEvent{
			StreamID: streamID,
			Phase:    "delta",
			Attempt:  session.attempt,
			StepNo:   stepNo,
			Offset:   offset,
			Text:     safeChunk.Content,
		}); err != nil {
			return nil, err
		}
		offset++
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("receive ADK assistant stream: empty stream")
	}
	response, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("concatenate ADK assistant stream: %w", err)
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

func (session *adkEmitSession) emitAssistantBoundary(message *schema.Message) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.state.Iteration++
	if !session.providerResponseCaptured {
		session.state.Messages = append(session.state.Messages, message)
	}
	session.providerResponseCaptured = false
	checkpoint, err := session.newCheckpoint(*session.ordinal + 1)
	if err != nil {
		return err
	}
	*session.ordinal++
	var event RuntimeEvent
	if len(message.ToolCalls) == 0 {
		session.final = message.Content
		event = RuntimeEvent{
			StepNo:     session.state.Iteration,
			Kind:       "model",
			Summary:    "Agent produced its final troubleshooting report",
			Final:      message.Content,
			Checkpoint: checkpoint,
		}
	} else {
		event = RuntimeEvent{
			StepNo:     session.state.Iteration,
			Kind:       "model",
			Summary:    "Agent selected read-only repository Tools",
			Checkpoint: checkpoint,
		}
	}
	session.logEinoOutput(session.logContext(), event)
	if err := session.emit(event); err != nil {
		return err
	}
	if len(message.ToolCalls) > 0 && session.runner != nil {
		if err := session.runner.waitAfterToolCallCheckpoint(session.logContext()); err != nil {
			return fmt.Errorf("wait after tool-call checkpoint: %w", err)
		}
	}
	return nil
}

func (session *adkEmitSession) emitToolStart(request ToolRequest) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	event := RuntimeEvent{
		StepNo:  session.state.Iteration,
		Kind:    "tool",
		Summary: "Agent started " + request.Name,
		Tool:    &request,
	}
	session.logEinoOutput(session.logContext(), event)
	return session.emit(event)
}

func (session *adkEmitSession) emitToolFinish(request ToolRequest, result ToolResult) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.state.Messages = append(session.state.Messages, schema.ToolMessage(
		result.Content,
		request.ToolMessageCallID(),
		schema.WithToolName(ModelToolName(request.Name)),
	))
	checkpoint, err := session.newCheckpoint(*session.ordinal + 1)
	if err != nil {
		return err
	}
	*session.ordinal++
	event := RuntimeEvent{
		StepNo:     session.state.Iteration,
		Kind:       "tool",
		Summary:    "Agent completed " + request.Name + ": " + result.Summary,
		Tool:       &request,
		ToolResult: &result,
		Checkpoint: checkpoint,
	}
	session.logEinoOutput(session.logContext(), event)
	return session.emit(event)
}

func (session *adkEmitSession) logContext() context.Context {
	if session.ctx != nil {
		return session.ctx
	}
	return context.Background()
}

func (session *adkEmitSession) newCheckpoint(ordinal int) (*domain.Checkpoint, error) {
	if session.runner != nil {
		return session.runner.newCheckpoint(
			session.runID, session.conversationID, ordinal, *session.state,
		)
	}
	return newCheckpoint(session.runID, ordinal, *session.state)
}
