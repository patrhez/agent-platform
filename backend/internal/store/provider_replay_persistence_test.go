package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPersistBoundaryCommitsRequiredReplayWithCheckpoint(t *testing.T) {
	t.Parallel()
	store, mock, closeDatabase := newReplayPersistenceTestStore(t)
	defer closeDatabase()
	boundary := replayPersistenceBoundary("run-1", "conversation-1", 7)

	expectOwnedRunLock(mock, boundary.RunID, boundary.ExecutionToken)
	mock.ExpectExec("INSERT INTO `run_checkpoints`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `run_checkpoint_replays`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `runs` SET .*latest_checkpoint_id").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if _, err := store.PersistBoundary(context.Background(), boundary); err != nil {
		t.Fatalf("PersistBoundary() error = %v", err)
	}
	assertSQLExpectations(t, mock)
}

func TestPersistBoundaryRollsBackWhenReplayInsertFails(t *testing.T) {
	t.Parallel()
	store, mock, closeDatabase := newReplayPersistenceTestStore(t)
	defer closeDatabase()
	boundary := replayPersistenceBoundary("run-1", "conversation-1", 7)

	expectOwnedRunLock(mock, boundary.RunID, boundary.ExecutionToken)
	mock.ExpectExec("INSERT INTO `run_checkpoints`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `run_checkpoint_replays`").
		WillReturnError(errors.New("injected replay insert failure"))
	mock.ExpectRollback()

	if _, err := store.PersistBoundary(context.Background(), boundary); err == nil {
		t.Fatal("PersistBoundary() error = nil, want replay insertion failure")
	}
	assertSQLExpectations(t, mock)
}

func TestCompleteRunAdvancesConversationHeadInTerminalTransaction(t *testing.T) {
	t.Parallel()
	store, mock, closeDatabase := newReplayPersistenceTestStore(t)
	defer closeDatabase()
	completion := replayRunCompletion(domain.RunStatusSucceeded)

	expectTerminalTransactionPrefix(mock, completion)
	mock.ExpectQuery("SELECT .*FROM `conversation_provider_continuations`.*LIMIT").
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `conversation_provider_continuations`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectExecutableRunRealignment(mock)
	mock.ExpectCommit()

	if _, err := store.CompleteRun(context.Background(), completion); err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}
	assertSQLExpectations(t, mock)
}

func TestFailedRunDoesNotAdvanceConversationHead(t *testing.T) {
	t.Parallel()
	store, mock, closeDatabase := newReplayPersistenceTestStore(t)
	defer closeDatabase()
	completion := replayRunCompletion(domain.RunStatusFailed)

	expectTerminalTransactionPrefix(mock, completion)
	expectExecutableRunRealignment(mock)
	mock.ExpectCommit()

	if _, err := store.CompleteRun(context.Background(), completion); err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}
	assertSQLExpectations(t, mock)
}

func newReplayPersistenceTestStore(
	t *testing.T,
) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn: sqlDatabase, SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		sqlDatabase.Close()
		t.Fatalf("open GORM: %v", err)
	}
	mock.ExpectBegin()
	return New(database), mock, func() { _ = sqlDatabase.Close() }
}

func replayPersistenceBoundary(
	runID string,
	conversationID string,
	executionToken int64,
) domain.ExecutionBoundary {
	now := time.Now().UTC()
	checkpointID := "checkpoint-1"
	return domain.ExecutionBoundary{
		RunID: runID, ExecutionToken: executionToken,
		Checkpoint: &domain.Checkpoint{
			ID: checkpointID, RunID: runID, Ordinal: 1, RuntimeName: "eino-react",
			StateSchemaVersion: 1, Payload: []byte(`{"messages":[]}`),
			ProviderReplayRequired: true, CreatedAt: now,
			ProviderReplay: &domain.ProviderReplay{
				CheckpointID: checkpointID, RunID: runID, ConversationID: conversationID,
				Provider: "openai", APIMode: "responses", Model: "gpt-5",
				ContinuationScope: "conversation", ArtifactSchemaVersion: 1,
				AdapterVersion: "openai-responses/v1", Encoding: "json", Compression: "none",
				EncryptionAlgorithm: "AES-256-GCM", KeyID: "active", Nonce: make([]byte, 12),
				Ciphertext: []byte("ciphertext"), CreatedAt: now,
			},
		},
	}
}

func replayRunCompletion(status domain.RunStatus) domain.RunCompletion {
	boundary := replayPersistenceBoundary("run-1", "conversation-1", 7)
	return domain.RunCompletion{
		RunID: boundary.RunID, ExecutionToken: boundary.ExecutionToken, Status: status,
		Event:            domain.RunEvent{Type: "run.completed", SafePayload: []byte(`{}`)},
		TerminalBoundary: &boundary,
	}
}

func expectOwnedRunLock(mock sqlmock.Sqlmock, runID string, executionToken int64) {
	mock.ExpectQuery("SELECT .*FROM `runs`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "execution_token", "status", "next_event_seq",
		}).AddRow(runID, "conversation-1", executionToken, string(domain.RunStatusRunning), int64(1)))
}

func expectTerminalTransactionPrefix(mock sqlmock.Sqlmock, completion domain.RunCompletion) {
	expectOwnedRunLock(mock, completion.RunID, completion.ExecutionToken)
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "next_message_seq", "next_run_seq", "next_executable_run_seq",
		}).AddRow("conversation-1", int64(2), int64(2), int64(1)))
	mock.ExpectExec("INSERT INTO `run_checkpoints`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `run_checkpoint_replays`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `runs` SET .*latest_checkpoint_id").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `runs` SET .*status").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `run_events`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `runs` SET .*next_event_seq").WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectExecutableRunRealignment(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT .*queue_seq.*FROM `runs`.*LIMIT").WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("UPDATE `conversations` SET .*next_executable_run_seq").
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func assertSQLExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
