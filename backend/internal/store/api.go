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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureUser creates the demo principal when it does not yet exist.
func (store *Store) EnsureUser(ctx context.Context, userID string, teamID string, displayName string) error {
	queries := query.Use(store.database)
	_, err := queries.User.WithContext(ctx).Where(queries.User.ID.Eq(userID)).First()
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("load User %s: %w", userID, err)
	}
	user := &model.User{ID: userID, TeamID: teamID, DisplayName: displayName, CreatedAt: time.Now().UTC()}
	if err := queries.User.WithContext(ctx).Create(user); err != nil {
		return fmt.Errorf("create User %s: %w", userID, err)
	}
	return nil
}

// CreateConversation creates an empty Conversation for an existing principal.
func (store *Store) CreateConversation(ctx context.Context, userID string, title string) (domain.Conversation, error) {
	queries := query.Use(store.database)
	conversation := newConversationRow(userID, title, time.Now().UTC())
	err := queries.Transaction(func(transaction *query.Query) error {
		if err := transaction.Conversation.WithContext(ctx).Create(conversation); err != nil {
			return fmt.Errorf("create Conversation: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Conversation{}, err
	}
	return domainConversation(conversation), nil
}

// CreateConversationWithFirstMessage creates a Conversation and its first user turn in one local transaction.
func (store *Store) CreateConversationWithFirstMessage(
	ctx context.Context,
	userID string,
	title string,
	clientMessageID string,
	content string,
	pinned domain.RunPins,
) (domain.Conversation, domain.Run, error) {
	queries := query.Use(store.database)
	now := time.Now().UTC()
	conversation := newConversationRow(userID, title, now)
	var createdRun *model.Run
	err := queries.Transaction(func(transaction *query.Query) error {
		if err := transaction.Conversation.WithContext(ctx).Create(conversation); err != nil {
			return fmt.Errorf("create Conversation: %w", err)
		}
		run, err := createMessageAndRun(
			ctx, transaction, conversation, conversation.ID, clientMessageID, content, pinned,
		)
		if err != nil {
			return err
		}
		createdRun = run
		conversation.NextMessageSeq++
		conversation.NextRunSeq++
		conversation.UpdatedAt = now
		conversation.LatestMessageAt = &now
		return nil
	})
	if err != nil {
		return domain.Conversation{}, domain.Run{}, fmt.Errorf("create Conversation with first Message: %w", err)
	}
	return domainConversation(conversation), domainRun(createdRun), nil
}

func newConversationRow(userID string, title string, now time.Time) *model.Conversation {
	return &model.Conversation{
		ID:                   ulid.Make().String(),
		UserID:               userID,
		Title:                title,
		NextMessageSeq:       1,
		NextRunSeq:           1,
		NextExecutableRunSeq: 1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

// ListConversations returns the current user's active Conversations in recent-first order.
func (store *Store) ListConversations(ctx context.Context, userID string) ([]domain.Conversation, error) {
	queries := query.Use(store.database)
	conversations, err := queries.Conversation.WithContext(ctx).
		Where(queries.Conversation.UserID.Eq(userID)).
		Order(queries.Conversation.LatestMessageAt.Desc(), queries.Conversation.CreatedAt.Desc()).
		Find()
	if err != nil {
		return nil, fmt.Errorf("list Conversations: %w", err)
	}
	result := make([]domain.Conversation, 0, len(conversations))
	for _, conversation := range conversations {
		result = append(result, domainConversation(conversation))
	}
	return result, nil
}

// GetConversation returns a user-owned Conversation and its durable chat snapshot.
func (store *Store) GetConversation(ctx context.Context, userID string, conversationID string) (domain.ConversationDetail, error) {
	queries := query.Use(store.database)
	conversation, err := findOwnedConversation(ctx, queries, userID, conversationID)
	if err != nil {
		return domain.ConversationDetail{}, err
	}
	messages, err := queries.Message.WithContext(ctx).
		Where(queries.Message.ConversationID.Eq(conversationID)).
		Order(queries.Message.Seq.Asc()).
		Find()
	if err != nil {
		return domain.ConversationDetail{}, fmt.Errorf("list Conversation Messages: %w", err)
	}
	runs, err := queries.Run.WithContext(ctx).
		Where(queries.Run.ConversationID.Eq(conversationID)).
		Order(queries.Run.QueueSeq.Asc()).
		Find()
	if err != nil {
		return domain.ConversationDetail{}, fmt.Errorf("list Conversation Runs: %w", err)
	}
	return domain.ConversationDetail{
		Conversation: domainConversation(conversation),
		Messages:     domainMessages(messages),
		Runs:         domainRuns(runs),
	}, nil
}

// DeleteConversation soft-deletes a user-owned Conversation from the normal UI.
func (store *Store) DeleteConversation(ctx context.Context, userID string, conversationID string) error {
	queries := query.Use(store.database)
	err := queries.Transaction(func(transaction *query.Query) error {
		conversation, err := lockConversation(ctx, transaction, conversationID)
		if err != nil {
			return err
		}
		if conversation.UserID != userID {
			return ErrUnauthorized
		}
		if _, err := transaction.Conversation.WithContext(ctx).Delete(conversation); err != nil {
			return fmt.Errorf("soft-delete Conversation %s: %w", conversationID, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete Conversation: %w", err)
	}
	return nil
}

// CreateUserMessageAndRunForUser authorizes and then creates one idempotent user turn.
func (store *Store) CreateUserMessageAndRunForUser(
	ctx context.Context,
	userID string,
	conversationID string,
	clientMessageID string,
	content string,
	mode domain.FollowUpMode,
	pinned domain.RunPins,
) (domain.Run, []domain.RunEvent, error) {
	queries := query.Use(store.database)
	if _, err := findOwnedConversation(ctx, queries, userID, conversationID); err != nil {
		return domain.Run{}, nil, err
	}
	run, events, err := store.CreateUserMessageAndRun(ctx, conversationID, clientMessageID, content, mode, pinned)
	if err != nil {
		return domain.Run{}, nil, err
	}
	return run, events, nil
}

// GetRunSnapshot returns the current safe status for a user-owned Run.
func (store *Store) GetRunSnapshot(ctx context.Context, userID string, runID string) (domain.RunSnapshot, error) {
	queries := query.Use(store.database)
	run, err := findOwnedRun(ctx, queries, userID, runID)
	if err != nil {
		return domain.RunSnapshot{}, err
	}
	message, err := queries.Message.WithContext(ctx).
		Where(queries.Message.RunID.Eq(run.ID), queries.Message.Role.Eq("assistant")).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return runSnapshot(run, ""), nil
	}
	if err != nil {
		return domain.RunSnapshot{}, fmt.Errorf("load final Message for Run %s: %w", runID, err)
	}
	return runSnapshot(run, message.ID), nil
}

// GetRunTrace returns the permission-checked safe trace for one Run.
func (store *Store) GetRunTrace(ctx context.Context, userID string, runID string) (domain.RunTrace, error) {
	queries := query.Use(store.database)
	if _, err := findOwnedRun(ctx, queries, userID, runID); err != nil {
		return domain.RunTrace{}, err
	}
	steps, err := queries.RunStep.WithContext(ctx).
		Where(queries.RunStep.RunID.Eq(runID)).
		Order(queries.RunStep.StepNo.Asc()).
		Find()
	if err != nil {
		return domain.RunTrace{}, fmt.Errorf("list Run Steps: %w", err)
	}
	calls, err := queries.ToolCall.WithContext(ctx).
		Where(queries.ToolCall.RunID.Eq(runID)).
		Order(queries.ToolCall.StepNo.Asc(), queries.ToolCall.CreatedAt.Asc()).
		Find()
	if err != nil {
		return domain.RunTrace{}, fmt.Errorf("list Tool calls: %w", err)
	}
	return domain.RunTrace{Steps: domainSteps(steps), ToolCalls: domainToolCalls(calls)}, nil
}

// ListRunEvents returns user-visible durable events after the supplied cursor.
func (store *Store) ListRunEvents(ctx context.Context, userID string, runID string, after int64) ([]domain.RunEvent, error) {
	queries := query.Use(store.database)
	if _, err := findOwnedRun(ctx, queries, userID, runID); err != nil {
		return nil, err
	}
	events, err := queries.RunEvent.WithContext(ctx).
		Where(queries.RunEvent.RunID.Eq(runID), queries.RunEvent.Seq.Gt(after)).
		Order(queries.RunEvent.Seq.Asc()).
		Find()
	if err != nil {
		return nil, fmt.Errorf("list Run events: %w", err)
	}
	result := make([]domain.RunEvent, 0, len(events))
	for _, event := range events {
		result = append(result, domain.RunEvent{
			ID: event.ID, RunID: event.RunID, Seq: event.Seq, Type: event.Type,
			SafePayload: json.RawMessage(event.SafePayload), CreatedAt: event.CreatedAt,
		})
	}
	return result, nil
}

// GetArtifact returns a user-owned immutable Artifact.
func (store *Store) GetArtifact(ctx context.Context, userID string, runID string, artifactID string) (domain.Artifact, error) {
	queries := query.Use(store.database)
	if _, err := findOwnedRun(ctx, queries, userID, runID); err != nil {
		return domain.Artifact{}, err
	}
	artifact, err := queries.Artifact.WithContext(ctx).
		Where(queries.Artifact.ID.Eq(artifactID), queries.Artifact.RunID.Eq(runID)).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Artifact{}, ErrArtifactNotFound
	}
	if err != nil {
		return domain.Artifact{}, fmt.Errorf("load Artifact %s: %w", artifactID, err)
	}
	return domainArtifact(artifact), nil
}

// RequestRunCancellation records an idempotent cancellation request for a user-owned Run.
func (store *Store) RequestRunCancellation(ctx context.Context, userID string, runID string) (domain.RunSnapshot, error) {
	queries := query.Use(store.database)
	var snapshot domain.RunSnapshot
	err := queries.Transaction(func(transaction *query.Query) error {
		run, err := transaction.Run.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(transaction.Run.ID.Eq(runID)).
			First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRunNotFound
		}
		if err != nil {
			return fmt.Errorf("lock Run %s: %w", runID, err)
		}
		conversation, err := lockConversation(ctx, transaction, run.ConversationID)
		if err != nil {
			return err
		}
		if conversation.UserID != userID {
			return ErrUnauthorized
		}
		if !isTerminalStatus(domain.RunStatus(run.Status)) && run.CancelRequestedAt == nil {
			now := time.Now().UTC()
			if _, err := transaction.Run.WithContext(ctx).
				Where(transaction.Run.ID.Eq(run.ID)).
				UpdateSimple(
					transaction.Run.CancelRequestedAt.Value(now),
					transaction.Run.UpdatedAt.Value(now),
				); err != nil {
				return fmt.Errorf("request cancellation for Run %s: %w", runID, err)
			}
			run.CancelRequestedAt = &now
			run.UpdatedAt = now
		}
		snapshot = runSnapshot(run, "")
		return nil
	})
	if err != nil {
		return domain.RunSnapshot{}, fmt.Errorf("request Run cancellation: %w", err)
	}
	return snapshot, nil
}

func findOwnedConversation(ctx context.Context, queries *query.Query, userID string, conversationID string) (*model.Conversation, error) {
	conversation, err := queries.Conversation.WithContext(ctx).
		Where(queries.Conversation.ID.Eq(conversationID)).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load Conversation %s: %w", conversationID, err)
	}
	if conversation.UserID != userID {
		return nil, ErrUnauthorized
	}
	return conversation, nil
}

func findOwnedRun(ctx context.Context, queries *query.Query, userID string, runID string) (*model.Run, error) {
	run, err := queries.Run.WithContext(ctx).Where(queries.Run.ID.Eq(runID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load Run %s: %w", runID, err)
	}
	if _, err := findOwnedConversation(ctx, queries, userID, run.ConversationID); err != nil {
		return nil, err
	}
	return run, nil
}

func domainConversation(conversation *model.Conversation) domain.Conversation {
	return domain.Conversation{
		ID: conversation.ID, UserID: conversation.UserID, Title: conversation.Title,
		CreatedAt: conversation.CreatedAt, UpdatedAt: conversation.UpdatedAt, LatestMessageAt: conversation.LatestMessageAt,
	}
}

func domainMessages(messages []*model.Message) []domain.Message {
	result := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		result = append(result, domain.Message{
			ID: message.ID, ConversationID: message.ConversationID, Seq: message.Seq, Role: message.Role,
			Content: message.Content, Status: message.Status, RunID: stringValue(message.RunID),
			CreatedAt: message.CreatedAt, FinalizedAt: message.FinalizedAt,
		})
	}
	return result
}

func domainRuns(runs []*model.Run) []domain.Run {
	result := make([]domain.Run, 0, len(runs))
	for _, run := range runs {
		result = append(result, domainRun(run))
	}
	return result
}

func runSnapshot(run *model.Run, finalMessageID string) domain.RunSnapshot {
	return domain.RunSnapshot{
		Run: domainRun(run), LatestEventSeq: run.NextEventSeq - 1, FinalMessageID: finalMessageID,
		ErrorCode: stringValue(run.TerminalErrorCode), FinishedAt: run.FinishedAt,
	}
}

func domainSteps(steps []*model.RunStep) []domain.RunStep {
	result := make([]domain.RunStep, 0, len(steps))
	for _, step := range steps {
		result = append(result, domain.RunStep{
			ID: step.ID, RunID: step.RunID, StepNo: step.StepNo, Kind: step.Kind, Status: step.Status,
			SafeSummary: step.SafeSummary, StartedAt: step.StartedAt, FinishedAt: step.FinishedAt, CreatedAt: step.CreatedAt,
		})
	}
	return result
}

func domainToolCalls(calls []*model.ToolCall) []domain.ToolCall {
	result := make([]domain.ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, domain.ToolCall{
			ID: call.ID, RunID: call.RunID, StepNo: call.StepNo, ServerKey: call.ServerKey, ToolName: call.ToolName,
			Arguments: json.RawMessage(call.Arguments), ResultSummary: call.ResultSummary, ArtifactID: stringValue(call.ArtifactID),
			IdempotencyKey: call.IdempotencyKey, Status: call.Status, CreatedAt: call.CreatedAt, UpdatedAt: call.UpdatedAt,
		})
	}
	return result
}

func domainArtifact(artifact *model.Artifact) domain.Artifact {
	return domain.Artifact{
		ID: artifact.ID, RunID: artifact.RunID, Kind: artifact.Kind, ContentType: artifact.ContentType,
		Content: artifact.Content, SHA256: artifact.SHA256, ByteSize: artifact.ByteSize, CreatedAt: artifact.CreatedAt,
	}
}
