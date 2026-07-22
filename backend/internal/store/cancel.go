package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/model"
	"github.com/patrhez/agent-platform/backend/internal/query"
	"gorm.io/gorm"
)

// CancelActiveRuns cancels every non-terminal Run in a user-owned Conversation.
// Queued and waiting Runs are terminalized immediately; running Runs only get
// cancel_requested_at. Returns durable run.cancelled events for SSE wake-ups.
func (store *Store) CancelActiveRuns(
	ctx context.Context,
	userID string,
	conversationID string,
) ([]domain.RunEvent, error) {
	queries := query.Use(store.database)
	if _, err := findOwnedConversation(ctx, queries, userID, conversationID); err != nil {
		return nil, err
	}
	var events []domain.RunEvent
	err := queries.Transaction(func(transaction *query.Query) error {
		conversation, err := lockConversation(ctx, transaction, conversationID)
		if err != nil {
			return err
		}
		events, err = cancelActiveRunsInTx(ctx, transaction, conversation)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("cancel active Runs: %w", err)
	}
	return events, nil
}

func cancelActiveRunsInTx(
	ctx context.Context,
	transaction *query.Query,
	conversation *model.Conversation,
) ([]domain.RunEvent, error) {
	runs, err := transaction.Run.WithContext(ctx).
		Where(
			transaction.Run.ConversationID.Eq(conversation.ID),
			transaction.Run.Status.In(
				string(domain.RunStatusQueued),
				string(domain.RunStatusRunning),
				string(domain.RunStatusWaiting),
			),
		).
		Order(transaction.Run.QueueSeq.Asc()).
		Find()
	if err != nil {
		return nil, fmt.Errorf("list active Runs for Conversation %s: %w", conversation.ID, err)
	}

	var events []domain.RunEvent
	now := time.Now().UTC()
	for _, run := range runs {
		status := domain.RunStatus(run.Status)
		if status == domain.RunStatusRunning {
			if err := requestRunCancellation(ctx, transaction, run, now); err != nil {
				return nil, err
			}
			continue
		}
		cancelled, err := terminalizeRunCancelled(ctx, transaction, run, now)
		if err != nil {
			return nil, err
		}
		events = append(events, cancelled...)
	}
	if err := realignExecutableRunSeq(ctx, transaction, conversation); err != nil {
		return nil, err
	}
	return events, nil
}

func requestRunCancellation(
	ctx context.Context,
	transaction *query.Query,
	run *model.Run,
	now time.Time,
) error {
	if run.CancelRequestedAt != nil {
		return nil
	}
	_, err := transaction.Run.WithContext(ctx).
		Where(
			transaction.Run.ID.Eq(run.ID),
			transaction.Run.Status.Eq(string(domain.RunStatusRunning)),
		).
		UpdateSimple(
			transaction.Run.CancelRequestedAt.Value(now),
			transaction.Run.UpdatedAt.Value(now),
		)
	if err != nil {
		return fmt.Errorf("request cancellation for Run %s: %w", run.ID, err)
	}
	run.CancelRequestedAt = &now
	run.UpdatedAt = now
	return nil
}

func terminalizeRunCancelled(
	ctx context.Context,
	transaction *query.Query,
	run *model.Run,
	now time.Time,
) ([]domain.RunEvent, error) {
	errorCode := "cancelled"
	errorMessage := "Run cancelled."
	result, err := transaction.Run.WithContext(ctx).
		Where(
			transaction.Run.ID.Eq(run.ID),
			transaction.Run.Status.In(
				string(domain.RunStatusQueued),
				string(domain.RunStatusWaiting),
			),
		).
		UpdateSimple(
			transaction.Run.Status.Value(string(domain.RunStatusCancelled)),
			transaction.Run.LeaseOwner.Null(),
			transaction.Run.LeaseExpiresAt.Null(),
			transaction.Run.CancelRequestedAt.Value(now),
			transaction.Run.TerminalErrorCode.Value(errorCode),
			transaction.Run.TerminalErrorMessage.Value(errorMessage),
			transaction.Run.FinishedAt.Value(now),
			transaction.Run.UpdatedAt.Value(now),
		)
	if err != nil {
		return nil, fmt.Errorf("terminalize cancelled Run %s: %w", run.ID, err)
	}
	if result.RowsAffected != 1 {
		return nil, nil
	}
	run.Status = string(domain.RunStatusCancelled)
	run.CancelRequestedAt = &now
	run.TerminalErrorCode = &errorCode
	run.TerminalErrorMessage = &errorMessage
	run.FinishedAt = &now
	run.UpdatedAt = now
	run.LeaseOwner = nil
	run.LeaseExpiresAt = nil

	payload, err := json.Marshal(map[string]string{"summary": errorMessage})
	if err != nil {
		return nil, fmt.Errorf("encode cancel event for Run %s: %w", run.ID, err)
	}
	return persistEvents(ctx, transaction, run, []domain.RunEvent{{
		Type:        "run.cancelled",
		SafePayload: payload,
		CreatedAt:   now,
	}})
}

func realignExecutableRunSeq(
	ctx context.Context,
	transaction *query.Query,
	conversation *model.Conversation,
) error {
	active, err := transaction.Run.WithContext(ctx).
		Select(transaction.Run.QueueSeq).
		Where(
			transaction.Run.ConversationID.Eq(conversation.ID),
			transaction.Run.Status.In(
				string(domain.RunStatusQueued),
				string(domain.RunStatusRunning),
				string(domain.RunStatusWaiting),
			),
		).
		Order(transaction.Run.QueueSeq.Asc()).
		First()
	next := conversation.NextRunSeq
	if err == nil {
		next = active.QueueSeq
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("find next executable Run for Conversation %s: %w", conversation.ID, err)
	}
	now := time.Now().UTC()
	_, err = transaction.Conversation.WithContext(ctx).
		Where(transaction.Conversation.ID.Eq(conversation.ID)).
		UpdateSimple(
			transaction.Conversation.NextExecutableRunSeq.Value(next),
			transaction.Conversation.UpdatedAt.Value(now),
		)
	if err != nil {
		return fmt.Errorf("realign executable Run sequence: %w", err)
	}
	conversation.NextExecutableRunSeq = next
	return nil
}
