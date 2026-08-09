# Provider Continuation Production Hardening Plan

**Spec:** `docs/superpowers/specs/2026-08-09-provider-continuation-hardening-design.md`

## Standards

- Follow the project Go coding standards supplied for this change.
- Keep production functions focused and preferably below 80 lines.
- Keep lines at or below 120 characters.
- Add regression tests before or with every behavior change.
- Preserve existing schemas, wire formats, encryption metadata, and safe public
  checkpoint payloads.

## Task 1: Correct the recovery boundary

- [x] Add runtime tests for compatible new-Run continuation restoration.
- [x] Add table-driven new-Run reset tests for incompatible replay conditions.
- [x] Add active-Run fail-closed tests for all stable replay error classes.
- [x] Refactor `restoreState` into focused new-Run and active-Run helpers.
- [x] Ensure `run` scope always starts a new chain at a Run boundary.
- [x] Classify authenticated malformed replay data as incompatible.

## Task 2: Remove the production fail-open path

- [x] Remove `HA_TEST_ALLOW_MISSING_PROVIDER_REPLAY` from `EinoRunner`.
- [x] Remove the environment read and conditional recovery bypass.
- [x] Change HA baseline mode to expect `provider_replay_missing`.
- [x] Update HA documentation and cleanup logic.

## Task 3: Make replay GC fair and race-safe

- [x] Add a regression test where protected oldest rows precede an orphan.
- [x] Exclude live latest-checkpoint references in the candidate query.
- [x] Add a deterministic checkpoint-ID ordering tie-breaker.
- [x] Keep the transactional reference recheck before deletion.
- [x] Test bounded repeated passes and reference races.

## Task 4: Verify atomic persistence

- [x] Test required checkpoint and replay commit in one transaction.
- [x] Test rollback when replay insertion fails.
- [x] Test atomic terminal head advancement.
- [x] Test that failed/cancelled completion does not advance the head.

## Task 5: Document and validate

- [x] Update the technical guide with the hardening decisions and Mermaid flow.
- [x] Run `gofmt` on affected Go files.
- [x] Run `git diff --check`.
- [x] Run `go vet ./...`.
- [x] Run `go test ./...`.
- [x] Run race tests for runtime, store, and Worker.
- [x] Run the HA missing-companion and protected modes.
- [x] Record the final verification evidence and create a separate hardening
  commit.
