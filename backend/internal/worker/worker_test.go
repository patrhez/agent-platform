package worker

import (
	"context"
	"errors"
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

func (store *deltaFailureStore) ClaimNextRun(context.Context, string, time.Time) (domain.Run, bool, error) {
	return domain.Run{}, false, nil
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
	worker, err := New(store, runner, events.Noop(), logging.Nop(), "worker-test")
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
