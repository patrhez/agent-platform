// Package worker leases and executes durable Agent Runs.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/events"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/pkg/async"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
	"github.com/patrhez/agent-platform/backend/internal/store"
	"go.uber.org/zap"
)

const (
	leaseRenewInterval  = 10 * time.Second
	leaseDuration       = 30 * time.Second
	defaultPollInterval = time.Second
)

// RunStore is the durable Worker dependency.
type RunStore interface {
	ClaimNextRun(context.Context, string, time.Time) (domain.Run, bool, error)
	RenewLease(context.Context, string, int64, time.Time) error
	LoadRunExecution(context.Context, string, int64) (domain.RunExecution, error)
	LatestCheckpoint(context.Context, string) (*domain.Checkpoint, error)
	PersistBoundary(context.Context, domain.ExecutionBoundary) ([]domain.RunEvent, error)
	AppendRunEvents(context.Context, string, int64, []domain.RunEvent) ([]domain.RunEvent, error)
	CompleteRun(context.Context, domain.RunCompletion) ([]domain.RunEvent, error)
	IsCancellationRequested(context.Context, string) (bool, error)
}

// Worker polls the durable Run queue and executes the configured Agent runtime.
type Worker struct {
	store        RunStore
	runner       runtime.AgentRunner
	notifier     events.Notifier
	logger       logging.Logger
	workerID     string
	pollInterval time.Duration
}

// New creates a Worker using the given durable Store and Agent runtime.
func New(
	store RunStore,
	runner runtime.AgentRunner,
	notifier events.Notifier,
	logger logging.Logger,
	workerID string,
) (*Worker, error) {
	if store == nil || runner == nil || notifier == nil || workerID == "" {
		return nil, fmt.Errorf("Worker requires Store, runner, Notifier, and Worker ID")
	}
	if logger == nil {
		logger = logging.Nop()
	}
	return &Worker{
		store:        store,
		runner:       runner,
		notifier:     notifier,
		logger:       logger,
		workerID:     workerID,
		pollInterval: defaultPollInterval,
	}, nil
}

// Run continuously claims and executes durable Runs until context cancellation.
func (worker *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(worker.pollInterval)
	defer ticker.Stop()
	for {
		processed, err := worker.processNext(ctx)
		if err != nil {
			return err
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (worker *Worker) processNext(ctx context.Context) (bool, error) {
	run, claimed, err := worker.store.ClaimNextRun(ctx, worker.workerID, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("claim next Run: %w", err)
	}
	if !claimed {
		return false, nil
	}
	worker.logger.Info(
		ctx,
		"run claimed",
		zap.String("run_id", run.ID),
		zap.String("worker_id", worker.workerID),
		zap.Int("attempt", run.Attempt),
		zap.Int64("queue_seq", run.QueueSeq),
	)
	if err := worker.Execute(ctx, run); err != nil && !errors.Is(err, store.ErrLeaseLost) {
		return true, err
	}
	return true, nil
}

// Execute runs one claimed Run and persists a terminal outcome when it still owns the lease.
func (worker *Worker) Execute(ctx context.Context, run domain.Run) error {
	startedAt := time.Now()
	worker.logger.Info(
		ctx,
		"run started",
		zap.String("run_id", run.ID),
		zap.String("worker_id", worker.workerID),
		zap.Int("attempt", run.Attempt),
	)
	cancelled, err := worker.store.IsCancellationRequested(ctx, run.ID)
	if err != nil {
		return err
	}
	if cancelled {
		return worker.completeFailure(ctx, run, runtime.ErrRunCancelled)
	}
	execution, err := worker.store.LoadRunExecution(ctx, run.ID, run.ExecutionToken)
	if err != nil {
		return err
	}
	checkpoint, err := worker.store.LatestCheckpoint(ctx, run.ID)
	if err != nil {
		return err
	}
	runContext, stopRenewal, leaseLost := worker.renewLease(ctx, run)
	emit, flush := worker.persistEmitter(runContext, run)
	result, runError := worker.runner.Run(
		runContext,
		runtime.AgentInput{RunID: run.ID, Attempt: run.Attempt, Messages: runtimeMessages(execution.Messages)},
		checkpoint,
		emit,
	)
	if flushError := flush(); runError == nil && flushError != nil {
		runError = flushError
	}
	stopRenewal()
	if lost := receiveLeaseLoss(leaseLost); lost != nil {
		return lost
	}
	if runError != nil {
		completionError := worker.completeFailure(ctx, run, runError)
		status := domain.RunStatusFailed
		if errors.Is(runError, runtime.ErrRunCancelled) {
			status = domain.RunStatusCancelled
		}
		writeRunFailureLog(ctx, worker.logger, run.ID, status, time.Since(startedAt), runError)
		return completionError
	}
	completionError := worker.completeSuccess(ctx, run, result)
	if completionError == nil {
		worker.logger.Info(
			ctx,
			"run finished",
			zap.String("run_id", run.ID),
			zap.String("status", string(domain.RunStatusSucceeded)),
			zap.Duration("duration", time.Since(startedAt).Round(time.Millisecond)),
		)
	}
	return completionError
}

func runtimeMessages(messages []domain.Message) []runtime.ConversationMessage {
	result := make([]runtime.ConversationMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, runtime.ConversationMessage{Role: message.Role, Content: message.Content})
	}
	return result
}

func (worker *Worker) renewLease(
	parent context.Context,
	run domain.Run,
) (context.Context, context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(parent)
	lost := make(chan error, 1)
	go func() {
		defer async.Recover(ctx, worker.logger)
		ticker := time.NewTicker(leaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := worker.store.RenewLease(ctx, run.ID, run.ExecutionToken, time.Now().UTC().Add(leaseDuration))
				if err != nil {
					lost <- err
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel, lost
}

func (worker *Worker) persistEmitter(
	ctx context.Context,
	run domain.Run,
) (func(runtime.RuntimeEvent) error, func() error) {
	toolStartedAt := make(map[string]time.Time)
	buffer := newStreamBuffer(func(values []domain.RunEvent) error {
		persisted, err := worker.store.AppendRunEvents(ctx, run.ID, run.ExecutionToken, values)
		if err != nil {
			return err
		}
		worker.publish(ctx, persisted)
		return nil
	}, time.Now)
	emit := func(event runtime.RuntimeEvent) error {
		cancelled, err := worker.store.IsCancellationRequested(ctx, run.ID)
		if err != nil {
			return err
		}
		if cancelled {
			return runtime.ErrRunCancelled
		}
		if event.Assistant != nil {
			return buffer.Add(*event.Assistant)
		}
		if err := buffer.Flush(); err != nil {
			return err
		}
		now := time.Now().UTC()
		startedAt := now
		payload, err := eventPayload(event)
		if event.Tool != nil {
			if event.ToolResult == nil {
				toolStartedAt[event.Tool.ID] = now
			} else if value, found := toolStartedAt[event.Tool.ID]; found {
				startedAt = value
				delete(toolStartedAt, event.Tool.ID)
			}
			payload, err = toolEventPayload(event, startedAt, now)
		}
		if err != nil {
			return err
		}
		boundary := boundaryFromRuntimeEvent(run, event, payload, startedAt, now)
		persisted, err := worker.store.PersistBoundary(ctx, boundary)
		if err != nil {
			return err
		}
		writeToolBoundaryLog(ctx, worker.logger, run.ID, event, startedAt, now)
		worker.publish(ctx, persisted)
		return nil
	}
	return emit, buffer.Flush
}

func boundaryFromRuntimeEvent(
	run domain.Run,
	event runtime.RuntimeEvent,
	payload json.RawMessage,
	startedAt time.Time,
	finishedAt time.Time,
) domain.ExecutionBoundary {
	boundary := domain.ExecutionBoundary{
		RunID:          run.ID,
		ExecutionToken: run.ExecutionToken,
		StepNo:         event.StepNo,
		Kind:           event.Kind,
		SafeSummary:    event.Summary,
		Checkpoint:     event.Checkpoint,
		Events: []domain.RunEvent{{
			Type:        runtimeEventType(event),
			SafePayload: payload,
		}},
	}
	if event.Tool != nil {
		boundary.ToolCall = toolCallFromRuntimeEvent(run, event, startedAt, finishedAt)
	}
	return boundary
}

func toolCallFromRuntimeEvent(
	run domain.Run,
	event runtime.RuntimeEvent,
	startedAt time.Time,
	finishedAt time.Time,
) *domain.ToolCall {
	call := &domain.ToolCall{
		ID:             event.Tool.ID,
		RunID:          run.ID,
		StepNo:         event.StepNo,
		ServerKey:      event.Tool.ServerKey,
		ToolName:       event.Tool.Name,
		Arguments:      event.Tool.Arguments,
		IdempotencyKey: event.Tool.IdempotencyKey,
		Status:         "running",
		CreatedAt:      startedAt,
		UpdatedAt:      finishedAt,
	}
	if event.ToolResult != nil {
		call.Status = "completed"
		call.ResultSummary = event.ToolResult.Summary
	}
	return call
}

func eventPayload(event runtime.RuntimeEvent) (json.RawMessage, error) {
	payload := map[string]string{"summary": event.Summary}
	if event.Tool != nil {
		payload["tool"] = event.Tool.Name
	}
	if event.Final != "" {
		payload["text"] = event.Final
	}
	contents, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode runtime event: %w", err)
	}
	return contents, nil
}

func runtimeEventType(event runtime.RuntimeEvent) string {
	if event.Tool != nil && event.ToolResult == nil {
		return "tool.started"
	}
	if event.ToolResult != nil {
		return "tool.completed"
	}
	if event.Final != "" {
		return "assistant.completed"
	}
	return "progress.updated"
}

func (worker *Worker) completeSuccess(ctx context.Context, run domain.Run, result runtime.Result) error {
	event, err := terminalEvent("run.completed", "Agent completed the troubleshooting report")
	if err != nil {
		return err
	}
	persisted, err := worker.store.CompleteRun(ctx, domain.RunCompletion{
		RunID:          run.ID,
		ExecutionToken: run.ExecutionToken,
		Status:         domain.RunStatusSucceeded,
		AssistantText:  result.Final,
		Event:          event,
	})
	if err != nil {
		return err
	}
	worker.publish(ctx, persisted)
	return nil
}

func (worker *Worker) completeFailure(ctx context.Context, run domain.Run, cause error) error {
	if errors.Is(cause, store.ErrLeaseLost) || errors.Is(cause, context.Canceled) {
		return cause
	}
	failure := runtime.ClassifyRunFailure(cause)
	status, eventType := domain.RunStatusFailed, "run.failed"
	if failure.Code == "cancelled" {
		status, eventType = domain.RunStatusCancelled, "run.cancelled"
	}
	event, err := terminalEvent(eventType, failure.Message)
	if err != nil {
		return err
	}
	persisted, err := worker.store.CompleteRun(ctx, domain.RunCompletion{
		RunID:          run.ID,
		ExecutionToken: run.ExecutionToken,
		Status:         status,
		ErrorCode:      failure.Code,
		ErrorMessage:   failure.Message,
		Event:          event,
	})
	if err != nil {
		return err
	}
	worker.publish(ctx, persisted)
	return nil
}

func (worker *Worker) publish(ctx context.Context, persisted []domain.RunEvent) {
	for _, event := range persisted {
		if err := worker.notifier.Publish(ctx, event.RunID, event.Seq); err != nil {
			worker.logger.Warn(
				ctx,
				"publish Run event hint failed",
				zap.String("run_id", event.RunID),
				zap.Int64("seq", event.Seq),
				zap.Error(err),
			)
		}
	}
}

func terminalEvent(eventType string, summary string) (domain.RunEvent, error) {
	payload, err := json.Marshal(map[string]string{"summary": summary})
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("encode terminal event: %w", err)
	}
	return domain.RunEvent{Type: eventType, SafePayload: payload}, nil
}

func receiveLeaseLoss(lost <-chan error) error {
	select {
	case err := <-lost:
		return err
	default:
		return nil
	}
}
