package store

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCancelActiveRunsTerminalizesQueuedAndRequestsRunning(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDatabase,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open GORM: %v", err)
	}

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT .*FROM `conversations`.*LIMIT").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "title", "next_message_seq", "next_run_seq", "next_executable_run_seq",
			"created_at", "updated_at",
		}).AddRow("conversation-1", "user-1", "Demo", int64(3), int64(3), int64(1), now, now))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "title", "next_message_seq", "next_run_seq", "next_executable_run_seq",
			"created_at", "updated_at",
		}).AddRow("conversation-1", "user-1", "Demo", int64(3), int64(3), int64(1), now, now))
	mock.ExpectQuery("SELECT .*FROM `runs`.*ORDER BY.*queue_seq").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "trigger_message_id", "queue_seq", "status", "attempt",
			"next_attempt_at", "execution_token", "agent_config_version", "skills_bundle_version",
			"next_event_seq", "created_at", "updated_at",
		}).
			AddRow("run-1", "conversation-1", "msg-1", int64(1), string(domain.RunStatusRunning), 1, now, int64(2), "agent", "skills", int64(1), now, now).
			AddRow("run-2", "conversation-1", "msg-2", int64(2), string(domain.RunStatusQueued), 0, now, int64(0), "agent", "skills", int64(1), now, now))
	mock.ExpectExec("UPDATE `runs` SET .*cancel_requested_at.*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `runs` SET `status`=\\?.*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `run_events`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `runs` SET .*next_event_seq.*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .*queue_seq.*FROM `runs`.*ORDER BY.*queue_seq").
		WillReturnRows(sqlmock.NewRows([]string{"queue_seq"}).AddRow(int64(1)))
	mock.ExpectExec("UPDATE `conversations` SET .*next_executable_run_seq.*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	events, err := New(database).CancelActiveRuns(context.Background(), "user-1", "conversation-1")
	if err != nil {
		t.Fatalf("CancelActiveRuns() error = %v", err)
	}
	if len(events) != 1 || events[0].Type != "run.cancelled" || events[0].RunID != "run-2" {
		t.Fatalf("CancelActiveRuns() events = %#v, want one run.cancelled for run-2", events)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
