package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/model"
	"github.com/patrhez/agent-platform/backend/internal/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReplayGCResult reports one bounded provider replay cleanup pass.
type ReplayGCResult struct {
	DeletedHeads   int
	DeletedReplays int
}

// CollectProviderReplays deletes expired heads and unreferenced replay rows in bounded batches.
func (store *Store) CollectProviderReplays(
	ctx context.Context,
	now time.Time,
	batchSize int,
) (ReplayGCResult, error) {
	if batchSize < 1 {
		return ReplayGCResult{}, fmt.Errorf("provider replay GC batch size must be positive")
	}
	result := ReplayGCResult{}
	heads, err := store.expiredContinuationHeads(ctx, now, batchSize)
	if err != nil {
		return result, err
	}
	for _, head := range heads {
		deleted, deleteErr := store.deleteExpiredContinuationHead(ctx, head, now)
		if deleteErr != nil {
			return result, deleteErr
		}
		if deleted {
			result.DeletedHeads++
		}
	}
	replays, err := store.replayCandidates(ctx, batchSize)
	if err != nil {
		return result, err
	}
	for _, replay := range replays {
		deleted, deleteErr := store.deleteUnreferencedReplay(ctx, replay.CheckpointID)
		if deleteErr != nil {
			return result, deleteErr
		}
		if deleted {
			result.DeletedReplays++
		}
	}
	return result, nil
}

func (store *Store) expiredContinuationHeads(
	ctx context.Context,
	now time.Time,
	batchSize int,
) ([]*model.ConversationProviderContinuation, error) {
	queries := query.Use(store.database)
	heads, err := queries.ConversationProviderContinuation.WithContext(ctx).
		Where(queries.ConversationProviderContinuation.ExpiresAt.Lte(now)).
		Order(queries.ConversationProviderContinuation.ExpiresAt.Asc()).
		Limit(batchSize).
		Find()
	if err != nil {
		return nil, fmt.Errorf("list expired provider continuation heads: %w", err)
	}
	return heads, nil
}

func (store *Store) deleteExpiredContinuationHead(
	ctx context.Context,
	candidate *model.ConversationProviderContinuation,
	now time.Time,
) (bool, error) {
	deleted := false
	err := query.Use(store.database).Transaction(func(transaction *query.Query) error {
		head, err := transaction.ConversationProviderContinuation.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				transaction.ConversationProviderContinuation.ConversationID.Eq(candidate.ConversationID),
				transaction.ConversationProviderContinuation.HeadVersion.Eq(candidate.HeadVersion),
			).
			First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock provider continuation head %s: %w", candidate.ConversationID, err)
		}
		if head.ExpiresAt.After(now) {
			return nil
		}
		run, err := transaction.Run.WithContext(ctx).
			Where(transaction.Run.ID.Eq(head.SourceRunID)).
			First()
		if err != nil {
			return fmt.Errorf("load source Run %s for provider continuation GC: %w", head.SourceRunID, err)
		}
		if !isTerminalStatus(domain.RunStatus(run.Status)) {
			return nil
		}
		info, err := transaction.ConversationProviderContinuation.WithContext(ctx).Delete(head)
		if err != nil {
			return fmt.Errorf("delete expired provider continuation head %s: %w", head.ConversationID, err)
		}
		deleted = info.RowsAffected == 1
		return nil
	})
	return deleted, err
}

func (store *Store) replayCandidates(
	ctx context.Context,
	batchSize int,
) ([]*model.RunCheckpointReplay, error) {
	rows := make([]*model.RunCheckpointReplay, 0, batchSize)
	database := store.database.WithContext(ctx)
	headSubquery := database.
		Model(&model.ConversationProviderContinuation{}).
		Select("1").
		Where("conversation_provider_continuations.checkpoint_id = run_checkpoint_replays.checkpoint_id")
	liveRunSubquery := database.
		Model(&model.Run{}).
		Select("1").
		Where("runs.latest_checkpoint_id = run_checkpoint_replays.checkpoint_id").
		Where("runs.status NOT IN ?", terminalRunStatuses())
	err := database.
		Where("NOT EXISTS (?)", headSubquery).
		Where("NOT EXISTS (?)", liveRunSubquery).
		Order("run_checkpoint_replays.created_at ASC").
		Order("run_checkpoint_replays.checkpoint_id ASC").
		Limit(batchSize).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list unreferenced provider replay candidates: %w", err)
	}
	return rows, nil
}

func terminalRunStatuses() []string {
	return []string{
		string(domain.RunStatusSucceeded),
		string(domain.RunStatusFailed),
		string(domain.RunStatusCancelled),
	}
}

func (store *Store) deleteUnreferencedReplay(
	ctx context.Context,
	checkpointID string,
) (bool, error) {
	deleted := false
	err := query.Use(store.database).Transaction(func(transaction *query.Query) error {
		replay, err := transaction.RunCheckpointReplay.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(transaction.RunCheckpointReplay.CheckpointID.Eq(checkpointID)).
			First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock provider replay %s: %w", checkpointID, err)
		}
		referenced, err := replayHasLiveReference(ctx, transaction, replay)
		if err != nil || referenced {
			return err
		}
		info, err := transaction.RunCheckpointReplay.WithContext(ctx).Delete(replay)
		if err != nil {
			return fmt.Errorf("delete provider replay %s: %w", checkpointID, err)
		}
		deleted = info.RowsAffected == 1
		return nil
	})
	return deleted, err
}

func replayHasLiveReference(
	ctx context.Context,
	transaction *query.Query,
	replay *model.RunCheckpointReplay,
) (bool, error) {
	headCount, err := transaction.ConversationProviderContinuation.WithContext(ctx).
		Where(transaction.ConversationProviderContinuation.CheckpointID.Eq(replay.CheckpointID)).
		Count()
	if err != nil {
		return false, fmt.Errorf("count provider continuation references for %s: %w", replay.CheckpointID, err)
	}
	if headCount > 0 {
		return true, nil
	}
	run, err := transaction.Run.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(transaction.Run.ID.Eq(replay.RunID)).
		First()
	if err != nil {
		return false, fmt.Errorf("load Run %s for provider replay GC: %w", replay.RunID, err)
	}
	return run.LatestCheckpointID != nil && *run.LatestCheckpointID == replay.CheckpointID &&
		!isTerminalStatus(domain.RunStatus(run.Status)), nil
}
