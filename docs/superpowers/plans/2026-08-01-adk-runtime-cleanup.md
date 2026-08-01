# ADK Runtime Cleanup Implementation Plan

**Goal:** Remove dead handwritten-loop code while preserving ADK execution and checkpoint-based pending-tool recovery.

## 1. Establish behavior with tests

- Add a direct test for ADK assistant stream consumption, including reasoning-field sanitization.
- Add a pending-tool recovery test beginning from an assistant `tool_calls` checkpoint state.

## 2. Remove the dead direct streaming path

- Delete `streamModelResponse`.
- Delete its obsolete test and move the shared fake streaming model to ADK-focused tests.
- Keep `emitAssistantStreamEvent`, because the ADK stream consumer uses it.

## 3. Narrow the handwritten recovery path

- Rename `execute` recovery logic to `resumePendingToolCalls` and `executePendingToolCalls`.
- Keep it before ADK execution because a restored assistant tool-call message has no matching tool result yet.
- Do not present this code as the primary ReAct/agent loop.

## 4. Consolidate ToolRequest construction

- Extract common validation, durable-ID, provider-call-ID, idempotency-key, and safe-argument logic.
- Make both `mcpInvokableTool` and pending-tool recovery use it.
- Preserve the recovery-only mutation that records a newly allocated durable tool ID in the restored assistant call.

## 5. Clarify terminology

- Replace active comments that describe the implementation as a handwritten ReAct loop with ADK-agent or pending-tool-recovery terminology.
- Preserve `eino-react` as a durable checkpoint compatibility value.

## 6. Verify

Run:

```sh
cd backend
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./internal/runtime -count=1
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./... -count=1
```

The cleanup is complete only when the focused and full backend suites pass.
