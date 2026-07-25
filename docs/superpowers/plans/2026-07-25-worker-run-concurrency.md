# Worker Run Concurrency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one Worker process claim and execute multiple durable Runs concurrently (default 10), via batch `ClaimNextRuns` plus a bounded pool, without changing lease/token semantics.

**Architecture:** Add `ClaimNextRuns` in Store (single TX, `SKIP LOCKED` + `LIMIT`). Keep deprecated `ClaimNextRun` as a thin wrapper. Worker runs one claimer loop that fills free slots and spawns up to `WORKER_CONCURRENCY` `Execute` goroutines. Fatal claim/store errors shut down the process after a 25s in-flight wait; per-Run business failures do not.

**Tech Stack:** Go, GORM Gen store, existing `internal/worker` + `internal/config`, sqlmock for Store tests, fake stores for Worker tests.

**Spec:** `docs/superpowers/specs/2026-07-25-worker-run-concurrency-design.md`

## Global Constraints

- Spec text, code, comments, and log messages for this work are English only.
- Do not change lease duration (30s), renew interval (10s), or MySQL schema.
- Use GORM Gen callback transactions only; no raw SQL in application code.
- `ClaimNextRun` remains exported but Deprecated; new code uses `ClaimNextRuns`.
- Shutdown wait is a fixed 25s constant (not configurable).
- `WORKER_CONCURRENCY` default 10, must be `>= 1`, no upper bound.

## File Structure

```text
backend/internal/config/config.go              Parse WORKER_CONCURRENCY
backend/internal/config/config_test.go         Default / reject invalid
backend/internal/store/runs.go                 ClaimNextRuns + deprecate ClaimNextRun
backend/internal/store/runs_claim_test.go      sqlmock tests for batch claim
backend/internal/worker/errors.go              IsFatalWorkerError + FatalError
backend/internal/worker/errors_test.go         Classifier unit tests
backend/internal/worker/worker.go              Claimer + pool + shutdown; RunStore API
backend/internal/worker/worker_test.go         Concurrency / fatal / non-fatal / shutdown
backend/cmd/worker/main.go                     Pass concurrency; log it
.env.example                                   Document WORKER_CONCURRENCY
compose.full.yaml                              Pass WORKER_CONCURRENCY for worker
deploy/ha/workloads.yaml                       Optional env (default OK if omitted)
```

---

### Task 1: Config `WORKER_CONCURRENCY`

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`
- Modify: `.env.example`
- Modify: `compose.full.yaml` (worker service env)
- Modify: `deploy/ha/workloads.yaml` (optional explicit env; omit is fine if default applies)

**Interfaces:**
- Produces `Config.WorkerConcurrency int`
- `Load()` reads `WORKER_CONCURRENCY`, default `10`, error if parsed value `< 1` or non-integer

- [ ] **Step 1: Write failing config tests**

Add to `backend/internal/config/config_test.go`:

```go
func TestLoadWorkerConcurrencyDefault(t *testing.T) {
	t.Setenv("MYSQL_DSN", "demo:demo@tcp(mysql:3306)/agent_platform")
	t.Setenv("WORKER_CONCURRENCY", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.WorkerConcurrency != 10 {
		t.Errorf("WorkerConcurrency = %d, want 10", got.WorkerConcurrency)
	}
}

func TestLoadWorkerConcurrencyRejectsNonPositive(t *testing.T) {
	t.Setenv("MYSQL_DSN", "demo:demo@tcp(mysql:3306)/agent_platform")
	t.Setenv("WORKER_CONCURRENCY", "0")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for WORKER_CONCURRENCY=0")
	}
}
```

Also add a positive explicit value case (`WORKER_CONCURRENCY=3` → `3`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C backend test ./internal/config -run TestLoadWorkerConcurrency -v`  
Expected: FAIL (field missing or wrong default)

- [ ] **Step 3: Implement config parsing**

In `config.go`:

```go
const defaultWorkerConcurrency = 10

// Config fields:
WorkerConcurrency int

// In Load(), after building the struct:
concurrency, err := concurrencyFromEnv("WORKER_CONCURRENCY", defaultWorkerConcurrency)
if err != nil {
	return Config{}, err
}
config.WorkerConcurrency = concurrency
```

Helper:

```go
func concurrencyFromEnv(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if value < 1 {
		return 0, fmt.Errorf("%s must be >= 1", key)
	}
	return value, nil
}
```

- [ ] **Step 4: Run config tests**

Run: `go -C backend test ./internal/config -v`  
Expected: PASS

- [ ] **Step 5: Document env**

Add to `.env.example` after `WORKER_ID`:

```bash
# Max concurrent Runs executed by one Worker process.
WORKER_CONCURRENCY=10
```

In `compose.full.yaml` worker `environment`, add:

```yaml
WORKER_CONCURRENCY: ${WORKER_CONCURRENCY:-10}
```

(Optional) In `deploy/ha/workloads.yaml` worker container env, add `WORKER_CONCURRENCY` from ConfigMap or a literal default; omitting is acceptable because the binary defaults to 10.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go .env.example compose.full.yaml deploy/ha/workloads.yaml
git commit -m "$(cat <<'EOF'
feat(config): add WORKER_CONCURRENCY with default 10

EOF
)"
```

---

### Task 2: Store `ClaimNextRuns` + deprecate `ClaimNextRun`

**Files:**
- Modify: `backend/internal/store/runs.go`
- Create: `backend/internal/store/runs_claim_test.go`

**Interfaces:**
- Produces `ClaimNextRuns(ctx, workerID string, now time.Time, limit int) ([]domain.Run, error)`
- Keeps `ClaimNextRun` as Deprecated wrapper calling `ClaimNextRuns(..., 1)`

- [ ] **Step 1: Write failing Store tests (sqlmock)**

Create `backend/internal/store/runs_claim_test.go` following the sqlmock style in `execution_test.go`.

Minimum cases:

1. `ClaimNextRuns` with `limit < 1` returns an error and issues no SQL.
2. `ClaimNextRuns(limit=2)` expects `BEGIN`, a `SELECT ... FOR UPDATE` (with `LIMIT`), two `UPDATE runs` claim statements, `COMMIT`, and returns two domain Runs.
3. Empty candidate set: `SELECT` returns `sql.ErrNoRows` / empty → `([]domain.Run{}, nil)`.

Keep regexes loose (`QueryMatcherRegexp`) like existing tests; assert `len(runs)` and `ExecutionToken` / `LeaseOwner` on success if the mock rows provide them.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go -C backend test ./internal/store -run ClaimNextRuns -v`  
Expected: FAIL (`ClaimNextRuns` undefined)

- [ ] **Step 3: Implement `selectClaimCandidates` + `ClaimNextRuns`**

In `runs.go`:

1. Rename/generalize selection:

```go
func selectClaimCandidates(
	ctx context.Context,
	transaction *query.Query,
	now time.Time,
	limit int,
) ([]*model.Run, error) {
	runs, err := transaction.Run.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Join(
			transaction.Conversation,
			transaction.Run.ConversationID.EqCol(transaction.Conversation.ID),
		).
		Where(
			// same predicates as selectClaimCandidate today
		).
		Order(transaction.Run.QueueSeq.Asc()).
		Limit(limit).
		Find()
	if err != nil {
		return nil, fmt.Errorf("select executable Runs: %w", err)
	}
	return runs, nil
}
```

2. Add:

```go
// ClaimNextRuns leases up to limit currently executable Runs for workerID
// in a single transaction.
func (store *Store) ClaimNextRuns(
	ctx context.Context,
	workerID string,
	now time.Time,
	limit int,
) ([]domain.Run, error) {
	if limit < 1 {
		return nil, fmt.Errorf("claim limit must be >= 1")
	}
	queries := query.Use(store.database)
	var claimed []*model.Run
	err := queries.Transaction(func(transaction *query.Query) error {
		candidates, err := selectClaimCandidates(ctx, transaction, now, limit)
		if err != nil {
			return err
		}
		claimed = make([]*model.Run, 0, len(candidates))
		for _, candidate := range candidates {
			run, err := claimRun(ctx, transaction, candidate, workerID, now)
			if err != nil {
				return err
			}
			claimed = append(claimed, run)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim next Runs: %w", err)
	}
	result := make([]domain.Run, 0, len(claimed))
	for _, run := range claimed {
		result = append(result, domainRun(run))
	}
	return result, nil
}
```

3. Replace body of `ClaimNextRun` with wrapper + deprecation comment:

```go
// ClaimNextRun leases the first currently executable queued Run for workerID.
//
// Deprecated: Use ClaimNextRuns. ClaimNextRun remains as a compatibility
// wrapper around ClaimNextRuns with limit 1.
func (store *Store) ClaimNextRun(
	ctx context.Context,
	workerID string,
	now time.Time,
) (domain.Run, bool, error) {
	runs, err := store.ClaimNextRuns(ctx, workerID, now, 1)
	if err != nil {
		return domain.Run{}, false, err
	}
	if len(runs) == 0 {
		return domain.Run{}, false, nil
	}
	return runs[0], true, nil
}
```

4. Remove the old single-row `selectClaimCandidate` if nothing else calls it, or keep it as `selectClaimCandidates(..., 1)` only if useful — prefer one code path.

- [ ] **Step 4: Run Store tests**

Run: `go -C backend test ./internal/store -v`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/runs.go backend/internal/store/runs_claim_test.go
git commit -m "$(cat <<'EOF'
feat(store): add ClaimNextRuns batch lease API

EOF
)"
```

---

### Task 3: Fatal error classifier

**Files:**
- Create: `backend/internal/worker/errors.go`
- Create: `backend/internal/worker/errors_test.go`

**Interfaces:**
- Produces `var ErrFatal = errors.New("worker fatal error")` or a typed wrapper
- Produces `func Fatal(err error) error` wrapping cause with fatal marker
- Produces `func IsFatalWorkerError(err error) bool`

Rules from spec:

- `nil` → false
- `context.Canceled` / `context.DeadlineExceeded` → false (shutdown)
- `store.ErrLeaseLost` → false
- errors wrapped with `Fatal(...)` → true
- Claim-path usage: treat any non-nil `ClaimNextRuns` error as fatal **unless** it is context cancellation/deadline (caller may use `IsFatalWorkerError` after filtering context, or `IsFatalWorkerError` returns false for context errors and claimer special-cases: `if err != nil && !errors.Is(err, context.Canceled) { return Fatal(err) }`)

Recommended API so claimer stays clear:

```go
// Fatal marks err as process-fatal for the Worker.
func Fatal(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrFatal, err)
}

func IsFatalWorkerError(err error) bool {
	return errors.Is(err, ErrFatal)
}
```

Claimer then does:

```go
runs, err := store.ClaimNextRuns(...)
if err != nil {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return Fatal(err)
}
```

Executor never wraps ordinary `Execute` errors with `Fatal`.

- [ ] **Step 1: Write classifier tests**

```go
func TestIsFatalWorkerError(t *testing.T) {
	if IsFatalWorkerError(nil) {
		t.Fatal("nil should not be fatal")
	}
	if IsFatalWorkerError(context.Canceled) {
		t.Fatal("context.Canceled should not be fatal")
	}
	if IsFatalWorkerError(store.ErrLeaseLost) {
		t.Fatal("ErrLeaseLost should not be fatal")
	}
	if !IsFatalWorkerError(Fatal(errors.New("db down"))) {
		t.Fatal("Fatal-wrapped error should be fatal")
	}
}
```

- [ ] **Step 2: Run to verify fail**

Run: `go -C backend test ./internal/worker -run TestIsFatalWorkerError -v`  
Expected: FAIL

- [ ] **Step 3: Implement `errors.go`**

- [ ] **Step 4: Run tests**

Run: `go -C backend test ./internal/worker -run TestIsFatalWorkerError -v`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/worker/errors.go backend/internal/worker/errors_test.go
git commit -m "$(cat <<'EOF'
feat(worker): add fatal error marker for process shutdown

EOF
)"
```

---

### Task 4: Worker claimer + pool + shutdown

**Files:**
- Modify: `backend/internal/worker/worker.go`
- Modify: `backend/internal/worker/worker_test.go`
- Modify: `backend/cmd/worker/main.go`

**Interfaces:**
- `RunStore` replaces `ClaimNextRun` with `ClaimNextRuns(ctx, workerID string, now time.Time, limit int) ([]domain.Run, error)`
- `New(..., workerID string, concurrency int) (*Worker, error)` — reject `concurrency < 1`
- `Run(ctx)` implements claimer + pool + 25s shutdown wait

Constants (add next to existing lease constants):

```go
const (
	leaseRenewInterval  = 10 * time.Second
	leaseDuration       = 30 * time.Second
	defaultPollInterval = time.Second
	shutdownWait        = 25 * time.Second
)
```

#### Scheduling sketch (replace current `Run` / `processNext`)

```go
func (worker *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		inFlight int
		slotFree = make(chan struct{}, 1)
		fatalCh  = make(chan error, 1)
		wg       sync.WaitGroup
	)
	signalFree := func() {
		select {
		case slotFree <- struct{}{}:
		default:
		}
	}
	reportFatal := func(err error) {
		select {
		case fatalCh <- err:
			cancel()
		default:
		}
	}

	// claimer loop in this goroutine (or a child — either is fine;
	// keeping claimer on Run's goroutine simplifies return).
	for {
		if err := firstFatal(fatalCh); err != nil {
			break
		}
		select {
		case <-ctx.Done():
			goto shutdown
		default:
		}

		mu.Lock()
		free := worker.concurrency - inFlight
		mu.Unlock()
		if free < 1 {
			select {
			case <-ctx.Done():
				goto shutdown
			case <-slotFree:
				continue
			}
		}

		runs, err := worker.store.ClaimNextRuns(ctx, worker.workerID, time.Now().UTC(), free)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				goto shutdown
			}
			reportFatal(Fatal(err))
			goto shutdown
		}
		if len(runs) == 0 {
			select {
			case <-ctx.Done():
				goto shutdown
			case <-time.After(worker.pollInterval):
			case <-slotFree:
			}
			continue
		}

		for _, run := range runs {
			mu.Lock()
			inFlight++
			mu.Unlock()
			wg.Add(1)
			go func(run domain.Run) {
				defer async.Recover(ctx, worker.logger)
				defer wg.Done()
				defer func() {
					mu.Lock()
					inFlight--
					mu.Unlock()
					signalFree()
				}()
				if err := worker.Execute(ctx, run); err != nil {
					if errors.Is(err, store.ErrLeaseLost) || errors.Is(err, context.Canceled) {
						return
					}
					if IsFatalWorkerError(err) {
						worker.logger.Error(ctx, "fatal run execution error",
							zap.String("run_id", run.ID), zap.Error(err))
						reportFatal(err)
						return
					}
					worker.logger.Error(ctx, "run execution failed",
						zap.String("run_id", run.ID), zap.Error(err))
				}
			}(run)
		}
	}

shutdown:
	waitCtx, waitCancel := context.WithTimeout(context.Background(), shutdownWait)
	defer waitCancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-waitCtx.Done():
		worker.logger.Warn(ctx, "shutdown wait timed out; exiting with in-flight runs")
	}
	if err := firstFatal(fatalCh); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	// If parent ctx was canceled for SIGTERM, return parent error:
	return context.Canceled // or return the original parent err captured at start
}
```

Refine return semantics to match `cmd/worker/main.go` (it ignores `context.Canceled`). Prefer capturing `parent := ctx` at entry and returning `parent.Err()` on clean signal shutdown.

Remove unused `processNext` once superseded.

Update `New`:

```go
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
	// ...
}
```

- [ ] **Step 1: Update `RunStore` + test fake to `ClaimNextRuns`**

Change interface and `deltaFailureStore` in `worker_test.go`. Existing `TestWorkerFailsRunWhenAssistantDeltaCannotBePersisted` still calls `Execute` directly — keep it; only adjust `New(..., concurrency)` (pass `1`).

- [ ] **Step 2: Write concurrency / fatal / shutdown tests**

Add fakes as needed:

1. **Concurrent execute:** store returns two runs on first `ClaimNextRuns`, then empty; runner blocks both until a release channel closes; assert both entered `Run` before release (use `sync.WaitGroup` or counters with timeout).
2. **Non-fatal execute error does not stop worker:** runner returns a normal error after emit/complete path (or store `CompleteRun` succeeds and Execute returns nil — match current failure behavior). Simpler: make `Execute` path return `errors.New("boom")` via a stub runner that fails immediately; claimer should continue and `Run` should only end on ctx cancel — cancel after observing a second claim call.
3. **Fatal claim error stops worker:** first `ClaimNextRuns` returns `errors.New("db down")`; `Run` returns fatal-wrapped error quickly.
4. **Shutdown bound:** optional; if hard to unit-test 25s, test that cancel triggers wait completion when in-flight finishes quickly (no need to assert the full 25s timeout in CI).

- [ ] **Step 3: Run worker tests — expect fail**

Run: `go -C backend test ./internal/worker -v`  
Expected: FAIL on new API / missing pool behavior

- [ ] **Step 4: Implement claimer + pool + shutdown in `worker.go`**

Follow the sketch; keep `Execute` / renew / persist helpers unchanged aside from shared `ctx` cancel on fatal.

- [ ] **Step 5: Wire `cmd/worker/main.go`**

```go
service, err := worker.New(
	store.New(db),
	runner,
	notifier,
	logger,
	configuration.WorkerID,
	configuration.WorkerConcurrency,
)
// ...
logger.Info(ctx, "worker started",
	zap.String("worker_id", configuration.WorkerID),
	zap.Int("concurrency", configuration.WorkerConcurrency),
)
```

- [ ] **Step 6: Run all affected tests**

```bash
go -C backend test ./internal/config ./internal/store ./internal/worker -v
go -C backend vet ./internal/config ./internal/store ./internal/worker ./cmd/worker
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add backend/internal/worker backend/cmd/worker/main.go
git commit -m "$(cat <<'EOF'
feat(worker): claim batch and run concurrent Executes

EOF
)"
```

---

### Task 5: Spec status + final verification

**Files:**
- Already approved: `docs/superpowers/specs/2026-07-25-worker-run-concurrency-design.md`
- This plan: `docs/superpowers/plans/2026-07-25-worker-run-concurrency.md`

- [ ] **Step 1: Full backend test pass for touched packages**

```bash
go -C backend test ./internal/config ./internal/store ./internal/worker ./cmd/worker -count=1
```

- [ ] **Step 2: Manual smoke (optional if local stack up)**

Set `WORKER_CONCURRENCY=2`, start worker, enqueue two Conversations, confirm overlapping `run started` logs with different `run_id`s.

- [ ] **Step 3: Commit docs if spec/plan not yet committed**

```bash
git add docs/superpowers/specs/2026-07-25-worker-run-concurrency-design.md \
        docs/superpowers/plans/2026-07-25-worker-run-concurrency.md
git commit -m "$(cat <<'EOF'
docs: add worker run concurrency spec and plan

EOF
)"
```

---

## Self-Review

1. **Spec coverage:** Config, `ClaimNextRuns`, deprecate wrapper, claimer+pool, 25s shutdown, fatal classifier, env/docs, English — each has a task/step.
2. **Placeholders:** None intentional; sqlmock expectations may need regex tuning when implementing — adjust to match generated SQL like existing `execution_test.go`.
3. **Type consistency:** `New(..., concurrency int)`, `ClaimNextRuns(...) ([]domain.Run, error)`, `IsFatalWorkerError` / `Fatal` used consistently across tasks.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-25-worker-run-concurrency.md`. Two execution options:

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks  
2. **Inline Execution** — execute tasks in this session with executing-plans checkpoints  

Which approach?
