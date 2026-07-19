# GORM Gen Data Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the uncommitted handwritten SQL prototype with a code-first GORM Gen persistence foundation for durable Conversations and Runs.

**Architecture:** Handwritten GORM model declarations define the MySQL schema. A checked-in GORM Gen generator produces typed query objects, while Store composes generated queries inside callback-only transactions. A dedicated migration binary runs `AutoMigrate`; API and Worker do not modify schema.

**Tech Stack:** Go 1.25, GORM, GORM Gen, GORM MySQL driver, MySQL 8.

## Global Constraints

- Handwritten application code must not use `database/sql`, `Raw`, `Exec`, SQL strings, dynamic
  query annotations, or handwritten SQL migration files. Generator-owned GORM Gen imports are permitted.
- Use only `db.Transaction(func(tx *gorm.DB) error { ... })` or GORM Gen's transaction callback; do not call `Begin`, `Commit`, or `Rollback`.
- All durable identifiers are 26-character ULID strings.
- Store functions accept `context.Context`; exported declarations have Go doc comments; functions normally stay under 80 lines.
- The user explicitly deferred MySQL integration tests until a reachable MySQL image is available. Run generation, formatting, compilation, and `go vet` during implementation; add and run integration tests before declaring this task complete.

---

## File Structure

```text
backend/cmd/gen/main.go                 Generates internal/query from model declarations.
backend/cmd/migrate/main.go             Runs one-shot GORM AutoMigrate.
backend/internal/database/database.go   Opens configured GORM MySQL connection.
backend/internal/model/*.go             Durable schema declarations and registry.
backend/internal/query/*.go             Checked-in GORM Gen output.
backend/internal/domain/*.go            Run state and Store-facing DTOs.
backend/internal/store/store.go         Store construction and shared errors.
backend/internal/store/conversations.go Conversation transaction workflow.
backend/internal/store/runs.go          Claim and lease transaction workflows.
```

### Task 1: Replace the handwritten SQL foundation

**Files:**
- Delete: `backend/migrations/000001_initial.up.sql`, `backend/migrations/000001_initial.down.sql`
- Delete: `backend/internal/store/mysql.go`, `backend/internal/store/conversations.go`, `backend/internal/store/runs.go`, `backend/internal/store/conversations_test.go`
- Modify: `backend/go.mod`, `backend/go.sum`
- Create: `backend/internal/database/database.go`, `backend/internal/model/*.go`,
  `backend/cmd/gen/main.go`, `backend/cmd/migrate/main.go`, `backend/internal/store/store.go`

**Interfaces:**
- Produces `database.Open(ctx context.Context, dsn string) (*gorm.DB, error)`.
- Produces `model.All() []any` containing all nine durable model types.
- Produces `store.New(database *gorm.DB) *Store`.

- [x] **Step 1: Remove the handwritten SQL, migration, and SQL-mock prototype.**
- [x] **Step 2: Add `gorm.io/gorm`, `gorm.io/driver/mysql`, and `gorm.io/gen`; remove sqlmock.**
- [x] **Step 3: Add code-first model declarations for User, Conversation, Message, Run, RunStep,
  ToolCall, RunCheckpoint, RunEvent, and Artifact.**
- [x] **Step 4: Add the generator entrypoint, run it, and check generated code into `internal/query`.**
- [x] **Step 5: Add `database.Open`, Store construction, and the callback-only `cmd/migrate` binary.**
- [x] **Step 6: Run `go fmt ./...` and `go vet ./...`; defer all `go test` execution until the
  reachable MySQL environment permits the complete test pass.**
- [x] **Step 7: Commit the generated persistence foundation.**

### Task 2: Implement generated-query Run workflows

**Files:**
- Create: `backend/internal/store/conversations.go`, `backend/internal/store/runs.go`
- Create: `backend/internal/store/conversations_test.go`, `backend/internal/store/runs_test.go`

**Interfaces:**
- Produces `CreateUserMessageAndRun(ctx, conversationID, clientMessageID, content, pinned) (domain.Run, error)`.
- Produces `ClaimNextRun(ctx, workerID, now) (domain.Run, bool, error)`.
- Produces `RenewLease(ctx, runID, executionToken, leaseUntil) error`.

- [x] **Step 1: Implement idempotent user-message and queued-Run creation using a GORM transaction
  callback, a structured `clause.Locking`, and generated field expressions.**
- [x] **Step 2: Implement queue-head claiming in a GORM transaction callback using
  `clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}` and generated conditional updates.**
- [x] **Step 3: Implement token-fenced lease renewal with generated conditional updates and
  `ErrLeaseLost` for any non-one affected result.**
- [ ] **Step 4: Add real-MySQL tests for ordering, duplicate client-message idempotency,
  skip-locked claiming, lease takeover, and stale-token rejection.**
- [ ] **Step 5: Once MySQL is available, run the tests with `TEST_MYSQL_DSN`, then run
  `go fmt ./...`, `go vet ./...`, and `go test ./...`.**
- [ ] **Step 6: Commit the durable Run workflows.**
