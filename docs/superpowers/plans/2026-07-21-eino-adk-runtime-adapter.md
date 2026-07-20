# Eino ADK Runtime Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run each Agent Run through Eino ADK `ChatModelAgent` behind the existing `AgentRunner` boundary, preserve PersistBoundary/Checkpoint/SSE/cancel/steer, then add optional summarization and reduction middleware.

**Architecture:** Keep `Worker` and `domain` unchanged. Replace the hand-rolled loop in `EinoRunner` with an adapter that builds ADK tools from MCP, consumes `AgentEvent`s, and emits platform `RuntimeEvent`s (including JSON Checkpoints). Context middleware is config-gated and must not become the durable source of truth.

**Tech Stack:** Go, `github.com/cloudwego/eino` ADK (`adk`, `adk/middlewares/summarization`, `adk/middlewares/reduction`), existing MCP toolset, MySQL Checkpoints via Worker `PersistBoundary`.

## Global Constraints

- Do not use ADK `CheckPointStore` as resume truth; platform Checkpoint JSON remains authoritative.
- Do not implement same-Run Steer via ADK Interrupt/Resume.
- Never persist or expose `ReasoningContent` / CoT.
- Domain packages must not import `adk` types; only `backend/internal/runtime` may.
- Queue/steer/Stop semantics stay as in `docs/superpowers/specs/2026-07-20-interrupt-queue-steer-design.md`.
- Prefer TDD; keep `go test ./internal/runtime ./internal/worker` green after each task.
- Follow existing GoDoc / wrapped-error style.

## File map

| File | Responsibility |
|------|----------------|
| `backend/internal/runtime/agent.go` | Unchanged `AgentRunner` contract |
| `backend/internal/runtime/mcp_invokable.go` | MCP → `tool.InvokableTool` + emit hooks |
| `backend/internal/runtime/adk_runner.go` | ADK agent build + event → `RuntimeEvent` |
| `backend/internal/runtime/eino_react.go` | Thin `EinoRunner.Run` delegating to ADK path; keep helpers still needed (sanitize, checkpoint encode) |
| `backend/internal/runtime/config.go` | Optional `context` limits for middleware |
| `backend/configs/agents/issue-troubleshooter.yaml` | Context middleware settings |
| `docs/tech/execution-and-streaming-faq.md` | Document ADK adapter + compression |

---

### Task 1: MCP InvokableTool wrapper

**Files:**
- Create: `backend/internal/runtime/mcp_invokable.go`
- Create: `backend/internal/runtime/mcp_invokable_test.go`
- Modify: `backend/internal/runtime/toolset.go` (helper to build `[]tool.BaseTool` if needed)

**Interfaces:**
- Produces: `func buildInvokableTools(toolset *mcpToolset, hooks toolEmitHooks) ([]tool.BaseTool, error)`
- Produces: `type toolEmitHooks struct { OnStart func(ToolRequest) error; OnFinish func(ToolRequest, ToolResult) error }`
- Consumes: existing `toolBinding`, `callWithRetry`, `toolRequest` construction patterns from `eino_react.go`

- [ ] **Step 1: Write failing test for InvokableTool MCP call**

```go
func TestMCPInvokableToolEmitsStartAndFinish(t *testing.T) {
    // fake executor returns "ok"
    // build one InvokableTool for file.read binding
    // InvokableRun with valid JSON args
    // assert OnStart then OnFinish order, result string "ok", safe args redaction unchanged
}
```

- [ ] **Step 2: Run test — expect RED**

```sh
cd backend && go test ./internal/runtime -run TestMCPInvokableToolEmitsStartAndFinish -count=1
```

- [ ] **Step 3: Implement `mcp_invokable.go`**

Wrap each binding as `InvokableTool`:
- `Info(ctx)` returns existing `schema.ToolInfo`
- `InvokableRun(ctx, argumentsInJSON string)` builds `ToolRequest` (reuse idempotency / allowlist logic), calls `OnStart`, `callWithRetry`, `OnFinish`, returns `result.Content`
- Propagate cancel via ctx / OnStart errors

- [ ] **Step 4: Run test — expect PASS**

- [ ] **Step 5: Commit**

```bash
git add backend/internal/runtime/mcp_invokable.go backend/internal/runtime/mcp_invokable_test.go
git commit -m "$(cat <<'EOF'
feat: wrap MCP tools as Eino InvokableTools with emit hooks

EOF
)"
```

---

### Task 2: ADK event adapter (parity path)

**Files:**
- Create: `backend/internal/runtime/adk_runner.go`
- Create: `backend/internal/runtime/adk_runner_test.go`
- Modify: `backend/internal/runtime/eino_react.go`
- Modify: `backend/internal/runtime/eino_react_test.go` (keep streaming assertions via adapter or shared helpers)

**Interfaces:**
- Produces: `func (runner *EinoRunner) runWithADK(ctx, input, state, ordinal, model, toolset, emit) (Result, error)`
- Mapping rules:
  - Assistant stream chunks → `RuntimeEvent.Assistant` (started/delta), sanitize reasoning
  - Assistant message with tool_calls → model boundary Checkpoint, then tools run inside ADK
  - Tool hooks → tool started (no Checkpoint) / tool completed (Checkpoint) matching current Worker expectations
  - Final assistant without tools → model Checkpoint + `Result.Final`
- `MaxIterations` = `definition.Agent.Limits.MaxSteps`
- Restore: same `restoreState` / `initialMessages`; feed restored messages into ADK `Run`
- On `ErrStepLimit` / cancel: preserve existing error types

- [ ] **Step 1: Failing integration-style test with fake ChatModel**

Drive one user message → streamed answer (no tools) through ADK adapter; assert assistant deltas + final Checkpoint messages include system+user+assistant.

- [ ] **Step 2: RED**

```sh
cd backend && go test ./internal/runtime -run TestADKRunnerStreamsFinalAnswer -count=1
```

- [ ] **Step 3: Implement `adk_runner.go`**

```go
agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Name:          runner.definition.Agent.ID,
    Model:         boundModel, // or model before WithTools — ADK ToolsConfig owns tools
    Instruction:   "", // system already in messages OR pass system via GenModelInput
    MaxIterations: runner.definition.Agent.Limits.MaxSteps,
    ToolsConfig: adk.ToolsConfig{
        ToolsNodeConfig: compose.ToolsNodeConfig{Tools: invokables},
    },
})
adkRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true})
iter := adkRunner.Run(ctx, state.Messages)
// consume events → emit
```

Notes for implementer:
- Prefer putting system prompt in initial messages (current behavior) and empty/minimal Instruction to avoid double system prompts.
- Track `iteration` for Checkpoint compatibility.
- Reuse `newCheckpoint`, `sanitizeAssistantMessage`.

- [ ] **Step 4: Switch `EinoRunner.execute` to ADK path; delete or narrow old manual loop once tests pass**

- [ ] **Step 5: Run full runtime + worker tests**

```sh
cd backend && go test ./internal/runtime ./internal/worker -count=1
```

- [ ] **Step 6: Commit**

```bash
git commit -m "$(cat <<'EOF'
feat: execute Agent Runs through Eino ADK behind AgentRunner

EOF
)"
```

---

### Task 3: Context config surface

**Files:**
- Modify: `backend/internal/runtime/config.go`
- Modify: `backend/internal/runtime/config_test.go`
- Modify: `backend/configs/agents/issue-troubleshooter.yaml`

**Interfaces:**
- Produces:

```go
type ContextDefinition struct {
    Summarization SummarizationDefinition `yaml:"summarization"`
    Reduction     ReductionDefinition     `yaml:"reduction"`
}

type SummarizationDefinition struct {
    Enabled         bool `yaml:"enabled"`
    ContextTokens   int  `yaml:"context_tokens"`   // 0 = library default when enabled
    ContextMessages int  `yaml:"context_messages"` // 0 = ignore message trigger
}

type ReductionDefinition struct {
    Enabled           bool `yaml:"enabled"`
    MaxTokensForClear int  `yaml:"max_tokens_for_clear"`
    MaxLengthForTrunc int  `yaml:"max_length_for_trunc"`
    // SkipOffload: when true, clear/truncate without Backend files (MCP-safe default)
    SkipOffload bool `yaml:"skip_offload"`
}
```

- Default YAML: both `enabled: false` initially (parity), or enabled with high thresholds after Task 4/5 land.

- [ ] **Step 1: Failing Validate/load tests for new fields**
- [ ] **Step 2: Implement parse + validate**
- [ ] **Step 3: Commit**

---

### Task 4: Reduction middleware

**Files:**
- Modify: `backend/internal/runtime/adk_runner.go`
- Create/Modify: `backend/internal/runtime/adk_context_test.go`

**Interfaces:**
- When `Reduction.Enabled`, register `reduction.New` as a ChatModelAgent Handler.
- MCP-safe default: `SkipOffload: true` → configure reduction to clear/truncate without requiring a Workspace-replacing `read_file` tool (use clear placeholders; set `SkipTruncation` or nil Backend per Eino API).
- Assert: after a large tool result exceeds threshold, next Checkpoint messages no longer contain the full tool payload.

- [ ] **Step 1: Failing test with oversized tool result + low threshold**
- [ ] **Step 2: Wire middleware from config**
- [ ] **Step 3: Enable in `issue-troubleshooter.yaml` with conservative thresholds**
- [ ] **Step 4: Commit**

---

### Task 5: Summarization middleware

**Files:**
- Modify: `backend/internal/runtime/adk_runner.go`
- Modify: `backend/internal/runtime/adk_context_test.go`
- Modify: `backend/configs/agents/issue-troubleshooter.yaml`

**Interfaces:**
- When `Summarization.Enabled`, register `summarization.New` with `Model` = same chat model (or a dedicated generate path), `Trigger` from config.
- Do not set `TranscriptFilePath` unless a real transcript store exists (leave empty for MVP).
- Assert: when message/token trigger fires, later Checkpoint has fewer messages / summary content-type markers, system prompt retained.

- [ ] **Step 1: Failing test with tiny ContextMessages trigger**
- [ ] **Step 2: Wire middleware**
- [ ] **Step 3: Enable with safe production thresholds (or keep disabled until soak)**
- [ ] **Step 4: Commit**

---

### Task 6: Docs

**Files:**
- Modify: `docs/tech/execution-and-streaming-faq.md`
- Modify: `docs/superpowers/specs/2026-07-21-eino-adk-runtime-adapter-design.md` status if needed

- [ ] **Step 1: Document ADK adapter, Checkpoint-after-compress behavior, queue/steer non-interaction**
- [ ] **Step 2: Commit**

---

## Self-review

1. Spec coverage: adapter ownership, no ADK Store truth, cancel/steer non-goals, reduction/summarization, Checkpoint semantics — Tasks 1–6.
2. No TBD placeholders in task steps.
3. Types: `ContextDefinition` used consistently in Tasks 3–5; `toolEmitHooks` in Tasks 1–2.
