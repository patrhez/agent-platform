# Provider Continuation Production Hardening Design

**Date:** 2026-08-09
**Status:** Implemented

## Goal

Close the production correctness gaps found after the initial durable provider
continuation implementation without changing its persistence model, wire
formats, encryption format, or provider adapter contract.

This hardening preserves the core ownership rule: MySQL checkpoints and their
encrypted replay companions are authoritative across Worker attempts, while
Eino state remains attempt-local working memory.

## Scope

1. Distinguish a new Run continuation reset from active same-Run recovery.
2. Remove the environment-controlled fail-open path from the production Worker.
3. Prevent replay garbage-collection candidate starvation.
4. Add deterministic regression coverage for recovery and transactional
   persistence invariants.
5. Classify authenticated but malformed replay envelopes as incompatible replay.
6. Update the technical guide and HA verification contract.

## Non-goals

- Changing the replay tables, ciphertext envelope, AES-GCM keyring, or seven-day
  retention policy.
- Adding a second Eino checkpoint store.
- Adding provider artifact conversion.
- Removing the local-development fallback from role-specific database DSNs.
  Production least-privilege grants remain a deployment responsibility, as
  specified by the original design.
- Reproducing an unsafe replay-less provider request in a hardened production
  Worker. The baseline commit remains available for historical reproduction.

## Decisions

### New Run resets; active Run fails closed

The runtime knows whether it is starting a new Run from the absence of a Run
checkpoint. This is the authoritative boundary for reset behavior.

For a new Run:

- a compatible, authenticated Conversation continuation is restored;
- the current triggering user message is appended exactly once;
- an incompatible, missing-key, unauthenticated, or malformed continuation head
  is ignored and the Run starts from safe final Conversation messages; and
- a `run`-scope model ignores any historical Conversation head and starts a new
  provider-native chain.

Resetting a new Run never mutates or deletes the old head in the execution path.
The successful terminal boundary advances it when the selected adapter has
`conversation` scope; normal retention later removes stale artifacts. This keeps
the request path transactionally simple and avoids making recovery depend on a
best-effort cleanup write.

For an active Run with a checkpoint:

- a `required` replay companion must exist;
- metadata, key lookup, authentication, envelope decoding, and adapter restore
  must all succeed; and
- every failure remains a stable fail-closed replay error.

This asymmetry is intentional. A new Run can safely reconstruct visible history;
an active tool loop cannot discard provider-native state without changing the
meaning of the in-progress execution.

### Production has no replay fail-open switch

`HA_TEST_ALLOW_MISSING_PROVIDER_REPLAY` and its corresponding runner field are
removed. No production environment variable may bypass `reasoning_replay:
required` during same-Run recovery.

The HA `baseline` mode now deletes the replay companion and verifies that the
replacement Worker fails with `provider_replay_missing`. The `protected` mode
continues to verify successful takeover. Provider rejection without replay was
already demonstrated by the pre-hardening baseline and does not justify keeping
an unsafe production path.

The checkpoint pause and reasoning-presence assertions may remain as diagnostic
HA hooks because they do not permit recovery without required durable state.
They must stay disabled by default and must not expose reasoning data.

### GC selects deletable work fairly

The candidate query excludes both:

1. replay rows referenced by a Conversation continuation head; and
2. the latest checkpoint of a non-terminal Run.

It orders remaining rows by `created_at`, then `checkpoint_id`, and applies the
batch limit only after both exclusions. The checkpoint ID tie-breaker makes the
batch stable when timestamps are equal.

Deletion still opens a transaction, locks the replay and Run, and repeats both
reference checks immediately before deletion. Query-time filtering guarantees
progress; transaction-time filtering preserves correctness under races.

The query uses portable `NOT EXISTS` predicates and ordinary comparisons so it
remains compatible with MySQL and a future PostgreSQL migration.

### Replay decode failures use the replay taxonomy

AES-GCM authentication failures remain `provider_replay_decrypt_failed`.
Authenticated plaintext that cannot be decoded as the supported envelope or
adapter item format is classified as `provider_replay_incompatible`. Error text
must not include plaintext, ciphertext, keys, nonces, or raw provider bodies.

## Verification requirements

### Runtime recovery tests

- A new Run restores a compatible Conversation continuation and appends its
  trigger once.
- A new Run resets to safe messages for provider, API mode, model, scope,
  adapter, key, authentication, and envelope incompatibility.
- A `run`-scope new Run does not consume a Conversation head.
- An active `required` Run fails with the stable missing, key-unavailable,
  decrypt-failed, or incompatible error as appropriate.
- No checkpoint contains reasoning content.

### Store transaction tests

- A required checkpoint and replay companion commit together.
- Replay insertion failure rolls back checkpoint insertion and latest-pointer
  advancement.
- Terminal success writes the terminal checkpoint/replay and continuation-head
  advancement in one transaction.
- A failed or cancelled terminal Run never advances the head.

### GC tests

- A continuation head protects its replay.
- A non-terminal Run's latest checkpoint is never selected or deleted.
- Protected old rows do not prevent later orphan rows from filling a batch.
- A row that becomes referenced after selection is retained by the deletion
  recheck.
- Repeated bounded passes eventually delete all eligible rows.

### Tooling

The implementation must pass `gofmt`, `git diff --check`, `go vet ./...`,
`go test ./...`, and race tests for the runtime, store, and Worker packages. The
HA protected and missing-companion modes must both reach their documented
terminal outcomes.

## Documentation

Update `docs/tech/reasoning-continuation-and-failover.md` to show the asymmetric
new-Run/same-Run decision, the two-phase GC protection, and the hardened HA
baseline semantics. The original design remains the architectural source; this
document records the production corrections.

## Verification evidence

- Full Go test suite and `go vet ./...` passed.
- Race tests passed for runtime, store, and Worker packages.
- HA missing-companion Run `01KZKJBEPPP55JS0ZXX2R1YJNY` failed closed with
  `provider_replay_missing` on attempt 2.
- HA protected Run `01KZKJCXNAXB6BFF03FQRZ14A7` resumed successfully on attempt
  2 after its owning Worker Pod was force-deleted.
