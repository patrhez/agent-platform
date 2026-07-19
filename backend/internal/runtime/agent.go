// Package runtime defines the replaceable Agent execution boundary.
package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/patrhez/agent-platform/backend/internal/domain"
)

const (
	// Name identifies the Eino-based runtime in durable Checkpoints.
	Name = "eino-react"
	// StateSchemaVersion identifies the current checkpoint payload format.
	StateSchemaVersion = 1
)

var (
	// ErrStepLimit indicates that a Run exceeded its configured ReAct step limit.
	ErrStepLimit = errors.New("Agent step limit exceeded")
	// ErrRunCancelled indicates that the user cancelled the Run during execution.
	ErrRunCancelled = errors.New("Run cancelled")
)

// AgentInput is the platform-owned input to one Agent execution.
type AgentInput struct {
	RunID    string
	Attempt  int
	Messages []ConversationMessage
}

// ConversationMessage is one final user-visible turn supplied to a new Run.
type ConversationMessage struct {
	Role    string
	Content string
}

// AssistantStreamEvent is one safe fragment of a model content stream.
type AssistantStreamEvent struct {
	StreamID string
	Phase    string
	Attempt  int
	StepNo   int
	Offset   int
	Text     string
}

// ToolRequest is an allowlisted MCP invocation requested by the model.
type ToolRequest struct {
	ID             string
	IdempotencyKey string
	ServerKey      string
	Name           string
	Arguments      json.RawMessage
	// SafeArguments contains only the argument keys allowlisted for
	// user-visible events and logs.
	SafeArguments map[string]any
}

// ToolResult is the safe model-facing result of one MCP Tool invocation.
type ToolResult struct {
	Content string
	Summary string
}

// RuntimeEvent is an execution boundary with safe data for persistence and UI events.
type RuntimeEvent struct {
	StepNo     int
	Kind       string
	Summary    string
	Final      string
	Assistant  *AssistantStreamEvent
	Tool       *ToolRequest
	ToolResult *ToolResult
	Checkpoint *domain.Checkpoint
}

// Result is the formal outcome returned by a successful Agent execution.
type Result struct {
	Final string
}

// AgentRunner executes a Run without knowing HTTP, database, or queue details.
type AgentRunner interface {
	Run(context.Context, AgentInput, *domain.Checkpoint, func(RuntimeEvent) error) (Result, error)
}

// ToolExecutor invokes an allowlisted Tool through its protocol boundary.
type ToolExecutor interface {
	Call(context.Context, ToolRequest) (ToolResult, error)
	Close() error
}
