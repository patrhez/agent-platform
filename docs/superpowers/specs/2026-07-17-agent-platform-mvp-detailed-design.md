# Enterprise Troubleshooting Agent Platform — MVP Detailed Design

**Status:** Proposed detailed design for review  
**Date:** 2026-07-17  
**Companion baseline:** `2026-07-17-agent-platform-mvp-design.md`

## 1. Design Objective

Deliver a standalone, one-command demonstrable issue-troubleshooting system. It provides a TypeScript chat UI, a Go API, a stateless Go/Eino Worker, a self-built read-only Workspace MCP provider, MySQL, and Redis. The first Agent answers multi-turn questions by searching and reading repositories that are already supplied and kept current by an external repository-sync project.

The design deliberately validates the platform boundaries that matter later: durable Conversations and Runs, recovery after Worker/API failure, MCP-first repository access, configuration-pinned Agent execution, and resumable browser streaming. It does not build a general Agent Registry, Wiki/Memory integration, repository synchronization, write tools, Kafka, or Temporal.

## 2. Fixed MVP Decisions

- Go + Eino is the first Agent runtime; TypeScript is the web client.
- API and Worker are two deployment roles of one Agent Platform bounded context and share one MySQL schema.
- MySQL is the authoritative queue and persistence layer. Redis is never a durable queue, lock, Checkpoint, or event store.
- One Conversation owns ordered multi-turn Messages and Runs; a normal user turn creates a new Run.
- A Worker may execute any Run. No conversation state is held in Worker memory between durable boundaries.
- The Agent uses a bounded Eino ReAct loop and only `code.search` and `file.read` Tools.
- Tools are supplied by a self-built Workspace MCP provider over Streamable HTTP. In Kubernetes it runs as a Worker Pod Sidecar; in local Compose it is a peer container reached by its service URL.
- The repository mount is read-only, assumed current, and owned by a separate synchronization project.
- HTTP/2 SSE streams user-visible Run events. MySQL event replay, not Redis, provides recovery.
- The local stack starts MySQL and Redis. Durable MVP data is retained permanently.
- The backend uses GORM Gen exclusively for persistence. No handwritten application code uses
  `database/sql`, `Raw`, `Exec`, handwritten SQL strings, or handwritten SQL migration files.
  Generator-owned imports in checked-in GORM Gen output are permitted.
- Database transactions use only GORM or GORM Gen transaction callbacks; direct `Begin`, `Commit`,
  and `Rollback` calls are prohibited.

## 3. Component Architecture

```text
Browser (TypeScript)
  | POST, GET, HTTP/2 SSE
  v
agent-api (Go, stateless) --------------------------+
  |                                                 |
  | MySQL reads/writes                              | Redis Pub/Sub hints
  v                                                 v
MySQL <---------------------------------------- agent-worker (Go, Eino, stateless)
  |                                                |
  |                                                | Streamable HTTP
  |                                                v
  |                                      workspace-mcp (Go, read-only)
  |                                                |
  +------------------------------------------ repository directory/PVC (read-only)

agent-worker --> Internal LLM Gateway (Chat Completions or Responses)
```

### 3.1 API role

The API accepts authenticated user actions, writes Conversation and Run records, returns snapshots, and holds SSE connections. It never invokes Eino or accesses repository paths.

### 3.2 Worker role

The Worker polls and leases Runs, loads the full required context, executes Eino, persists each durable boundary, and publishes lightweight Redis notifications after database commit. Its in-memory state is disposable.

### 3.3 Workspace MCP role

The Workspace MCP provider is the only process allowed to read the repository directory. It validates repository aliases and paths, applies result limits, writes no files, and exposes only the two allowlisted Tools.

## 4. HTTP API Contract

All API endpoints are rooted at `/api/v1`. The MVP authentication middleware creates one fixed demonstration principal, for example `demo-user` in `demo-team`. Production replaces only this middleware with SSO-derived identity and team claims.

### 4.1 Conversation endpoints

| Method and path | Purpose | Success response |
|---|---|---|
| `GET /conversations` | List the current user's undeleted Conversations | `200` paginated summaries |
| `POST /conversations` | Create a Conversation; optional first message | `201` Conversation or `202` when a Run is created |
| `GET /conversations/{conversationId}` | Return Messages and active/queued Run summaries | `200` |
| `DELETE /conversations/{conversationId}` | Soft-delete from normal UI; durable records remain | `204` |
| `POST /conversations/{conversationId}/messages` | Add a user turn and create/continue a Run | `202 {messageId, runId, status}` |

`POST .../messages` accepts:

```json
{
  "content": "Where is the order-service 500 thrown?",
  "clientMessageId": "01J..."
}
```

`clientMessageId` is a client-generated idempotency key. A retry returns the Run already created for that message rather than creating another one.

If the Conversation's current Run is `waiting`, the Message resumes that same Run. Otherwise the API creates a new queued Run with the next conversation-local Run sequence.

### 4.2 Run endpoints

| Method and path | Purpose |
|---|---|
| `GET /runs/{runId}` | Current status, attempt, latest event sequence, visible progress, and final-message reference |
| `GET /runs/{runId}/events?after={seq}` | SSE stream and durable replay of user-visible events after `seq` |
| `GET /runs/{runId}/trace` | Permission-checked summary of Steps and Tool calls |
| `GET /runs/{runId}/artifacts/{artifactId}` | Permission-checked full code/log result, with download/size guardrails |
| `POST /runs/{runId}/cancel` | Request cancellation of queued, waiting, or running Run |

### 4.3 SSE wire format

The endpoint sets `Content-Type: text/event-stream`, disables proxy buffering, sends a heartbeat comment every 15 seconds, and uses `run_events.seq` as the SSE `id`.

```text
id: 17
event: tool.completed
data: {"runId":"run_...","tool":"code.search","summary":"Found 8 matches in order-service","durationMs":184}

id: 18
event: assistant.delta
data: {"messageId":"msg_...","text":"The error originates from..."}
```

Supported initial event types are `run.started`, `progress.updated`, `tool.started`, `tool.completed`, `assistant.delta`, `assistant.completed`, `run.waiting`, `run.completed`, `run.failed`, and `run.cancelled`.

The API first authorizes the Run, registers for Redis notifications, reads durable events where `seq > after`, and subsequently uses Redis only as a prompt to read and send missing rows. Per-connection delivery is serialized by sequence number. A normal EventSource reconnect supplies `Last-Event-ID`; a full page refresh opens active Run streams with `after=0` and reconstructs transient output from events.

When a terminal event is sent, the API closes the SSE response. The frontend closes its EventSource after receiving the terminal event. A lost terminal event is replayed from MySQL on the next connection.

## 5. MySQL Data Design

All identifiers are ULIDs stored as `CHAR(26)`. Timestamps are UTC `DATETIME(6)`. JSON fields use MySQL `JSON`; potentially large immutable content is stored in `LONGTEXT`/`LONGBLOB` Artifacts. The initial schema is owned exclusively by the Agent Platform.

### 5.1 `users`

| Column | Notes |
|---|---|
| `id` PK | Demo principal id; maps to SSO subject later |
| `team_id` | Demo team id; maps to SSO organization/team claim later |
| `display_name` | UI display value |
| `created_at` | Audit timestamp |

### 5.2 `conversations`

| Column | Notes |
|---|---|
| `id` PK, `user_id` | Ownership and authorization |
| `title` | Derived from the first message, editable later |
| `next_message_seq` | Allocates total order for Messages |
| `next_run_seq` | Allocates total order for Runs |
| `next_executable_run_seq` | The only queued Run sequence eligible to start |
| `deleted_at` | Soft deletion from the UI only |
| `created_at`, `updated_at`, `latest_message_at` | List ordering |

Indexes: `(user_id, deleted_at, latest_message_at DESC)` for the list; primary key for ownership joins.

### 5.3 `messages`

| Column | Notes |
|---|---|
| `id` PK, `conversation_id`, `seq` | Unique `(conversation_id, seq)` ordering |
| `role` | `user` or `assistant` |
| `content` | Final Markdown/text content; never raw chain-of-thought |
| `status` | `final` or `streaming` for an active assistant response |
| `run_id` nullable | Run that generated an assistant Message |
| `client_message_id` nullable | Unique per Conversation for user POST idempotency |
| `created_at`, `finalized_at` | Lifecycle |

Indexes: unique `(conversation_id, seq)` and unique `(conversation_id, client_message_id)` where non-null in application logic.

### 5.4 `runs`

| Column | Notes |
|---|---|
| `id` PK, `conversation_id`, `trigger_message_id` | Run identity and initiating Message |
| `queue_seq` | Conversation-local execution order |
| `status` | `queued`, `running`, `waiting`, `succeeded`, `failed`, `cancelled` |
| `attempt`, `next_attempt_at` | Retry scheduling |
| `lease_owner`, `lease_expires_at`, `execution_token` | Exclusive Worker ownership and fencing |
| `agent_config_version`, `skills_bundle_version` | Immutable build/config pin |
| `model_config` JSON | Chosen API mode, model, and runtime parameters |
| `workspace_ref` JSON | Repository aliases and the external mount identity |
| `latest_checkpoint_id`, `next_event_seq` | Recovery and event allocation |
| `cancel_requested_at`, `terminal_error_code` | Control and failure reporting |
| `started_at`, `finished_at`, `created_at`, `updated_at` | Observability |

Indexes: `(status, next_attempt_at, lease_expires_at)`, `(conversation_id, queue_seq)`, and `(conversation_id, status)`.

### 5.5 Execution records

| Table | Key fields | Purpose |
|---|---|---|
| `run_steps` | `(run_id, step_no)` unique; `kind`, `status`, `safe_summary`, timestamps | Durable, user-explainable execution boundaries |
| `tool_calls` | `id`, `run_id`, `step_no`, `server_key`, `tool_name`, `arguments`, `result_summary`, `artifact_id`, `idempotency_key`, `status` | Tool audit and retry record |
| `run_checkpoints` | `id`, `run_id`, `ordinal`, `runtime_name`, `state_schema_version`, `payload`, `created_at` | Runtime-specific state for recovery |
| `run_events` | `(run_id, seq)` unique; `type`, `safe_payload`, `created_at` | Durable SSE replay log |
| `artifacts` | `id`, `run_id`, `kind`, `content_type`, `content`, `sha256`, `byte_size`, `created_at` | Full immutable Tool results/reports when too large for a summary |

No raw model chain-of-thought column exists. Steps record a concise action summary; model request/response metadata may be added later under restricted audit policy, but is not part of this MVP schema.

## 6. Run State Machine and Transactions

```text
queued -> running -> succeeded
                  -> failed
                  -> cancelled
                  -> waiting -> queued

running -- lease expiry / recoverable failure --> queued
queued or waiting -- cancellation --> cancelled
```

### 6.1 Create a normal user turn

In one MySQL transaction, the API locks the Conversation row, allocates `messages.seq`, writes the user Message, allocates `runs.queue_seq`, writes a `queued` Run with its pinned configuration values, and increments the Conversation counters. There is no separate outbox or Kafka producer in MVP because the Run table is the queue.

### 6.2 Claim and renew

The Worker polls eligible Runs using MySQL 8 row locking (`FOR UPDATE SKIP LOCKED`). A candidate must be `queued`, due by `next_attempt_at`, and have `queue_seq = conversations.next_executable_run_seq`. The claim transaction sets `running`, increments `attempt` and `execution_token`, assigns `lease_owner`, and sets `lease_expires_at` to now plus 30 seconds.

The Worker renews the lease every 10 seconds using `WHERE id = ? AND execution_token = ?`. Every Step, Checkpoint, event, and terminal update uses the same fencing predicate. If renewal fails, the Worker stops without writing further state.

### 6.3 Durable Step and Checkpoint boundary

After a model decision or completed Tool call, the Worker writes the Step outcome, Tool call record/result reference when applicable, Checkpoint, and one or more safe `run_events` in one transaction. It increments `runs.next_event_seq` transactionally to allocate event IDs.

The Worker publishes `{runId, seq}` on Redis only after commit. Model Token deltas are batched into one event approximately every 250 ms and are not individual Checkpoints.

### 6.4 Terminal transaction

On success, the Worker writes/finalizes the assistant Message, changes the Run to `succeeded`, appends `assistant.completed` and `run.completed`, clears the lease, advances `conversations.next_executable_run_seq`, and sets timestamps in one transaction. Failure and cancellation follow the same pattern with their terminal event.

The next queued Run becomes eligible only after this transaction. This enforces in-order multi-turn execution without holding a Worker session for the Conversation.

## 7. Redis Design

Redis is intentionally small. MySQL remains authoritative after Redis restart, eviction, or message loss.

| Kind | Name / pattern | Value | TTL | Purpose |
|---|---|---|---|---|
| Pub/Sub channel | `agent-platform:run-events:{runId}` | `{runId, seq}` | N/A | Prompt API instances to read committed events |
| Rate-limit key | `agent-platform:rate:user:{userId}:{window}` | integer counter | 120 s | Optional API message limit |
| Rate-limit key | `agent-platform:rate:team:{teamId}:{window}` | integer counter | 120 s | Optional team protection |

There are deliberately no Redis keys for Run leases, queues, Checkpoints, Conversation history, token replay, or event persistence. There is no Redis Stream requirement in MVP.

## 8. Workspace MCP Contract

The MCP Server declares exactly two Tools in `tools/list`.

### 8.1 `code.search`

```json
{
  "repo": "order-service",
  "query": "ErrOrderNotFound",
  "pathPrefix": "",
  "glob": "**/*.go",
  "maxResults": 20
}
```

Returns repository-relative path, line number, matching line, limited surrounding context, truncation flag, and observed repository revision when available. The server enforces `1 <= maxResults <= 50`, searches only configured repository roots, and never follows paths outside the root.

### 8.2 `file.read`

```json
{
  "repo": "order-service",
  "path": "internal/order/service.go",
  "startLine": 1,
  "endLine": 220
}
```

Returns repository-relative path, the requested numbered line range, truncation metadata, and observed repository revision when available. It rejects absolute paths, `..` traversal, symlinks resolving outside configured roots, non-allowlisted repositories, and oversized ranges. The initial maximum is 1,000 lines or 256 KiB per invocation.

### 8.3 Error and timeout policy

The MCP provider emits machine-readable error codes: `INVALID_REPO`, `INVALID_PATH`, `PATH_OUTSIDE_WORKSPACE`, `NOT_FOUND`, `RESULT_LIMIT_EXCEEDED`, `TIMEOUT`, and `INTERNAL`. The Worker gives each Tool call a default 30-second deadline and retries transient read-only failures up to two times with bounded exponential backoff. Every Tool call carries a generated idempotency key even though the initial Tools are read-only.

## 9. Eino Runtime Design

### 9.1 Versioned Agent configuration

The release bundles an immutable configuration file such as:

```yaml
agent:
  id: issue-troubleshooter
  version: 2026-07-17.1
  runtime: eino-react
  model:
    api_mode: responses # or chat_completions
    model: internal-default
    temperature: 0.1
  limits:
    max_steps: 12
    run_timeout_seconds: 600
  mcp_servers:
    - key: workspace
      url: ${WORKSPACE_MCP_URL}
      allowed_tools: [code.search, file.read]
  skills_bundle_version: 2026-07-17.1
```

The Worker records the effective config and Skills version in every Run. Future MCP Servers are added only by configuration plus their own service; the platform core remains unchanged.

### 9.2 ReAct loop

1. Build context from the system prompt, bounded Conversation history, current user Message, and latest Checkpoint.
2. Invoke the configured Eino model adapter with the two Tool schemas.
3. If the model requests Tools, create Tool-call Step records, invoke the Workspace MCP provider, store safe summaries/Artifacts, and feed observations back to the model.
4. If the model returns a final answer, normalize it to a Markdown troubleshooting report, persist the final Message, and complete the Run.
5. If the step or time budget is exceeded, produce a clear partial report with executed evidence and terminate as `failed` or `succeeded-with-limits` according to the final error policy. MVP uses `failed` with a retained partial Artifact.

The prompt requires evidence-based conclusions, repository/path/line citations where available, explicit uncertainty, and a concise final report containing findings, evidence, likely cause, and suggested next investigation. It does not request or expose raw chain-of-thought.

### 9.3 Runtime checkpoint payload

The versioned Eino payload contains only recoverable execution state: current ReAct iteration, normalized Conversation context reference, completed Tool-call IDs and observations, pending model/tool action, and streaming assistant Message state. It includes `runtime_name` and `state_schema_version`; a Worker rejects unsupported versions rather than corrupting a Run.

## 10. Frontend Design

The TypeScript client has three screens/components: Conversation list, Conversation detail, and collapsed/expanded Run trace.

- The Conversation detail fetches durable Messages first.
- A user Message POST receives `runId`, immediately renders queued/running UI, and opens the SSE endpoint.
- The main chat renders `assistant.delta` content; the trace renders safe progress and Tool summaries.
- On refresh, the client reloads the Conversation and subscribes each active Run with `after=0`. Event handling is idempotent by `(runId, seq)`.
- On a terminal event, it closes the EventSource and refetches the Conversation snapshot to ensure final persisted content is shown.
- Full Artifacts are never embedded into the chat event stream; an authorized user explicitly requests them from the Artifact endpoint.

## 11. Local Docker Delivery

The repository separates code by independently deployable product boundary:

```text
frontend/                 React and TypeScript application
backend/                  one Go module for API and Worker roles
mcp-server/               independent Go module for Workspace MCP
compose.yaml, .env.example, scripts/, README.md
```

`scripts/up.sh` validates `REPOS_DIR`, verifies that Docker Compose v2 and the Docker daemon are available, and starts the stack through Docker Compose. It never installs or starts host software automatically. Container image builds use multi-stage Go and Node/TypeScript builds; no host Go or Node installation is required beyond Docker.

Compose starts `frontend`, `api`, `worker`, `workspace-mcp`, `mysql`, and `redis`. `workspace-mcp` alone receives `${REPOS_DIR}:/workspace/repos:ro`; Worker uses `WORKSPACE_MCP_URL=http://workspace-mcp:8081/mcp`. This local peer-container layout preserves the exact MCP boundary. In a future Kubernetes deployment the same MCP container is moved into the Worker Pod as a true Sidecar and addressed at `localhost`.

The README Quickstart covers: install and start Docker Desktop (or Docker Engine with Compose v2), clone this project, supply/verify the external repository directory, copy `.env.example` to `.env`, configure LLM gateway credentials, run `./scripts/up.sh`, open the local UI, run a sample question, inspect logs, and run `./scripts/down.sh`.

## 12. Observability and Failure Handling

- Every HTTP request, Run, attempt, Step, Tool call, Checkpoint, and SSE event carries correlation IDs in logs.
- API and Worker expose `/healthz` and `/readyz`; Worker readiness requires MySQL, Redis, LLM configuration, and Workspace MCP reachability.
- Structured logs contain IDs, durations, status, safe error codes, and model/Tool names, never raw chain-of-thought.
- A periodic Worker recovery loop requeues expired leases and observes cancellation requests.
- API restart only closes SSE connections; clients reconnect and replay from MySQL.
- Worker restart loses only uncheckpointed in-memory work. A new Worker recovers from the last committed Checkpoint after lease expiry.

## 13. MVP Acceptance Criteria

1. `./scripts/up.sh` starts the full stack on a clean macOS or Linux host with Docker and an external read-only repository directory.
2. A demo user can create a Conversation, ask a code question, see streamed progress and answer, refresh the page mid-Run, and recover the final answer without rerunning the Run.
3. The Agent can call `code.search` and `file.read` through MCP; it cannot write files or access paths outside the configured repository roots.
4. Killing the Worker during a Tool sequence causes a lease-based recovery and continuation from the last Checkpoint.
5. Restarting the API during an SSE stream allows browser reconnection and replay by event sequence.
6. The Conversation list, Messages, Runs, Steps, Tool records, Checkpoints, Artifacts, and events remain inspectable in MySQL after completion.
7. The selected Chat Completions or Responses model API is switched only by Agent configuration and environment values, not business-code changes.

## 14. Implementation Order

1. Repository skeleton, Compose launcher, local MySQL/Redis, configuration loading, and README Quickstart.
2. MySQL migrations and repository layer for users, Conversations, Messages, Runs, events, and leases.
3. API: demo authentication, Conversation endpoints, Run creation/cancel, and snapshot endpoints.
4. Workspace MCP provider: configuration, path safety, `code.search`, `file.read`, and tests.
5. Worker: leasing, state machine, Eino model adapter, Streamable HTTP MCP client, ReAct loop, Steps, Checkpoints, retries, and terminal transactions.
6. SSE API and Redis notification path, then reconnect and refresh tests.
7. TypeScript UI for conversations, chat streaming, trace, error/cancellation states, and Artifact expansion.
8. Failure-injection tests, container smoke test, documentation polish, and MVP demo script.

## 15. Deferred Evolution

The following remain intentionally outside this implementation but are preserved by the boundaries above: Agent Registry UI, dynamic Skills management, Wiki/Memory MCP services, write-capable tools and isolated writable Workspaces, repository synchronization, multiple Workers per local stack, SSO, Kafka, Temporal, multi-Agent orchestration, and production Kubernetes manifests.
