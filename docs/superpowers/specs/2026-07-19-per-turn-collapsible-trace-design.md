# Per-Turn Collapsible Execution Trace Design

**Date:** 2026-07-19  
**Status:** Approved for implementation

## Goal

Replace the page-level, default-open Execution Trace list with a per-turn, default-collapsed region placed under each user question and above that turn’s assistant answer — closer to common coding-agent UIs.

## Decisions

1. **Scope:** Every turn (not only the latest run).
2. **Order:** User message → Execution Trace → Agent answer (including streaming draft).
3. **Collapsed summary:** Progress-style English copy.
   - Running: `Running` or `Running · {tool}…`
   - Done with tools: `Used {n} tools · {duration}`
   - Done with no tools: `Completed · {duration}`
   - Queued / Failed / Cancelled: `Queued` / `Failed` / `Cancelled` (failed may include error code).
4. **Expand behavior:** Always default collapsed, including while the run is active. Summary updates live; open state is not persisted.
5. **Language:** All user-facing interaction copy is English.
6. **Approach:** Frontend-only. Bind turns with `Run.triggerMessageId`. Active run uses SSE `run_events`; historical runs use `GET /runs/{id}/trace`. No new backend summary fields for MVP.

## UI structure

```
[User message]
[▸ Running · code.search…]     ← <details> without open
[Agent answer / streaming draft]
```

- Remove the bottom-of-page `<RunTrace />`.
- Keep failure/cancel messaging near the turn (inline `RunStatus` or equivalent), not as a separate page footer for the happy path.
- Expanded body keeps the existing safe timeline (model / tool / result summary).

## Component changes

- **`Chat`:** Render turns; accept per-run events (and draft); embed `RunTrace` + optional `RunStatus` between user and assistant.
- **`RunTrace`:** Take a single `run` (+ `events`); default collapsed; summary from run status + timeline.
- **`App`:** Pass `events` keyed by run id; stop mounting page-level `RunTrace` / footer-only status for the main path.

## Non-goals

- Backend API changes
- Persisting expand/collapse preference
- Auto-expand while running
- Changing event payload / CoT policy
