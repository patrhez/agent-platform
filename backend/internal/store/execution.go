package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/model"
	"github.com/patrhez/agent-platform/backend/internal/query"
	"gorm.io/datatypes"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LoadRunExecution returns the claimed Run and its triggering user Message.
func (store *Store) LoadRunExecution(
	context context.Context,
	runID string,
	executionToken int64,
) (domain.RunExecution, error) {
	queries := query.Use(store.database)
	run, err := queries.Run.WithContext(context).
		Where(
			queries.Run.ID.Eq(runID),
			queries.Run.ExecutionToken.Eq(executionToken),
			queries.Run.Status.Eq(string(domain.RunStatusRunning)),
		).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.RunExecution{}, ErrLeaseLost
	}
	if err != nil {
		return domain.RunExecution{}, fmt.Errorf("load Run %s: %w", runID, err)
	}

	message, err := queries.Message.WithContext(context).
		Where(queries.Message.ID.Eq(run.TriggerMessageID)).
		First()
	if err != nil {
		return domain.RunExecution{}, fmt.Errorf("load trigger Message for Run %s: %w", runID, err)
	}
	messages, err := queries.Message.WithContext(context).
		Where(
			queries.Message.ConversationID.Eq(run.ConversationID),
			queries.Message.Seq.Lte(message.Seq),
			queries.Message.Status.Eq("final"),
			queries.Message.Role.In("user", "assistant"),
		).
		Order(queries.Message.Seq.Asc()).
		Find()
	if err != nil {
		return domain.RunExecution{}, fmt.Errorf("load Conversation history for Run %s: %w", runID, err)
	}
	if len(messages) == 0 || messages[len(messages)-1].ID != run.TriggerMessageID {
		return domain.RunExecution{}, fmt.Errorf("Conversation history for Run %s does not end at trigger Message", runID)
	}
	return domain.RunExecution{Run: domainRun(run), Messages: domainMessages(messages)}, nil
}

// LatestCheckpoint returns the latest durable runtime state for a Run.
func (store *Store) LatestCheckpoint(
	context context.Context,
	runID string,
) (*domain.Checkpoint, error) {
	queries := query.Use(store.database)
	checkpoint, err := queries.RunCheckpoint.WithContext(context).
		Where(queries.RunCheckpoint.RunID.Eq(runID)).
		Order(queries.RunCheckpoint.Ordinal.Desc()).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load latest Checkpoint for Run %s: %w", runID, err)
	}
	return domainCheckpoint(checkpoint), nil
}

// PersistBoundary stores one fenced execution boundary and its safe events.
func (store *Store) PersistBoundary(
	context context.Context,
	boundary domain.ExecutionBoundary,
) ([]domain.RunEvent, error) {
	queries := query.Use(store.database)
	var events []domain.RunEvent
	err := queries.Transaction(func(transaction *query.Query) error {
		run, err := lockOwnedRun(context, transaction, boundary.RunID, boundary.ExecutionToken)
		if err != nil {
			return err
		}
		if err := persistStep(context, transaction, boundary, time.Now().UTC()); err != nil {
			return err
		}
		if err := persistToolCall(context, transaction, boundary.ToolCall); err != nil {
			return err
		}
		if err := persistCheckpoint(context, transaction, run, boundary.Checkpoint); err != nil {
			return err
		}
		events, err = persistEvents(context, transaction, run, boundary.Events)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("persist Run boundary %s: %w", boundary.RunID, err)
	}
	return events, nil
}

// AppendRunEvents appends fenced Run events without creating execution boundaries or Checkpoints.
func (store *Store) AppendRunEvents(
	context context.Context,
	runID string,
	executionToken int64,
	values []domain.RunEvent,
) ([]domain.RunEvent, error) {
	queries := query.Use(store.database)
	var events []domain.RunEvent
	err := queries.Transaction(func(transaction *query.Query) error {
		run, err := lockOwnedRun(context, transaction, runID, executionToken)
		if err != nil {
			return err
		}
		events, err = persistEvents(context, transaction, run, values)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("append Run events for %s: %w", runID, err)
	}
	return events, nil
}

func lockOwnedRun(
	context context.Context,
	transaction *query.Query,
	runID string,
	executionToken int64,
) (*model.Run, error) {
	run, err := transaction.Run.WithContext(context).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			transaction.Run.ID.Eq(runID),
			transaction.Run.ExecutionToken.Eq(executionToken),
			transaction.Run.Status.Eq(string(domain.RunStatusRunning)),
		).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLeaseLost
	}
	if err != nil {
		return nil, fmt.Errorf("lock owned Run %s: %w", runID, err)
	}
	return run, nil
}

func persistStep(
	context context.Context,
	transaction *query.Query,
	boundary domain.ExecutionBoundary,
	now time.Time,
) error {
	if boundary.StepNo < 1 || boundary.Kind == "" {
		return nil
	}
	step, err := transaction.RunStep.WithContext(context).
		Where(
			transaction.RunStep.RunID.Eq(boundary.RunID),
			transaction.RunStep.StepNo.Eq(boundary.StepNo),
		).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return createStep(context, transaction, boundary, now)
	}
	if err != nil {
		return fmt.Errorf("load Run Step %d: %w", boundary.StepNo, err)
	}
	_, err = transaction.RunStep.WithContext(context).
		Where(transaction.RunStep.ID.Eq(step.ID)).
		UpdateSimple(
			transaction.RunStep.Kind.Value(boundary.Kind),
			transaction.RunStep.Status.Value("completed"),
			transaction.RunStep.SafeSummary.Value(boundary.SafeSummary),
			transaction.RunStep.FinishedAt.Value(now),
		)
	if err != nil {
		return fmt.Errorf("update Run Step %d: %w", boundary.StepNo, err)
	}
	return nil
}

func createStep(
	context context.Context,
	transaction *query.Query,
	boundary domain.ExecutionBoundary,
	now time.Time,
) error {
	step := &model.RunStep{
		ID:          ulid.Make().String(),
		RunID:       boundary.RunID,
		StepNo:      boundary.StepNo,
		Kind:        boundary.Kind,
		Status:      "completed",
		SafeSummary: boundary.SafeSummary,
		StartedAt:   &now,
		FinishedAt:  &now,
		CreatedAt:   now,
	}
	if err := transaction.RunStep.WithContext(context).Create(step); err != nil {
		return fmt.Errorf("create Run Step %d: %w", boundary.StepNo, err)
	}
	return nil
}

func persistToolCall(
	context context.Context,
	transaction *query.Query,
	call *domain.ToolCall,
) error {
	if call == nil {
		return nil
	}
	toolCall := &model.ToolCall{
		ID:             call.ID,
		RunID:          call.RunID,
		StepNo:         call.StepNo,
		ServerKey:      call.ServerKey,
		ToolName:       call.ToolName,
		Arguments:      datatypes.JSON(call.Arguments),
		ResultSummary:  call.ResultSummary,
		ArtifactID:     optionalString(call.ArtifactID),
		IdempotencyKey: call.IdempotencyKey,
		Status:         call.Status,
		CreatedAt:      call.CreatedAt,
		UpdatedAt:      call.UpdatedAt,
	}
	if err := transaction.ToolCall.WithContext(context).Save(toolCall); err != nil {
		return fmt.Errorf("save Tool call %s: %w", call.ID, err)
	}
	return nil
}

func persistCheckpoint(
	context context.Context,
	transaction *query.Query,
	run *model.Run,
	checkpoint *domain.Checkpoint,
) error {
	if checkpoint == nil {
		return nil
	}
	modelCheckpoint := &model.RunCheckpoint{
		ID:                 checkpoint.ID,
		RunID:              checkpoint.RunID,
		Ordinal:            checkpoint.Ordinal,
		RuntimeName:        checkpoint.RuntimeName,
		StateSchemaVersion: checkpoint.StateSchemaVersion,
		Payload:            datatypes.JSON(checkpoint.Payload),
		CreatedAt:          checkpoint.CreatedAt,
	}
	if err := transaction.RunCheckpoint.WithContext(context).Create(modelCheckpoint); err != nil {
		return fmt.Errorf("create Checkpoint %s: %w", checkpoint.ID, err)
	}
	_, err := transaction.Run.WithContext(context).
		Where(transaction.Run.ID.Eq(run.ID), transaction.Run.ExecutionToken.Eq(run.ExecutionToken)).
		UpdateSimple(transaction.Run.LatestCheckpointID.Value(checkpoint.ID))
	if err != nil {
		return fmt.Errorf("set latest Checkpoint for Run %s: %w", run.ID, err)
	}
	return nil
}

func persistEvents(
	context context.Context,
	transaction *query.Query,
	run *model.Run,
	events []domain.RunEvent,
) ([]domain.RunEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	persisted := make([]domain.RunEvent, 0, len(events))
	for index := range events {
		event := events[index]
		event.ID = ulid.Make().String()
		event.RunID = run.ID
		event.Seq = run.NextEventSeq + int64(index)
		if event.CreatedAt.IsZero() {
			event.CreatedAt = now
		}
		row := &model.RunEvent{
			ID:          event.ID,
			RunID:       event.RunID,
			Seq:         event.Seq,
			Type:        event.Type,
			SafePayload: datatypes.JSON(event.SafePayload),
			CreatedAt:   event.CreatedAt,
		}
		if err := transaction.RunEvent.WithContext(context).Create(row); err != nil {
			return nil, fmt.Errorf("create Run event %d: %w", event.Seq, err)
		}
		persisted = append(persisted, event)
	}
	_, err := transaction.Run.WithContext(context).
		Where(transaction.Run.ID.Eq(run.ID), transaction.Run.ExecutionToken.Eq(run.ExecutionToken)).
		UpdateSimple(transaction.Run.NextEventSeq.Value(run.NextEventSeq + int64(len(events))))
	if err != nil {
		return nil, fmt.Errorf("advance event sequence for Run %s: %w", run.ID, err)
	}
	return persisted, nil
}

// CompleteRun writes the terminal state, final assistant Message, and terminal event.
func (store *Store) CompleteRun(context context.Context, completion domain.RunCompletion) ([]domain.RunEvent, error) {
	if !isTerminalStatus(completion.Status) {
		return nil, fmt.Errorf("complete Run %s: non-terminal status %q", completion.RunID, completion.Status)
	}
	queries := query.Use(store.database)
	var events []domain.RunEvent
	err := queries.Transaction(func(transaction *query.Query) error {
		run, err := lockOwnedRun(context, transaction, completion.RunID, completion.ExecutionToken)
		if err != nil {
			return err
		}
		conversation, err := lockConversation(context, transaction, run.ConversationID)
		if err != nil {
			return err
		}
		if err := createAssistantMessage(context, transaction, conversation, run, completion.AssistantText); err != nil {
			return err
		}
		if err := completeRunRecord(context, transaction, run, completion); err != nil {
			return err
		}
		events, err = persistEvents(context, transaction, run, []domain.RunEvent{completion.Event})
		if err != nil {
			return err
		}
		return advanceExecutableRun(context, transaction, conversation)
	})
	if err != nil {
		return nil, fmt.Errorf("complete Run %s: %w", completion.RunID, err)
	}
	return events, nil
}

func createAssistantMessage(
	context context.Context,
	transaction *query.Query,
	conversation *model.Conversation,
	run *model.Run,
	content string,
) error {
	if content == "" {
		return nil
	}
	now := time.Now().UTC()
	message := &model.Message{
		ID:             ulid.Make().String(),
		ConversationID: conversation.ID,
		Seq:            conversation.NextMessageSeq,
		Role:           "assistant",
		Content:        content,
		Status:         "final",
		RunID:          &run.ID,
		CreatedAt:      now,
		FinalizedAt:    &now,
	}
	if err := transaction.Message.WithContext(context).Create(message); err != nil {
		return fmt.Errorf("create assistant Message: %w", err)
	}
	_, err := transaction.Conversation.WithContext(context).
		Where(transaction.Conversation.ID.Eq(conversation.ID)).
		UpdateSimple(
			transaction.Conversation.NextMessageSeq.Value(conversation.NextMessageSeq+1),
			transaction.Conversation.UpdatedAt.Value(now),
			transaction.Conversation.LatestMessageAt.Value(now),
		)
	if err != nil {
		return fmt.Errorf("advance assistant Message sequence: %w", err)
	}
	return nil
}

func completeRunRecord(
	context context.Context,
	transaction *query.Query,
	run *model.Run,
	completion domain.RunCompletion,
) error {
	now := time.Now().UTC()
	updates := []field.AssignExpr{
		transaction.Run.Status.Value(string(completion.Status)),
		transaction.Run.LeaseOwner.Null(),
		transaction.Run.LeaseExpiresAt.Null(),
		transaction.Run.FinishedAt.Value(now),
		transaction.Run.UpdatedAt.Value(now),
	}
	if completion.ErrorCode != "" {
		updates = append(updates, transaction.Run.TerminalErrorCode.Value(completion.ErrorCode))
	}
	_, err := transaction.Run.WithContext(context).
		Where(transaction.Run.ID.Eq(run.ID), transaction.Run.ExecutionToken.Eq(run.ExecutionToken)).
		UpdateSimple(updates...)
	if err != nil {
		return fmt.Errorf("complete Run record %s: %w", run.ID, err)
	}
	return nil
}

func advanceExecutableRun(
	context context.Context,
	transaction *query.Query,
	conversation *model.Conversation,
) error {
	return realignExecutableRunSeq(context, transaction, conversation)
}

// IsCancellationRequested reports whether a Run has an outstanding cancellation request.
func (store *Store) IsCancellationRequested(context context.Context, runID string) (bool, error) {
	queries := query.Use(store.database)
	run, err := queries.Run.WithContext(context).Where(queries.Run.ID.Eq(runID)).First()
	if err != nil {
		return false, fmt.Errorf("load cancellation state for Run %s: %w", runID, err)
	}
	return run.CancelRequestedAt != nil, nil
}

func domainCheckpoint(checkpoint *model.RunCheckpoint) *domain.Checkpoint {
	return &domain.Checkpoint{
		ID:                 checkpoint.ID,
		RunID:              checkpoint.RunID,
		Ordinal:            checkpoint.Ordinal,
		RuntimeName:        checkpoint.RuntimeName,
		StateSchemaVersion: checkpoint.StateSchemaVersion,
		Payload:            json.RawMessage(checkpoint.Payload),
		CreatedAt:          checkpoint.CreatedAt,
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func isTerminalStatus(status domain.RunStatus) bool {
	return status == domain.RunStatusSucceeded || status == domain.RunStatusFailed ||
		status == domain.RunStatusCancelled
}
