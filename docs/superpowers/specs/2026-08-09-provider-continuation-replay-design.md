# Durable Provider Continuation Replay Design

**Date:** 2026-08-09
**Status:** Implemented

Production recovery and GC corrections are specified by
[Provider Continuation Production Hardening Design](2026-08-09-provider-continuation-hardening-design.md).

## Goal

Make reasoning-capable tool loops and provider-native continuation survive Worker
loss without making Workers stateful. The platform persists the safe execution
checkpoint and the exact provider protocol state as one durable boundary, while
keeping reasoning data encrypted and outside APIs, traces, events, and logs.

The design covers:

1. the current Eino OpenAI-compatible Chat Completions runtime;
2. a platform-managed OpenAI Responses adapter using `store: false`; and
3. a provider-neutral replay contract that can later support native Anthropic,
   Gemini, Moonshot, and DeepSeek adapters without normalizing their private
   continuation formats.

## Context

Some providers return data that is not merely explanatory text. Examples include
Chat Completions `reasoning_content`, OpenAI Responses reasoning output items,
Anthropic signed thinking blocks, and Gemini thought signatures. During a live
Eino invocation, raw model messages remain in process memory and can be reused by
the next ReAct iteration. The platform currently sanitizes reasoning before its
public checkpoint projection is serialized. A replacement Worker therefore
cannot reconstruct all provider-required request data from the checkpoint.

The HA prototype proved the state-loss boundary and provided a deterministic test
harness. It is not the production persistence design: it embeds replay in the
checkpoint payload and serializes Eino message structs. This design replaces
both choices.

## Vocabulary

- **Safe checkpoint:** platform execution state that may contain messages,
  iteration state, pending tool calls, and control data, but no private reasoning.
- **Provider continuation state:** the provider-native response data required to
  make the next request valid and continuous.
- **Provider continuation chain:** the ordered continuation state associated with
  a Conversation. It is logical protocol state, not a network connection or a
  provider-hosted session.
- **Replay artifact:** one encrypted, complete, self-contained snapshot of the
  provider continuation state at a committed boundary.
- **Continuation head:** the latest successful terminal replay that a later Run
  in the same Conversation may continue from.
- **Attempt-local state:** Eino state held in memory for one Worker attempt.

## Scope

- Persist encrypted replay artifacts beside platform checkpoints.
- Restore an active Run after lease expiry or Worker failure.
- Carry provider continuation across successful Runs in one Conversation.
- Preserve existing Queue and Steer semantics.
- Add key rotation, access separation, retention, garbage collection, failure
  classification, and verification coverage.
- Keep the storage and maintenance design portable between MySQL and PostgreSQL.

## Non-goals

- Exposing model reasoning to users, administrators, traces, or ordinary logs.
- Converting one provider's private continuation format into another provider's
  format.
- Depending on provider-side response storage or `previous_response_id`.
- Configuring Eino's `CheckPointStore` as a second durable checkpoint authority.
- Persisting or restoring an in-progress streaming response before a complete
  provider message or output item exists.
- Introducing KMS/HSM envelope encryption in the first implementation. The
  application keyring contract must allow that future extension.

## Decisions

### Platform storage is authoritative

MySQL is the durable recovery authority. Eino state is only a working copy for
the current attempt. The runtime does not configure Eino's optional external
`CheckPointStore`, because two independently advancing checkpoint stores would
create ambiguous recovery order and split-brain state.

At every durable provider boundary, the safe checkpoint and encrypted replay
artifact are committed in the same database transaction. A boundary is visible
only when both records are durable.

### Replay policy has two modes

- `disabled`: the adapter does not persist provider continuation state.
- `required`: every resumable provider boundary must include a valid encrypted
  replay artifact.

There is no `optional` mode. For a `required` active Run, a missing, corrupt,
undecryptable, or incompatible artifact fails closed. Silently falling back to a
sanitized checkpoint could produce a provider protocol error or a semantically
different tool decision.

### Artifacts are native, versioned snapshots

The encrypted plaintext is a versioned provider-native JSON envelope, not an
Eino Go struct and not a normalized reasoning string:

```json
{
  "schema_version": 1,
  "provider": "openai",
  "api_mode": "responses",
  "model": "gpt-5",
  "continuation_scope": "conversation",
  "adapter_version": "openai-responses/v1",
  "items": [
    { "type": "reasoning", "encrypted_content": "..." },
    { "type": "function_call", "call_id": "..." }
  ]
}
```

The adapter owns `items` and their ordering:

- Chat Completions stores complete native assistant messages needed for replay,
  including `reasoning_content`, `tool_calls`, IDs, and provider extensions.
- OpenAI Responses stores complete output items needed for manually managed
  history, including encrypted reasoning and function-call items.
- Future adapters retain native signed blocks, signatures, parts, or equivalent
  structures without translating them through a generic message model.

Each artifact is a complete snapshot usable to construct the next request. It is
not a delta chain, so recovery does not require reading every older checkpoint.
The adapter may retain the cumulative native items required by its API contract;
it must not omit required state merely to reduce storage.

The adapter also declares `continuation_scope` from the provider/model/API-mode
contract:

- `run`: replay is mandatory for ReAct iterations and same-Run failover, but a
  successful terminal artifact is not promoted to a Conversation head; or
- `conversation`: replay additionally continues across successful Runs through
  the Conversation continuation head.

This scope is independent from the `disabled|required` persistence policy. A
`required` artifact always fails closed when it is needed to restore an active
Run, regardless of scope.

Version 1 uses `encoding=json` and `compression=none`. Compression can be added
as a versioned decode option later. If added, plaintext is compressed before it
is encrypted.

### OpenAI Responses is platform-managed

The Responses adapter always sends `store: false`. It manually supplies the
required previous user inputs and response output items and does not use
`previous_response_id`. This keeps recovery, retention, deletion, and failover
under one platform-owned authority.

### Conversation execution remains serialized

The platform already permits multiple non-terminal Runs to be queued in one
Conversation, while `next_executable_run_seq` permits only the queue head to run.
This behavior remains unchanged:

- a queued Run does not pin a continuation head when it is created;
- when claimed, a `conversation`-scope adapter loads the latest compatible
  successful head;
- a successful `conversation`-scope Run advances the head;
- a `run`-scope Run never creates or advances a cross-Run head;
- a failed or cancelled Run does not advance it; and
- Steer cancels non-terminal Runs; its replacement reads the last successful
  head for `conversation` scope or starts a new chain for `run` scope.

The head update still uses conversation locking, execution-token fencing, and a
compare-and-swap condition. These are defense in depth against a stale Worker or
implementation defect, not a new product-level concurrency restriction.

### Incompatible configuration starts a new chain

A replay artifact is compatible only when provider, API mode, model continuation
contract, artifact schema, and adapter version are supported by the selected
adapter. An active Run cannot reset its chain to bypass incompatibility; it fails
closed.

After the old Run is terminal, a new Run may start a new provider-native chain
from safe final Conversation messages when:

- the provider, model, API mode, or incompatible adapter version changed;
- the continuation head expired; or
- no historical continuation head exists.

No artifact conversion occurs. Starting a new chain can reduce reasoning
continuity, provider-side cache reuse, or answer consistency, but preserves the
visible conversation and tool history that is safe to replay.

## State ownership and precedence

| State | Owner | Lifetime | Purpose |
| --- | --- | --- | --- |
| `messages` | Platform | Conversation retention | Safe user/final assistant history |
| `run_checkpoints` | Platform | Run recovery retention | Safe execution and tool-loop control state |
| `run_checkpoint_replays` | Provider adapter, stored by platform | Active recovery or continuation TTL | Exact encrypted provider protocol state |
| `conversation_provider_continuations` | Platform | Seven inactive days | Points to the current successful chain head |
| Eino agent state | Current Worker attempt | Process/attempt | Mutable working copy between durable boundaries |

On a new attempt, the platform checkpoint and replay artifact initialize Eino.
If Eino also contains state during that attempt, it is simply the working state
derived from the last committed boundary plus uncommitted progress. It never
overrides a newer database boundary during takeover. A replacement Worker has no
access to the old process memory and rebuilds from the database.

## Provider adapter contract

The runtime introduces a provider continuation adapter with these conceptual
operations:

```text
Capture(provider response, prior artifact) -> provider-native envelope
Validate(envelope, pinned provider configuration) -> compatible | error
Scope(pinned provider configuration) -> run | conversation
BuildRequest(safe messages, envelope, current input) -> provider request
RestoreAttempt(checkpoint, envelope) -> Eino/runtime working state
```

`BuildRequest` must merge safe messages and native artifacts without duplicating
an assistant response. For Chat Completions, the native assistant message replaces
the corresponding sanitized projection when its private fields are required. For
Responses, the adapter reconstructs manually managed input using user inputs and
the original output items in provider-defined order.

The persistence layer treats the envelope as opaque bytes. It may inspect only
the unencrypted routing metadata stored in columns.

## Persistence model

### `run_checkpoint_replays`

One replay row is the encrypted companion of one checkpoint.

| Column | Logical type | Notes |
| --- | --- | --- |
| `checkpoint_id` | identifier, primary/foreign key | References exactly one `run_checkpoints` row |
| `run_id` | identifier, indexed | Duplicated for fenced lookup and GC |
| `conversation_id` | identifier, indexed | Duplicated for continuation and GC |
| `provider` | bounded text | Routing metadata, not secret reasoning |
| `api_mode` | bounded text | For example `chat_completions` or `responses` |
| `model` | bounded text | Pinned model identifier |
| `continuation_scope` | bounded text | `run` or `conversation`, declared by the adapter contract |
| `artifact_schema_version` | integer | Envelope schema compatibility |
| `adapter_version` | bounded text | Provider codec/request contract |
| `encoding` | bounded text | `json` in version 1 |
| `compression` | bounded text | `none` in version 1 |
| `encryption_algorithm` | bounded text | `AES-256-GCM` in version 1 |
| `key_id` | bounded text | Selects a key from the Worker keyring |
| `nonce` | binary | Fresh 96-bit nonce for this encryption |
| `ciphertext` | binary large object | Encrypted envelope plus GCM authentication tag |
| `created_at` | timestamp | UTC creation time |

Use portable logical types and ORM models. MySQL may map binary content to
`BLOB`/`LONGBLOB`; PostgreSQL may map it to `BYTEA`. Application code must not
depend on MySQL JSON functions, Event Scheduler, partial indexes, or trigger-only
behavior for replay correctness.

### `conversation_provider_continuations`

One row identifies the current successful continuation head for a Conversation.

| Column | Logical type | Notes |
| --- | --- | --- |
| `conversation_id` | identifier, primary/foreign key | At most one current chain per Conversation |
| `chain_id` | identifier | Changes whenever a provider-native chain resets |
| `checkpoint_id` | identifier, foreign key | References the terminal replay at the head |
| `source_run_id` | identifier | Successful Run that produced the head |
| `provider` / `api_mode` / `model` | bounded text | Fast compatibility selection |
| `continuation_scope` | bounded text | Always `conversation` for a persisted head |
| `artifact_schema_version` | integer | Head compatibility metadata |
| `adapter_version` | bounded text | Head compatibility metadata |
| `head_version` | integer | Monotonic CAS value |
| `expires_at` | timestamp | Seven days after the last successful advance |
| `created_at` / `updated_at` | timestamp | UTC timestamps |

The continuation table is a lightweight pointer, not a second copy of the
ciphertext. Its `checkpoint_id` resolves to `run_checkpoint_replays`.

## Atomic write boundaries

### Tool-loop boundary

`PersistBoundary` performs the following under the existing Run execution-token
fence in one transaction:

1. lock the owned Run;
2. persist the safe RunStep and ToolCall changes;
3. insert the safe `run_checkpoints` row;
4. validate and encrypt the complete provider replay envelope;
5. insert `run_checkpoint_replays` for that checkpoint;
6. update `runs.latest_checkpoint_id`; and
7. persist safe RunEvents.

For `required`, any failure rolls back the entire boundary.

### Terminal success

The final provider response is also a continuation boundary. The completion
transaction persists a terminal safe checkpoint and its encrypted replay before
it writes the final assistant Message, marks the Run succeeded, and advances the
Conversation queue. For `conversation` scope, the same transaction inserts or
CAS-updates the continuation head and sets its expiry to
`completion_time + 7 days`. For `run` scope, no head is created and the terminal
replay becomes an orphan candidate after Run completion.

### Failure or cancellation

Failure/cancellation completes the Run and advances/realigns the executable queue
as it does today, but does not update the continuation head. A cancelled partial
assistant Message retains `status=cancelled`; it is excluded from future model
history by the existing `status=final` filter. Replay rows belonging only to the
terminal failed/cancelled Run become GC candidates.

## Recovery flows

### Same-Run Worker takeover

1. The replacement Worker claims the expired Run and receives a new
   `execution_token`.
2. It loads `runs.latest_checkpoint_id` and the checkpoint's replay companion.
3. It verifies routing metadata, artifact and adapter versions, key availability,
   AES-GCM authentication, and the envelope's internal metadata.
4. It restores safe iteration/tool state and provider-native continuation state.
5. It resumes pending tools or builds the next provider request.
6. Every later write is fenced by the new token, so the old Worker cannot commit.

### New Run in an existing chain

At claim time, a `conversation`-scope adapter reads the current continuation head
under the Conversation's serial execution invariant. If compatible and
unexpired, the adapter uses it with the new triggering user input. If missing,
expired, or incompatible, the new Run creates a new chain from safe final
messages. A `run`-scope adapter always starts a new native chain for each new Run.
These reset rules apply only at a new Run boundary, never during active-Run
recovery.

## Encryption and key management

Version 1 uses AES-256-GCM authenticated encryption.

- Plaintext is the serialized provider-native envelope.
- Ciphertext is the encrypted envelope plus the GCM authentication tag.
- A fresh random 96-bit nonce is generated for every replay row. A nonce is not
  secret, but the same key/nonce pair must never be reused.
- Additional authenticated data binds the ciphertext to its database context.
  It uses a canonical versioned encoding of `run_id`, `checkpoint_id`,
  `conversation_id`, `provider`, `api_mode`, `model`, `continuation_scope`,
  `artifact_schema_version`, `adapter_version`, `encryption_algorithm`, and
  `key_id`.
- The nonce, key ID, routing metadata, and ciphertext are stored in the database.
  Key bytes are never stored there.

Workers receive an application-managed keyring through the deployment secret:

- the **active key** encrypts new rows and can decrypt its own rows;
- **historical keys** decrypt unexpired rows written before rotation; and
- startup validation requires the active ID to exist and every AES key to decode
  to exactly 32 bytes.

Rotation is: distribute the new key as readable, switch it to active, retain old
keys until all referenced rows expire or are re-encrypted, then remove old keys.
KMS/HSM-backed key resolution can implement the same key-ID interface later.

## Access control and data exposure

Database privileges are capability-based and expressed independently of a
specific database engine:

- migration role: owns and migrates the schema;
- API role: cannot read or write replay ciphertext;
- Worker role: reads/writes checkpoints, replay rows, and continuation heads;
- maintenance role: selects/deletes expired heads and orphan replay rows.

Infrastructure supplies equivalent MySQL `GRANT` or PostgreSQL role/table grants.
Processes select role-specific credentials through `API_MYSQL_DSN`,
`WORKER_MYSQL_DSN`, `MIGRATION_MYSQL_DSN`, and `MAINTENANCE_MYSQL_DSN`. Each
falls back to `MYSQL_DSN` for local development; production deployments must use
separate credentials to enforce the capability boundary.
The replay plaintext, nonce, and ciphertext are never emitted through RunEvents,
RunSteps, trace APIs, browser payloads, metrics labels, panic values, or ordinary
logs. Logs may include safe identifiers, byte counts, provider routing metadata,
key ID, and error classification.

## Retention and garbage collection

- The latest replay for an active/resumable Run never expires.
- Once a newer boundary commits, older snapshots for the same active Run may be
  deleted after verifying they are neither `runs.latest_checkpoint_id` nor a
  continuation head.
- On successful `conversation`-scope completion, only the terminal head must
  remain for cross-Run continuation. Each later successful Run advances the head
  and refreshes its inactivity expiry to seven days.
- On successful `run`-scope completion, no head is created and unreferenced
  terminal replay can be deleted.
- Failed/cancelled terminal Runs do not advance the head; their unreferenced
  replay rows can be deleted.
- When a head expires, encrypted provider state is deleted, while safe
  Conversation messages remain. The next Run starts a new chain.

Cleanup is implemented as an idempotent database-independent `replay-gc` command,
normally run as a Kubernetes CronJob. It uses small batches and, immediately
before deletion, rechecks that the Run is not active, the checkpoint is not a
Run's latest recovery pointer, and no live continuation head references it. It
first removes expired head rows, then deletes orphan replay rows. It does not use
MySQL Event Scheduler, PostgreSQL-specific jobs, or database triggers.

## Failure semantics

The runtime exposes safe stable error codes and keeps provider/decryption details
out of user-facing messages:

| Condition | Active `required` Run behavior | Retry classification |
| --- | --- | --- |
| Replay companion missing | Fail closed with `provider_replay_missing` | Non-retryable data invariant failure |
| Unknown key ID / key unavailable | Fail with `provider_replay_key_unavailable` | Deployment/configuration failure; retry only after configuration changes |
| GCM authentication/decryption failure | Fail closed with `provider_replay_decrypt_failed` | Non-retryable for unchanged data/keyring |
| Envelope or adapter incompatible | Fail closed with `provider_replay_incompatible` | Non-retryable within the active Run |
| Replay persistence failure | Roll back boundary | Retry according to existing database policy |
| Head expired for a new Run | Start a new chain | Expected lifecycle event |

No error response contains replay plaintext, ciphertext, raw provider bodies, or
cryptographic material.

## Rollout

There are no active Runs at deployment time. Therefore:

1. apply the new tables and indexes with the migration role;
2. distribute a valid keyring to Workers;
3. deploy adapter and persistence support;
4. enable `required` only for validated provider/model/API-mode configurations;
5. treat historical completed Conversations as having no continuation head, so
   their next Run starts a new chain; and
6. deploy the `replay-gc` CronJob after read/write verification.

The runtime does not synthesize replay artifacts for historical checkpoints.
Schema and adapter versions make mixed or unsupported artifacts explicit.

## Required verification

### Persistence and cryptography

- Safe checkpoints contain no reasoning or encrypted provider output items.
- Replay plaintext round-trips for every adapter through AES-256-GCM.
- Every encryption uses a distinct nonce under the active key.
- Modified nonce, AAD, ciphertext, or tag fails authentication.
- Active and historical key rotation paths decrypt the expected rows.
- API credentials cannot select the replay table.

### Adapter contract

- Chat Completions preserves complete native assistant fields and tool ordering.
- Responses sends `store: false` and manually replays all required output items.
- Adapter scope is verified against the exact provider/model/API-mode contract:
  `run` never creates a head, while `conversation` advances one.
- Restoring an artifact through a newer compatible adapter succeeds.
- Provider/model/API-mode/unsupported adapter mismatches follow reset or
  fail-closed rules at the correct Run boundary.

### HA matrix

For every enabled provider/model/API mode, kill the Worker:

1. after the assistant tool-call response and boundary commit;
2. after tool execution but before the result boundary commits;
3. after the tool-result boundary commits; and
4. during streaming before a complete provider response exists.

Verify valid provider continuation, correct tool-call/result pairing, no duplicate
external effect, execution-token fencing, and absence of private reasoning from
events/APIs/logs. Keep `scripts/ha/test-reasoning-failover.sh` as the deterministic
Chat Completions harness and add Responses coverage through its native adapter.

### Queue, retention, and migration

- Multiple queued `conversation`-scope Runs read the head at claim time and
  advance it serially.
- Steer/cancel never promotes a cancelled Run's replay to head.
- Seven-day expiry retains safe messages and starts a new chain next time.
- GC cannot delete an active latest checkpoint or a referenced head.
- Repository tests run against MySQL; storage integration tests also exercise
  PostgreSQL-compatible types, transactions, grants, and cleanup queries before a
  production database migration is attempted.

## Documentation

The companion technical explanation is
[reasoning-continuation-and-failover.md](../../tech/reasoning-continuation-and-failover.md).
It is the maintenance guide for state precedence, database tables, encryption,
failover, chain reset, key rotation, and retention.
