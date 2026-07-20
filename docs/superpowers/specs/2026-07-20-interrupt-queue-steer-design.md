# Interrupt + Queue/Steer Design

**Date:** 2026-07-20  
**Status:** Approved for implementation

## Goal

Web chat gains two complementary controls while a Run is active:

1. **Stop** — interrupt analysis for the current Conversation.
2. **Queue / Steer** — send a follow-up while a prior reply is still in progress, with an explicit mode.

## Product semantics

| Action | Behavior |
|--------|----------|
| **Stop** | Cancel every non-terminal Run in the Conversation (`queued` / `running` / `waiting`). |
| **Queue send** | Do not interrupt. Append a new Message + queued Run; it runs after the current executable Run finishes. |
| **Steer send** | Cancel every non-terminal Run in the Conversation, then append a new Message + Run so the new question is next. |

- Default mode: **queue**.
- Cancelled Runs do not persist a partial assistant Message; the turn shows `Cancelled` and the streaming draft clears on snapshot reload.
- Mode preference is remembered in the browser (`localStorage`).

## Architecture

Server-authoritative `mode` on `POST /conversations/{id}/messages`, plus a Conversation-level cancel endpoint. This avoids cancel/send races and correctly clears already-queued Runs on steer (FIFO `queue_seq` cannot reorder).

```text
Stop  → POST /conversations/{id}/cancel-active
Queue → POST /messages { mode: "queue" }   // existing insert path
Steer → POST /messages { mode: "steer" }  // CancelActiveRuns then insert
```

### CancelActiveRuns (store, under Conversation row lock)

- **`queued` / `waiting`:** immediately mark `cancelled`, write `run.cancelled`, clear lease fields.
- **`running`:** set `cancel_requested_at` only; Worker completes via existing cancel checks.
- After mutations, **realign** `conversations.next_executable_run_seq` to the minimum `queue_seq` among remaining non-terminal Runs, or to `next_run_seq` if none remain.
- `CompleteRun` uses the same realign helper instead of blind `+1`, so a cancelled mid-queue Run cannot block later Runs.

### Messages API

```json
{ "content": "...", "clientMessageId": "01…", "mode": "queue" | "steer" }
```

- Missing / empty `mode` → `queue`.
- Idempotency on `(conversation_id, client_message_id)` unchanged; retries return the existing Run and do not cancel again.

### Worker

- Keep per-emit cancellation checks.
- At `Execute` start, if cancel was requested, complete as `cancelled` without calling the model.
- Claim only selects `queued` / expired-`running` rows; sync-cancelled queued Runs are never claimed.

### Frontend

- Keep Send enabled while Runs are active.
- Composer: Queue | Steer toggle + Stop (visible when any Run is `queued`/`running`/`waiting`).
- `onSend(content, mode)` → messages API; Stop → `cancel-active` then reload conversation.

## Non-goals

- Promoting a streaming draft to a formal assistant Message on cancel.
- Same-Run Eino interrupt/resume steering.
- Changing MySQL-authoritative / Redis-hint SSE design.
