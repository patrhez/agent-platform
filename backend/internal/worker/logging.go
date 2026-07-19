package worker

import (
	"context"
	"errors"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
	"go.uber.org/zap"
)

func writeToolBoundaryLog(
	ctx context.Context,
	logger logging.Logger,
	runID string,
	event runtime.RuntimeEvent,
	startedAt time.Time,
	finishedAt time.Time,
) {
	if event.Tool == nil {
		return
	}
	if event.ToolResult == nil {
		logger.Info(
			ctx,
			"tool boundary",
			zap.String("run_id", runID),
			zap.String("tool", event.Tool.Name),
			zap.String("status", "running"),
		)
		return
	}
	duration := finishedAt.Sub(startedAt).Round(time.Millisecond)
	if duration < 0 {
		duration = 0
	}
	logger.Info(
		ctx,
		"tool boundary",
		zap.String("run_id", runID),
		zap.String("tool", event.Tool.Name),
		zap.String("status", "completed"),
		zap.Duration("duration", duration),
	)
}

func writeRunFailureLog(
	ctx context.Context,
	logger logging.Logger,
	runID string,
	status domain.RunStatus,
	duration time.Duration,
	cause error,
) {
	logger.Info(
		ctx,
		"run finished",
		zap.String("run_id", runID),
		zap.String("status", string(status)),
		zap.Duration("duration", duration.Round(time.Millisecond)),
		zap.String("error_code", runFailureCode(cause)),
	)
}

func runFailureCode(cause error) string {
	if errors.Is(cause, runtime.ErrRunCancelled) {
		return "cancelled"
	}
	return "runtime_error"
}
