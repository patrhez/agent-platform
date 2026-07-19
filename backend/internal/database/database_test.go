package database

import (
	"bytes"
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestGORMLoggerIgnoresExpectedRecordNotFound(t *testing.T) {
	var output bytes.Buffer
	databaseLogger := newGORMLogger(&output)
	databaseLogger.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT expected lookup", 0
	}, gorm.ErrRecordNotFound)

	if output.Len() != 0 {
		t.Errorf("GORM logger output = %q, want no record-not-found noise", output.String())
	}
}

func TestGORMLoggerRemovesQueryParameters(t *testing.T) {
	var output bytes.Buffer
	databaseLogger := newGORMLogger(&output)
	filter, ok := databaseLogger.(interface {
		ParamsFilter(context.Context, string, ...interface{}) (string, []interface{})
	})
	if !ok {
		t.Fatal("GORM logger does not expose parameter filtering")
	}

	_, parameters := filter.ParamsFilter(
		context.Background(),
		"UPDATE run_steps SET safe_summary = ?",
		"private code result",
	)
	if len(parameters) != 0 {
		t.Fatalf("GORM logger retained %d query parameters, want none", len(parameters))
	}
}
