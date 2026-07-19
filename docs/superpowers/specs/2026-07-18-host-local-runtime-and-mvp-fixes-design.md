# Host-Local Runtime and MVP Fixes Design

## Scope

This change makes host-local execution the default MVP workflow and fixes the three user-visible gaps confirmed during browser testing:

1. no explicit local “new conversation” interaction;
2. no read-only way for the Agent to enumerate Workspace repositories;
3. failed Runs terminate without a visible explanation in the chat UI.

It also adds the minimum operational logging needed to distinguish API, Worker, MCP, and frontend failures.

## Default Runtime Topology

Docker Compose owns only durable dependencies:

- MySQL 8.4 on `127.0.0.1:3306`;
- Redis 7.4 on `127.0.0.1:6379`.

The following processes build and run directly on macOS:

- Workspace MCP on `127.0.0.1:8081`, with `WORKSPACE_ROOT=$REPOS_DIR`;
- API Server on `127.0.0.1:8080`;
- Agent Worker as a background process;
- Vite development server on `127.0.0.1:5173`, proxying `/api` to the API Server.

`compose.yaml` becomes dependency-only. The current complete container stack remains available as `compose.full.yaml`; its service URLs are defined inside that file so the host-local values in `.env` cannot break container networking.

## Local Lifecycle

Runtime files are stored under the ignored `.local/` directory:

```text
.local/
├── bin/
├── run/
└── logs/
```

`scripts/up.sh` performs these ordered operations:

1. validate Docker Compose, Go, Node, npm, `.env`, and `REPOS_DIR`;
2. read only known `.env` keys without shell-sourcing the file;
3. start MySQL and Redis and wait for their health checks;
4. download dependencies and build `migrate`, `api`, `worker`, and `workspace-mcp` into `.local/bin`;
5. run migration once against the host MySQL DSN;
6. start MCP and API, then wait for their HTTP health endpoints;
7. start Worker and Vite with `nohup`, dedicated logs, and PID files;
8. print endpoints, process IDs, and log locations.

Stale PID files are replaced; a live matching process is not started twice. If startup fails, processes started by that invocation are terminated and the relevant log path is printed. `scripts/down.sh` idempotently terminates host processes before stopping MySQL and Redis. System prerequisites are reported with installation guidance, not installed automatically.

The existing recovery demonstrations are updated to restart the host API or Worker through a small role-aware local restart script. Full-container recovery remains available by explicitly using `compose.full.yaml`.

## Environment Contract

The default `.env.example` uses host endpoints:

```dotenv
MYSQL_DSN=agent:agent@tcp(127.0.0.1:3306)/agent_platform?parseTime=true&charset=utf8mb4&loc=UTC
REDIS_URL=redis://127.0.0.1:6379/0
WORKSPACE_MCP_URL=http://127.0.0.1:8081/mcp
```

`REPOS_DIR` remains an absolute external directory. It is passed directly to the host MCP process and is never exposed to API or frontend processes.

## Operational Logging

Logs are separated by role:

- `.local/logs/api.log`: startup plus HTTP method, path, status, and duration;
- `.local/logs/worker.log`: Run claim, start, success/failure, attempt, and duration;
- `.local/logs/mcp.log`: Tool name, success/failure, and duration;
- `.local/logs/web.log`: Vite startup and proxy errors.

Logs must not contain LLM API keys, full HTTP request bodies, source-code contents, complete Tool results, or model reasoning. GORM is configured at warning level with expected record-not-found errors suppressed.

## Workspace Repository Enumeration

Workspace MCP adds a read-only `workspace.list_repositories` Tool with no arguments. It returns sorted aliases for direct child directories of `WORKSPACE_ROOT` and does not return absolute paths, files, hidden directories, or symlinks.

The Agent-facing OpenAI-compatible function name is `workspace_list_repositories`; the runtime maps it to the dotted MCP Tool name, as it already does for search and read. The Agent configuration allowlist must contain exactly these three tools:

- `workspace.list_repositories`;
- `code.search`;
- `file.read`.

The system prompt instructs the Agent to enumerate repositories before searching whenever the user has not supplied a known repository alias. MCP error results prefer their explicit safe error content over a zero-value structured output so invalid repository failures remain diagnosable.

## Frontend Conversation and Failure UX

The existing approved local-draft design remains unchanged: clicking `+ New conversation` clears the selected conversation locally and performs no request; the first submitted message creates and selects the durable Conversation.

Conversation snapshots expose each terminal Run's safe `errorCode` and `finishedAt`. When the latest Run is failed or cancelled and has no assistant message, the chat renders a compact status panel such as `Run failed (runtime_error). Check the execution trace or retry.` It does not expose provider responses or internal error strings. Successful conversations render as before.

## Testing and Acceptance

- Shell tests cover host endpoint validation, dependency-only Compose startup, stale/live PID handling, and shutdown idempotency.
- Go tests cover repository enumeration, model-to-MCP Tool mapping, safe error result selection, GORM logger configuration, request logging, and Worker lifecycle logging where observable.
- React tests cover the local draft action, no API request before first send, first-message creation, and visible failed-Run status.
- Full backend, MCP, frontend, and shell test suites pass.
- Manual acceptance starts the default stack with `scripts/up.sh`, opens `http://127.0.0.1:5173`, creates a new conversation, asks `List the projects in the current workspace.`, receives a repository list, and confirms role-specific logs exist.

## Out of Scope

- Production process supervision such as launchd or systemd;
- automatic installation of Docker, Go, Node, or npm;
- repository clone, fetch, pull, or synchronization;
- source-file mutation tools;
- exposing raw provider errors or chain-of-thought to the browser.
