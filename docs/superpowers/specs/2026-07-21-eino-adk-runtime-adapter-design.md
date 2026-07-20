# Eino ADK Runtime Adapter Design

**Date:** 2026-07-21  
**Status:** Implemented (ADK adapter + config-gated summarization/reduction; defaults off)

## Goal

Replace the hand-rolled ReAct loop inside `EinoRunner` with Eino ADK `ChatModelAgent`, while keeping the platform as the owner of Run lifecycle, PersistBoundary, Checkpoint, SSE, cancel, and queue/steer.

Optionally enable ADK **summarization** (conversation compact) and **reduction** (tool-result trim/clear) so a single Run can stay within the model context window.

## Non-goals

- Same-Run ADK Interrupt/Resume for Steer (already rejected; Steer remains Conversation-level cancel + new Run).
- Replacing platform MySQL Checkpoints with ADK `CheckPointStore` gob blobs.
- Worker mounting a local filesystem for tool offload that bypasses Workspace MCP.
- Changing queue/steer/Stop product semantics.

## Architecture

```text
Worker (claim, lease, PersistBoundary, cancel, SSE)
  → AgentRunner.Run(input, checkpoint, emit)
    → ADK adapter (platform)
         maps AgentEvent ↔ RuntimeEvent + platform Checkpoint
         wraps MCP as InvokableTool (allowlist, idempotency, safe args)
    → adk.ChatModelAgent + Handlers (Eino)
         ReAct loop, MaxIterations
         optional summarization / reduction middleware
```

### Ownership

| Concern | Owner |
|--------|--------|
| Queue / Steer / Stop / lease / `execution_token` | Platform |
| Step / ToolCall / RunEvent / Checkpoint (JSON) | Platform adapter via `emit` |
| ReAct iteration, tool dispatch, model stream | Eino ADK |
| Context compact (summarization) / tool trim (reduction) | Eino ADK middleware (config-gated) |

### Checkpoint rule

- Platform Checkpoint payload remains `{ iteration, messages }` (schema version bump only if fields change).
- After summarization/reduction, **subsequent** Checkpoints store the rewritten messages (model-visible state).
- Prior Trace rows are not rewritten. Conversation Messages are not rewritten.
- Do not use ADK `CheckPointStore` as the resume source of truth.

### Cancel

Keep cooperative cancel: `emit` checks `cancel_requested_at`. Adapter must keep emitting on stream/tool boundaries so Steer/Stop remain responsive. Do not rely on ADK Resume for cancel.

### Context middleware (config-gated)

1. **Reduction** (Run-local tool bloat): prefer clear/truncate of large or old tool results. Avoid requiring a Worker-local `read_file` offload path that contradicts MCP-only Workspace access. If truncation needs a Backend, use an optional temp Backend only when explicitly enabled; default clear-without-offload or in-message truncate is acceptable for MVP.
2. **Summarization** (token/message threshold): LLM summary replaces history in agent state; next Checkpoint persists compact state. Thresholds configurable under Agent `limits` / a dedicated `context` block.

Defaults should be conservative (opt-in or high thresholds) so existing short Runs behave like today.

## Success criteria

1. Worker still talks only to `AgentRunner`; domain types stay free of ADK types.
2. Streaming assistant deltas, tool started/completed, final answer, and Checkpoint resume still work.
3. Cancel + Steer still complete Runs as `cancelled` without partial assistant Messages.
4. With middleware enabled, oversized tool/history Runs compress before the next model call; compressed state appears in later Checkpoints.
5. Existing tests for streaming, restore, sanitize, and Worker cancel continue to pass (updated only where the runtime seam moves).

## Rollout

1. ADK adapter with behavioral parity (no middleware).
2. Wire reduction (safe defaults).
3. Wire summarization (configurable thresholds).
4. Docs/FAQ update.
