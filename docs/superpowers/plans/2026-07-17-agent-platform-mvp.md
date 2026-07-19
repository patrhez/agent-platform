# Agent Platform MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Docker/Podman-runnable TypeScript and Go MVP that performs multi-turn, read-only repository troubleshooting through an Eino ReAct Agent and a self-built Workspace MCP server.

**Architecture:** The `backend` Go module supplies separate API and Worker binaries; the independent `mcp-server` Go module supplies the Workspace MCP binary. MySQL owns Conversations, Runs, leases, Checkpoints, events, and immutable results; Redis only notifies API instances that committed events are available. The Worker calls Workspace MCP through Streamable HTTP, and the frontend renders the API's resumable SSE stream.

**Tech Stack:** Go, GORM, GORM Gen, Eino, MySQL 8, Redis 7, Streamable HTTP MCP, HTTP/2 SSE, React + TypeScript + Vite, Docker Compose / Podman Compose.

## Global Constraints

- Runtime business logic must not read repository files directly; only Workspace MCP may access the read-only repository mount.
- MVP enables only `code.search` and `file.read`; no write Tools, shell, Wiki, Memory, Kafka, Temporal, or repository sync.
- MySQL is authoritative. Redis must not store queue, lease, Checkpoint, Conversation, or replay state.
- Database access uses GORM Gen exclusively; handwritten application code must not introduce
  `database/sql`, `Raw`, `Exec`, SQL strings, or handwritten SQL migration files. Generator-owned
  imports in checked-in GORM Gen output are permitted.
- Use only GORM or GORM Gen transaction callbacks; do not call `Begin`, `Commit`, or `Rollback`.
- Each Run pins Agent configuration and Skills bundle versions; configuration files are release-bundled.
- Do not store or expose raw model chain-of-thought; persist only safe action summaries and formal output.
- Local startup uses `./scripts/up.sh` on macOS and Linux with Podman preferred and Docker as fallback; `--install-runtime` is the explicit opt-in for host Podman installation. MySQL and Redis start by default.
- All durable MVP records are retained permanently; only connections, leases, and process memory are ephemeral.

---

## File Structure

```text
backend/cmd/api/main.go                 HTTP API and SSE process
backend/cmd/worker/main.go              durable Run Worker process
backend/internal/model/*                GORM model definitions and schema constraints
backend/internal/query/*                checked-in GORM Gen output
backend/internal/*                      platform configuration, database, domain, store, runtime, worker, HTTP API
backend/cmd/gen/main.go                 GORM Gen generator entrypoint
backend/cmd/migrate/main.go             one-shot AutoMigrate entrypoint
backend/configs/agents/*.yaml           release-bundled Agent configuration
mcp-server/cmd/workspace-mcp/main.go    read-only MCP process
mcp-server/internal/workspace/*.go      repository-root validation and code Tools
frontend/src/*                          React UI, API client, SSE reducer
compose.yaml                            local services and mounts
scripts/up.sh, scripts/down.sh          Docker/Podman launcher
README.md                               Quickstart and demo instructions
```

## Recommended Implementation Order

1. Bootstrap and configuration.
2. MySQL schema, Conversation ordering, Run state machine, and leases.
3. Workspace MCP server.
4. Worker and Eino ReAct execution.
5. API commands and query endpoints.
6. SSE plus Redis hints.
7. Frontend.
8. Compose end-to-end verification and Quickstart.

This order validates the highest-risk product path—Agent reads code through MCP and recovers from a persisted Run—before UI work.

## Task 1: Bootstrap the runnable repository

**Files:**
- Create: `backend/go.mod`, `backend/cmd/api/main.go`, `backend/cmd/worker/main.go`
- Create: `backend/internal/config/config.go`, `backend/internal/config/config_test.go`
- Create: `mcp-server/go.mod`, `mcp-server/cmd/workspace-mcp/main.go`
- Create: `backend/configs/agents/issue-troubleshooter.yaml`, `compose.yaml`, `.env.example`, `scripts/up.sh`, `scripts/down.sh`, `README.md`
- Create: `frontend/package.json`, `frontend/vite.config.ts`, `frontend/src/main.tsx`

**Interfaces:**
- Produces `config.Load() (config.Config, error)`.
- Produces configuration fields `MySQLDSN`, `RedisURL`, `LLMBaseURL`, `WorkspaceMCPURL`, and `WorkerID`.

- [ ] **Step 1: Write the failing configuration test.**

```go
func TestLoadRejectsMissingMySQLDSN(t *testing.T) {
    t.Setenv("MYSQL_DSN", "")
    _, err := config.Load()
    require.ErrorContains(t, err, "MYSQL_DSN")
}
```

- [ ] **Step 2: Run the test to verify it fails.**

Run: `go -C backend test ./internal/config -run TestLoadRejectsMissingMySQLDSN -v`
Expected: FAIL because package `internal/config` does not exist.

- [ ] **Step 3: Implement the typed configuration loader and process entrypoints.**

```go
type Config struct {
    MySQLDSN, RedisURL, LLMBaseURL, WorkspaceMCPURL, WorkerID string
}
func Load() (Config, error) {
    c := Config{MySQLDSN: os.Getenv("MYSQL_DSN")}
    if c.MySQLDSN == "" { return Config{}, errors.New("MYSQL_DSN is required") }
    return c, nil
}
```

- [ ] **Step 4: Add Compose services for MySQL, Redis, API, Worker, Workspace MCP, and web. Mount `REPOS_DIR:/workspace/repos:ro` only into Workspace MCP.**
- [ ] **Step 5: Implement `scripts/up.sh` to choose `docker compose`, `podman compose`, or `podman-compose`; reject an unset `REPOS_DIR`; then run `up --build`.**
- [ ] **Step 6: Run verification.**

Run: `go -C backend test ./internal/config -v && sh -n scripts/up.sh && sh -n scripts/down.sh`
Expected: PASS.

- [ ] **Step 7: Commit.**

```bash
git add backend frontend mcp-server compose.yaml .env.example scripts README.md
git commit -m "chore: bootstrap local agent platform stack"
```

## Task 2: Add MySQL schema, Conversation ordering, and Run leases

**Files:**
- Create: `backend/migrations/000001_initial.up.sql`, `backend/migrations/000001_initial.down.sql`
- Create: `backend/internal/domain/models.go`, `backend/internal/domain/states.go`
- Create: `backend/internal/store/mysql.go`, `backend/internal/store/conversations.go`, `backend/internal/store/runs.go`
- Test: `backend/internal/store/conversations_test.go`, `backend/internal/store/runs_test.go`

**Interfaces:**
- Produces `CreateUserMessageAndRun(ctx, conversationID, clientMessageID, content, pinned) (Run, error)`.
- Produces `ClaimNextRun(ctx, workerID, now) (Run, bool, error)` and `RenewLease(ctx, runID, token, until) error`.

- [ ] **Step 1: Write the failing ordered-Run test.**

```go
first, _ := store.CreateUserMessageAndRun(ctx, cid, "c1", "first", pinned)
_, _ = store.CreateUserMessageAndRun(ctx, cid, "c2", "second", pinned)
got, ok, _ := store.ClaimNextRun(ctx, "worker-a", time.Now())
require.True(t, ok)
require.Equal(t, first.ID, got.ID)
```

- [ ] **Step 2: Run it against local MySQL and verify failure.**

Run: `go -C backend test ./internal/store -run TestClaimNextRun -v`
Expected: FAIL before schema and Store exist.

- [ ] **Step 3: Create all tables from the detailed design: `users`, `conversations`, `messages`, `runs`, `run_steps`, `tool_calls`, `run_checkpoints`, `run_events`, and `artifacts`.**

```sql
CREATE TABLE runs (
  id CHAR(26) PRIMARY KEY,
  conversation_id CHAR(26) NOT NULL,
  queue_seq BIGINT NOT NULL,
  status VARCHAR(16) NOT NULL,
  execution_token BIGINT NOT NULL DEFAULT 0,
  lease_owner VARCHAR(128), lease_expires_at DATETIME(6),
  next_event_seq BIGINT NOT NULL DEFAULT 0,
  UNIQUE KEY uq_runs_conversation_seq (conversation_id, queue_seq),
  KEY idx_runs_claim (status, next_attempt_at, lease_expires_at)
);
```

- [ ] **Step 4: Implement Conversation-row locking, Message/Run sequence allocation, and idempotency by `(conversation_id, client_message_id)`.**
- [ ] **Step 5: Implement MySQL 8 `FOR UPDATE SKIP LOCKED` claiming, a 30-second lease, and fencing-token renewal.**

```go
func (s *Store) RenewLease(ctx context.Context, id string, token int64, until time.Time) error {
    result, err := s.db.ExecContext(ctx, "UPDATE runs SET lease_expires_at=? WHERE id=? AND execution_token=? AND status='running'", until, id, token)
    if err != nil || affected(result) != 1 { return ErrLeaseLost }
    return nil
}
```

- [ ] **Step 6: Add tests for lease expiry takeover and advancing `next_executable_run_seq` only in a terminal transaction.**
- [ ] **Step 7: Run verification.**

Run: `go -C backend test ./internal/store -v`
Expected: PASS.

- [ ] **Step 8: Commit.**

```bash
git add backend/migrations backend/internal/domain backend/internal/store
git commit -m "feat: add durable conversation and run persistence"
```

## Task 3: Build the read-only Workspace MCP provider

**Files:**
- Create: `mcp-server/internal/workspace/roots.go`, `mcp-server/internal/workspace/search.go`, `mcp-server/internal/workspace/read.go`, `mcp-server/internal/workspace/mcp.go`
- Modify: `mcp-server/cmd/workspace-mcp/main.go`
- Test: `mcp-server/internal/workspace/search_test.go`, `mcp-server/internal/workspace/read_test.go`, `mcp-server/internal/workspace/mcp_test.go`

**Interfaces:**
- Produces Streamable HTTP MCP Tools `code.search` and `file.read` at `POST /mcp`.
- Consumes configured repository aliases and absolute root paths.

- [ ] **Step 1: Write the failing traversal test.**

```go
_, err := svc.Read(ctx, ReadInput{Repo: "orders", Path: "../../etc/passwd", StartLine: 1, EndLine: 2})
require.ErrorIs(t, err, workspace.ErrPathOutsideWorkspace)
```

- [ ] **Step 2: Run it and verify failure.**

Run: `go -C mcp-server test ./internal/workspace -run TestReadRejectsTraversal -v`
Expected: FAIL because the Workspace service does not exist.

- [ ] **Step 3: Implement root resolution and bounded `file.read`: reject absolute paths, traversal, symlink escape, unknown repositories, more than 1,000 lines, and more than 256 KiB.**
- [ ] **Step 4: Implement bounded `code.search`: enforce 1–50 matches and return repository-relative path, line, text, context, and truncation flag.**

```go
type SearchInput struct { Repo, Query, PathPrefix, Glob string; MaxResults int }
func (s *Service) Search(ctx context.Context, in SearchInput) (SearchOutput, error) {
    if in.MaxResults < 1 || in.MaxResults > 50 { return SearchOutput{}, ErrResultLimitExceeded }
    return searchRoot(ctx, s.roots[in.Repo], in)
}
```

- [ ] **Step 5: Register the two functions as MCP Tools and map errors to `INVALID_REPO`, `INVALID_PATH`, `PATH_OUTSIDE_WORKSPACE`, `NOT_FOUND`, `RESULT_LIMIT_EXCEEDED`, `TIMEOUT`, and `INTERNAL`.**
- [ ] **Step 6: Run unit and black-box MCP `tools/list`/call tests.**

Run: `go -C mcp-server test ./internal/workspace -v`
Expected: PASS.

- [ ] **Step 7: Commit.**

```bash
git add mcp-server
git commit -m "feat: add read-only workspace MCP tools"
```

## Task 4: Implement Worker, Eino ReAct execution, Checkpoints, and recovery

**Files:**
- Create: `backend/internal/runtime/agent.go`, `backend/internal/runtime/eino_react.go`, `backend/internal/runtime/checkpoint.go`
- Create: `backend/internal/worker/worker.go`, `backend/internal/worker/execute.go`, `backend/internal/worker/recovery.go`
- Modify: `backend/cmd/worker/main.go`
- Test: `backend/internal/runtime/checkpoint_test.go`, `backend/internal/worker/execute_test.go`, `backend/internal/worker/recovery_test.go`

**Interfaces:**
- Consumes Store lease methods, the bundled Agent YAML, LLM gateway configuration, and Workspace MCP URL.
- Produces `Worker.Run(ctx) error`; durable terminal state and safe events are Store writes.

- [ ] **Step 1: Write the failing recovery test.**

```go
run := seedRunningRun(t, store)
fake := runtime.NewScriptedAgent(runtime.ToolCall("code.search"), runtime.CrashAfterCheckpoint())
require.Error(t, worker.Execute(ctx, run, fake))
require.NotNil(t, store.LatestCheckpoint(ctx, run.ID))
```

- [ ] **Step 2: Run it and verify failure.**

Run: `go -C backend test ./internal/worker -run TestWorkerResumesAfterCheckpoint -v`
Expected: FAIL because Worker execution is absent.

- [ ] **Step 3: Define a runtime adapter independent of Eino.**

```go
type AgentRunner interface {
    Run(ctx context.Context, input AgentInput, checkpoint *Checkpoint, emit func(RuntimeEvent) error) error
}
type RuntimeEvent struct { Kind, Summary, Delta, Final string; Tool *ToolRequest }
```

- [ ] **Step 4: Implement the Eino ReAct adapter using the configured Chat Completions or Responses mode, a 12-step limit, a 10-minute deadline, and only the two allowlisted Tool schemas.**
- [ ] **Step 5: Implement a fenced boundary transaction that writes Step, Tool call/result, versioned Checkpoint, and ordered safe events before publishing a Redis notification.**
- [ ] **Step 6: Add 10-second lease renewal, cancellation checks between boundaries, two transient retries for read-only MCP calls, and expired-lease recovery.**
- [ ] **Step 7: Run verification.**

Run: `go -C backend test ./internal/runtime ./internal/worker -v`
Expected: PASS for recovery, stale-fence rejection, Tool allowlisting, cancellation, and step limits.

- [ ] **Step 8: Commit.**

```bash
git add backend/cmd/worker backend/internal/runtime backend/internal/worker
git commit -m "feat: execute durable Eino troubleshooting runs"
```

## Task 5: Implement API commands and snapshots

**Files:**
- Create: `backend/internal/httpapi/server.go`, `backend/internal/httpapi/auth.go`, `backend/internal/httpapi/conversations.go`, `backend/internal/httpapi/runs.go`, `backend/internal/httpapi/errors.go`
- Modify: `backend/cmd/api/main.go`
- Test: `backend/internal/httpapi/conversations_test.go`, `backend/internal/httpapi/runs_test.go`

**Interfaces:**
- Produces every REST endpoint in detailed design section 4 except the SSE endpoint.
- Consumes Conversation and Run Store methods.

- [ ] **Step 1: Write the failing idempotent Message POST test.**

```go
first := postMessage(t, api, cid, "client-1", "find error")
second := postMessage(t, api, cid, "client-1", "find error")
require.Equal(t, first.RunID, second.RunID)
require.Equal(t, http.StatusAccepted, second.StatusCode)
```

- [ ] **Step 2: Run it and verify failure.**

Run: `go -C backend test ./internal/httpapi -run TestPostMessageReturnsSameRunForSameClientMessageID -v`
Expected: FAIL because handlers are absent.

- [ ] **Step 3: Implement demo authentication behind a `Principal` interface and inject `demo-user` / `demo-team` in middleware.**
- [ ] **Step 4: Implement list/create/get/delete Conversation, Message POST, Run snapshot, trace/artifact reads, cancel, and ownership checks.**
- [ ] **Step 5: Run verification.**

Run: `go -C backend test ./internal/httpapi -v`
Expected: PASS for ownership, idempotency, ordered Messages, cancellation, and soft deletion.

- [ ] **Step 6: Commit.**

```bash
git add backend/cmd/api backend/internal/httpapi
git commit -m "feat: add conversation and run API"
```

## Task 6: Add Redis-hinted, MySQL-replayable SSE

**Files:**
- Create: `backend/internal/events/notifier.go`, `backend/internal/httpapi/sse.go`
- Modify: `backend/internal/store/events.go`, `backend/cmd/api/main.go`, `backend/cmd/worker/main.go`
- Test: `backend/internal/events/notifier_test.go`, `backend/internal/httpapi/sse_test.go`

**Interfaces:**
- Produces `Notifier.Publish(ctx, runID, seq)` after a committed event.
- Produces `GET /api/v1/runs/{id}/events?after={seq}`.

- [ ] **Step 1: Write the failing replay test.**

```go
seedEvents(t, runID, 1, 2, 3, 4)
rr := openSSE(t, api, runID, 3)
require.Contains(t, rr.Body.String(), "id: 4")
require.NotContains(t, rr.Body.String(), "id: 3")
```

- [ ] **Step 2: Run it and verify failure.**

Run: `go -C backend test ./internal/httpapi -run TestSSEReplaysOnlyEventsAfterCursor -v`
Expected: FAIL because the SSE endpoint is absent.

- [ ] **Step 3: Publish only `{runId,seq}` to `agent-platform:run-events:{runId}` after a MySQL commit.**
- [ ] **Step 4: Implement authorization, durable replay, 15-second heartbeat, `Last-Event-ID` fallback, ordered delivery, and terminal-response closure.**
- [ ] **Step 5: Test missed Redis notification and API restart by reading MySQL events after the cursor.**

Run: `go -C backend test ./internal/events ./internal/httpapi -run 'TestSSE|TestNotifier' -v`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add backend/internal/events backend/internal/httpapi backend/internal/store/events.go backend/cmd
git commit -m "feat: stream durable run events over SSE"
```

## Task 7: Build the TypeScript UI against real API contracts

**Files:**
- Create: `frontend/src/api/client.ts`, `frontend/src/api/events.ts`, `frontend/src/state/conversation.ts`
- Create: `frontend/src/components/ConversationList.tsx`, `frontend/src/components/Chat.tsx`, `frontend/src/components/RunTrace.tsx`, `frontend/src/App.tsx`
- Test: `frontend/src/state/conversation.test.ts`, `frontend/src/api/events.test.ts`

**Interfaces:**
- Consumes REST and SSE contracts from Tasks 5 and 6.
- Produces idempotent event reduction keyed by `(runId, seq)`.

- [ ] **Step 1: Write the failing duplicate-event reducer test.**

```ts
it("ignores an event already applied to the same run", () => {
  const state = applyEvent(initialState, event("run-1", 7, "assistant.delta"));
  expect(applyEvent(state, event("run-1", 7, "assistant.delta"))).toEqual(state);
});
```

- [ ] **Step 2: Run it and verify failure.**

Run: `cd frontend && npm test -- --runInBand`
Expected: FAIL because the reducer is absent.

- [ ] **Step 3: Implement Conversation snapshot fetch, Message POST, and EventSource connections with `after=0` on refreshed active Runs.**
- [ ] **Step 4: Implement main chat, collapsible safe trace, terminal EventSource close, and explicit Artifact request.**
- [ ] **Step 5: Run verification.**

Run: `cd frontend && npm test -- --runInBand && npm run build`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add frontend
git commit -m "feat: add troubleshooting chat frontend"
```

## Task 8: Verify portable deployment, recovery, and Quickstart

**Files:**
- Modify: `compose.yaml`, `scripts/up.sh`, `scripts/down.sh`, `README.md`
- Create: `tests/e2e/recovery.sh`, `tests/e2e/stream-reconnect.sh`, `docs/demo-script.md`

**Interfaces:**
- Consumes the full local stack and external `REPOS_DIR`.
- Produces repeatable Worker-recovery and SSE-recovery demonstrations.

- [ ] **Step 1: Write `tests/e2e/recovery.sh`: create a Conversation, post a Message, kill Worker, and poll the Run until terminal.**
- [ ] **Step 2: Run it before recovery wiring and verify failure.**

Run: `REPOS_DIR=/absolute/path/to/repos tests/e2e/recovery.sh`
Expected: FAIL until the full stack is complete.

- [ ] **Step 3: Add Compose health checks, LLM configuration examples, startup/shutdown instructions, and a scripted demonstration to README.**
- [ ] **Step 4: Write and run `tests/e2e/stream-reconnect.sh`: restart API during SSE, reconnect, and assert the same Run reaches terminal without a new Run.**
- [ ] **Step 5: Run the full verification suite.**

Run: `go -C backend test ./... && go -C mcp-server test ./... && (cd frontend && npm test -- --runInBand && npm run build) && REPOS_DIR=/absolute/path/to/repos tests/e2e/recovery.sh && REPOS_DIR=/absolute/path/to/repos tests/e2e/stream-reconnect.sh`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add compose.yaml scripts README.md tests docs/demo-script.md
git commit -m "docs: add portable MVP quickstart and recovery demos"
```

## Plan Self-Review

- **Spec coverage:** Tasks 1–2 implement deployment/configuration and MySQL state; Task 3 implements the MCP boundary; Task 4 implements Eino, Checkpoints, recovery, and Tool policy; Tasks 5–6 implement API and recoverable SSE; Task 7 implements the TypeScript UI; Task 8 validates Docker/Podman, failure recovery, and Quickstart.
- **Placeholder scan:** This plan contains no unresolved markers or deferred implementation steps.
- **Type consistency:** `config.Load`, Store lease methods, `AgentRunner`, Run event sequences, and the two Workspace MCP Tool names are defined once and consumed consistently.
