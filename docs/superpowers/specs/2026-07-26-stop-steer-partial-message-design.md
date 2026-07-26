# Stop / Steer Partial Message Retention

**Date:** 2026-07-26  
**Status:** Approved for implementation

## Goal

When the user **Stop**s or **Steer**s during a streaming Run, keep any already-generated assistant text in the durable Conversation history so refresh and later browsing still show it. Do not inject that interrupted text into the next model context.

This supersedes one product bullet in [[2026-07-20-interrupt-queue-steer-design]]: cancelled Runs with streamed text now persist a partial assistant Message. Queue/Steer/Stop control surfaces and cancel APIs stay unchanged.

## Product semantics

| Action | Run outcome | Partial text present | No text yet | Next model context |
|--------|-------------|----------------------|-------------|--------------------|
| **Stop** | `cancelled` | Assistant Message `status=cancelled` with that text | Only `Run cancelled.` (no Message) | Excludes cancelled Messages |
| **Steer** | Same cancel path, then new user Message + Run | Same as Stop | Same as Stop | Excludes cancelled; uses prior `final` + new user turn |
| **Queue** | Unchanged | — | — | Unchanged |

**Partial** means the **current stream** text the live draft would show: flushed deltas plus unflushed pending for the active `assistant.started` stream. A new `assistant.started` resets accumulation (same as the frontend draft). Do not concatenate earlier streams in the same Run.

## Data model

- Reuse `messages.status` (`varchar(16)`). Values:
  - `final` — successful assistant answer (unchanged)
  - `cancelled` — interrupted partial assistant answer (new usage)
- No schema migration.
- Conversation snapshot APIs continue to return all Messages (including `cancelled`).
- `LoadRunExecution` continues to load only `status=final` user/assistant Messages, so cancelled partials never enter the Agent prompt.

## Architecture

```text
Stop  → cancel-active → Worker observes cancel_requested_at
Steer → CancelActiveRuns + insert (unchanged) → Worker completes cancelled Run
Worker → flush streamBuffer → CompleteRun(cancelled, AssistantText?)
Store  → optional Message status=cancelled + run.cancelled event
UI     → render Message body + Stopped marker; RunStatus still shows cancelled
```

Redis remains hint-only; answer text stays in MySQL (`run_events` during stream, `messages` after cancel promote).

### Worker

1. Extend `streamBuffer` to accumulate current-stream text (`CurrentText()` = committed + pending).
2. On `assistant.started`, reset accumulation after flushing the previous stream’s pending events.
3. Before terminal cancel completion, `Flush()` so pending bytes are in `run_events` and in `CurrentText()`.
4. `completeFailure` for `cancelled` passes non-empty `CurrentText()` as `AssistantText`. Other failures do not promote partial text.
5. Early cancel at `Execute` start (cancel already requested, model never run) still completes with empty text → no Message.

### Store

1. `createAssistantMessage` sets Message `status` from the terminal Run outcome:
   - `succeeded` → `final`
   - `cancelled` → `cancelled`
2. Empty `AssistantText` still skips Message creation.
3. Queued/waiting sync `terminalizeRunCancelled` (no Worker) unchanged — no Message.
4. Do not change the `status=final` filter in `LoadRunExecution`.

### Frontend

1. Render assistant Messages with `status=cancelled` as normal Markdown body.
2. Add a light **Stopped** (or equivalent) marker on that bubble so it is not mistaken for a finished answer.
3. Keep `RunStatus` / trace for the cancelled Run alongside the Message.
4. Draft clearing on snapshot already drops drafts when any assistant Message exists for the `runId` — verify this covers `cancelled`.
5. No new endpoints; Stop / Steer / Queue UX controls unchanged.

## Testing and acceptance

- Worker/unit: cancel with buffer text → Message `cancelled`; cancel with empty text → no Message.
- Store: history for the next Run excludes `cancelled` Messages and includes prior `final` ones.
- Frontend: cancelled Message renders with Stopped marker; snapshot clears draft when Message present.
- Manual: mid-stream Stop then refresh still shows partial; Steer keeps interrupted text in UI without feeding it to the next model turn.

## Non-goals

- Promoting partial text on `failed` Runs.
- Concatenating multiple assistant streams into one Message.
- Changing MySQL-authoritative / Redis-hint SSE design.
- Same-Run ADK interrupt/resume.
- Backfilling historical cancelled Runs that only have `run_events` deltas.

## Success criteria

1. Mid-stream Stop → refresh shows partial answer plus cancelled/stopped affordance.
2. Steer after partial → UI keeps interrupted text; next `LoadRunExecution` omits it.
3. Cancel before any delta → only `Run cancelled.`
4. Successful streaming and queue mode behave as today.
