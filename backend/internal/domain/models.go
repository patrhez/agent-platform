// Package domain defines durable Agent Platform business records.
package domain

import (
	"encoding/json"
	"time"
)

// RunPins records immutable Agent and Skills versions selected when a Run is created.
type RunPins struct {
	AgentConfigVersion  string
	SkillsBundleVersion string
}

// Run is one durable Agent execution triggered by a user Message.
type Run struct {
	ID                  string     `json:"id"`
	ConversationID      string     `json:"conversationId"`
	TriggerMessageID    string     `json:"triggerMessageId"`
	QueueSeq            int64      `json:"queueSeq"`
	Status              RunStatus  `json:"status"`
	ExecutionToken      int64      `json:"-"`
	LeaseOwner          string     `json:"-"`
	LeaseExpiresAt      *time.Time `json:"-"`
	Attempt             int        `json:"attempt"`
	AgentConfigVersion  string     `json:"agentConfigVersion"`
	SkillsBundleVersion string     `json:"skillsBundleVersion"`
	ErrorCode           string     `json:"errorCode,omitempty"`
	ErrorMessage        string     `json:"errorMessage,omitempty"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// Conversation is the user-owned, durable container for ordered Messages and Runs.
type Conversation struct {
	ID              string     `json:"id"`
	UserID          string     `json:"-"`
	Title           string     `json:"title"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	LatestMessageAt *time.Time `json:"latestMessageAt"`
}

// ConversationDetail is the snapshot used to restore a complete chat view.
type ConversationDetail struct {
	Conversation Conversation `json:"conversation"`
	Messages     []Message    `json:"messages"`
	Runs         []Run        `json:"runs"`
}

// Message is a safe, user-visible conversation turn.
type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversationId"`
	Seq            int64      `json:"seq"`
	Role           string     `json:"role"`
	Content        string     `json:"content"`
	Status         string     `json:"status"`
	RunID          string     `json:"runId,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	FinalizedAt    *time.Time `json:"finalizedAt,omitempty"`
}

// RunSnapshot is the safe API representation of a Run's current state.
type RunSnapshot struct {
	Run            Run        `json:"run"`
	LatestEventSeq int64      `json:"latestEventSeq"`
	FinalMessageID string     `json:"finalMessageId,omitempty"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
}

// RunTrace contains safe execution Steps and Tool call summaries.
type RunTrace struct {
	Steps     []RunStep  `json:"steps"`
	ToolCalls []ToolCall `json:"toolCalls"`
}

// RunStep is a concise, user-explainable runtime boundary.
type RunStep struct {
	ID          string     `json:"id"`
	RunID       string     `json:"runId"`
	StepNo      int        `json:"stepNo"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	SafeSummary string     `json:"safeSummary"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Artifact is an immutable full result that is fetched outside the event stream.
type Artifact struct {
	ID          string    `json:"id"`
	RunID       string    `json:"runId"`
	Kind        string    `json:"kind"`
	ContentType string    `json:"contentType"`
	Content     []byte    `json:"-"`
	SHA256      string    `json:"sha256"`
	ByteSize    int64     `json:"byteSize"`
	CreatedAt   time.Time `json:"createdAt"`
}

// RunExecution contains the data a Worker needs to execute one claimed Run.
type RunExecution struct {
	Run      Run
	Messages []Message
}

// Checkpoint is a versioned runtime state snapshot that can resume a Run.
type Checkpoint struct {
	ID                 string
	RunID              string
	Ordinal            int
	RuntimeName        string
	StateSchemaVersion int
	Payload            json.RawMessage
	CreatedAt          time.Time
}

// ToolCall records the safe fields of one allowlisted Tool invocation.
type ToolCall struct {
	ID             string          `json:"id"`
	RunID          string          `json:"runId"`
	StepNo         int             `json:"stepNo"`
	ServerKey      string          `json:"serverKey"`
	ToolName       string          `json:"toolName"`
	Arguments      json.RawMessage `json:"arguments"`
	ResultSummary  string          `json:"resultSummary"`
	ArtifactID     string          `json:"artifactId,omitempty"`
	IdempotencyKey string          `json:"-"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// RunEvent is one durable user-visible execution event.
type RunEvent struct {
	ID          string
	RunID       string
	Seq         int64
	Type        string
	SafePayload json.RawMessage
	CreatedAt   time.Time
}

// ExecutionBoundary is one fenced, durable Worker persistence boundary.
type ExecutionBoundary struct {
	RunID          string
	ExecutionToken int64
	StepNo         int
	Kind           string
	SafeSummary    string
	ToolCall       *ToolCall
	Checkpoint     *Checkpoint
	Events         []RunEvent
}

// RunCompletion is the durable terminal outcome for a Run.
type RunCompletion struct {
	RunID          string
	ExecutionToken int64
	Status         RunStatus
	AssistantText  string
	ErrorCode      string
	ErrorMessage   string
	Event          RunEvent
}
