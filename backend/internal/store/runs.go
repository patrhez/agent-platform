package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/model"
	"github.com/patrhez/agent-platform/backend/internal/query"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const claimLeaseDuration = 30 * time.Second

// ClaimNextRun leases the first currently executable queued Run for workerID.
func (store *Store) ClaimNextRun(
	context context.Context,
	workerID string,
	now time.Time,
) (domain.Run, bool, error) {
	queries := query.Use(store.database)
	var claimedRun *model.Run
	err := queries.Transaction(func(transaction *query.Query) error {
		candidate, found, err := selectClaimCandidate(context, transaction, now)
		if err != nil || !found {
			return err
		}
		claimedRun, err = claimRun(context, transaction, candidate, workerID, now)
		return err
	})
	if err != nil {
		return domain.Run{}, false, fmt.Errorf("claim next Run: %w", err)
	}
	if claimedRun == nil {
		return domain.Run{}, false, nil
	}
	return domainRun(claimedRun), true, nil
}

func selectClaimCandidate(
	context context.Context,
	transaction *query.Query,
	now time.Time,
) (*model.Run, bool, error) {
	run, err := transaction.Run.WithContext(context).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Join(
			transaction.Conversation,
			transaction.Run.ConversationID.EqCol(transaction.Conversation.ID),
		).
		Where(
			field.Or(
				transaction.Run.Status.Eq(string(domain.RunStatusQueued)),
				field.And(
					transaction.Run.Status.Eq(string(domain.RunStatusRunning)),
					transaction.Run.LeaseExpiresAt.Lt(now),
				),
			),
			transaction.Run.NextAttemptAt.Lte(now),
			transaction.Run.QueueSeq.EqCol(transaction.Conversation.NextExecutableRunSeq),
		).
		Order(transaction.Run.QueueSeq.Asc()).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("select next executable Run: %w", err)
	}
	return run, true, nil
}

func claimRun(
	context context.Context,
	transaction *query.Query,
	run *model.Run,
	workerID string,
	now time.Time,
) (*model.Run, error) {
	leaseUntil := now.Add(claimLeaseDuration)
	result, err := transaction.Run.WithContext(context).
		Where(
			transaction.Run.ID.Eq(run.ID),
			transaction.Run.ExecutionToken.Eq(run.ExecutionToken),
			field.Or(
				transaction.Run.Status.Eq(string(domain.RunStatusQueued)),
				field.And(
					transaction.Run.Status.Eq(string(domain.RunStatusRunning)),
					transaction.Run.LeaseExpiresAt.Lt(now),
				),
			),
		).
		UpdateSimple(
			transaction.Run.Status.Value(string(domain.RunStatusRunning)),
			transaction.Run.Attempt.Value(run.Attempt+1),
			transaction.Run.ExecutionToken.Value(run.ExecutionToken+1),
			transaction.Run.LeaseOwner.Value(workerID),
			transaction.Run.LeaseExpiresAt.Value(leaseUntil),
			transaction.Run.StartedAt.Value(now),
			transaction.Run.UpdatedAt.Value(now),
		)
	if err != nil {
		return nil, fmt.Errorf("claim Run %s: %w", run.ID, err)
	}
	if result.RowsAffected != 1 {
		return nil, ErrLeaseLost
	}

	run.Status = string(domain.RunStatusRunning)
	run.Attempt++
	run.ExecutionToken++
	run.LeaseOwner = &workerID
	run.LeaseExpiresAt = &leaseUntil
	run.StartedAt = &now
	run.UpdatedAt = now
	return run, nil
}

// RenewLease extends a running Run lease only when the token is still current.
func (store *Store) RenewLease(
	context context.Context,
	runID string,
	executionToken int64,
	leaseUntil time.Time,
) error {
	queries := query.Use(store.database)
	err := queries.Transaction(func(transaction *query.Query) error {
		result, err := transaction.Run.WithContext(context).
			Where(
				transaction.Run.ID.Eq(runID),
				transaction.Run.ExecutionToken.Eq(executionToken),
				transaction.Run.Status.Eq(string(domain.RunStatusRunning)),
			).
			UpdateSimple(
				transaction.Run.LeaseExpiresAt.Value(leaseUntil),
				transaction.Run.UpdatedAt.Value(time.Now().UTC()),
			)
		if err != nil {
			return fmt.Errorf("renew Run lease %s: %w", runID, err)
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("renew Run lease: %w", err)
	}
	return nil
}

func domainRun(run *model.Run) domain.Run {
	return domain.Run{
		ID:                  run.ID,
		ConversationID:      run.ConversationID,
		TriggerMessageID:    run.TriggerMessageID,
		QueueSeq:            run.QueueSeq,
		Status:              domain.RunStatus(run.Status),
		ExecutionToken:      run.ExecutionToken,
		LeaseOwner:          stringValue(run.LeaseOwner),
		LeaseExpiresAt:      run.LeaseExpiresAt,
		Attempt:             run.Attempt,
		AgentConfigVersion:  run.AgentConfigVersion,
		SkillsBundleVersion: run.SkillsBundleVersion,
		ErrorCode:           stringValue(run.TerminalErrorCode),
		FinishedAt:          run.FinishedAt,
		CreatedAt:           run.CreatedAt,
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
