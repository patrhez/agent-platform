# Enterprise Troubleshooting Agent Platform — MVP Architecture Baseline

**Status:** Discussion baseline (confirmed decisions recorded; detailed design pending)  
**Date:** 2026-07-17  
**Primary runtime:** Go + Eino  
**Web client:** TypeScript

## 1. Purpose

Build a standalone MVP to demonstrate an internal issue-troubleshooting Agent Platform. The MVP is deliberately separate from the existing frontend and backend so that architecture choices can be validated independently before any refactor or migration.

The initial product is one Issue Troubleshooting Agent for backend engineers. It should support multi-turn troubleshooting conversations across multiple repositories, use the existing internal LLM gateway and MCP services, and produce useful troubleshooting reports.

The MVP is also the foundation for a future configuration-driven Agent Platform. The platform must not be coupled to Eino as a business model: Eino is the first runtime adapter, not the owner of conversations, tasks, permissions, or persistence.

## 2. MVP Scope

### In scope

- A simple TypeScript web UI with a conversation list, conversation history, and multi-turn chat.
- A Go backend with separate API and Worker runtime roles that scale independently.
- An Eino-based Agent Runtime supporting streaming, MCP tools, Skills, repository analysis, and report generation.
- Durable Conversations, Runs, execution checkpoints, and user-visible execution events.
- Browser reconnection and refresh recovery for in-progress Runs.
- Existing MySQL and Redis integration.
- Read-only repository and diagnostic capabilities.
- One-command local deployment on macOS and Linux through Docker, with Quickstart documentation.

### Explicitly out of scope for MVP

- Letting end users create or publish arbitrary Agents from a configuration UI.
- Kafka as a task queue or event bus.
- Temporal or another external durable workflow platform.
- Write-capable repository, production, deployment, or configuration-changing tools.
- Generic unrestricted shell access.
- A full enterprise workspace platform integration.
- Exposing raw model chain-of-thought to users or storing it as product data.
- Git clone, fetch, pull, branch synchronization, or any repository lifecycle management.

## 3. Confirmed Architectural Decisions

| Decision | MVP direction | Rationale |
|---|---|---|
| Runtime | Eino in Go | Matches the team stack and provides Agent, graph, MCP, stream, and interrupt/resume capabilities. |
| Service form | Modular monolith with API and Worker deployment roles | Keeps one coherent domain model while isolating request/SSE load from long-running Agent work. |
| Database ownership | One logical Agent Platform MySQL schema | API and Worker are roles of the same bounded context, not independently owned microservices. |
| Reliable task queue | MySQL-backed Run state machine and leases | Reuses existing infrastructure and keeps execution state easy to inspect. |
| Redis role | Non-authoritative real-time fan-out, rate limits, and short-lived coordination | Redis loss must not lose Conversations, Run state, Checkpoints, or UI events. |
| Streaming protocol | HTTP/2 Server-Sent Events (SSE) | Browser-native one-way stream with simple reconnect semantics; user input remains normal HTTP POST. |
| Browser recovery | Durable `run_events` in MySQL, replayed by event sequence | Reconnection never depends on a particular API instance or Redis message retention. |
| Security boundary | Strictly read-only for MVP | Enforced by workspace mounts, MCP scopes, and tool allowlists rather than prompts alone. |
| Repository source | Read the latest content on the mounted branch | Chosen for MVP simplicity; reproducibility through a pinned commit is deferred. |
| Workspace lifecycle | Per-Run logical read-only workspace | Each Run references the common repository mount without a physical copy or cross-Run temporary state. |
| Execution trace UI | Summary by default; full result on demand | Chat shows formal answers and collapsible safe summaries. Full code/log result views require user action, authorization, and size limits. |
| MVP identity | Simplified demo login with fixed team identity | Keeps the demo self-contained. Production will use company SSO user and team/organization claims for authorization. |
| Agent and Skills loading | Versioned files bundled with the service release | The one MVP Agent uses a released configuration and Skills bundle; each Run records both versions. Database Agent Registry is deferred. |
| MCP transport | Streamable HTTP | Workers act as MCP Clients; the Workspace Sidecar is reached over loopback HTTP. |
| Enabled Tools | `code.search` and `file.read` only | The self-built Workspace MCP Sidecar is the sole enabled Tool provider for MVP validation. |
| Future MCP extension | Configuration-only server registration | Wiki, Memory, and other MCP Servers are not implemented or integrated in MVP; future Agent configurations can enable them without changing platform core. |
| Agent control flow | Eino ReAct tool-calling loop | The Agent iterates analysis, read-only Tool calls, and observation with configured step and time limits; multi-Agent and custom graph orchestration are deferred. |
| LLM Gateway | Configurable OpenAI-compatible API selection | The runtime supports both Chat Completions and Responses via adapters; the released Agent configuration selects interface, model, and parameters. |
| Data retention | Permanently retain all durable MVP data | No deletion, expiry, compaction, or archival workflow is implemented for Conversations, Runs, events, tool results, Artifacts, or reports. |
| Repository synchronization | External prerequisite | A separate project maintains clone/fetch/pull and the latest branch content. This MVP only reads the supplied directory or PVC. |
| Local deployment | One-command container deployment on macOS and Linux | Docker Compose manifests and a Docker launcher build dependencies in images and start the complete MVP stack. |
| Local dependencies | Start MySQL and Redis by default | The Compose stack is self-contained for demonstrations; environment variables may override these endpoints with external instances. |
| Default demonstration scale | One frontend, API, Worker, and Workspace MCP service; Worker concurrency 1 | Keeps local startup simple. Stateless roles, Run leases, and configuration preserve a path to later horizontal scaling. |

## 4. Core Domain Model

### 4.1 Conversation, Message, Run, Step, and Checkpoint

```text
Conversation
  ├─ Message (user) ──> Run 1 ──> Step 1..N ──> Assistant Message
  ├─ Message (user) ──> Run 2 ──> Step 1..N ──> Assistant Message
  └─ ...
```

- **Conversation:** A user-owned multi-turn troubleshooting context. It contains the full chat history and its Runs.
- **Message:** One user or assistant chat message.
- **Run:** One background Agent execution triggered by a user message or, in the future, another platform event. A normal user follow-up creates a new Run.
- **Step:** A meaningful execution boundary inside a Run: model decision, tool invocation, tool result processing, report generation, or user-interaction pause.
- **Checkpoint:** A durable snapshot after a meaningful Step boundary. It lets a new Worker resume an interrupted Run without repeating finished work.

If the Agent deliberately pauses for user input or approval, the Run becomes `waiting`; that response resumes the same Run. A normal follow-up after a completed response creates a new Run.

### 4.2 Tool and MCP relationship

- **Tool:** One model-callable operation with a name, JSON-schema input, result, and permission policy. `code.search`, `file.read`, `wiki.search`, and `memory.retrieve` are all Tools.
- **MCP Server:** A provider that exposes one or more Tools (and optionally MCP Resources and Prompts) over an MCP transport. It is not itself a Tool.
- **Agent capability:** An Agent-visible ability created by enabling a specific Tool. The capability is independent from whether that Tool is delivered through MCP or another future adapter.

The MVP runtime follows the requirement that it contains an MCP Client rather than repository business logic. Therefore `code.search` and `file.read` are exposed by a self-built, read-only Workspace MCP Sidecar over Streamable HTTP on `localhost`. The Sidecar is the only enabled Tool provider in MVP. The repository PVC is mounted read-only into the Sidecar only; the Agent Worker has no direct repository filesystem access. `file.write`, Wiki search, and Memory retrieval are future Tool categories and are not enabled or integrated in MVP. Agent configuration retains the ability to register additional MCP Servers later without changing platform core.

### 4.3 Single active Run per Conversation

At most one Run is executing for a Conversation at a time. Later user messages create queued Runs, preserving multi-turn ordering and avoiding conflicting interpretations of the same Conversation context.

## 5. Deployment and Responsibility Boundaries

```text
                    +-------------------------+
                    | TypeScript Web Client    |
                    +------------+------------+
                                 | HTTPS / SSE
                    +------------v------------+
                    | agent-api               |
                    | auth, conversation API, |
                    | Run queries, SSE        |
                    +------------+------------+
                                 |
                    +------------v------------+
                    | MySQL: Agent Platform   |
                    | authoritative state     |
                    +------------+------------+
                                 ^
                    +------------+------------+
                    | agent-worker            |
                    | Run lease, Eino, MCP,   |
                    | checkpoints, events     |
                    +---+---------------+-----+
                        |               |
                 +------v-----+   +-----v----------------+
                 | Redis      |   | LLM Gateway / MCP /   |
                 | live hints |   | Skills / Workspace    |
                 +------------+   +-----------------------+
```

### 5.1 API role

The API role:

- authenticates users and authorizes access to Conversations and Runs;
- creates user Messages and corresponding queued Runs;
- returns conversation and Run snapshots;
- streams user-visible Run events through SSE;
- accepts cancel and future approval/resume commands.

It does not execute Eino, hold Conversation state in memory, or own a Worker session.

### 5.2 Worker role

The Worker role:

- atomically claims eligible Runs from MySQL;
- loads the Conversation, pinned Agent configuration, Skills, workspace reference, and latest Checkpoint;
- invokes Eino, the LLM gateway, MCP tools, and permitted read-only commands;
- writes Steps, Checkpoints, user-visible events, and terminal results;
- releases execution ownership at completion.

MCP servers are accessed through Streamable HTTP. Workers manage the MCP Client lifecycle, authentication, connection/request timeouts, and platform-generated `tool_call_id` values. The self-built Workspace MCP Server runs as a Sidecar in the Worker Pod and is accessed through a loopback HTTP endpoint; the protocol boundary does not require crossing a network. No Worker-local MCP subprocess is started for MVP.

An arbitrary healthy Worker can execute or resume any Run. Runs are never bound to a particular Worker instance.

### 5.3 State ownership

| Component | State classification | Source of truth |
|---|---|---|
| Web client | Ephemeral UI state | API/MySQL on reload |
| API process | Stateless | MySQL and Redis |
| Worker / Eino process | Stateless between persistence boundaries | MySQL Checkpoint and Run state |
| MySQL | Durable authoritative state | Conversation, Run, event, and configuration records |
| Redis | Ephemeral, non-authoritative state | Live fan-out and short-lived coordination only |
| Workspace | External execution resource | Persisted workspace reference and lifecycle metadata |

## 6. Reliable Run Execution Without Kafka or Temporal

### 6.1 MySQL is the durable queue

The `runs` table is the source of truth and queue. Each Run has, at minimum:

```text
status: queued | running | waiting | succeeded | failed | cancelled
attempt
next_attempt_at
lease_owner
lease_expires_at
execution_token
checkpoint_id
agent_version
skills_version_set
workspace_ref
```

Workers poll for eligible queued or expired-lease Runs, claim one transactionally, and periodically renew its lease. A crashed or upgraded Worker loses the lease. Another Worker then claims the Run and loads its latest durable Checkpoint.

`execution_token` is incremented when ownership changes. Every Worker write must verify that token, preventing an old Worker that recovers late from overwriting the current attempt.

### 6.2 Checkpoint policy

The Worker writes a Checkpoint directly to the shared Agent Platform MySQL schema. It does not route the write through the API.

Checkpoint synchronisation occurs at side-effect boundaries, not for every Token:

- after an important model decision;
- before and after a tool invocation when recovery semantics require it;
- after a tool result is processed;
- before entering `waiting`;
- when the final response or report is committed.

The Checkpoint transaction also records the completed Step and any related user-visible event. This synchronous durability cost is small compared with LLM and MCP latency. Token deltas are batched separately (for example, every 200–500 ms) and are not Checkpoints.

### 6.3 Idempotency rule

The platform provides at-least-once task delivery, not magical exactly-once external side effects. Each MCP invocation receives a platform-generated `tool_call_id` / idempotency key.

- Read-only calls may be retried safely.
- Future write-capable calls must be idempotent at the tool side or require explicit human approval.
- An uncertain timeout is recorded as `unknown`; it is not blindly repeated.

### 6.4 Why Kafka and Temporal are deferred

Kafka is not needed while a MySQL Run queue meets throughput requirements. It would add producer/consumer operations and dual-state diagnosis without helping the typical MVP bottleneck: LLM, MCP, repository, and log-query latency.

Temporal offers powerful durable orchestration for workflows lasting days or weeks, timers, signals, complex human approval, and multi-agent orchestration. It also introduces a separate operational system and requires workers to understand deterministic workflow replay and Activity boundaries. The MVP instead establishes a clean Run/Checkpoint adapter boundary so Temporal can be evaluated later without redesigning the product model.

## 7. Event, Output, and Trace Model

### 7.1 Three separate data layers

| Layer | Contents | User visibility |
|---|---|---|
| Execution record | Run state, Step state, Checkpoints, tool invocation metadata and result references | Internal; selected summaries may be shown |
| User-visible event stream | Progress summaries, tool start/end, safe result summaries, output deltas, terminal state | SSE and recoverable UI history |
| Conversation message | Final assistant response/report and user messages | Main chat history |

Raw model chain-of-thought is not saved or streamed. The platform may generate or store concise, structured action summaries such as “searched repo-a for this error code” or “retrieved three matching log entries.”

### 7.2 Tool results

Tool calls have structured request metadata, lifecycle state, duration, result summary, and result reference. Small safe results can be stored inline; large code/log outputs are represented as Artifacts with size limits, retention policies, and a permission-checked preview.

The normal UI shows the formal response and a collapsible progress trace. Full tool output is a separate, permission-controlled detail view rather than a default chat payload. It is loaded only after a user action and is subject to result-size limits.

### 7.3 Event schema and ordering

Each event has a durable, strictly increasing sequence inside one Run:

```text
run_events(run_id, seq, type, safe_payload, created_at)
```

Recommended initial event types:

```text
run.started
progress.updated
tool.started
tool.completed
assistant.delta
assistant.completed
run.waiting
run.completed
run.failed
run.cancelled
```

`seq` is the user-visible event cursor. It is not a Run ID, Message ID, or Checkpoint ID.

## 8. Streaming and Browser Recovery

### 8.1 API contract shape

```text
POST /conversations/{conversationId}/messages
  -> 202 Accepted { messageId, runId }

GET /runs/{runId}
  -> Run snapshot and terminal/current status

GET /runs/{runId}/events?after={seq}
  -> HTTP/2 SSE stream
```

The API authorizes every Run subscription. The browser never supplies a user identity as a trusted stream selector.

For MVP, the API uses a simplified login with a fixed team identity. This is an explicit demonstration-only boundary. Production authentication and team/tenant authorization will be sourced from the company SSO or identity gateway.

### 8.2 SSE behaviour

The Worker commits a durable `run_event` before publishing a Redis hint containing its `runId` and `seq`. API instances use Redis for low-latency fan-out and MySQL for event replay. A Redis loss cannot lose UI state.

For a live EventSource reconnection, the browser can send the standard `Last-Event-ID` header. The value corresponds directly to `run_events.seq`; the API replays events with a larger sequence.

For a full page refresh, JavaScript memory and EventSource state have been lost. The safe MVP recovery sequence is:

1. Load Conversation history.
2. Identify active Runs.
3. Subscribe each active Run using `after=0` and reconstruct its transient progress from durable events.

Only a later optimisation may return an output snapshot plus `snapshot_event_seq`. The client may subscribe after that value only when the returned snapshot includes all effects through that exact sequence.

### 8.3 Terminal state and stream closing

The Worker atomically writes the final assistant Message when applicable, changes `runs.status` to a terminal state, records `finished_at`, releases the lease, and appends a terminal Run event.

Terminal Run states are `succeeded`, `failed`, and `cancelled`. `waiting` is not terminal.

The API streams the terminal event, then closes the SSE response. The frontend closes its EventSource after receiving it to prevent reconnection. If the terminal event is missed, a later subscription replays it from MySQL and closes normally.

## 9. Read-only MVP Security Model

Read-only must be enforced by infrastructure, not just Agent instructions.

- Repositories are mounted read-only and read from the latest content of the selected mounted branch. The platform records the observed ref/commit for diagnostics when available, but does not require a pinned snapshot for MVP.
- In a deployed Worker Pod, the repository PVC is mounted only into the Workspace MCP Sidecar. In local development, a local repository directory is supplied to that Sidecar in place of the PVC.
- The repository directory or PVC is an external prerequisite and is assumed to contain the latest required branch content. Repository synchronization is owned by a separate engineering project and is outside MVP scope.
- MCP access tokens and exposed methods are read-scoped.
- Only an explicit allowlist of read-only commands is permitted when shell access is needed, such as code search and Git inspection.
- No generic shell, repository write, deployment, configuration, production-operation, or deletion capability is included.
- Reports and platform records are persisted by the platform; they are not written back to repositories.

## 10. Resource Lifecycle and Retention

- On terminal Run completion, release the lease and close runtime/MCP streams. The logical read-only Workspace has no physical directory to clean up in MVP.
- The MVP Workspace is a logical, per-Run read-only reference to the common repository mount. It creates no physical checkout or cross-Run temporary state, so terminal Run cleanup only releases the reference. A future write-capable Workspace may introduce isolated physical directories and retention cleanup.
- User Conversation deletion is a soft delete from the normal UI only. The durable Conversation and Run records remain retained permanently in MVP. Active Runs must be cancelled or allowed to finish according to a future product policy.
- Redis channels and API SSE subscriber state are ephemeral and removed when request contexts end.
- Conversations, Messages, Runs, Steps, Checkpoints, events, tool results, Artifacts, and reports are retained permanently. No deletion, expiry, compaction, or archival job is part of MVP.

## 11. Runtime Replaceability

The Platform owns domain state. Eino is accessed through a runtime adapter that receives a pinned Agent definition, Conversation context, workspace handle, tool set, and latest Run Checkpoint; it returns events, step outcomes, and an updated runtime checkpoint payload.

This keeps future runtime replacement feasible without changing Conversation, Run, event, permission, or workspace business semantics.

For MVP, the Agent definition (Prompt, Rules, model selection, enabled MCP servers, and Skill references) is a versioned configuration file bundled with the service release. The existing Skills repository is assembled into the released Skills bundle during build/release rather than fetched dynamically at Run time. At Run creation, the platform records the immutable Agent configuration version and Skills bundle version. A database-backed Agent Registry and dynamic configuration UI remain future platform work.

The internal LLM Gateway exposes both OpenAI-compatible Chat Completions and Responses APIs. The runtime provides a common model adapter over both interfaces; each released Agent configuration selects the API mode, model identifier, and model parameters without changing platform business code.

The MVP uses Eino's ReAct-style tool-calling Agent. It iterates model analysis, read-only Tool calls, and observations until it emits a final Markdown troubleshooting report or reaches a configured step/time limit. Custom multi-branch graphs and multi-Agent orchestration are deliberately deferred.

## 12. Industry References Used in This Baseline

- [Eino](https://github.com/cloudwego/eino): Go Agent development, graphs, streams, MCP tools, and interrupt/resume.
- [LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence): durable step checkpoints and recovery patterns.
- [LangGraph Agent Server](https://docs.langchain.com/langsmith/agent-server): API/worker separation, durable queue workers, checkpoints, and streaming.
- [Temporal AI reference architecture](https://go.temporal.io/platform-hub/ai-engineering/ai-reference-architecture): durable orchestration trade-offs and non-deterministic I/O boundaries.
- [MCP Streamable HTTP transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports): MCP client/server transport, distinct from browser chat streaming.
- [SSE / EventSource](https://html.spec.whatwg.org/dev/server-sent-events.html): reconnect and `Last-Event-ID` semantics.
- [AG-UI](https://docs.ag-ui.com/): emerging event vocabulary for agent-to-UI integrations; informational only for MVP.

## 13. Local Delivery Requirement

The final MVP repository must support one-command local deployment on macOS and Linux. It provides Docker Compose service definitions and a small launcher that validates Docker Compose and its daemon before starting the stack. The deployment includes the frontend, API, Worker, Workspace MCP Sidecar, and local MySQL and Redis containers by default. Environment variables allow MySQL and Redis endpoints to be overridden with external instances. Go and TypeScript dependencies are resolved in reproducible container image builds rather than installed manually on the host. A README Quickstart documents prerequisites, configuration, the required read-only repository directory mount, startup, validation, and shutdown.

Production deployment topology is a separate concern; the local Compose stack is the portable MVP demonstration environment.

The default local profile starts one frontend, one API, one Worker, and one Workspace MCP service, with one active Run per Worker. Worker concurrency and replica count are configuration concerns for later environments; the API and Worker remain stateless and Run leases remain valid under scale-out.

## 14. Decisions Required Before Detailed Design

The following decisions are intentionally deferred and will drive the next design stage:

1. Deployment target and expected concurrency, which determine Worker sizing and MySQL polling/lease parameters.

## 15. Next Design Deliverables

After the decisions above are made, produce:

1. Service API and SSE event contracts.
2. MySQL logical schema, indexes, state transitions, and migration plan.
3. Redis channels, keys, TTLs, and failure behaviour.
4. Eino runtime adapter, tool contract, checkpoint format, and error/retry design.
5. TypeScript UI state machine and refresh/reconnection flows.
6. Read-only workspace and MCP permission design.
7. Docker local deployment, image builds, Quickstart, observability, testing, and MVP acceptance criteria.
