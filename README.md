# Agent Platform MVP

A learning-oriented, standalone MVP of a **read-only, multi-turn troubleshooting Agent
platform**. The product surface is a chat UI; the real point of the repo is the architecture
underneath: durable Runs, recoverability, MCP-scoped tools, and a runtime adapter that does not
own persistence.

MySQL is the durable source of truth. Redis only wakes API processes to replay committed Run
events. Assistant answers stream as durable `assistant.delta` events — the Worker batches model
content, commits to MySQL, publishes a post-commit hint, and any API instance can replay the same
sequence over SSE. The completed (or cancelled-partial) assistant Message remains the formal
Conversation record.

## Features

- **Multi-turn troubleshooting chat** — conversation list, draft new chat, GFM Markdown answers, and pinned composer with smart scroll-to-bottom.
- **Durable streaming** — assistant deltas commit to MySQL first; SSE replays by sequence (`Last-Event-ID` / `after`) so refresh and API failover keep the same timeline.
- **Live execution trace** — per-turn collapsible timeline with safe Tool names, allowlisted arguments, statuses, durations, and collapsed result summaries. Model `ReasoningContent` / Chain-of-Thought is never shown.
- **Follow-up control** — while a Run is active, send with **Queue** (append) or **Steer** (cancel active, then start a new Run); **Stop** cancels active Runs. Interrupted streams keep a partial assistant Message marked Stopped; cancelled text is not fed into the next model context.
- **Conversation delete** — single-row delete and multi-select bulk delete (soft delete; confirmation dialog).
- **Read-only Workspace MCP** — host-mounted repositories via `workspace.list_repositories`, `file.list`, `file.stat`, `code.search`, and `file.read`. Only Workspace MCP receives `REPOS_DIR`.
- **Eino ADK agent runtime** — versioned Agent config and Skills bundle, OpenAI-compatible LLM gateway, step/time limits, and context summarization / reduction.
- **API + Worker roles** — MySQL-backed Run leases and checkpoints; configurable `WORKER_CONCURRENCY` per Worker process.
- **Recovery demos** — Worker restart, API SSE reconnect, and durable-streaming e2e scripts; optional k3d HA lab for multi-API / multi-Worker failover.

## System design

This repo is meant to be studied as a small but complete Agent platform — not as a thin LLM
wrapper. One bounded context is split into **stateless deployment roles** that share MySQL and
coordinate through Redis hints.

### Runtime topology

```text
Browser (TypeScript / Vite)
  |  REST + HTTP SSE
  v
agent-api (Go, stateless) ------------------------+
  |                                               |
  | authoritative R/W                             | subscribe / publish hints
  v                                               v
MySQL  <------------------------------------  agent-worker (Go + Eino, stateless)
  ^        Conversations, Messages, Runs,         |
  |        Checkpoints, run_events, leases        | Streamable HTTP MCP
  |                                               v
  |                                     workspace-mcp (Go, read-only)
  |                                               |
  +---------------------------------------- repository directory (read-only)

agent-worker --> OpenAI-compatible LLM gateway
```

| Role | Responsibility | Does **not** |
|---|---|---|
| **Web** | Render chat, open SSE for active Runs, send user turns / Stop / Steer / delete | Talk to Redis, MySQL, LLM, or repository paths |
| **API** | AuthZ demo principal, write Conversation/Run rows, serve snapshots, hold SSE, replay `run_events` | Run Eino, call MCP, or read `REPOS_DIR` |
| **Worker** | Lease Runs, execute the Eino ADK agent, persist boundaries/deltas, publish Redis hints after commit | Hold browser connections or own Conversation truth in memory |
| **Workspace MCP** | Validate paths, enforce limits, expose allowlisted read-only tools | Accept writes or expose host paths to API/UI |
| **MySQL** | Source of truth for Conversations, Messages, Runs, leases, checkpoints, ordered `run_events` | — |
| **Redis** | Pub/Sub wake-up: `{runId, seq}` after durable commit | Store answers, leases, checkpoints, or event payloads |

### Clean layering

Dependency direction is inward: transport → application → domain ← adapters.

```text
frontend/
  components/     UI (Chat, ConversationList, RunTrace, ...)
  state/          pure conversation/event reducers
  api/            HTTP + EventSource clients
       |
       | /api/v1  (REST + SSE)
       v
backend/   (one module, two binaries: cmd/api and cmd/worker)
  httpapi/        transport: routes, SSE, access log, demo auth
  worker/         application: claim loop, lease renewal, stream buffer, completion
  store/          application persistence API over GORM Gen
  domain/         entities, FollowUpMode, Run pins, execution boundaries
  runtime/        Agent adapter (Eino ADK) — swappable, does not own DB
  events/         Redis notifier / subscriber (hint channel only)
  model/ + query/ persistence models + GORM Gen (infrastructure)
  config/ logging/ database/  cross-cutting infrastructure

mcp-server/       separate process; only process with REPOS_DIR
  workspace/      tool handlers (list / stat / search / read)
```

Rules the layout encodes:

1. **Domain is framework-free** — Conversations, Messages, Runs, and follow-up modes live in `domain/`.
2. **API and Worker share `store` + `domain`** — same transactions and invariants; different entrypoints.
3. **Runtime is an adapter** — `runtime/` turns a leased Run + history into model/tool steps; it never writes Conversations directly.
4. **Persistence is generated** — application code goes through `store/` → GORM Gen `query/`; no handwritten SQL in app paths.
5. **UI state is derived** — the browser reducer applies durable events; refresh reloads the MySQL snapshot and replays SSE.

### How a turn flows

**1. User sends a message (control plane)**

```text
Web  --POST /conversations/{id}/messages-->  API
       { content, clientMessageId, mode: queue|steer }

API  -> MySQL: insert user Message + queued Run
       (steer/stop first cancel active Runs in the same control path)
API  <- 202 { messageId, runId, status }

Web  -> GET /runs/{runId}/events?after=0   (SSE)
API  -> authorize Run, SUBSCRIBE Redis channel, SELECT run_events WHERE seq > after
```

`clientMessageId` (ULID) makes the POST idempotent: retries return the existing Run instead of
creating another.

**2. Worker executes (data plane)**

```text
Worker  -> ClaimNextRuns (MySQL lease + execution token)
Worker  -> load final Messages + checkpoints
Worker  -> runtime (Eino): LLM tokens + MCP tool calls
         |
         +--> streamBuffer batches text
         +--> AppendRunEvents / PersistBoundary / CompleteRun  (MySQL commit)
         +--> Redis PUBLISH { runId, seq }                     (hint only)
```

In-memory Worker state is disposable. Lease renewal keeps ownership; if the Worker dies, another
Worker reclaims the same Run with a higher attempt after the lease expires.

**3. API streams to the browser (read path)**

```text
Redis hint wakes API subscriber
API  -> SELECT run_events WHERE seq > cursor   (always MySQL, never Redis payload)
API  -> SSE frame: id=<seq> event=<type> data=<json>
Web  -> reducer appends deltas / tool rows; terminal event closes EventSource
```

Refresh recovery: Conversation snapshot from MySQL + SSE replay from `after` /
`Last-Event-ID`. Redis loss only adds latency until the next poll/heartbeat re-reads MySQL.

### Domain model

```text
Conversation
  ├─ Message (user) ──> Run ──> Steps / ToolCalls / Checkpoints / run_events
  │                         └─> Message (assistant: final | cancelled)
  └─ ...
```

- Chat history and execution events are separate: snapshots are per-Conversation; SSE is per-Run.
- Cancelled partial assistant text is kept for the UI and excluded from the next model context.

### Ideas worth studying

| Idea | Why it matters |
|---|---|
| **MySQL as queue + event store** | Leases and checkpoints demonstrate recoverability without Kafka/Temporal. |
| **Redis is a hint, never truth** | Losing Redis loses latency, not history. |
| **Durable streaming path** | `LLM → streamBuffer → MySQL → Redis hint → API SELECT → SSE`. |
| **Stateless API / Worker roles** | Either process can die; Conversation truth stays in MySQL. |
| **Queue / Steer / Stop** | Explicit control plane for concurrent user turns vs active work. |
| **MCP security boundary** | Repository IO is isolated; tools are allowlisted with safe arguments. |
| **Runtime as adapter** | Platform owns persistence and permissions; Eino is replaceable. |
| **GORM Gen data layer** | One persistence style; transactions stay in `store/`. |
| **Idempotent client turns** | ULID `clientMessageId` prevents duplicate Runs on retry. |
| **HA lab as proof** | Multi-API / multi-Worker failover scripts validate lease reclaim + SSE replay. |

Deeper notes: [`docs/tech/`](docs/tech/) (streaming FAQ, logging), design baselines under
[`docs/superpowers/`](docs/superpowers/), and the live walkthrough in
[`docs/demo-script.md`](docs/demo-script.md).

## One-command Quickstart on macOS

The default topology runs only MySQL and Redis in the selected container runtime. API, Worker,
Workspace MCP, and the Vite frontend build and run directly on the host. Docker Desktop is the
default; Podman is supported for Macs that cannot run Docker Desktop.

After cloning, run the bootstrap command from the repository root:

```sh
# Docker Desktop (default)
./scripts/bootstrap.sh

# Podman
./scripts/bootstrap.sh --runtime podman
```

The script checks macOS dependencies, installs missing Homebrew, Go 1.25+, Node.js 20.19+, and the
selected container runtime, starts Docker Desktop or the Podman machine, starts MySQL and Redis,
downloads locked Go/npm dependencies, builds the services, migrates the database, and launches the
application. The Podman path also installs `podman-compose` and uses it explicitly as the Compose
provider.

The first run creates `.env` from `.env.example` when it is missing and exits so secrets are never
invented or placed in shell history. Set `REPOS_DIR`, `LLM_BASE_URL`, `LLM_API_KEY`, and `LLM_MODEL`,
then rerun the same bootstrap command. To keep configuration outside the clone, use:

```sh
./scripts/bootstrap.sh --runtime podman --env-file /absolute/path/to/agent-platform.env
```

The application is available at `http://127.0.0.1:5173`. Keep the bootstrap terminal open while
the host services are running. Stop everything with:

```sh
./scripts/down.sh
```

The selected runtime is recorded under `.local/`, so `down.sh` remembers whether Docker or Podman
was used. It can also be overridden explicitly with `--runtime docker` or `--runtime podman`.

The default host endpoints are:

```dotenv
MYSQL_DSN=agent:agent@tcp(127.0.0.1:3306)/agent_platform?parseTime=true&charset=utf8mb4&loc=UTC
REDIS_URL=redis://127.0.0.1:6379/0
WORKSPACE_MCP_URL=http://127.0.0.1:8081/mcp
```

`scripts/up.sh --runtime docker|podman` installs locked project dependencies, builds the Go
binaries, applies the GORM schema migration, and supervises host processes while the command
remains in the foreground. PID and log files are stored under `.local/`:

```text
.local/logs/api.log
.local/logs/worker.log
.local/logs/mcp.log
.local/logs/web.log
```

Restart one role without changing its durable Run data:

```sh
./scripts/restart.sh api
./scripts/restart.sh worker
./scripts/restart.sh mcp
./scripts/restart.sh web
```

Only Workspace MCP receives `REPOS_DIR`. It exposes the read-only
`workspace.list_repositories`, `file.list`, `file.stat`, `code.search`, and `file.read` Tools.
API and frontend never receive the host repository path.

The web UI renders assistant content as GFM Markdown and shows a live execution timeline with
safe Tool names, allowlisted arguments, statuses, durations, and collapsed result summaries.
It never displays model `ReasoningContent` or Chain-of-Thought.

## Optional full-container stack

The previous all-container topology remains available for Linux or container recovery checks:

```sh
docker compose -f compose.full.yaml up --build
docker compose -f compose.full.yaml down
```

`compose.full.yaml` uses Docker service names internally and is independent from the host-local
DSN and URLs in `.env`.

## Recovery demonstrations

After the default stack is healthy:

```sh
tests/e2e/recovery.sh
tests/e2e/stream-reconnect.sh
tests/e2e/durable-streaming.sh
```

The first restarts the host Worker and waits for the same durable Run to become terminal. The
second restarts the host API and verifies `Last-Event-ID` recovery. The third proves an assistant
Delta is visible before terminal completion and that the final Message and trace are durable. See
[`docs/demo-script.md`](docs/demo-script.md) for the presentation flow.

## Visual HA lab on macOS

The HA lab runs three API Pods and two Worker Pods across a two-agent k3d cluster. MySQL and Redis
remain the existing Docker Compose dependencies, so the experiment stays focused on stateless API
recovery and leased Worker recovery. The workspace directory is mounted read-only into Workspace
MCP. The local dashboard can inspect Pod state and logs and can forcefully terminate a selected API
or Worker Pod.

The HA lab intentionally remains Docker-only because k3d depends on the Docker API. Podman support
applies to the default host-service topology above.

Start the lab from the repository root:

```sh
./scripts/ha/up.sh
```

On macOS the script installs k3d with Homebrew when it is missing. It builds and imports local
images, migrates the database, deploys the cluster, and starts these endpoints:

```text
Application:  http://127.0.0.1:5173
HA Dashboard: http://127.0.0.1:8090
```

The dashboard is bound only to `127.0.0.1`. Its destructive action is limited to forcefully
terminating an API or Worker Pod in the `agent-platform-ha` namespace. Kubernetes immediately
creates a replacement replica.

Command-line observability remains available:

```sh
./scripts/ha/pods.sh
./scripts/ha/logs.sh POD_NAME
./scripts/ha/shell.sh POD_NAME
```

Run deterministic recovery checks after all Pods are Ready:

```sh
./scripts/ha/test-api-failover.sh
./scripts/ha/test-worker-failover.sh
```

The API check opens SSE directly against one known API Pod, kills that Pod, reconnects through the
cluster entry point with `Last-Event-ID`, and verifies that durable event sequence numbers remain
contiguous. The Worker check waits for a checkpoint, kills its lease owner, and verifies that a
different Worker reclaims the same Run with a higher attempt number and reaches `succeeded`.

Stop the lab while retaining MySQL and Redis data:

```sh
./scripts/ha/down.sh
```

Pass `--dependencies` to stop MySQL and Redis too.

## Local checks

```sh
go -C backend test ./...
go -C backend vet ./...
go -C mcp-server test ./...
go -C mcp-server vet ./...
(cd frontend && npm test -- --run && npm run build)
sh tests/scripts/up_test.sh
sh tests/scripts/down_test.sh
sh tests/scripts/restart_test.sh
sh -n scripts/lib/local.sh scripts/up.sh scripts/down.sh scripts/restart.sh
```
