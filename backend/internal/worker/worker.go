// Package worker leases and executes durable Agent Runs.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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
	// shutdownWait bounds in-flight drain after cancel. Deployments must keep a
	// stop/termination grace period above this value (with buffer) so SIGKILL
	// does not cut the drain short; see compose.full.yaml and deploy/ha/workloads.yaml.
	shutdownWait = 25 * time.Second
)

// RunStore is the durable Worker dependency.
type RunStore interface {
	ClaimNextRuns(context.Context, string, time.Time, int) ([]domain.Run, error)
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
	concurrency  int
	pollInterval time.Duration
}

// runPool tracks in-flight Executes and fatal shutdown signals for Worker.Run.
type runPool struct {
	mu       sync.Mutex
	inFlight int
	slotFree chan struct{}
	fatalCh  chan error
	wg       sync.WaitGroup
	cancel   context.CancelFunc
}

func newRunPool(cancel context.CancelFunc) *runPool {
	return &runPool{
		slotFree: make(chan struct{}, 1),
		fatalCh:  make(chan error, 1),
		cancel:   cancel,
	}
}

func (pool *runPool) signalFree() {
	select {
	case pool.slotFree <- struct{}{}:
	default:
	}
}

func (pool *runPool) reportFatal(err error) {
	select {
	case pool.fatalCh <- err:
		pool.cancel()
	default:
	}
}

func (pool *runPool) freeSlots(concurrency int) int {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return concurrency - pool.inFlight
}

func (pool *runPool) firstFatal() error {
	select {
	case err := <-pool.fatalCh:
		return err
	default:
		return nil
	}
}

// New creates a Worker using the given durable Store and Agent runtime.
func New(
	store RunStore,
	runner runtime.AgentRunner,
	notifier events.Notifier,
	logger logging.Logger,
	workerID string,
	concurrency int,
) (*Worker, error) {
	if store == nil || runner == nil || notifier == nil || workerID == "" {
		return nil, fmt.Errorf("Worker requires Store, runner, Notifier, and Worker ID")
	}
	if concurrency < 1 {
		return nil, fmt.Errorf("Worker concurrency must be >= 1")
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
		concurrency:  concurrency,
		pollInterval: defaultPollInterval,
	}, nil
}

// Run claims batches of Runs and executes them concurrently until shutdown or a fatal error.
func (worker *Worker) Run(ctx context.Context) error {
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pool := newRunPool(cancel)
	var runErr error
	for {
		if err := pool.firstFatal(); err != nil {
			runErr = err
			break
		}
		if err := worker.claimOnce(ctx, pool); err != nil {
			if errors.Is(err, errClaimerStop) {
				break
			}
			worker.logger.Error(ctx, "fatal claim error", zap.Error(err))
			pool.reportFatal(err)
			runErr = err
			break
		}
	}

	worker.waitInFlight(ctx, pool)
	if runErr != nil {
		return runErr
	}
	if err := pool.firstFatal(); err != nil {
		return err
	}
	return parent.Err()
}

// errClaimerStop signals the claimer should exit to shutdown without a new fatal.
var errClaimerStop = errors.New("claimer stop")

func (worker *Worker) claimOnce(ctx context.Context, pool *runPool) error {
	select {
	case <-ctx.Done():
		return errClaimerStop
	default:
	}

	free := pool.freeSlots(worker.concurrency)
	if free < 1 {
		return worker.waitForSlot(ctx, pool)
	}

	runs, err := worker.store.ClaimNextRuns(ctx, worker.workerID, time.Now().UTC(), free)
	if err != nil {
		// Cancel/deadline from ClaimNextRuns is clean shutdown only when the Worker ctx is done.
		if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) &&
			ctx.Err() != nil {
			return errClaimerStop
		}
		return Fatal(fmt.Errorf("claim next Runs: %w", err))
	}
	if len(runs) == 0 {
		return worker.waitForWork(ctx, pool)
	}

	for _, run := range runs {
		worker.startExecute(ctx, pool, run)
	}
	return nil
}

func (worker *Worker) waitForSlot(ctx context.Context, pool *runPool) error {
	select {
	case <-ctx.Done():
		return errClaimerStop
	case <-pool.slotFree:
		return nil
	}
}

func (worker *Worker) waitForWork(ctx context.Context, pool *runPool) error {
	select {
	case <-ctx.Done():
		return errClaimerStop
	case <-time.After(worker.pollInterval):
		return nil
	case <-pool.slotFree:
		return nil
	}
}

func (worker *Worker) startExecute(ctx context.Context, pool *runPool, run domain.Run) {
	pool.mu.Lock()
	pool.inFlight++
	pool.mu.Unlock()
	pool.wg.Add(1)
	go worker.executeClaimed(ctx, pool, run)
}

func (worker *Worker) executeClaimed(ctx context.Context, pool *runPool, run domain.Run) {
	defer async.Recover(ctx, worker.logger)
	defer pool.wg.Done()
	defer func() {
		pool.mu.Lock()
		pool.inFlight--
		pool.mu.Unlock()
		pool.signalFree()
	}()

	worker.logger.Info(
		ctx,
		"run claimed",
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.String("worker_id", worker.workerID),
		zap.Int("attempt", run.Attempt),
		zap.Int64("queue_seq", run.QueueSeq),
	)
	if err := worker.Execute(ctx, run); err != nil {
		if errors.Is(err, store.ErrLeaseLost) || errors.Is(err, context.Canceled) {
			return
		}
		if IsFatalWorkerError(err) {
			worker.logger.Error(ctx, "fatal run execution error",
				zap.String("run_id", run.ID), zap.Error(err))
			pool.reportFatal(err)
			return
		}
		worker.logger.Error(ctx, "run execution failed",
			zap.String("run_id", run.ID), zap.Error(err))
	}
}

func (worker *Worker) waitInFlight(ctx context.Context, pool *runPool) {
	waitCtx, waitCancel := context.WithTimeout(context.Background(), shutdownWait)
	defer waitCancel()
	done := make(chan struct{})
	go func() {
		defer async.Recover(waitCtx, worker.logger)
		pool.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-waitCtx.Done():
		worker.logger.Warn(ctx, "shutdown wait timed out; exiting with in-flight runs")
	}
}

// Execute runs one claimed Run and persists a terminal outcome when it still owns the lease.
func (worker *Worker) Execute(ctx context.Context, run domain.Run) error {
	startedAt := time.Now()
	ctx, _ = logging.EnsureTraceID(ctx)
	worker.logger.Info(
		ctx,
		"run started",
		zap.String("conversation_id", run.ConversationID),
		zap.String("run_id", run.ID),
		zap.String("worker_id", worker.workerID),
		zap.Int("attempt", run.Attempt),
	)
	cancelled, err := worker.store.IsCancellationRequested(ctx, run.ID)
	if err != nil {
		return err
	}
	if cancelled {
		return worker.completeFailure(ctx, run, runtime.ErrRunCancelled, "")
	}
	execution, err := worker.store.LoadRunExecution(ctx, run.ID, run.ExecutionToken)
	if err != nil {
		return err
	}
	checkpoint, err := worker.store.LatestCheckpoint(ctx, run.ID)
	if err != nil {
		return err
	}
	agentInput := runtime.AgentInput{
		RunID: run.ID, ConversationID: run.ConversationID, Attempt: run.Attempt,
		Messages: runtimeMessages(execution.Messages), ProviderReplay: execution.ProviderReplay,
	}
	writeRuntimeInputLog(ctx, worker.logger, run, agentInput, checkpoint != nil)
	runContext, stopRenewal, leaseLost := worker.renewLease(ctx, run)
	emit, finish := worker.persistEmitter(runContext, run)
	result, runError := worker.runner.Run(runContext, agentInput, checkpoint, emit)
	partial, terminalBoundary, flushError := finish()
	if runError == nil && flushError != nil {
		runError = flushError
	}
	stopRenewal()
	if lost := receiveLeaseLoss(leaseLost); lost != nil {
		return lost
	}
	if runError != nil {
		completionError := worker.completeFailure(ctx, run, runError, partial)
		status := domain.RunStatusFailed
		if errors.Is(runError, runtime.ErrRunCancelled) {
			status = domain.RunStatusCancelled
		}
		writeRunFailureLog(ctx, worker.logger, run, status, time.Since(startedAt), runError)
		return completionError
	}
	completionError := worker.completeSuccess(ctx, run, result, terminalBoundary)
	if completionError == nil {
		worker.logger.Info(
			ctx,
			"run finished",
			zap.String("conversation_id", run.ConversationID),
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
) (func(runtime.RuntimeEvent) error, func() (string, *domain.ExecutionBoundary, error)) {
	toolStartedAt := make(map[string]time.Time)
	var terminalBoundary *domain.ExecutionBoundary
	buffer := newStreamBuffer(func(values []domain.RunEvent) error {
		persisted, err := worker.store.AppendRunEvents(ctx, run.ID, run.ExecutionToken, values)
		if err != nil {
			return err
		}
		writePersistedEventsLog(ctx, worker.logger, run, "append_run_events", persisted)
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
		if event.Final != "" {
			terminalBoundary = &boundary
			return nil
		}
		persisted, err := worker.store.PersistBoundary(ctx, boundary)
		if err != nil {
			return err
		}
		writeToolBoundaryLog(ctx, worker.logger, run, event, startedAt, now)
		writePersistedBoundaryLog(ctx, worker.logger, run, event, boundary)
		writePersistedEventsLog(ctx, worker.logger, run, "persist_boundary", persisted)
		worker.publish(ctx, persisted)
		return nil
	}
	finish := func() (string, *domain.ExecutionBoundary, error) {
		err := buffer.Flush()
		return buffer.CurrentText(), terminalBoundary, err
	}
	return emit, finish
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

func (worker *Worker) completeSuccess(
	ctx context.Context,
	run domain.Run,
	result runtime.Result,
	terminalBoundary *domain.ExecutionBoundary,
) error {
	if terminalBoundary == nil {
		return fmt.Errorf("successful Run %s has no terminal provider boundary", run.ID)
	}
	event, err := terminalEvent("run.completed", "Agent completed the troubleshooting report")
	if err != nil {
		return err
	}
	completion := domain.RunCompletion{
		RunID:            run.ID,
		ExecutionToken:   run.ExecutionToken,
		Status:           domain.RunStatusSucceeded,
		AssistantText:    result.Final,
		Event:            event,
		TerminalBoundary: terminalBoundary,
	}
	persisted, err := worker.store.CompleteRun(ctx, completion)
	if err != nil {
		return err
	}
	writeCompleteRunLog(ctx, worker.logger, run, completion)
	writePersistedEventsLog(ctx, worker.logger, run, "complete_run", persisted)
	worker.publish(ctx, persisted)
	return nil
}

func (worker *Worker) completeFailure(ctx context.Context, run domain.Run, cause error, partial string) error {
	if errors.Is(cause, store.ErrLeaseLost) || errors.Is(cause, context.Canceled) {
		return cause
	}
	failure := runtime.ClassifyRunFailure(cause)
	status, eventType := domain.RunStatusFailed, "run.failed"
	assistantText := ""
	if failure.Code == "cancelled" {
		status, eventType = domain.RunStatusCancelled, "run.cancelled"
		assistantText = partial
	}
	event, err := terminalEvent(eventType, failure.Message)
	if err != nil {
		return err
	}
	completion := domain.RunCompletion{
		RunID:          run.ID,
		ExecutionToken: run.ExecutionToken,
		Status:         status,
		AssistantText:  assistantText,
		ErrorCode:      failure.Code,
		ErrorMessage:   failure.Message,
		Event:          event,
	}
	persisted, err := worker.store.CompleteRun(ctx, completion)
	if err != nil {
		return err
	}
	writeCompleteRunLog(ctx, worker.logger, run, completion)
	writePersistedEventsLog(ctx, worker.logger, run, "complete_run", persisted)
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
