# Host-Local Runtime and MVP Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run application services directly on macOS with only MySQL and Redis in Docker, while fixing Workspace enumeration, failed-Run visibility, and local new-conversation UX.

**Architecture:** Shell scripts supervise host-built binaries and Vite using PID/log files under `.local/`; dependency-only and full-container Compose files coexist. Workspace MCP gains one bounded read-only enumeration Tool, and safe terminal Run metadata flows through the existing API snapshot to the React UI.

**Tech Stack:** POSIX shell, Docker Compose v2, Go 1.25, GORM Gen, Eino, MCP Streamable HTTP, React 19, TypeScript, Vite, Vitest, Testing Library

## Global Constraints

- Do not use subagents.
- All GORM transactions use `Transaction` callbacks; no manual Begin/Commit/Rollback and no handwritten SQL.
- Logs never include API keys, request bodies, code contents, full Tool results, or model reasoning.
- `workspace.list_repositories` returns only sorted direct non-hidden directory aliases, never absolute paths or symlinks.
- Host-local `.env` endpoints use `127.0.0.1`; `compose.full.yaml` owns its internal service endpoints.
- Docker, Go, Node, and npm are validated but never installed automatically.
- Existing user data is retained.

---

### Task 1: Frontend Test Harness

**Files:**
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Modify: `frontend/vite.config.ts`

**Interfaces:**
- Produces: `npm test -- --run` using Vitest with jsdom.

- [ ] Install `vitest`, `@testing-library/react`, `@testing-library/user-event`, and `jsdom` as dev dependencies; add `"test": "vitest"`.
- [ ] Configure `vite.config.ts` with `defineConfig` from `vitest/config`, `test.environment = "jsdom"`, and a development proxy:

```ts
server: {
  host: "127.0.0.1",
  port: 5173,
  proxy: { "/api": "http://127.0.0.1:8080" },
},
```

- [ ] Run `npm test -- --run --passWithNoTests`; expect exit code 0.
- [ ] Run `npm run build`; expect a successful Vite build.
- [ ] Commit with `test: add frontend component test harness`.

### Task 2: Workspace Repository Enumeration

**Files:**
- Create: `mcp-server/internal/workspace/list.go`
- Create: `mcp-server/internal/workspace/list_test.go`
- Modify: `mcp-server/internal/workspace/mcp.go`
- Modify: `backend/internal/runtime/eino_react.go`
- Modify: `backend/internal/runtime/eino_react_test.go`
- Modify: `backend/internal/runtime/config.go`
- Modify: `backend/internal/config/config_test.go`
- Modify: `backend/configs/agents/issue-troubleshooter.yaml`
- Modify: `backend/internal/runtime/workspace_mcp.go`

**Interfaces:**
- Produces MCP Tool: `workspace.list_repositories` with `{}` input.
- Produces model Tool: `workspace_list_repositories`, mapped to MCP Tool name.

- [ ] Write MCP tests using a temporary root containing `alpha/`, `zeta/`, `.hidden/`, `file.txt`, and a directory symlink. Assert `ListRepositories` returns only `alpha`, `zeta` in order and no absolute paths.
- [ ] Run `go test ./internal/workspace -run TestListRepositories`; expect failure because the method is absent.
- [ ] Implement:

```go
type ListRepositoriesInput struct{}
type ListRepositoriesOutput struct { Repositories []string `json:"repositories"` }
func (service *Service) ListRepositories(context.Context, ListRepositoriesInput) (ListRepositoriesOutput, error)
```

Use `os.ReadDir(service.root)`, check context, skip hidden names, files, and symlinks, and rely on `os.ReadDir` ordering.
- [ ] Register `workspace.list_repositories` in `NewMCPServer` with a typed handler.
- [ ] Write runtime tests asserting `workspace_list_repositories -> workspace.list_repositories` and that all three model Tool names match `^[a-zA-Z0-9_-]+$`.
- [ ] Run runtime tests and verify RED before adding the mapping and zero-argument `schema.ToolInfo`.
- [ ] Update the system prompt to enumerate repositories when no alias is known; require exactly three MCP allowlist entries in config validation.
- [ ] Add the new dotted Tool to the YAML allowlist.
- [ ] Fix `marshalToolResult`: when `result.IsError`, serialize explicit text content before considering `StructuredContent`; cover with a test using both fields.
- [ ] Run `go test ./...` and `go vet ./...` in both `backend` and `mcp-server`.
- [ ] Commit with `feat: add workspace repository enumeration`.

### Task 3: Safe Runtime Metadata and Operational Logs

**Files:**
- Modify: `backend/internal/domain/models.go`
- Modify: `backend/internal/store/runs.go`
- Modify: `backend/internal/database/database.go`
- Create: `backend/internal/httpapi/access_log.go`
- Create: `backend/internal/httpapi/access_log_test.go`
- Modify: `backend/internal/httpapi/server.go`
- Modify: `backend/internal/worker/worker.go`
- Modify: `mcp-server/internal/workspace/mcp.go`

**Interfaces:**
- `domain.Run` adds `ErrorCode string` and `FinishedAt *time.Time` JSON fields.
- HTTP access middleware records method/path/status/duration.

- [ ] Write a store mapping test proving terminal error code and finish time appear in a Conversation `Run`; verify RED.
- [ ] Add `ErrorCode` and `FinishedAt` to `domain.Run` and populate them in `domainRun` from generated GORM model fields.
- [ ] Write an HTTP middleware test using `httptest` and an injected `io.Writer`; assert one line contains `method=GET path=/healthz status=204` and does not include query parameters.
- [ ] Implement a status-capturing `http.ResponseWriter` wrapper and wrap the demo-principal handler with access logging in `httpapi.New`.
- [ ] Configure GORM with `logger.Warn`, `IgnoreRecordNotFoundError: true`, a one-second slow query threshold, and non-colorized output.
- [ ] Log Worker claim/start/terminal outcome with Run ID, attempt, status, and duration. Keep the existing underlying failure cause only in Worker logs.
- [ ] Log MCP Tool name, success/failure, and duration inside each typed handler without arguments or results.
- [ ] Run backend and MCP full tests plus vet.
- [ ] Commit with `feat: add safe runtime observability`.

### Task 4: New Conversation and Failed-Run UI

**Files:**
- Create: `frontend/src/components/ConversationList.test.tsx`
- Create: `frontend/src/App.test.tsx`
- Create: `frontend/src/components/RunStatus.tsx`
- Create: `frontend/src/components/RunStatus.test.tsx`
- Modify: `frontend/src/api/client.ts`
- Modify: `frontend/src/components/ConversationList.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/app.css`

**Interfaces:**
- `ConversationList` adds `onCreate: () => void`.
- `Run` adds optional `errorCode` and `finishedAt`.
- `RunStatus` consumes `run?: Run` and displays only safe terminal status text.

- [ ] Write and run a failing component test that clicks `New conversation` and observes one `onCreate` call.
- [ ] Add the sidebar button and focused `.new-conversation` styling; verify GREEN.
- [ ] Write and run a failing App test that loads an existing message, clicks `New conversation`, observes an empty chat and no `createConversation` call, then submits the first message and observes `createConversation(content)`.
- [ ] Add local `drafting` state, derive `visibleDetail`, exit draft in `loadConversation`, and refresh the list without selecting an unrelated Conversation after first-message creation.
- [ ] Write `RunStatus` tests for failed, cancelled, and succeeded Runs. Failed text is `Run failed (runtime_error). Check the execution trace or retry.`; cancelled text is `Run cancelled.`; succeeded renders nothing.
- [ ] Render `RunStatus` for the latest visible Run and add `.run-status` styles.
- [ ] Run `npm test -- --run` and `npm run build`.
- [ ] Commit with `feat: improve conversation terminal UX`.

### Task 5: Host-Local Process Lifecycle

**Files:**
- Modify: `.gitignore`
- Modify: `.env.example`
- Create: `compose.full.yaml`
- Modify: `compose.yaml`
- Create: `scripts/lib/local.sh`
- Modify: `scripts/up.sh`
- Modify: `scripts/down.sh`
- Create: `scripts/restart.sh`
- Modify: `tests/scripts/up_test.sh`
- Create: `tests/scripts/down_test.sh`
- Create: `tests/scripts/restart_test.sh`
- Extend fakes under: `tests/scripts/fakes/`

**Interfaces:**
- `scripts/up.sh` starts the complete default local stack and returns after health checks.
- `scripts/down.sh` idempotently stops local roles and Docker dependencies.
- `scripts/restart.sh ROLE` accepts `api`, `worker`, `mcp`, or `web`.

- [ ] Copy the existing full Compose topology to `compose.full.yaml`; make its API/Worker environment use container service endpoints directly.
- [ ] Reduce `compose.yaml` to MySQL and Redis with existing volumes, ports, and health checks.
- [ ] Change `.env.example` to host DSN/URLs and ignore `.local/`.
- [ ] Write shell tests with fake `docker`, `go`, `npm`, `curl`, and `kill` commands. Assert startup order includes `docker compose up -d mysql redis`, four Go builds, migration, MCP/API health checks, Worker/Vite starts, and PID creation.
- [ ] Verify shell tests fail against the current Docker-only script.
- [ ] Implement `scripts/lib/local.sh` helpers:

```sh
read_env KEY
require_command NAME GUIDANCE
pid_is_live ROLE
start_role ROLE COMMAND...
stop_role ROLE
wait_for_url NAME URL
```

All paths derive from the repository root; role logs and PIDs use `.local/logs/$role.log` and `.local/run/$role.pid`.
- [ ] Rewrite `up.sh` to validate, start dependencies, build binaries, migrate, start roles in dependency order, and print endpoints/logs. Use traps to clean only roles started by the failed invocation.
- [ ] Rewrite `down.sh` to stop `web`, `worker`, `api`, `mcp`, then run `docker compose down`.
- [ ] Implement `restart.sh` using the same helpers and exact role commands.
- [ ] Run `sh tests/scripts/up_test.sh`, `sh tests/scripts/down_test.sh`, `sh tests/scripts/restart_test.sh`, and `sh -n` for all scripts.
- [ ] Commit with `feat: run application services on the host`.

### Task 6: Documentation, Recovery, and End-to-End Acceptance

**Files:**
- Modify: `README.md`
- Modify: `docs/demo-script.md`
- Modify: `tests/e2e/recovery.sh`
- Modify: `tests/e2e/stream-reconnect.sh`

**Interfaces:**
- Default Quickstart is `./scripts/up.sh`; full-container mode is explicitly optional.

- [ ] Update recovery scripts to call `scripts/restart.sh worker` and `scripts/restart.sh api` instead of Docker service restarts.
- [ ] Document prerequisites, `.env` host endpoints, start/stop/restart commands, log locations, and `compose.full.yaml` fallback.
- [ ] Run all Go tests/vet, all frontend tests/build, all shell tests/syntax checks.
- [ ] Stop the old full-container application roles, start the new default stack, and verify health endpoints on 8080, 8081, and 5173.
- [ ] Submit `List the projects in the current workspace.` and verify a succeeded Run with repository aliases.
- [ ] Verify new-conversation local draft behavior and failed-Run status in the browser.
- [ ] Commit with `docs: update host-local MVP quickstart`.
