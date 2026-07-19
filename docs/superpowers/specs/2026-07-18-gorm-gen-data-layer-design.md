# GORM Gen Data Layer Design

**Status:** Approved design; awaiting written-spec review
**Date:** 2026-07-18

## Decision

The Agent Platform backend uses GORM Gen as its exclusive persistence API. Handwritten application
code does not import `database/sql`, call `Raw` or `Exec`, construct SQL strings, or maintain
handwritten SQL migrations. Checked-in `internal/query` files are generated GORM Gen output; their
generator-owned imports are not an application-level persistence escape hatch.

## Structure

- `backend/internal/model/` contains the handwritten, code-first GORM model definitions. These
  models are the only schema source.
- `backend/cmd/gen/` is the checked-in generator entrypoint. It produces
  `backend/internal/query/`, which is checked in and regenerated whenever a model changes.
- `backend/internal/database/` opens and configures `*gorm.DB` for MySQL.
- `backend/internal/store/` owns persistence workflows. It consumes GORM Gen's generated
  `query.Query` and model types only.
- `backend/cmd/migrate/` runs `AutoMigrate` over the registered models. It is a distinct one-shot
  deployment process; API and Worker processes never alter schema at startup.

GORM-generated DDL is the MVP migration path. There are no `.sql` migration files. This is
intentional for the initial demo: schema changes are additive and all durable records are retained.
Before the production rollout, the migration process will be reviewed separately for versioned,
auditable rollout requirements.

## Models and Generated Queries

The model package defines `User`, `Conversation`, `Message`, `Run`, `RunStep`, `ToolCall`,
`RunCheckpoint`, `RunEvent`, and `Artifact`. Every durable identifier is a 26-character ULID string.
Tags define MySQL column types, foreign keys, unique constraints, and the indexes needed by replay
and work claiming.

The generator uses `gen.ApplyBasic` for every model and enables default query, query interface,
and generic generation modes. Store code imports the generated query package and its typed field
expressions; it never adds a GORM dynamic-SQL interface or custom SQL annotation.

## Transactions and Concurrency

`CreateUserMessageAndRun` is one generated-query transaction. It locks the Conversation row with
the structured GORM `clause.Locking` API, allocates the next message and Run sequences, creates
the Message and queued Run, then advances the Conversation counters before committing.

Every backend transaction uses GORM's callback form: `db.Transaction(func(tx *gorm.DB) error {
... })`, or GORM Gen's equivalent `query.Transaction` callback. Store code must never call
`Begin`, `Commit`, or `Rollback` directly. This centralizes rollback behavior and ensures an error
returned by any generated-query operation aborts the complete workflow.

`ClaimNextRun` is also one transaction. It uses generated Run and Conversation fields to constrain
the claim to a queued Run whose sequence equals the Conversation's executable sequence. It adds
`clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}` through GORM's clause API, then makes a
conditional generated-query update that increments the execution token and records a 30-second
lease.

`RenewLease` and terminal transitions use conditional generated-query updates containing the Run
ID, execution token, and required status. A result other than exactly one affected row maps to the
existing `ErrLeaseLost` domain error. No Worker keeps database state in memory after a transaction
commits.

## Migration and Operations

`cmd/migrate` receives the same `MYSQL_DSN` configuration as API and Worker. Deployments run it
once, successfully, before rolling API or Worker replicas. Local `scripts/up.sh` starts MySQL and
Redis; a documented one-shot migrate command prepares the schema after containers become healthy.

`database.Open` configures bounded connection pooling and uses GORM's MySQL driver. All database
operations receive caller-provided `context.Context`; `context.Context` is never retained by a
Store or GORM model.

## Testing

Pure domain tests remain unit tests. Persistence tests run against a real MySQL DSN supplied through
`TEST_MYSQL_DSN`, execute `AutoMigrate` for an isolated database, and verify observable behavior:
idempotency, conversation ordering, skip-locked claiming, lease-fence rejection, and terminal
sequence advancement. SQL mocks and assertions on emitted SQL are removed.

When `TEST_MYSQL_DSN` is absent, integration tests explicitly skip with the configuration name;
they never replace MySQL semantics with an in-memory database.
