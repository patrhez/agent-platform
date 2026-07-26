# Stop/Steer Partial Message Retention Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On cancel (Stop or Steer), persist the current-stream assistant draft as a `status=cancelled` Message so refresh keeps the text, while excluding it from the next model context.

**Architecture:** Extend `streamBuffer` to accumulate current-stream text; on Run cancel completion, flush then pass that text into `CompleteRun` as `AssistantText` with Message `status=cancelled`. `LoadRunExecution` keeps filtering `status=final`. Frontend renders cancelled Messages with a Stopped marker.

**Tech Stack:** Go (Worker/Store), React/Vitest (Chat), existing MySQL `messages.status` column.

## Global Constraints

- No schema migration; reuse `messages.status` values `final` and `cancelled`.
- Do not promote partial text for `failed` Runs.
- Do not change Redis/SSE architecture or Stop/Steer API shapes.
- Do not backfill historical cancelled Runs.
- Commit steps are optional unless the user asks to commit.

---

### Task 1: streamBuffer CurrentText

**Files:**
- Modify: `backend/internal/worker/stream_buffer.go`
- Test: `backend/internal/worker/stream_buffer_test.go`

**Interfaces:**
- Produces: `func (buffer *streamBuffer) CurrentText() string` — committed + pending for the active stream; empty when no stream or after reset.

- [ ] **Step 1: Write the failing test**

Add to `stream_buffer_test.go`:

```go
func TestStreamBufferCurrentTextTracksStreamAndResets(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	buffer := newStreamBuffer(func([]domain.RunEvent) error { return nil }, func() time.Time { return now })

	if buffer.CurrentText() != "" {
		t.Fatalf("CurrentText() = %q, want empty before start", buffer.CurrentText())
	}
	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:1", Phase: "started", Attempt: 1, StepNo: 1,
	}); err != nil {
		t.Fatalf("Add(started) error = %v", err)
	}
	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:1", Phase: "delta", Attempt: 1, StepNo: 1, Text: "hello ",
	}); err != nil {
		t.Fatalf("Add(delta) error = %v", err)
	}
	if got := buffer.CurrentText(); got != "hello " {
		t.Fatalf("CurrentText() = %q, want %q", got, "hello ")
	}
	now = now.Add(streamFlushInterval)
	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:1", Phase: "delta", Attempt: 1, StepNo: 1, Text: "world",
	}); err != nil {
		t.Fatalf("Add(flushing delta) error = %v", err)
	}
	if got := buffer.CurrentText(); got != "hello world" {
		t.Fatalf("CurrentText() after flush = %q, want %q", got, "hello world")
	}
	if err := buffer.Add(runtime.AssistantStreamEvent{
		StreamID: "run:1:2", Phase: "started", Attempt: 1, StepNo: 2,
	}); err != nil {
		t.Fatalf("Add(second started) error = %v", err)
	}
	if got := buffer.CurrentText(); got != "" {
		t.Fatalf("CurrentText() after new stream = %q, want empty", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/worker/ -run TestStreamBufferCurrentTextTracksStreamAndResets -count=1`

Expected: FAIL — `CurrentText` undefined.

- [ ] **Step 3: Implement CurrentText accumulation**

In `stream_buffer.go`, add `committed string` to `streamBuffer`. On `started` after Flush, set `committed = ""`. On Flush after persisting, append flushed text to `committed`. On delta, keep adding to `pending` as today. Implement:

```go
func (buffer *streamBuffer) CurrentText() string {
	return buffer.committed + buffer.pending
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/worker/ -run 'TestStreamBuffer' -count=1`

Expected: PASS.

---

### Task 2: CompleteRun writes cancelled Message status

**Files:**
- Modify: `backend/internal/store/execution.go`
- Test: `backend/internal/store/execution_test.go`

**Interfaces:**
- Consumes: `domain.RunCompletion{Status, AssistantText}`
- Produces: assistant Message with `status=cancelled` when Status is cancelled and text non-empty; `final` when succeeded.

- [ ] **Step 1: Write the failing test**

Add `TestCompleteRunPersistsCancelledAssistantMessage` using sqlmock: expect INSERT into `messages` with `status` cancelled when completing a cancelled Run with `AssistantText`. Mirror existing transaction expectations from AppendRunEvents / CompleteRun patterns in the package (lock run, lock conversation, insert message, update conversation seq, update run terminal fields, insert event, realign executable seq).

If CompleteRun has no existing sqlmock test, assert via a focused unit-style test of the status selection by extracting a tiny helper:

```go
func assistantMessageStatus(status domain.RunStatus) string {
	if status == domain.RunStatusCancelled {
		return "cancelled"
	}
	return "final"
}
```

Test that helper and wire `createAssistantMessage` to take `status string`. Keep empty content → no insert.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/ -run 'AssistantMessageStatus|CompleteRunPersistsCancelled' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement status wiring**

Update `createAssistantMessage` to accept `status string` and set `message.Status = status`. In `CompleteRun`, when `AssistantText != ""` and status is `succeeded` or `cancelled`, create with `assistantMessageStatus(completion.Status)`. Do not create Messages for `failed`.

- [ ] **Step 4: Run store tests**

Run: `cd backend && go test ./internal/store/ -count=1`

Expected: PASS.

---

### Task 3: Worker cancel passes CurrentText into CompleteRun

**Files:**
- Modify: `backend/internal/worker/worker.go`
- Test: `backend/internal/worker/worker_test.go`

**Interfaces:**
- Change `persistEmitter` finish func to return `(text string, err error)` after Flush.
- `completeFailure(ctx, run, cause, partial string)` passes `partial` as `AssistantText` only when failure code is `cancelled`.

- [ ] **Step 1: Write the failing test**

Extend/add a Worker Execute test with a stub store capturing `CompleteRun` and a runner that emits assistant started+deltas then returns `runtime.ErrRunCancelled` (or cancel-on-emit). Assert `CompleteRun` received `Status=cancelled` and `AssistantText` equal to streamed text.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/worker/ -run Cancel -count=1`

Expected: FAIL (AssistantText empty or not passed).

- [ ] **Step 3: Wire flush + partial**

```go
emit, finish := worker.persistEmitter(runContext, run)
result, runError := worker.runner.Run(...)
partial, flushError := finish()
if runError == nil && flushError != nil {
	runError = flushError
}
// ...
completionError := worker.completeFailure(ctx, run, runError, partial)
```

In `completeFailure`, set `AssistantText: partial` only when `failure.Code == "cancelled"`.

Change `persistEmitter` to `return emit, func() (string, error) { err := buffer.Flush(); return buffer.CurrentText(), err }`.

- [ ] **Step 4: Run worker tests**

Run: `cd backend && go test ./internal/worker/ -count=1`

Expected: PASS.

---

### Task 4: Frontend Stopped marker for cancelled Messages

**Files:**
- Modify: `frontend/src/components/Chat.tsx`
- Modify: `frontend/src/app.css` (minimal marker style)
- Test: `frontend/src/components/Chat.test.tsx`
- Optional: `frontend/src/state/conversation.test.ts` if draft-clear needs coverage for cancelled status

- [ ] **Step 1: Write the failing test**

```tsx
it("renders a cancelled assistant Message with a Stopped marker", () => {
  const cancelled: Message = {
    ...assistantMessage,
    content: "## Partial verdict",
    status: "cancelled",
  };
  const cancelledRun: Run = { ...run, status: "cancelled" };
  render(<Chat
    messages={[userMessage, cancelled]}
    runs={[cancelledRun]}
    onSend={vi.fn()}
  />);
  expect(screen.getByRole("heading", { name: "Partial verdict" })).toBeTruthy();
  expect(screen.getByText("Stopped")).toBeTruthy();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npm test -- --run src/components/Chat.test.tsx`

Expected: FAIL — no "Stopped" text.

- [ ] **Step 3: Implement UI**

In `Chat.tsx`, when rendering assistant articles, if `message.status === "cancelled"`, add a small `<span className="stopped-marker">Stopped</span>` (or `aria`-friendly equivalent) next to the Agent label. Keep `RunStatus` as today.

- [ ] **Step 4: Run frontend tests**

Run: `cd frontend && npm test -- --run`

Expected: PASS.

---

### Task 5: Docs FAQ touch-up

**Files:**
- Modify: `docs/tech/execution-and-streaming-faq.md` (CompleteRun / cancel section)

- [ ] Note that cancelled Completions may insert `messages.status=cancelled` with partial text; model history still uses `final` only.

---

## Spec coverage checklist

| Spec requirement | Task |
|------------------|------|
| Current-stream accumulation / reset | Task 1 |
| Cancel Message `status=cancelled` | Task 2 |
| Empty cancel → no Message | Task 2 + 3 |
| Worker flush + pass text on cancelled only | Task 3 |
| LoadRunExecution stays `final`-only | Task 2 (no filter change) |
| Frontend Stopped + body | Task 4 |
| Failed non-promote / no backfill | Non-goals (no tasks) |
