// Package model defines the durable GORM schema for the Agent Platform.
package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// User is the durable identity record for the demo principal.
type User struct {
	ID          string    `gorm:"type:char(26);primaryKey"`
	TeamID      string    `gorm:"type:char(26);not null;index"`
	DisplayName string    `gorm:"type:varchar(255);not null"`
	CreatedAt   time.Time `gorm:"not null;precision:6"`
}

// Conversation groups ordered Messages and Runs for one User.
type Conversation struct {
	ID                   string         `gorm:"type:char(26);primaryKey"`
	UserID               string         `gorm:"type:char(26);not null;index:idx_conversations_user_deleted_latest,priority:1"`
	Title                string         `gorm:"type:varchar(255);not null"`
	NextMessageSeq       int64          `gorm:"not null;default:1"`
	NextRunSeq           int64          `gorm:"not null;default:1"`
	NextExecutableRunSeq int64          `gorm:"not null;default:1"`
	DeletedAt            gorm.DeletedAt `gorm:"index:idx_conversations_user_deleted_latest,priority:2"`
	CreatedAt            time.Time      `gorm:"not null;precision:6"`
	UpdatedAt            time.Time      `gorm:"not null;precision:6"`
	LatestMessageAt      *time.Time     `gorm:"precision:6;index:idx_conversations_user_deleted_latest,priority:3"`
}

// Message is a final or active user-visible conversation turn.
type Message struct {
	ID              string     `gorm:"type:char(26);primaryKey"`
	ConversationID  string     `gorm:"type:char(26);not null;uniqueIndex:uq_messages_conversation_seq,priority:1;uniqueIndex:uq_messages_conversation_client,priority:1"`
	Seq             int64      `gorm:"not null;uniqueIndex:uq_messages_conversation_seq,priority:2"`
	Role            string     `gorm:"type:varchar(16);not null"`
	Content         string     `gorm:"type:longtext;not null"`
	Status          string     `gorm:"type:varchar(16);not null"`
	RunID           *string    `gorm:"type:char(26);index"`
	ClientMessageID *string    `gorm:"type:char(26);uniqueIndex:uq_messages_conversation_client,priority:2"`
	CreatedAt       time.Time  `gorm:"not null;precision:6"`
	FinalizedAt     *time.Time `gorm:"precision:6"`
}

// Run is the durable Worker execution record and lease-fencing authority.
type Run struct {
	ID                  string         `gorm:"type:char(26);primaryKey"`
	ConversationID      string         `gorm:"type:char(26);not null;uniqueIndex:uq_runs_conversation_seq,priority:1;index:idx_runs_conversation_status,priority:1"`
	TriggerMessageID    string         `gorm:"type:char(26);not null;uniqueIndex"`
	QueueSeq            int64          `gorm:"not null;uniqueIndex:uq_runs_conversation_seq,priority:2"`
	Status              string         `gorm:"type:varchar(16);not null;index:idx_runs_claim,priority:1;index:idx_runs_conversation_status,priority:2"`
	Attempt             int            `gorm:"not null;default:0"`
	NextAttemptAt       time.Time      `gorm:"not null;precision:6;index:idx_runs_claim,priority:2"`
	LeaseOwner          *string        `gorm:"type:varchar(128)"`
	LeaseExpiresAt      *time.Time     `gorm:"precision:6;index:idx_runs_claim,priority:3"`
	ExecutionToken      int64          `gorm:"not null;default:0"`
	AgentConfigVersion  string         `gorm:"type:varchar(128);not null"`
	SkillsBundleVersion string         `gorm:"type:varchar(128);not null"`
	ModelConfig         datatypes.JSON `gorm:"type:json"`
	WorkspaceRef        datatypes.JSON `gorm:"type:json"`
	LatestCheckpointID  *string        `gorm:"type:char(26)"`
	NextEventSeq        int64          `gorm:"not null;default:1"`
	CancelRequestedAt   *time.Time     `gorm:"precision:6"`
	TerminalErrorCode   *string        `gorm:"type:varchar(128)"`
	StartedAt           *time.Time     `gorm:"precision:6"`
	FinishedAt          *time.Time     `gorm:"precision:6"`
	CreatedAt           time.Time      `gorm:"not null;precision:6"`
	UpdatedAt           time.Time      `gorm:"not null;precision:6"`
}

// RunStep is one user-explainable durable execution boundary.
type RunStep struct {
	ID          string     `gorm:"type:char(26);primaryKey"`
	RunID       string     `gorm:"type:char(26);not null;uniqueIndex:uq_run_steps_run_step,priority:1"`
	StepNo      int        `gorm:"not null;uniqueIndex:uq_run_steps_run_step,priority:2"`
	Kind        string     `gorm:"type:varchar(32);not null"`
	Status      string     `gorm:"type:varchar(16);not null"`
	SafeSummary string     `gorm:"type:text;not null"`
	StartedAt   *time.Time `gorm:"precision:6"`
	FinishedAt  *time.Time `gorm:"precision:6"`
	CreatedAt   time.Time  `gorm:"not null;precision:6"`
}

// ToolCall records a retriable, allowlisted tool invocation.
type ToolCall struct {
	ID             string         `gorm:"type:char(26);primaryKey"`
	RunID          string         `gorm:"type:char(26);not null;index"`
	StepNo         int            `gorm:"not null"`
	ServerKey      string         `gorm:"type:varchar(128);not null"`
	ToolName       string         `gorm:"type:varchar(128);not null"`
	Arguments      datatypes.JSON `gorm:"type:json"`
	ResultSummary  string         `gorm:"type:text;not null"`
	ArtifactID     *string        `gorm:"type:char(26)"`
	IdempotencyKey string         `gorm:"type:varchar(128);not null;uniqueIndex"`
	Status         string         `gorm:"type:varchar(16);not null"`
	CreatedAt      time.Time      `gorm:"not null;precision:6"`
	UpdatedAt      time.Time      `gorm:"not null;precision:6"`
}

// RunCheckpoint stores runtime state required to resume a durable Run.
type RunCheckpoint struct {
	ID                 string         `gorm:"type:char(26);primaryKey"`
	RunID              string         `gorm:"type:char(26);not null;uniqueIndex:uq_run_checkpoints_run_ordinal,priority:1"`
	Ordinal            int            `gorm:"not null;uniqueIndex:uq_run_checkpoints_run_ordinal,priority:2"`
	RuntimeName        string         `gorm:"type:varchar(128);not null"`
	StateSchemaVersion int            `gorm:"not null"`
	Payload            datatypes.JSON `gorm:"type:json"`
	CreatedAt          time.Time      `gorm:"not null;precision:6"`
}

// RunEvent is the durable replay source for a Run's SSE stream.
type RunEvent struct {
	ID          string         `gorm:"type:char(26);primaryKey"`
	RunID       string         `gorm:"type:char(26);not null;uniqueIndex:uq_run_events_run_seq,priority:1"`
	Seq         int64          `gorm:"not null;uniqueIndex:uq_run_events_run_seq,priority:2"`
	Type        string         `gorm:"type:varchar(64);not null"`
	SafePayload datatypes.JSON `gorm:"type:json"`
	CreatedAt   time.Time      `gorm:"not null;precision:6"`
}

// Artifact stores an immutable binary or textual result retained for a Run.
type Artifact struct {
	ID          string    `gorm:"type:char(26);primaryKey"`
	RunID       string    `gorm:"type:char(26);not null;index"`
	Kind        string    `gorm:"type:varchar(64);not null"`
	ContentType string    `gorm:"type:varchar(255);not null"`
	Content     []byte    `gorm:"type:longblob;not null"`
	SHA256      string    `gorm:"type:char(64);not null;index"`
	ByteSize    int64     `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null;precision:6"`
}

// All returns every durable model in foreign-key-safe creation order.
func All() []any {
	return []any{
		&User{},
		&Conversation{},
		&Message{},
		&Run{},
		&RunStep{},
		&ToolCall{},
		&RunCheckpoint{},
		&RunEvent{},
		&Artifact{},
	}
}
