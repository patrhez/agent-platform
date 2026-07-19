package worker

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
)

func TestStreamBufferFlushesByBytesTimeAndCompletion(t *testing.T) {
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	batches := make([][]domain.RunEvent, 0)
	buffer := newStreamBuffer(func(events []domain.RunEvent) error {
		batches = append(batches, append([]domain.RunEvent(nil), events...))
		return nil
	}, func() time.Time { return now })

	started := runtime.AssistantStreamEvent{
		StreamID: "run:1:1",
		Phase:    "started",
		Attempt:  1,
		StepNo:   1,
	}
	if err := buffer.Add(started); err != nil {
		t.Fatalf("Add(started) error = %v", err)
	}
	if len(batches) != 1 || batches[0][0].Type != "assistant.started" {
		t.Fatalf("started batches = %#v, want one assistant.started event", batches)
	}
	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:1", Phase: "delta", Attempt: 1, StepNo: 1, Offset: 0, Text: "hello ",
	}); err != nil {
		t.Fatalf("Add(first delta) error = %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("len(batches) = %d after small delta, want 1", len(batches))
	}

	now = now.Add(streamFlushInterval)
	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:1", Phase: "delta", Attempt: 1, StepNo: 1, Offset: 1, Text: "world",
	}); err != nil {
		t.Fatalf("Add(timed delta) error = %v", err)
	}
	assertDeltaPayload(t, batches[1][0], 0, "hello world")

	large := strings.Repeat("x", streamFlushBytes)
	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:1", Phase: "delta", Attempt: 1, StepNo: 1, Offset: 2, Text: large,
	}); err != nil {
		t.Fatalf("Add(large delta) error = %v", err)
	}
	assertDeltaPayload(t, batches[2][0], 1, large)

	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:1", Phase: "delta", Attempt: 1, StepNo: 1, Offset: 3, Text: "tail",
	}); err != nil {
		t.Fatalf("Add(tail) error = %v", err)
	}
	if err := buffer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	assertDeltaPayload(t, batches[3][0], 2, "tail")
}

func assertDeltaPayload(t *testing.T, event domain.RunEvent, offset int, text string) {
	t.Helper()
	if event.Type != "assistant.delta" {
		t.Fatalf("event.Type = %q, want assistant.delta", event.Type)
	}
	var payload struct {
		StreamID string `json:"streamId"`
		Attempt  int    `json:"attempt"`
		StepNo   int    `json:"stepNo"`
		Offset   int    `json:"offset"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(event.SafePayload, &payload); err != nil {
		t.Fatalf("decode Delta payload: %v", err)
	}
	if payload.StreamID != "run:1:1" || payload.Attempt != 1 || payload.StepNo != 1 ||
		payload.Offset != offset || payload.Text != text {
		t.Errorf("payload = %#v, want stream run:1:1 attempt 1 step 1 offset %d text %q", payload, offset, text)
	}
}
