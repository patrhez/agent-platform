package store

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAppendRunEventsPersistsOnlyEventsInTransaction(t *testing.T) {
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

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `runs`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"id", "execution_token", "status", "next_event_seq"}).
			AddRow("run-id", int64(7), string(domain.RunStatusRunning), int64(4)))
	mock.ExpectExec("INSERT INTO `run_events`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `runs` SET .*next_event_seq.*").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	values, err := New(database).AppendRunEvents(context.Background(), "run-id", 7, []domain.RunEvent{{
		Type:        "assistant.delta",
		SafePayload: []byte(`{"streamId":"run-id:1:1","offset":0,"text":"hello"}`),
	}})
	if err != nil {
		t.Fatalf("AppendRunEvents() error = %v", err)
	}
	if len(values) != 1 || values[0].Seq != 4 {
		t.Fatalf("AppendRunEvents() = %#v, want one event at sequence 4", values)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestLoadRunExecutionReturnsFinalConversationHistoryThroughTrigger(t *testing.T) {
	t.Parallel()

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

	mock.ExpectQuery("SELECT .*FROM `runs`.*LIMIT").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "trigger_message_id", "execution_token", "status",
		}).AddRow("run-2", "conversation-1", "message-3", int64(4), string(domain.RunStatusRunning)))
	mock.ExpectQuery("SELECT .*FROM `messages`.*LIMIT").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "seq", "role", "content", "status",
		}).AddRow("message-3", "conversation-1", int64(3), "user", "Explain those bugs.", "final"))
	mock.ExpectQuery("SELECT .*FROM `messages`.*ORDER BY.*seq").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "conversation_id", "seq", "role", "content", "status",
		}).
			AddRow("message-1", "conversation-1", int64(1), "user", "Find the bugs.", "final").
			AddRow("message-2", "conversation-1", int64(2), "assistant", "I found two bugs.", "final").
			AddRow("message-3", "conversation-1", int64(3), "user", "Explain those bugs.", "final"))

	execution, err := New(database).LoadRunExecution(context.Background(), "run-2", 4)
	if err != nil {
		t.Fatalf("LoadRunExecution() error = %v", err)
	}
	if len(execution.Messages) != 3 {
		t.Fatalf("LoadRunExecution() messages = %d, want 3", len(execution.Messages))
	}
	if execution.Messages[1].Role != "assistant" || execution.Messages[1].Content != "I found two bugs." {
		t.Errorf("prior assistant message = %#v", execution.Messages[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
