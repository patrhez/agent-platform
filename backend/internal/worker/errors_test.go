package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/patrhez/agent-platform/backend/internal/store"
)

func TestIsFatalWorkerError(t *testing.T) {
	if IsFatalWorkerError(nil) {
		t.Fatal("nil should not be fatal")
	}
	if IsFatalWorkerError(context.Canceled) {
		t.Fatal("context.Canceled should not be fatal")
	}
	if IsFatalWorkerError(store.ErrLeaseLost) {
		t.Fatal("ErrLeaseLost should not be fatal")
	}
	if !IsFatalWorkerError(Fatal(errors.New("db down"))) {
		t.Fatal("Fatal-wrapped error should be fatal")
	}
}

func TestShouldExitNonZero(t *testing.T) {
	if ShouldExitNonZero(nil) {
		t.Fatal("nil should not exit non-zero")
	}
	if ShouldExitNonZero(context.Canceled) {
		t.Fatal("clean context.Canceled should exit 0")
	}
	if !ShouldExitNonZero(errors.New("startup failed")) {
		t.Fatal("ordinary error should exit non-zero")
	}
	fatalCancel := Fatal(fmt.Errorf("claim next Runs: %w", context.Canceled))
	if !ShouldExitNonZero(fatalCancel) {
		t.Fatal("fatal-wrapped context.Canceled should exit non-zero")
	}
}
