# Worker Run Concurrency Design

**Date:** 2026-07-25  
**Status:** Approved for implementation

## Goal

Allow one Worker process to execute multiple durable Agent Runs concurrently, with a configurable concurrency limit (default **10**), without changing lease / `execution_token` semantics or Conversation queue ordering.

## Non-goals

- Changing MySQL schema, lease duration (30s), or renew interval (10s).
- Introducing a Redis (or other) external work queue for Runs.
- Coordinating a global concurrency cap across Worker replicas.
- Changing product semantics for cancel, steer, checkpoint resume, or failover.

## Language

Spec text, implementation code, comments, and log messages for this work are **English only**.

## Background

Today `Worker.Run` is a single loop: claim one Run, `Execute` it to completion, then claim again. `ClaimNextRun` already uses `SELECT ... FOR UPDATE SKIP LOCKED` with Conversation executable-sequence gating, so the database can safely serve concurrent claimers. The bottleneck is process-local: one in-flight `Execute` at a time.

Lease authority remains on the **Run** row (`lease_owner`, `lease_expires_at`, `execution_token`), not on Conversation. Conversation row locks are used for queue advancement and message creation, not for lease ownership.

## Decisions

| Topic | Choice |
|-------|--------|
| Concurrency model | **B2**: one claimer + batch `ClaimNextRuns` + bounded execution pool |
| Single-claim API | Keep `ClaimNextRun`, mark **Deprecated**, implement as `ClaimNextRuns(..., 1)` |
| Config | `WORKER_CONCURRENCY`, default `10`, must be `>= 1`, no upper bound |
| Shutdown | Cancel root context, wait for in-flight work up to a **fixed 25s**, then return; leftovers reclaim via lease expiry |
| Shutdown timeout | Code constant only (not configurable) |
| Process-fatal errors | Explicit classifier with a **narrow fatal whitelist** (see below) |

## Architecture

```text
Worker.Run(ctx)
├── claimer loop (1 goroutine)
│     free = concurrency - inFlight
│     if free > 0:
│       runs = Store.ClaimNextRuns(ctx, workerID, now, free)
│       submit each run to the execution pool
│     else / empty queue → wait (slot free signal or pollInterval)
│
└── execution pool (up to C concurrent Execute calls)
      Execute(run)  // existing per-run lease renew + runtime + persist paths
```

Idle claim load scales with the number of Worker **processes** (`W`), not with per-process concurrency (`C`). When the pool is full, the claimer does not call the database.

### Ownership (unchanged)

| Concern | Owner |
|---------|--------|
| Queue / steer / stop / lease / `execution_token` | Platform Store + Worker |
| Step / ToolCall / RunEvent / Checkpoint | Existing `Execute` / persist paths |
| Model / tool loop | `AgentRunner` |

## Store: `ClaimNextRuns`

```go
// ClaimNextRuns leases up to limit currently executable Runs for workerID
// in a single transaction.
func (store *Store) ClaimNextRuns(
    ctx context.Context,
    workerID string,
    now time.Time,
    limit int,
) ([]domain.Run, error)
```

### Behavior

| Case | Result |
|------|--------|
| `limit < 1` | Return an error (caller bug / invalid config path) |
| No eligible Runs | `([]domain.Run{}, nil)` |
| `k` eligible with `k < limit` | Return `k` runs, `nil` error |
| Success | Each returned Run is `running` with bumped `attempt` / `execution_token` and a fresh 30s lease |

### Transaction shape

```text
TX {
  SELECT ... FOR UPDATE SKIP LOCKED
    JOIN conversations (same WHERE / ORDER as today)
    LIMIT :limit
  for each candidate:
    claimRun(...)  // reuse existing conditional update + fence
}
```

- Eligibility predicates stay identical to `ClaimNextRun` (queued, or running with expired lease; due `next_attempt_at`; `queue_seq = next_executable_run_seq`).
- If a mid-batch `claimRun` fails unexpectedly, **roll back the whole transaction** and return the error. Partial success is not exposed.
- No schema migration. `RenewLease`, `PersistBoundary`, `AppendRunEvents`, and `CompleteRun` remain per-Run and token-fenced.

### Compatibility

```go
// Deprecated: Use ClaimNextRuns. Kept as a thin wrapper around ClaimNextRuns(limit=1).
func (store *Store) ClaimNextRun(...) (domain.Run, bool, error)
```

The Worker `RunStore` interface switches to `ClaimNextRuns`. Test fakes follow.

## Claimer + pool + shutdown

### Scheduling

- Track `inFlight` with a mutex or atomic counter and a `sync.WaitGroup`.
- Claim batch size = current free slots (`concurrency - inFlight`), never more.
- On empty claim result, sleep `pollInterval` (default 1s) or wait on context cancellation.
- On full pool, wait until a slot frees or context cancellation; do not poll the DB while full.
- Each accepted Run starts one goroutine that calls existing `Execute` (each Run keeps its own renew goroutine).

Recommended shape: count-limited on-demand goroutines (not a second buffered job queue), so claim backpressure is exactly “free slots”.

### Shutdown

On `SIGINT` / `SIGTERM` (root context cancel):

1. Claimer stops claiming.
2. In-flight `Execute` contexts cancel (renewal stops; runner cooperates via existing cancel checks).
3. Wait for `inFlight` to reach zero, up to **`shutdownWait = 25s`** (constant).
4. If still running after 25s, `Run` returns; the process exits. Remaining Runs reclaim after ~30s lease expiry by another Worker.

Shipped deployments must allow the drain to finish: Worker `terminationGracePeriodSeconds` / Compose `stop_grace_period` must be **greater than `shutdownWait`** (currently **40s** in `deploy/ha/workloads.yaml` and `compose.full.yaml`). Otherwise the platform SIGKILLs the process mid-drain.

Today’s single-threaded loop often waits for the current `Execute` naturally. Concurrent execution requires an explicit wait + timeout so rolling deploys do not block indefinitely.

## Error classification (process exit)

Introduce an explicit helper, e.g. `IsFatalWorkerError(error) bool`, used by the claimer and pool. **Only fatal errors stop the Worker process.**

### Fatal (stop claimer, shut down, `Run` returns the error → process exits)

| Source | Rule |
|--------|------|
| Claimer | Any non-nil error from `ClaimNextRuns` is fatal **except** `context.Canceled` / `context.DeadlineExceeded` caused by Worker shutdown. An empty queue must be `(nil error, empty slice)`, never an error. |
| Claimer | Other store/scheduling errors that prevent further claiming are fatal (same conservative default). |
| Anywhere | Errors explicitly wrapped as a reserved fatal sentinel (e.g. `FatalError`) are fatal. |

Rationale: if the Worker cannot claim from MySQL, continuing to run a half-alive pool hides outage and stalls drain/failover signals. Orchestration should restart the process.

### Non-fatal (log and continue; free the slot)

| Source | Rule |
|--------|------|
| Claimer / Executor | `context.Canceled` (and shutdown-driven deadline) — expected; `Run` returns `ctx.Err()` after the shutdown wait. |
| Executor | `store.ErrLeaseLost` — do not terminalize; another owner may exist. |
| Executor | All other `Execute` errors after the existing failure/completion paths — **non-fatal by default** (model/tool/business failure must not kill the pool). |
| Executor | Only become fatal if wrapped/marked for the fatal whitelist above. |

### Fatal handling flow

1. Record an error log with run/worker fields when applicable.
2. Cancel the root Worker context so the claimer and other work begin shutdown.
3. Apply the same **25s** in-flight wait.
4. Return the fatal error from `Worker.Run` so `main` exits non-zero (except pure `context.Canceled` shutdown).

Do **not** treat ordinary per-Run business failures as fatal. Do **not** require panic for fatal paths; structured log + controlled shutdown is enough. Panic remains only for true programmer invariants if the codebase already uses that pattern elsewhere (out of scope unless an invariant is unreachable by returned errors).

## Configuration

```go
type Config struct {
    // ...
    WorkerID          string
    WorkerConcurrency int // WORKER_CONCURRENCY, default 10, must be >= 1
}
```

- Missing / empty env → default `10`.
- Value `< 1` → `Load` fails.
- No maximum clamp.
- Wire through `cmd/worker` into `worker.New`.
- Startup info log includes `worker_id` and `concurrency`.
- Document in `.env.example` (and deploy samples where Worker env is listed). `WORKER_SHUTDOWN_TIMEOUT` is **not** added.

## Testing

| Layer | Cases |
|-------|--------|
| config | Default 10; reject `0` / negative; accept explicit positive values |
| store | `ClaimNextRuns(limit=3)` claims three; fewer available returns fewer; invalid limit errors; concurrent claimers do not double-claim (follow existing `TEST_MYSQL_DSN` patterns if present) |
| worker | Multiple claimed Runs run concurrently up to `C`; no claim while pool full; fatal claim error stops `Run`; non-fatal `Execute` error does not stop the pool; shutdown waits at most 25s |

## Success criteria

1. One process runs up to `WORKER_CONCURRENCY` Runs at once (default 10).
2. Idle claim QPS does not scale with `C`.
3. Lease, token fencing, checkpoint resume, and HA reclaim behavior stay unchanged.
4. Per-Run business failures do not take down the Worker; claim/DB failures classified as fatal do.
5. After SIGTERM, the process waits at most ~25s for in-flight work before exiting.
6. Spec, code, comments, and logs for this feature are English.

## Rollout

1. Store `ClaimNextRuns` + deprecate `ClaimNextRun`.
2. Config `WORKER_CONCURRENCY`.
3. Worker claimer + pool + fatal classifier + shutdown wait.
4. Tests and env/deploy docs.

## Open questions

None for v1; revisit only if production needs a configurable shutdown wait or a broader fatal MySQL error taxonomy.
