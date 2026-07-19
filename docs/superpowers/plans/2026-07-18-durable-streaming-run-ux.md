# Durable Streaming Run UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream durable assistant text to the browser, render final and partial Markdown, and show a live safe Run timeline without exposing model reasoning.

**Architecture:** Eino streams model chunks into RuntimeEvents. The Worker batches assistant Deltas, commits them to MySQL `run_events`, and only then publishes Redis hints; API instances replay those events over SSE. The React reducer projects durable events into an attempt-aware draft and safe timeline, while the formal assistant Message remains authoritative at Run completion.

**Tech Stack:** Go 1.25, Eino 0.9, GORM Gen, MySQL 8.4, Redis 7.4, SSE/EventSource, React 19, TypeScript, Vite, Vitest, Testing Library, react-markdown, remark-gfm

## Global Constraints

- Do not use subagents.
- Follow the supplied Go coding standards, including GoDoc for exported identifiers, wrapped errors, gofmt, and go vet.
- Do not write SQL by hand; use GORM Gen for all database access.
- All database transactions use `Transaction` callbacks; never call Begin, Commit, or Rollback manually.
- MySQL is the durable source of truth; Redis carries only post-commit notification hints.
- Never persist or expose `ReasoningContent`, Chain-of-Thought, API keys, provider request bodies, or absolute Workspace paths.
- Delta persistence never creates a RunStep, ToolCall, or Checkpoint.
- Flush assistant text after 100 milliseconds, 256 UTF-8 bytes, or stream completion.
- Tool results are collapsed by default and contain only safe summaries.
- Existing Conversation and Run data is retained.

---

### Task 1: Eino Model Streaming Boundary

**Files:**
- Modify: `backend/internal/runtime/agent.go`
- Modify: `backend/internal/runtime/eino_react.go`
- Modify: `backend/internal/runtime/eino_react_test.go`

**Interfaces:**
- Produces: `AssistantStreamEvent { StreamID, Phase, Text string; Attempt, StepNo, Offset int }`.
- Changes: `AgentInput` adds `Attempt int`.
- Changes: `RuntimeEvent` adds `Assistant *AssistantStreamEvent`.
- Consumes: Eino `ToolCallingChatModel.Stream` and `schema.ConcatMessages`.

- [ ] **Step 1: Add a failing streaming model test**

Create a test model implementing `model.ToolCallingChatModel` whose `Stream` returns three chunks: content `"hello "`, content `"world"` plus non-empty `ReasoningContent`, and EOF. Construct an `EinoRunner`, execute one model turn, and assert emitted assistant events are exactly:

```go
[]AssistantStreamEvent{
    {StreamID: "run-1:2:1", Phase: "started", Attempt: 2, StepNo: 1, Offset: 0},
    {StreamID: "run-1:2:1", Phase: "delta", Attempt: 2, StepNo: 1, Offset: 0, Text: "hello "},
    {StreamID: "run-1:2:1", Phase: "delta", Attempt: 2, StepNo: 1, Offset: 1, Text: "world"},
}
```

Also assert the final result is `"hello world"` and no event contains the reasoning text.

- [ ] **Step 2: Run the test and verify RED**

Run:

```sh
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./internal/runtime -run TestEinoRunnerStreamsAssistantContent -count=1
```

Expected: compilation fails because `AssistantStreamEvent`, `AgentInput.Attempt`, and streamed execution are absent.

- [ ] **Step 3: Add the runtime event types**

Add:

```go
// AssistantStreamEvent is one safe fragment of a model content stream.
type AssistantStreamEvent struct {
    StreamID string
    Phase    string
    Attempt  int
    StepNo   int
    Offset   int
    Text     string
}

type AgentInput struct {
    RunID       string
    Attempt     int
    UserContent string
}

type RuntimeEvent struct {
    StepNo     int
    Kind       string
    Summary    string
    Final      string
    Assistant  *AssistantStreamEvent
    Tool       *ToolRequest
    ToolResult *ToolResult
    Checkpoint *domain.Checkpoint
}
```

- [ ] **Step 4: Replace `Generate` with a focused stream consumer**

Implement:

```go
func (runner *EinoRunner) streamModelResponse(
    ctx context.Context,
    messages []*schema.Message,
    streamID string,
    attempt int,
    stepNo int,
    emit func(RuntimeEvent) error,
) (*schema.Message, error)
```

The function calls `runner.model.Stream`, closes the reader, reads until `io.EOF`, clears every chunk's `ReasoningContent`, emits `started` before the first non-empty Content Delta, emits zero-based fragment offsets, and uses `schema.ConcatMessages(chunks)` to produce the response needed for Tool Calls and Checkpoint state. Wrap Stream, receive, concatenate, and emit failures with operation context.

Call it from `execute` using `streamID := fmt.Sprintf("%s:%d:%d", runID, attempt, state.Iteration+1)`. Preserve the existing final boundary and Tool loop behavior.

- [ ] **Step 5: Run runtime tests and vet**

```sh
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./internal/runtime -count=1
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go vet ./internal/runtime
```

Expected: all tests pass with no vet diagnostics.

- [ ] **Step 6: Commit**

```sh
git add backend/internal/runtime/agent.go backend/internal/runtime/eino_react.go backend/internal/runtime/eino_react_test.go
git commit -m "feat: stream Eino assistant content"
```

### Task 2: Durable Buffered Delta Events

**Files:**
- Modify: `backend/internal/store/execution.go`
- Create: `backend/internal/store/execution_test.go`
- Modify: `backend/internal/worker/worker.go`
- Create: `backend/internal/worker/stream_buffer.go`
- Create: `backend/internal/worker/stream_buffer_test.go`

**Interfaces:**
- Produces Store method: `AppendRunEvents(context.Context, string, int64, []domain.RunEvent) ([]domain.RunEvent, error)`.
- Produces worker helper: `newStreamBuffer(func([]domain.RunEvent) error, time.Time) *streamBuffer` with `Add`, `Flush`, and `Reset` methods.
- Consumes: Task 1 `RuntimeEvent.Assistant`.

- [ ] **Step 1: Write failing Store and buffer tests**

The Store test creates `database/sql` with `sqlmock.New`, opens GORM with `mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true})`, and passes it to `store.New`. Expect one begin/commit pair, the fenced Run lookup, Run Event inserts, and the `next_event_seq` update. Call `mock.ExpectationsWereMet()` and do not configure expectations for inserts into `run_steps` or `run_checkpoints`.

The buffer test uses an injected clock and capture function:

```go
func TestStreamBufferFlushesByBytesTimeAndCompletion(t *testing.T) {
    // Add fragments below 256 bytes: no batch.
    // Advance to 100ms and add another fragment: one combined assistant.delta.
    // Add at least 256 bytes: immediate second batch.
    // Flush(): final pending fragment is emitted.
}
```

Assert the persisted payload contains `streamId`, `attempt`, `stepNo`, zero-based `offset`, and combined `text`.

Add a Worker test with a fake Store whose `AppendRunEvents` returns an error. Assert the runner receives that error, no uncommitted Delta is published through the fake Notifier, and the Worker attempts the existing safe failed-Run completion while it still owns the lease.

- [ ] **Step 2: Run focused tests and verify RED**

```sh
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./internal/store ./internal/worker -run 'TestAppendRunEvents|TestStreamBuffer' -count=1
```

Expected: compilation fails because the Store method and buffer do not exist.

- [ ] **Step 3: Implement `AppendRunEvents` with a Transaction callback**

Implement:

```go
func (store *Store) AppendRunEvents(
    ctx context.Context,
    runID string,
    executionToken int64,
    values []domain.RunEvent,
) ([]domain.RunEvent, error) {
    var persisted []domain.RunEvent
    queries := query.Use(store.database)
    err := queries.Transaction(func(transaction *query.Query) error {
        run, err := lockOwnedRun(ctx, transaction, runID, executionToken)
        if err != nil {
            return err
        }
        persisted, err = persistEvents(ctx, transaction, run, values)
        return err
    })
    if err != nil {
        return nil, fmt.Errorf("append Run events: %w", err)
    }
    return persisted, nil
}
```

Do not add handwritten SQL or manual transaction calls.

- [ ] **Step 4: Implement the stream buffer**

Use constants:

```go
const streamFlushInterval = 100 * time.Millisecond
const streamFlushBytes = 256
```

The buffer combines adjacent `delta` fragments only for the same stream ID, attempt, and step. Runtime fragment offsets are internal input ordering; the buffer rewrites persisted Delta offsets as contiguous batch ordinals `0, 1, 2...`, so merging raw fragments cannot create gaps in the browser contract. A `started` event flushes prior pending text and is persisted immediately. `Flush` persists remaining text before a non-stream runtime boundary or runner return. Persistence errors are returned synchronously to the Agent runner.

- [ ] **Step 5: Wire the Worker**

Extend `RunStore` with `AppendRunEvents`. Pass `run.Attempt` in `runtime.AgentInput`. Wrap the existing boundary emitter with the stream buffer:

- assistant `started` and buffered `delta` use `AppendRunEvents` and publish only returned committed events;
- final/model/Tool boundaries call `Flush` first, then use `PersistBoundary`;
- runner return calls `Flush` before `CompleteRun`;
- a Delta persistence error stops execution and produces the normal safe failed Run terminal event when the lease is still owned.

- [ ] **Step 6: Run backend tests and vet**

```sh
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./internal/store ./internal/worker ./internal/runtime -count=1
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go vet ./internal/store ./internal/worker ./internal/runtime
```

- [ ] **Step 7: Commit**

```sh
git add backend/internal/store/execution.go backend/internal/store/execution_test.go backend/internal/worker/worker.go backend/internal/worker/stream_buffer.go backend/internal/worker/stream_buffer_test.go
git commit -m "feat: persist buffered assistant deltas"
```

### Task 3: SSE Cursor Recovery and Safe Tool Events

**Files:**
- Create: `backend/internal/httpapi/sse_test.go`
- Modify: `backend/internal/httpapi/sse.go`
- Modify: `backend/internal/worker/worker.go`
- Create: `backend/internal/worker/tool_event.go`
- Create: `backend/internal/worker/tool_event_test.go`

**Interfaces:**
- Changes: `eventCursor` prefers `Last-Event-ID` over `after` when both exist.
- Produces safe Tool payload fields: `toolCallId`, `tool`, `status`, `arguments`, `resultSummary`, `durationMs`.

- [ ] **Step 1: Write failing SSE cursor tests**

Add table tests for:

```go
{query: "0", header: "7", want: 7}
{query: "3", header: "", want: 3}
{query: "", header: "", want: 0}
{query: "0", header: "invalid", wantError: true}
```

The first case must fail against the current query-first implementation.

- [ ] **Step 2: Write failing Tool payload tests**

For `code.search`, assert only `repo`, `query`, `pathPrefix`, `glob`, and `maxResults` survive. For `file.read`, assert only `repo`, `path`, `startLine`, and `endLine` survive. Unknown keys and any absolute root field are absent. Assert completed payloads include status, safe result summary, and non-negative duration.

- [ ] **Step 3: Run focused tests and verify RED**

```sh
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./internal/httpapi ./internal/worker -run 'TestEventCursor|TestSafeTool' -count=1
```

- [ ] **Step 4: Implement header-first cursor selection**

Change `eventCursor` to read and validate `Last-Event-ID` first. Only when the header is empty may it use `after`. Keep non-negative integer validation unchanged.

- [ ] **Step 5: Implement Tool-specific safe payloads**

Implement:

```go
func safeToolArguments(name string, raw json.RawMessage) map[string]any
func toolEventPayload(event runtime.RuntimeEvent, startedAt time.Time, finishedAt time.Time) (json.RawMessage, error)
```

Decode into a map, copy only the approved keys for the known Tool name, and return an empty map for unknown Tools. Track Tool start times by Tool Call ID inside one Run emitter. Never log or return rejected keys.

- [ ] **Step 6: Run backend tests and vet**

```sh
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./... -count=1
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go vet ./...
```

- [ ] **Step 7: Commit**

```sh
git add backend/internal/httpapi/sse.go backend/internal/httpapi/sse_test.go backend/internal/worker/worker.go backend/internal/worker/tool_event.go backend/internal/worker/tool_event_test.go
git commit -m "feat: make SSE and tool progress resumable"
```

### Task 4: Attempt-Aware Frontend Run Projection

**Files:**
- Modify: `frontend/src/api/events.ts`
- Modify: `frontend/src/state/conversation.ts`
- Create: `frontend/src/state/conversation.test.ts`
- Modify: `frontend/src/App.tsx`

**Interfaces:**
- Produces: `AssistantDraft { runID, streamID, attempt, stepNo, nextOffset, content }`.
- Produces selectors: `assistantDraft(state, runID)` and `runEvents(state, runID)`.
- Changes: EventSource subscribes to `assistant.started`, `assistant.delta`, and `assistant.completed`.

- [ ] **Step 1: Write failing reducer tests**

Cover this exact sequence:

1. `assistant.started` for attempt 1 creates an empty draft.
2. offsets 0 and 1 append `"hello "` and `"world"`.
3. replaying the same `(runID, seq)` does nothing.
4. a stale offset or attempt is ignored.
5. `assistant.started` for attempt 2 replaces attempt 1 content.
6. a `snapshot` containing the Run's final assistant Message clears the draft.

- [ ] **Step 2: Run the reducer test and verify RED**

```sh
npm test -- --run src/state/conversation.test.ts
```

Expected: compilation fails because draft state and selectors are absent.

- [ ] **Step 3: Extend event subscription and reducer state**

Use:

```ts
export type AssistantDraft = {
  runID: string;
  streamID: string;
  attempt: number;
  stepNo: number;
  nextOffset: number;
  content: string;
};

export type ConversationState = {
  detail?: ConversationDetail;
  events: Record<string, RunEvent[]>;
  drafts: Record<string, AssistantDraft>;
  seen: Set<string>;
};
```

Make `snapshot` update `state.detail` directly instead of requiring `App` to inspect the synthetic snapshot event. Remove a draft when the snapshot contains an assistant Message with the same Run ID.

- [ ] **Step 4: Simplify App state consumption**

Read `state.detail`, select the latest Run's draft and events, and pass them to Chat and RunTrace. Keep terminal reload behavior. Do not create a second assistant Message when the authoritative snapshot arrives.

- [ ] **Step 5: Run frontend tests and build**

```sh
npm test -- --run
npm run build
```

- [ ] **Step 6: Commit**

```sh
git add frontend/src/api/events.ts frontend/src/state/conversation.ts frontend/src/state/conversation.test.ts frontend/src/App.tsx
git commit -m "feat: project durable assistant streams"
```

### Task 5: Streaming Markdown Chat

**Files:**
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Create: `frontend/src/components/MarkdownContent.tsx`
- Create: `frontend/src/components/MarkdownContent.test.tsx`
- Modify: `frontend/src/components/Chat.tsx`
- Create: `frontend/src/components/Chat.test.tsx`
- Modify: `frontend/src/app.css`

**Interfaces:**
- Produces: `MarkdownContent({ content }: { content: string })`.
- Changes: `Chat` accepts `draft?: AssistantDraft`.

- [ ] **Step 1: Install locked Markdown dependencies**

```sh
npm install react-markdown remark-gfm
```

Do not add a raw-HTML plugin.

- [ ] **Step 2: Write failing Markdown and draft tests**

Assert headings, GFM tables, fenced code, and lists render as elements. Render `<script>window.bad=true</script>` and assert no executable script element is present. In Chat, assert a draft appears as an Agent article with `aria-busy="true"`, then disappears when a formal assistant Message with the same Run ID is provided.

- [ ] **Step 3: Run tests and verify RED**

```sh
npm test -- --run src/components/MarkdownContent.test.tsx src/components/Chat.test.tsx
```

- [ ] **Step 4: Implement safe Markdown and streaming Chat**

`MarkdownContent` uses:

```tsx
<ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
```

Render assistant formal messages and drafts through it. Keep user messages as `<p>{message.content}</p>`. Add a CSS-only generation cursor and responsive styles for tables, preformatted code, block quotes, and links.

- [ ] **Step 5: Run frontend tests and build**

```sh
npm test -- --run
npm run build
```

- [ ] **Step 6: Commit**

```sh
git add frontend/package.json frontend/package-lock.json frontend/src/components/MarkdownContent.tsx frontend/src/components/MarkdownContent.test.tsx frontend/src/components/Chat.tsx frontend/src/components/Chat.test.tsx frontend/src/app.css
git commit -m "feat: render streaming assistant markdown"
```

### Task 6: Live Safe Run Timeline

**Files:**
- Modify: `frontend/src/api/client.ts`
- Modify: `frontend/src/components/RunTrace.tsx`
- Create: `frontend/src/components/RunTrace.test.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/app.css`

**Interfaces:**
- Changes: `RunTrace` accepts `{ runID?: string; events: RunEvent[] }`.
- Changes: `TraceStep` and `ToolCall` include persisted timestamps; Tool arguments remain `unknown` and are rendered through a UI whitelist.

- [ ] **Step 1: Write failing timeline tests**

Mock `api.getTrace` with a persisted completed Tool Call, then supply live `tool.started`, `tool.completed`, and assistant stream events. Assert:

- persisted entries render after refresh;
- live entries update without changing Run ID;
- safe repository/path/query/line fields render;
- an injected `workspaceRoot` field does not render;
- Tool Result is inside a closed `<details>` element;
- failed Tool status is visible;
- model reasoning text supplied in an unknown payload field is not rendered.

- [ ] **Step 2: Run the test and verify RED**

```sh
npm test -- --run src/components/RunTrace.test.tsx
```

- [ ] **Step 3: Implement trace normalization and merge**

Create focused internal helpers in `RunTrace.tsx`:

```ts
function persistedTimeline(trace: RunTrace): TimelineItem[]
function liveTimeline(events: RunEvent[]): TimelineItem[]
function mergeTimeline(persisted: TimelineItem[], live: TimelineItem[]): TimelineItem[]
function safeArguments(toolName: string, value: unknown): Array<[string, string]>
```

Merge by Tool Call ID or model step key, preferring live status. Compute persisted duration from `createdAt` and `updatedAt`. Render semantic ordered-list items with status labels and a collapsed result summary.

- [ ] **Step 4: Wire App and styles**

Pass the latest Run's projected events to RunTrace. Show queued/running terminal states without hiding failed details. Style completed, active, and failed items distinctly without exposing full payload JSON.

- [ ] **Step 5: Run frontend tests and build**

```sh
npm test -- --run
npm run build
```

- [ ] **Step 6: Commit**

```sh
git add frontend/src/api/client.ts frontend/src/components/RunTrace.tsx frontend/src/components/RunTrace.test.tsx frontend/src/App.tsx frontend/src/app.css
git commit -m "feat: show live safe run timeline"
```

### Task 7: Documentation and End-to-End Streaming Acceptance

**Files:**
- Modify: `README.md`
- Modify: `docs/demo-script.md`
- Create: `tests/e2e/durable-streaming.sh`
- Modify: `tests/e2e/stream-reconnect.sh`

**Interfaces:**
- Produces: one repeatable DeepSeek streaming acceptance script using the existing local stack.

- [ ] **Step 1: Add an E2E script that proves pre-terminal output**

The script creates a Conversation, opens `/runs/{runId}/events?after=0` with `curl -N`, and records event types and IDs in a temporary directory. It must assert:

- at least one `assistant.delta` arrives before `run.completed`;
- event IDs are strictly increasing;
- concatenated Delta text is non-empty;
- the terminal Conversation contains a formal assistant Message;
- trace contains at least one model or Tool step.

Use `--noproxy '*'`, condition-based polling, a configurable 120-second deadline, and a trap that removes only its temporary files.

- [ ] **Step 2: Extend restart acceptance**

Update `stream-reconnect.sh` to capture the latest SSE ID, restart API, reconnect with `Last-Event-ID`, and assert no event at or below that ID is returned. Preserve the existing same-Run assertion.

- [ ] **Step 3: Update documentation**

Document durable Delta events, Markdown behavior, the safe timeline, why reasoning is not shown, and how to inspect `.local/logs` plus the trace API. Update the demo sequence to visibly show Delta output before terminal state and expand one Tool summary.

- [ ] **Step 4: Run complete static and unit verification**

```sh
sh -n scripts/lib/local.sh scripts/up.sh scripts/down.sh scripts/restart.sh tests/e2e/*.sh tests/scripts/*.sh
sh tests/scripts/up_test.sh
sh tests/scripts/down_test.sh
sh tests/scripts/restart_test.sh
GOCACHE=$(pwd)/backend/.gocache GOMODCACHE=$(pwd)/backend/.gomodcache go -C backend test ./...
GOCACHE=$(pwd)/backend/.gocache GOMODCACHE=$(pwd)/backend/.gomodcache go -C backend vet ./...
GOCACHE=$(pwd)/mcp-server/.gocache GOMODCACHE=$(pwd)/mcp-server/.gomodcache go -C mcp-server test ./...
GOCACHE=$(pwd)/mcp-server/.gocache GOMODCACHE=$(pwd)/mcp-server/.gomodcache go -C mcp-server vet ./...
npm --prefix frontend test -- --run
npm --prefix frontend run build
docker compose --env-file ../../.env config --quiet
docker compose --env-file ../../.env -f compose.full.yaml config --quiet
```

Expected: every command exits zero; frontend reports all tests passed; both Go modules report no failures or vet diagnostics.

- [ ] **Step 5: Run real local acceptance**

Start with `./scripts/up.sh`, run `tests/e2e/durable-streaming.sh`, `tests/e2e/stream-reconnect.sh`, and `tests/e2e/recovery.sh`, then verify in the browser that Markdown and the Tool timeline render while the same Run is active. Confirm logs contain Run IDs, Tool names, statuses, and durations but no configured API key or complete code body.

- [ ] **Step 6: Commit**

```sh
git add README.md docs/demo-script.md tests/e2e/durable-streaming.sh tests/e2e/stream-reconnect.sh
git commit -m "docs: verify durable streaming run UX"
```
