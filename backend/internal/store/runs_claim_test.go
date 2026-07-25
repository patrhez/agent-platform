package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newClaimTestStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()

	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { sqlDatabase.Close() })

	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDatabase,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open GORM: %v", err)
	}
	return New(database), mock
}

func TestClaimNextRunsRejectsInvalidLimit(t *testing.T) {
	store, mock := newClaimTestStore(t)

	for _, limit := range []int{0, -1} {
		runs, err := store.ClaimNextRuns(context.Background(), "worker-a", time.Now().UTC(), limit)
		if err == nil {
			t.Fatalf("ClaimNextRuns(limit=%d) error = nil, want error", limit)
		}
		if runs != nil {
			t.Fatalf("ClaimNextRuns(limit=%d) runs = %#v, want nil", limit, runs)
		}
		if !strings.Contains(err.Error(), "claim limit must be >= 1") {
			t.Fatalf("ClaimNextRuns(limit=%d) error = %v, want limit validation", limit, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestClaimNextRunsClaimsMultipleRuns(t *testing.T) {
	store, mock := newClaimTestStore(t)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `runs`.*JOIN.*LIMIT.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "trigger_message_id", "queue_seq", "status", "attempt",
			"next_attempt_at", "execution_token", "agent_config_version", "skills_bundle_version",
			"next_event_seq", "created_at", "updated_at",
		}).
			AddRow("run-1", "conversation-1", "msg-1", int64(1), string(domain.RunStatusQueued), 0, now, int64(0), "agent", "skills", int64(1), now, now).
			AddRow("run-2", "conversation-2", "msg-2", int64(1), string(domain.RunStatusQueued), 0, now, int64(0), "agent", "skills", int64(1), now, now))
	mock.ExpectExec("UPDATE `runs` SET .*lease_owner.*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `runs` SET .*lease_owner.*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	runs, err := store.ClaimNextRuns(context.Background(), "worker-a", now, 2)
	if err != nil {
		t.Fatalf("ClaimNextRuns() error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("ClaimNextRuns() len = %d, want 2", len(runs))
	}
	if runs[0].ID != "run-1" || runs[0].ExecutionToken != 1 || runs[0].LeaseOwner != "worker-a" {
		t.Errorf("first claimed run = %#v, want run-1 with token 1 and worker-a lease", runs[0])
	}
	if runs[1].ID != "run-2" || runs[1].ExecutionToken != 1 || runs[1].LeaseOwner != "worker-a" {
		t.Errorf("second claimed run = %#v, want run-2 with token 1 and worker-a lease", runs[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestClaimNextRunsReturnsEmptyWhenNoCandidates(t *testing.T) {
	store, mock := newClaimTestStore(t)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `runs`.*JOIN.*LIMIT.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "trigger_message_id", "queue_seq", "status", "attempt",
			"next_attempt_at", "execution_token", "agent_config_version", "skills_bundle_version",
			"next_event_seq", "created_at", "updated_at",
		}))
	mock.ExpectCommit()

	runs, err := store.ClaimNextRuns(context.Background(), "worker-a", now, 3)
	if err != nil {
		t.Fatalf("ClaimNextRuns() error = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("ClaimNextRuns() = %#v, want empty slice", runs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
