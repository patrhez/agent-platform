# Agent Platform MVP

This repository is a standalone, read-only, multi-turn troubleshooting Agent. MySQL is the
durable source of truth; Redis only notifies API processes to replay committed Run events.

Assistant answers stream through durable `assistant.delta` Run events. The Worker batches model
content before committing it to MySQL, Redis sends only a post-commit hint, and any API instance
can replay the same sequence through SSE. The completed assistant Message remains the formal
Conversation record.

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
`workspace.list_repositories`, `code.search`, and `file.read` Tools. API and frontend never
receive the host repository path.

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
