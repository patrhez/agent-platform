package store

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const replayCandidateQueryPattern = "SELECT .*FROM `run_checkpoint_replays`.*" +
	"NOT EXISTS .*`conversation_provider_continuations`.*" +
	"NOT EXISTS .*`runs`.*runs.status NOT IN .*" +
	"ORDER BY run_checkpoint_replays.created_at ASC,run_checkpoint_replays.checkpoint_id ASC LIMIT \\?"

func TestContinuationResetRequired(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	base := &model.ConversationProviderContinuation{
		Provider: "openai", APIMode: "responses", Model: "gpt-5",
		AdapterVersion: "openai-responses/v1", ArtifactSchemaVersion: 1,
		ExpiresAt: now.Add(time.Hour),
	}
	replay := &domain.ProviderReplay{
		Provider: "openai", APIMode: "responses", Model: "gpt-5",
		AdapterVersion: "openai-responses/v1", ArtifactSchemaVersion: 1,
	}
	tests := []struct {
		name      string
		mutate    func(*model.ConversationProviderContinuation)
		wantReset bool
	}{
		{name: "compatible"},
		{name: "provider changed", mutate: func(value *model.ConversationProviderContinuation) {
			value.Provider = "other"
		}, wantReset: true},
		{name: "expired before run", mutate: func(value *model.ConversationProviderContinuation) {
			value.ExpiresAt = now.Add(-time.Second)
		}, wantReset: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := *base
			if test.mutate != nil {
				test.mutate(&value)
			}
			if got := continuationResetRequired(&value, replay, &now); got != test.wantReset {
				t.Fatalf("continuationResetRequired() = %t, want %t", got, test.wantReset)
			}
		})
	}
}

func TestReplayCandidatesExcludeLiveReferencesBeforeApplyingBatchLimit(t *testing.T) {
	t.Parallel()
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn: sqlDatabase, SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open GORM: %v", err)
	}

	mock.ExpectQuery(replayCandidateQueryPattern).
		WithArgs(
			string(domain.RunStatusSucceeded),
			string(domain.RunStatusFailed),
			string(domain.RunStatusCancelled),
			2,
		).
		WillReturnRows(sqlmock.NewRows([]string{"checkpoint_id", "run_id", "created_at"}).
			AddRow("orphan-checkpoint", "terminal-run", time.Now().UTC()))

	candidates, err := New(database).replayCandidates(context.Background(), 2)
	if err != nil {
		t.Fatalf("replayCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].CheckpointID != "orphan-checkpoint" {
		t.Fatalf("replayCandidates() = %#v, want the eligible orphan", candidates)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestCollectProviderReplaysMakesProgressAcrossBoundedPasses(t *testing.T) {
	t.Parallel()
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn: sqlDatabase, SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open GORM: %v", err)
	}
	now := time.Now().UTC()
	expectReplayGCDeletesCandidate(mock, now, "checkpoint-1", "run-1")
	expectReplayGCDeletesCandidate(mock, now, "checkpoint-2", "run-2")

	store := New(database)
	for pass := 1; pass <= 2; pass++ {
		result, collectErr := store.CollectProviderReplays(context.Background(), now, 1)
		if collectErr != nil {
			t.Fatalf("CollectProviderReplays() pass %d error = %v", pass, collectErr)
		}
		if result.DeletedReplays != 1 {
			t.Fatalf("CollectProviderReplays() pass %d = %#v", pass, result)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestDeleteUnreferencedReplayRetainsRowReferencedAfterSelection(t *testing.T) {
	t.Parallel()
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn: sqlDatabase, SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open GORM: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `run_checkpoint_replays`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"checkpoint_id", "run_id"}).
			AddRow("checkpoint-1", "run-1"))
	mock.ExpectQuery(`SELECT count\(\*\) FROM \x60conversation_provider_continuations\x60`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectCommit()

	deleted, err := New(database).deleteUnreferencedReplay(context.Background(), "checkpoint-1")
	if err != nil {
		t.Fatalf("deleteUnreferencedReplay() error = %v", err)
	}
	if deleted {
		t.Fatal("deleteUnreferencedReplay() deleted a newly referenced row")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func expectReplayGCDeletesCandidate(
	mock sqlmock.Sqlmock,
	now time.Time,
	checkpointID string,
	runID string,
) {
	mock.ExpectQuery("SELECT .*FROM `conversation_provider_continuations`.*expires_at.*ORDER BY.*LIMIT").
		WithArgs(now, 1).
		WillReturnRows(sqlmock.NewRows([]string{"conversation_id"}))
	mock.ExpectQuery(replayCandidateQueryPattern).
		WithArgs(
			string(domain.RunStatusSucceeded),
			string(domain.RunStatusFailed),
			string(domain.RunStatusCancelled),
			1,
		).
		WillReturnRows(sqlmock.NewRows([]string{"checkpoint_id", "run_id", "created_at"}).
			AddRow(checkpointID, runID, now))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `run_checkpoint_replays`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"checkpoint_id", "run_id"}).
			AddRow(checkpointID, runID))
	mock.ExpectQuery(`SELECT count\(\*\) FROM \x60conversation_provider_continuations\x60`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT .*FROM `runs`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "latest_checkpoint_id"}).
			AddRow(runID, string(domain.RunStatusSucceeded), checkpointID))
	mock.ExpectExec("DELETE FROM `run_checkpoint_replays`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}
