package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDeleteConversationsSoftDeletesOwnedRows(t *testing.T) {
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
	conversationColumns := []string{
		"id", "user_id", "title", "next_message_seq", "next_run_seq", "next_executable_run_seq",
		"created_at", "updated_at",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-1", "user-1", "One", int64(1), int64(1), int64(1), now, now))
	mock.ExpectExec("UPDATE `conversations` SET `deleted_at`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-2", "user-1", "Two", int64(1), int64(1), int64(1), now, now))
	mock.ExpectExec("UPDATE `conversations` SET `deleted_at`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := New(database).DeleteConversations(
		context.Background(),
		"user-1",
		[]string{"conversation-1", "conversation-2"},
	); err != nil {
		t.Fatalf("DeleteConversations() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestDeleteConversationsRejectsForeignOwnerWithoutPartialDelete(t *testing.T) {
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
	conversationColumns := []string{
		"id", "user_id", "title", "next_message_seq", "next_run_seq", "next_executable_run_seq",
		"created_at", "updated_at",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-1", "user-1", "One", int64(1), int64(1), int64(1), now, now))
	mock.ExpectExec("UPDATE `conversations` SET `deleted_at`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-2", "other-user", "Two", int64(1), int64(1), int64(1), now, now))
	mock.ExpectRollback()

	err = New(database).DeleteConversations(
		context.Background(),
		"user-1",
		[]string{"conversation-1", "conversation-2"},
	)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("DeleteConversations() error = %v, want ErrUnauthorized", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
