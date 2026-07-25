package worker

import (
	"context"
	"errors"
	"fmt"
)

// ErrFatal marks an error that should stop the Worker process.
var ErrFatal = errors.New("worker fatal error")

// Fatal wraps err as a process-fatal Worker error.
func Fatal(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrFatal, err)
}

// IsFatalWorkerError reports whether err is marked as process-fatal for the Worker.
func IsFatalWorkerError(err error) bool {
	return errors.Is(err, ErrFatal)
}

// ShouldExitNonZero reports whether cmd/worker should exit with a non-zero status.
// Clean SIGTERM shutdown returns context.Canceled without a fatal marker and exits 0.
func ShouldExitNonZero(err error) bool {
	if err == nil {
		return false
	}
	if IsFatalWorkerError(err) {
		return true
	}
	return !errors.Is(err, context.Canceled)
}
