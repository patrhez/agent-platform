package store

import (
	"testing"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/model"
)

func TestDomainRunIncludesTerminalMetadata(t *testing.T) {
	errorCode := "runtime_error"
	finishedAt := time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)

	result := domainRun(&model.Run{TerminalErrorCode: &errorCode, FinishedAt: &finishedAt})

	if result.ErrorCode != errorCode {
		t.Errorf("domainRun() ErrorCode = %q, want %q", result.ErrorCode, errorCode)
	}
	if result.FinishedAt == nil || !result.FinishedAt.Equal(finishedAt) {
		t.Errorf("domainRun() FinishedAt = %v, want %v", result.FinishedAt, finishedAt)
	}
}
