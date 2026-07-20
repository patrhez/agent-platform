package runtime

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
)

func buildContextHandlers(
	ctx context.Context,
	definition Definition,
	session *adkEmitSession,
) ([]adk.ChatModelAgentMiddleware, error) {
	handlers := make([]adk.ChatModelAgentMiddleware, 0, 3)
	contextConfig := definition.Agent.Context

	if contextConfig.Reduction.Enabled {
		// SkipOffload maps to SkipTruncation: without a Backend, truncation would
		// fail validation; clear-only placeholders keep MCP-only Workspace access.
		reductionCfg := &reduction.Config{
			SkipTruncation:    contextConfig.Reduction.SkipOffload,
			SkipClear:         false,
			MaxLengthForTrunc: contextConfig.Reduction.MaxLengthForTrunc,
			MaxTokensForClear: int64(contextConfig.Reduction.MaxTokensForClear),
			ReadFileToolName:  ModelToolName("file.read"),
		}
		handler, err := reduction.New(ctx, reductionCfg)
		if err != nil {
			return nil, fmt.Errorf("create reduction middleware: %w", err)
		}
		handlers = append(handlers, handler)
	}

	if contextConfig.Summarization.Enabled {
		trigger := &summarization.TriggerCondition{}
		if contextConfig.Summarization.ContextTokens > 0 {
			trigger.ContextTokens = contextConfig.Summarization.ContextTokens
		}
		if contextConfig.Summarization.ContextMessages > 0 {
			trigger.ContextMessages = contextConfig.Summarization.ContextMessages
		}
		if trigger.ContextTokens == 0 && trigger.ContextMessages == 0 {
			trigger = nil // library default (~160k tokens)
		}
		handler, err := summarization.New(ctx, &summarization.Config{
			Model:   session.model,
			Trigger: trigger,
		})
		if err != nil {
			return nil, fmt.Errorf("create summarization middleware: %w", err)
		}
		handlers = append(handlers, handler)
	}

	handlers = append(handlers, &checkpointStateSync{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		session:                      session,
	})
	return handlers, nil
}

// checkpointStateSync copies ADK messages into the platform Checkpoint session after
// context middlewares rewrite history (reduction / summarization).
type checkpointStateSync struct {
	*adk.BaseChatModelAgentMiddleware
	session *adkEmitSession
}

func (handler *checkpointStateSync) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	_ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	handler.session.replaceMessages(state.Messages)
	return ctx, state, nil
}

func (session *adkEmitSession) replaceMessages(messages []*schema.Message) {
	session.mu.Lock()
	defer session.mu.Unlock()
	copied := make([]*schema.Message, len(messages))
	copy(copied, messages)
	session.state.Messages = copied
}
