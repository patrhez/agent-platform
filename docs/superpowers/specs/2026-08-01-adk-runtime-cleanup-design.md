# ADK Runtime Cleanup Design

**Date:** 2026-08-01  
**Status:** Implemented

## Goal

Remove the unused direct-model streaming path left by the pre-ADK runtime, make checkpoint recovery for pending tool calls explicit, and consolidate duplicated tool-request construction without changing Run, checkpoint, or event semantics.

## Current execution model

The normal execution path is owned by Eino ADK:

```text
EinoRunner.Run
  -> restore checkpoint state
  -> resume pending tool calls when necessary
  -> adk.ChatModelAgent
       -> model invocation
       -> ToolsNode
       -> mcpInvokableTool
  -> platform RuntimeEvents and Checkpoints
```

`streamModelResponse` is not part of this path. It was used by the former handwritten loop and is now referenced only by a unit test. ADK's `adkEmitSession.consumeAssistantStream` is the single production implementation of assistant stream consumption.

## Scope

1. Delete `streamModelResponse` and move its important test coverage to the ADK stream consumer.
2. Keep recovery for a checkpoint whose last message is an assistant message with unresolved `tool_calls`.
3. Rename the recovery functions so they do not look like the main agent loop.
4. Share ToolRequest construction between the ADK `InvokableTool` adapter and pending-tool recovery.
5. Use "agent iteration" terminology in active runtime comments and configuration documentation.

## Non-goals

- Do not remove platform JSON checkpoints or replace them with an ADK CheckPointStore.
- Do not change checkpoint payloads, ordinals, database schema, or runtime name (`eino-react`).
- Do not change retry policy, tool ordering, safe-argument redaction, or SSE event payloads.
- Do not remove the pending-tool recovery path until ADK provides a durable, compatible resume mechanism for an assistant tool-call message.

## Design decisions

### One production assistant-stream consumer

`adkEmitSession.consumeAssistantStream` owns chunk sanitization, `assistant.started` / `assistant.delta` emission, and message concatenation. The direct `model.Stream` helper is removed.

### Pending tool calls remain a recovery adapter

An assistant tool-call checkpoint is persisted before the corresponding tool result checkpoint. A process can therefore resume with unresolved calls. `resumePendingToolCalls` executes only that state transition and then hands control to ADK; it is not an alternative agent loop.

### Shared ToolRequest construction

Both paths validate JSON, preserve a provider call ID for `ToolMessage` pairing, allocate a durable platform ID, derive an idempotency key, and redact unsafe arguments. Those rules are centralized in one helper. The recovery path additionally persists its generated durable ID into the restored assistant call before the next checkpoint.

## Required tests

- ADK stream consumption emits ordered `started` and `delta` events, returns concatenated content, and removes reasoning fields.
- Recovery from an assistant tool-call checkpoint emits tool start and completion boundaries, appends the matching `ToolMessage`, and advances the checkpoint ordinal.
- Existing tool request, checkpoint restore, sanitization, and full runtime tests remain green.

## Compatibility

Existing checkpoints remain readable because the payload shape, state schema version, and runtime name do not change. The cleanup is implementation-only.
