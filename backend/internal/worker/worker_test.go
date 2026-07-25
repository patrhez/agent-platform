package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/events"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
)

var errAppendDelta = errors.New("append Delta failed")

type deltaFailureStore struct {
	appendCalls     int
	boundaryCalls   int
	completion      domain.RunCompletion
	completionCalls int
}

func (store *deltaFailureStore) ClaimNextRuns(context.Context, string, time.Time, int) ([]domain.Run, error) {
	return nil, nil
}

func (store *deltaFailureStore) RenewLease(context.Context, string, int64, time.Time) error {
	return nil
}

func (store *deltaFailureStore) LoadRunExecution(context.Context, string, int64) (domain.RunExecution, error) {
	return domain.RunExecution{Messages: []domain.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "follow-up question"},
	}}, nil
}

func (store *deltaFailureStore) LatestCheckpoint(context.Context, string) (*domain.Checkpoint, error) {
	return nil, nil
}

func (store *deltaFailureStore) PersistBoundary(context.Context, domain.ExecutionBoundary) ([]domain.RunEvent, error) {
	store.boundaryCalls++
	return nil, nil
}

func (store *deltaFailureStore) AppendRunEvents(
	context.Context,
	string,
	int64,
	[]domain.RunEvent,
) ([]domain.RunEvent, error) {
	store.appendCalls++
	return nil, errAppendDelta
}

func (store *deltaFailureStore) CompleteRun(
	_ context.Context,
	completion domain.RunCompletion,
) ([]domain.RunEvent, error) {
	store.completion = completion
	store.completionCalls++
	return []domain.RunEvent{{RunID: completion.RunID, Seq: 1, Type: "run.failed"}}, nil
}

func (store *deltaFailureStore) IsCancellationRequested(context.Context, string) (bool, error) {
	return false, nil
}

type assistantEventRunner struct {
	emitError error
	input     runtime.AgentInput
}

func (runner *assistantEventRunner) Run(
	_ context.Context,
	input runtime.AgentInput,
	_ *domain.Checkpoint,
	emit func(runtime.RuntimeEvent) error,
) (runtime.Result, error) {
	runner.input = input
	runner.emitError = emit(runtime.RuntimeEvent{
		StepNo: 1,
		Kind:   "model",
		Assistant: &runtime.AssistantStreamEvent{
			StreamID: "run-id:2:1",
			Phase:    "started",
			Attempt:  2,
			StepNo:   1,
		},
	})
	if runner.emitError != nil {
		return runtime.Result{}, runner.emitError
	}
	return runtime.Result{Final: "unexpected success"}, nil
}

func TestWorkerFailsRunWhenAssistantDeltaCannotBePersisted(t *testing.T) {
	store := &deltaFailureStore{}
	runner := &assistantEventRunner{}
	worker, err := New(store, runner, events.Noop(), logging.Nop(), "worker-test", 1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	run := domain.Run{
		ID:             "run-id",
		Status:         domain.RunStatusRunning,
		Attempt:        2,
		ExecutionToken: 7,
	}

	if err := worker.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !errors.Is(runner.emitError, errAppendDelta) {
		t.Errorf("runner emit error = %v, want %v", runner.emitError, errAppendDelta)
	}
	if runner.input.Attempt != 2 {
		t.Errorf("runner input attempt = %d, want 2", runner.input.Attempt)
	}
	if len(runner.input.Messages) != 3 || runner.input.Messages[1].Content != "first answer" {
		t.Errorf("runner input messages = %#v, want ordered Conversation history", runner.input.Messages)
	}
	if store.appendCalls != 1 {
		t.Errorf("AppendRunEvents calls = %d, want 1", store.appendCalls)
	}
	if store.boundaryCalls != 0 {
		t.Errorf("PersistBoundary calls = %d, want 0", store.boundaryCalls)
	}
	if store.completionCalls != 1 || store.completion.Status != domain.RunStatusFailed {
		t.Errorf("completion = %#v (%d calls), want one failed completion", store.completion, store.completionCalls)
	}
}

func TestNewRejectsConcurrencyLessThanOne(t *testing.T) {
	store := &poolTestStore{}
	runner := &blockingRunner{release: make(chan struct{})}
	_, err := New(store, runner, events.Noop(), logging.Nop(), "worker-test", 0)
	if err == nil {
		t.Fatal("New(concurrency=0) error = nil, want error")
	}
	_, err = New(store, runner, events.Noop(), logging.Nop(), "worker-test", -1)
	if err == nil {
		t.Fatal("New(concurrency=-1) error = nil, want error")
	}
}

func TestWorkerRunsClaimedRunsConcurrently(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan string, 2)
	store := &poolTestStore{
		batches: [][]domain.Run{
			{
				{ID: "run-1", Status: domain.RunStatusRunning, Attempt: 1, ExecutionToken: 1},
				{ID: "run-2", Status: domain.RunStatusRunning, Attempt: 1, ExecutionToken: 2},
			},
		},
	}
	runner := &blockingRunner{release: release, entered: entered}
	service, err := New(store, runner, events.Noop(), logging.Nop(), "worker-test", 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	service.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	seen := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case id := <-entered:
			seen[id] = true
		case <-deadline:
			t.Fatalf("timed out waiting for concurrent Executes; seen=%v", seen)
		case err := <-done:
			t.Fatalf("Run() returned early: %v", err)
		}
	}

	close(release)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancel")
	}
}

func TestWorkerContinuesAfterNonFatalExecuteError(t *testing.T) {
	secondClaim := make(chan struct{}, 1)
	store := &poolTestStore{
		batches: [][]domain.Run{
			{{ID: "run-boom", Status: domain.RunStatusRunning, Attempt: 1, ExecutionToken: 1}},
		},
		loadErr:     errors.New("boom"),
		onClaim:     make(chan int, 8),
		secondClaim: secondClaim,
	}
	runner := &blockingRunner{release: make(chan struct{})}
	service, err := New(store, runner, events.Noop(), logging.Nop(), "worker-test", 1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	service.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	select {
	case <-secondClaim:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second ClaimNextRuns after non-fatal Execute error")
	case err := <-done:
		t.Fatalf("Run() returned early: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancel")
	}
}

func TestWorkerStopsOnFatalClaimError(t *testing.T) {
	store := &poolTestStore{claimErr: errors.New("db down")}
	runner := &blockingRunner{release: make(chan struct{})}
	service, err := New(store, runner, events.Noop(), logging.Nop(), "worker-test", 1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = service.Run(ctx)
	if !IsFatalWorkerError(err) {
		t.Fatalf("Run() error = %v, want fatal worker error", err)
	}
	if !errors.Is(err, store.claimErr) {
		t.Fatalf("Run() error = %v, want wrapped %v", err, store.claimErr)
	}
}

func TestWorkerFatalOnSpuriousClaimCancel(t *testing.T) {
	for _, claimErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(claimErr.Error(), func(t *testing.T) {
			store := &poolTestStore{claimErr: claimErr}
			service, err := New(
				store,
				&blockingRunner{release: make(chan struct{})},
				events.Noop(),
				logging.Nop(),
				"worker-test",
				1,
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = service.Run(ctx)
			if !IsFatalWorkerError(err) {
				t.Fatalf("Run() error = %v, want fatal worker error", err)
			}
			if !errors.Is(err, claimErr) {
				t.Fatalf("Run() error = %v, want wrapped %v", err, claimErr)
			}
		})
	}
}

func TestWorkerShutdownOnClaimCancel(t *testing.T) {
	store := &ctxAwareClaimStore{entered: make(chan struct{}, 1)}
	service, err := New(
		store,
		&blockingRunner{release: make(chan struct{})},
		events.Noop(),
		logging.Nop(),
		"worker-test",
		1,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ClaimNextRuns")
	}

	cancel()
	select {
	case err := <-done:
		if IsFatalWorkerError(err) {
			t.Fatalf("Run() error = %v, want non-fatal shutdown", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancel")
	}
}

func TestWorkerShutdownWaitsForInFlight(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan string, 1)
	store := &poolTestStore{
		batches: [][]domain.Run{
			{{ID: "run-slow", Status: domain.RunStatusRunning, Attempt: 1, ExecutionToken: 1}},
		},
	}
	// Ignore ctx so Execute stays in-flight until release; exercises shutdown wait.
	runner := &stubbornRunner{release: release, entered: entered}
	service, err := New(store, runner, events.Noop(), logging.Nop(), "worker-test", 1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	service.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Execute to start")
	}

	cancel()
	select {
	case <-done:
		t.Fatal("Run() returned before in-flight Execute finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after in-flight Execute finished")
	}
}

type poolTestStore struct {
	mu          sync.Mutex
	batches     [][]domain.Run
	claimCalls  atomic.Int32
	claimErr    error
	loadErr     error
	onClaim     chan int
	secondClaim chan struct{}
}

// ctxAwareClaimStore blocks in ClaimNextRuns until ctx is done, then returns ctx.Err().
type ctxAwareClaimStore struct {
	poolTestStore
	entered chan struct{}
}

func (store *ctxAwareClaimStore) ClaimNextRuns(
	ctx context.Context,
	_ string,
	_ time.Time,
	_ int,
) ([]domain.Run, error) {
	if store.entered != nil {
		select {
		case store.entered <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (store *poolTestStore) ClaimNextRuns(
	_ context.Context,
	_ string,
	_ time.Time,
	limit int,
) ([]domain.Run, error) {
	n := int(store.claimCalls.Add(1))
	if store.onClaim != nil {
		select {
		case store.onClaim <- n:
		default:
		}
	}
	if store.secondClaim != nil && n == 2 {
		select {
		case store.secondClaim <- struct{}{}:
		default:
		}
	}
	if store.claimErr != nil {
		return nil, store.claimErr
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.batches) == 0 {
		return nil, nil
	}
	batch := store.batches[0]
	store.batches = store.batches[1:]
	if limit < len(batch) {
		batch = batch[:limit]
	}
	return batch, nil
}

func (store *poolTestStore) RenewLease(context.Context, string, int64, time.Time) error {
	return nil
}

func (store *poolTestStore) LoadRunExecution(context.Context, string, int64) (domain.RunExecution, error) {
	if store.loadErr != nil {
		return domain.RunExecution{}, store.loadErr
	}
	return domain.RunExecution{Messages: []domain.Message{{Role: "user", Content: "hi"}}}, nil
}

func (store *poolTestStore) LatestCheckpoint(context.Context, string) (*domain.Checkpoint, error) {
	return nil, nil
}

func (store *poolTestStore) PersistBoundary(context.Context, domain.ExecutionBoundary) ([]domain.RunEvent, error) {
	return nil, nil
}

func (store *poolTestStore) AppendRunEvents(
	context.Context,
	string,
	int64,
	[]domain.RunEvent,
) ([]domain.RunEvent, error) {
	return nil, nil
}

func (store *poolTestStore) CompleteRun(context.Context, domain.RunCompletion) ([]domain.RunEvent, error) {
	return []domain.RunEvent{{Seq: 1, Type: "run.completed"}}, nil
}

func (store *poolTestStore) IsCancellationRequested(context.Context, string) (bool, error) {
	return false, nil
}

type blockingRunner struct {
	release chan struct{}
	entered chan string
}

func (runner *blockingRunner) Run(
	ctx context.Context,
	input runtime.AgentInput,
	_ *domain.Checkpoint,
	_ func(runtime.RuntimeEvent) error,
) (runtime.Result, error) {
	if runner.entered != nil {
		select {
		case runner.entered <- input.RunID:
		default:
		}
	}
	select {
	case <-runner.release:
		return runtime.Result{Final: "ok"}, nil
	case <-ctx.Done():
		return runtime.Result{}, ctx.Err()
	}
}

// stubbornRunner blocks until release and ignores context cancellation.
type stubbornRunner struct {
	release chan struct{}
	entered chan string
}

func (runner *stubbornRunner) Run(
	_ context.Context,
	input runtime.AgentInput,
	_ *domain.Checkpoint,
	_ func(runtime.RuntimeEvent) error,
) (runtime.Result, error) {
	if runner.entered != nil {
		select {
		case runner.entered <- input.RunID:
		default:
		}
	}
	<-runner.release
	return runtime.Result{Final: "ok"}, nil
}
