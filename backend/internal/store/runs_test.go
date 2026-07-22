package store

import (
	"testing"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/model"
)

func TestDomainRunIncludesTerminalMetadata(t *testing.T) {
	errorCode := "llm_invalid_temperature"
	errorMessage := "The language model rejected the temperature setting. Check the agent model configuration and retry."
	finishedAt := time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)

	result := domainRun(&model.Run{
		TerminalErrorCode:    &errorCode,
		TerminalErrorMessage: &errorMessage,
		FinishedAt:           &finishedAt,
	})

	if result.ErrorCode != errorCode {
		t.Errorf("domainRun() ErrorCode = %q, want %q", result.ErrorCode, errorCode)
	}
	if result.ErrorMessage != errorMessage {
		t.Errorf("domainRun() ErrorMessage = %q, want %q", result.ErrorMessage, errorMessage)
	}
	if result.FinishedAt == nil || !result.FinishedAt.Equal(finishedAt) {
		t.Errorf("domainRun() FinishedAt = %v, want %v", result.FinishedAt, finishedAt)
	}
}
