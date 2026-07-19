# Execution and Streaming FAQ

This note answers concrete questions about how Conversations, Runs, SSE, Redis, and Workspace MCP interact. Examples use real rows from a local MySQL instance when available.

Example conversation used below:

| Field | Value |
|---|---|
| Conversation | `01KXT43KJZNHNTH8SE5FRSWA8Y` |
| Run 1 (list projects) | `01KXT43KKEN91FT9TMF7GKJ5ZY` |
| Follow-up user message | `Explain paxos-demo in a single sentense.` |
| Run 2 (follow-up) | `01KXT5M1RZTTPN1AGQ4Y1RVD2Y` |

---

## 1. How does the API know which Redis channel to subscribe to? How is Conversation content filtered? What about SSE resume?

### Channel selection is by Run ID, not Conversation ID

SSE is opened against a **Run**, not a Conversation:

```http
GET /api/v1/runs/{runID}/events?after={cursor}
```

The handler takes `runID` from the path and subscribes to:

```text
agent-platform:run-events:{runID}
```

See `backend/internal/events/notifier.go` (`channelPrefix + runID`) and `backend/internal/httpapi/sse.go`.

There is **no Conversation-level Redis channel**. Filtering is structural:

1. Frontend only opens SSE for **active** Runs of the loaded Conversation (`queued` / `running` / `waiting`).
2. API authorizes the Run with `GetRunSnapshot(userID, runID)` before streaming.
3. Redis Pub/Sub and MySQL `run_events` are both keyed by `run_id`.

Conversation chat history is loaded separately via `GET /api/v1/conversations/{conversationID}` (Messages + Runs snapshot). SSE only streams **execution events for one Run**.

### Fresh subscribe vs resume

| Mode | Frontend | API |
|---|---|---|
| Fresh | `EventSource(/runs/{runID}/events?after=0)` | cursor = 0 |
| Resume | Browser reconnects with `Last-Event-ID: N`, or client passes `?after=N` | cursor = N |

Cursor parsing (`eventCursor` in `sse.go`):

1. Prefer HTTP header `Last-Event-ID` (SSE standard).
2. Else query param `after`.
3. Else `0`.

**How prior content is recovered on resume:**

1. Chat messages: `GET /conversations/{id}` from MySQL (authoritative).
2. Missed Run events: `ListRunEvents(runID, seq > after)` from MySQL `run_events`.
3. Redis is only a wake-up hint; resume never depends on Redis retaining messages.

Current frontend always opens with `after=0` and deduplicates by `runID:seq` in memory (`frontend/src/state/conversation.ts`). A production client can pass the last seen seq to avoid replaying everything.

---

## 2. What is an `assistant.delta`? Does it use Redis Pub/Sub then SSE?

### Concept

A **delta** is one durable, ordered fragment of the model's streamed answer text.

While the LLM streams tokens, the Worker does not wait for the full answer before notifying the UI. It batches stream chunks into `assistant.delta` events with:

- `streamId` — identifies one assistant generation attempt (for example `{runID}:{attempt}:{stepNo}`)
- `attempt` / `stepNo` — generation identity
- `offset` — monotonic fragment index within that stream (`0, 1, 2, ...`)
- `text` — the fragment body

Lifecycle for one final answer:

1. `assistant.started` — open a draft stream
2. many `assistant.delta` — append text in order
3. `assistant.completed` — publish the full safe text once
4. later `run.completed` — Run terminal

Buffering rules (`backend/internal/worker/stream_buffer.go`):

- flush when pending text >= 256 bytes, or
- flush when >= 100ms since last flush

So "43 deltas" means 43 flushed fragments, not 43 raw LLM tokens.

### Delivery path (yes: MySQL commit -> Redis hint -> API SSE)

```text
LLM token
  -> Worker streamBuffer
  -> AppendRunEvents / PersistBoundary  (MySQL run_events)
  -> Redis PUBLISH {runId, seq}         (hint only)
  -> API Subscribe wakes up
  -> API SELECT run_events WHERE seq > cursor
  -> SSE: id / event / data to browser
  -> frontend applyEvent concatenates draft.content
```

Redis does **not** carry the answer text. The API always re-reads MySQL after a hint (or heartbeat).

---

## 3. What do Worker, API, and frontend clean up on `CompleteRun`?

### Worker

On success (`completeSuccess` -> `CompleteRun`):

1. Flush any remaining stream buffer.
2. In one MySQL transaction:
   - insert final assistant `messages` row (`status=final`, linked `run_id`)
   - set Run `status=succeeded`, clear `lease_owner` / `lease_expires_at`, set `finished_at`
   - append terminal `run.completed` event
   - advance Conversation `next_executable_run_seq` so the next queued Run can be claimed
3. Publish Redis hint for the terminal event seq.
4. Stop lease renewal for that Run; loop back to claim the next eligible Run.
5. Close the Workspace MCP client for that execution (runtime `Close` path).

Worker does **not** close the browser SSE connection; it only finishes durable state.

### API

When SSE handler sees a terminal event type (`run.completed` / `run.failed` / `run.cancelled`):

1. Writes that SSE frame to the client.
2. Returns from `streamRunEvents`.
3. `defer subscription.Close()` unsubscribes the Redis Pub/Sub channel.
4. HTTP response ends; no further heartbeats for that connection.

If the client disconnects earlier, `request.Context().Done()` exits the loop and the same Redis unsubscribe runs.

### Frontend

On `run.completed` / `run.failed` / `run.cancelled` (`frontend/src/api/events.ts`):

1. `EventSource.close()` — stop the browser SSE.
2. `onTerminal` -> `loadConversation(activeID)` — reload authoritative Messages/Runs from API.
3. Draft streaming bubble is dropped once a formal assistant Message for that `runId` exists.
4. Send button re-enables because no Run remains in `queued`/`running`.

No Redis cleanup is required on the frontend; it never talks to Redis.

---

## 4. Why ULID instead of UUID? Is ULID the industry mainstream?

### Why this repo uses ULID

IDs are generated with `github.com/oklog/ulid` (backend) and a Crockford Base32 helper (frontend `newClientMessageID`). Schema columns are `char(26)`.

Practical reasons for this MVP:

1. **Lexicographically sortable by time** — newer IDs sort after older ones as strings, which helps debugging and approximate time ordering without always joining `created_at`.
2. **Shorter public strings** — 26 characters vs UUID's 36.
3. **URL / log friendly** — case-insensitive Crockford Base32, no hyphens.
4. **Fits `clientMessageId` idempotency** — client and server share the same ID shape.

### Is ULID "mainstream"?

Not as a single industry default. Rough landscape:

| ID | Status | Notes |
|---|---|---|
| UUIDv4 | Historically most common | Random; can fragment B-tree indexes as PKs |
| ULID | Popular community choice | Spec: [ulid/spec](https://github.com/ulid/spec); sortable; not an IETF RFC |
| UUIDv7 | Emerging standards choice | [RFC 9562](https://www.rfc-editor.org/rfc/rfc9562.html) (2024); timestamp-ordered UUID |

References:

- ULID specification: <https://github.com/ulid/spec>
- UUID formats including v7: <https://www.rfc-editor.org/rfc/rfc9562.html>
- Comparison discussions commonly note: use **UUIDv7** when you want a formal UUID-compatible sortable ID; use **ULID** when you want a shorter string ID with the same time-sort property.

Honest summary for this project: ULID is a deliberate, common engineering choice for string primary keys; it is not "the" universal mainstream. UUIDv7 is the closest standards-backed peer.

---

## 5. `CreateUserMessageAndRun`: `clientMessageID`, `advanceConversation`, transactions, `status=final`

### What is `clientMessageID`?

A **client-generated idempotency key** for one user turn.

- Frontend creates it with `newClientMessageID()` (ULID-shaped) before `POST .../messages`.
- Stored on `messages.client_message_id`.
- On create, store looks up `(conversation_id, client_message_id)`. If found, returns the existing Run instead of inserting again.

Purpose: safe retries (double-click, network retry) without creating duplicate Messages/Runs.

### What does `advanceConversation` do?

After inserting a new user Message + queued Run, it advances Conversation counters:

- `next_message_seq += 1`
- `next_run_seq += 1`
- updates `updated_at` / `latest_message_at`

It does **not** advance `next_executable_run_seq`. That counter advances only when a Run **completes** (`advanceExecutableRun`), so at most one Run executes per Conversation at a time and later follow-ups stay queued in order.

### Are Conversation create and the first Message one local transaction?

**Yes.** `POST /api/v1/conversations` requires non-empty `content` and calls `CreateConversationWithFirstMessage`, which creates the Conversation, user Message, and queued Run inside **one** `query.Transaction` (local MySQL transaction). Empty `content` is rejected with `400 invalid_content`.

Empty drafts stay **client-local** only (see new-conversation interaction design): clicking **New conversation** does not hit the API until the first real message is sent.

This repo uses gorm-gen's `queries.Transaction(func(tx *query.Query) ...)` rather than a separate `TxManager` + context-carried `*gorm.DB` (as in `database-tx-pattern`). The durability guarantee is the same: one `BEGIN`/`COMMIT` boundary owned by the store method.

Implications after the fix:

| Scenario | Effect |
|---|---|
| Crash during create | No orphan Conversation: Message/Run failure rolls back the Conversation insert |
| Empty `content` | `400 invalid_content` |
| Concurrent two messages with **different** `clientMessageID` | Safe: Conversation row is locked (`SELECT ... FOR UPDATE`) while allocating seq / inserting |
| Concurrent retries with **same** `clientMessageID` | Safe on follow-up path: idempotent return of the existing Run |

### Why is Message `status` initialized to `final`?

User Messages are written complete in one shot. There is no user-side streaming draft in persistence.

`status=final` means "this Message content is complete and safe to show as history." Assistant Messages created at `CompleteRun` are also `final`. Streaming UI text lives in `run_events` (`assistant.delta`) until the formal assistant Message is inserted.

---

## 6. Heartbeat, and subscribe-vs-replay ordering

### Whose heartbeat?

The **API writes an SSE comment to the browser** every 15 seconds:

```text
: heartbeat
```

It is **not** a Redis heartbeat and not Worker->API keepalive. Purpose: keep proxies / browsers from closing an idle SSE connection when no Run events arrive.

When the ticker fires, the handler also calls `replayEvents` again, so even without Redis the API periodically polls MySQL for new seqs.

### Actual Phase-2 order in code

```text
1) Authorize Run
2) Write SSE response headers
3) Subscribe Redis channel for runID     <-- first
4) Replay MySQL events where seq > after <-- second
5) Loop: Redis hint OR 15s heartbeat -> replay again
```

So: **subscribe first, then catch up from DB**, not the reverse.

### Why subscribe before replay?

Classic gap avoidance:

```text
If replay-first then subscribe:
  T1 replay sees events through seq=10
  T2 Worker commits seq=11 and PUBLISH (nobody listening yet)
  T3 subscribe starts
  => seq=11 hint missed until next heartbeat/poll

If subscribe-first then replay:
  T1 subscribe is live
  T2 Worker commits seq=11; hint may arrive during/after replay
  T3 replay loads everything through current max
  T4 later hints only advance cursor
  => at worst a duplicate wake-up; MySQL cursor makes delivery idempotent
```

Either way MySQL is source of truth; subscribe-first minimizes latency gaps. Heartbeat is the safety net if Redis is down (`events.Noop` still works via polling).

---

## 7. After Redis wakes the API, what does MySQL read? Replica lag? Redis-as-queue?

### What is read?

`ListRunEvents(userID, runID, after)` selects from **`run_events`**, not chat `messages`:

```sql
-- conceptual
SELECT ... FROM run_events
WHERE run_id = ? AND seq > ?
ORDER BY seq ASC
```

Those rows were just written by the Worker (`PersistBoundary` / `AppendRunEvents` / `CompleteRun`). Chat `messages` are separate; the final assistant Message appears at CompleteRun and is loaded on conversation refresh.

### Primary / replica lag

In the Compose MVP, API and Worker share **one MySQL primary**. There is no async replica in the path, so "write on primary, read on lagging replica" does not apply today.

If you later point event reads at a replica, you **can** observe: Worker committed + Redis published, API replica read still stale. Mitigations:

- read `run_events` from the primary (recommended for this path)
- or include enough data in a durable log (still keep MySQL as resume source)
- or retry/backoff on empty reads after a hint

### Could Redis be used as a queue / payload bus?

Possible optimizations, with trade-offs:

| Approach | Pros | Cons vs current design |
|---|---|---|
| Redis Pub/Sub carries full event payload | Lower read QPS on MySQL | Pub/Sub is fire-and-forget; resume/refresh still needs MySQL; multi-API fan-out must not lose durability story |
| Redis Streams as queue | Consumer groups, persistence options | Becomes another source of truth; contradicts "MySQL authoritative, Redis non-authoritative" |
| Keep Pub/Sub as hint only (current) | Simple resume, Redis loss safe | Extra MySQL read per wake-up |

Current design intentionally keeps Redis as a **doorbell**. For this MVP that is the right default. Optimizing with payload-in-Redis is optional and must not break `after` replay from MySQL.

---

## 8. Follow-up question execution flow (real data)

Conversation `01KXT43KJZNHNTH8SE5FRSWA8Y`:

1. Turn 1: list projects -> Run `...GKJ5ZY` succeeded (`queue_seq=1`)
2. Turn 2: `Explain paxos-demo in a single sentense.` -> Run `01KXT5M1RZTTPN1AGQ4Y1RVD2Y` (`queue_seq=2`)

### Step-by-step

1. **Frontend** (existing conversation selected):
   - `POST /api/v1/conversations/{id}/messages`
   - body includes new `clientMessageId` + content
   - then `GET` conversation detail and open SSE on the new `runId`

2. **API / Store** (`CreateUserMessageAndRun`):
   - lock Conversation
   - insert user Message `seq=3`, `status=final`, new `client_message_id`
   - insert Run `queue_seq=2`, `status=queued`
   - advance `next_message_seq` / `next_run_seq`
   - **do not** change `next_executable_run_seq` yet (it became `2` when Run 1 completed; Run 2 is now the executable one)

3. **Worker claim**:
   - eligible because `runs.queue_seq = conversations.next_executable_run_seq` (`2=2`)
   - lease Run 2, `execution_token=1`

4. **Runtime input (important MVP limitation)**:
   - `LoadRunExecution` loads **only the trigger user Message content**
   - `restoreState` starts from `[system, user(follow-up)]` unless a Checkpoint exists
   - prior turn chat history is **not** currently injected into the model context
   - this follow-up still worked because the question named `paxos-demo` explicitly

5. **Observed tool loop for Run 2** (from `run_events` / `tool_calls`):
   - `workspace.list_repositories`
   - `code.search` `{repo: paxos-demo, query: paxos}`
   - `file.read` `{repo: paxos-demo, path: README.md, startLine: 1, endLine: 50}`
   - stream assistant one-sentence answer (5 deltas)
   - `assistant.completed` + `run.completed`

6. **CompleteRun**:
   - assistant Message `seq=4`
   - Run 2 `succeeded`
   - `next_executable_run_seq` -> `3`

Ordering guarantee: if the user had sent another follow-up while Run 2 was running, that Run would be `queue_seq=3` and stay queued until Run 2 finished.

---

## 9. Is Workspace MCP a local MCP server? How do `code.search` / `file.read` work?

### What it is

Yes: **`mcp-server` is a first-party, local/sidecar MCP server** (`workspace-mcp`) in this repository.

- Process: `mcp-server/cmd/workspace-mcp`
- Transport: MCP Streamable HTTP at `/mcp` (port `8081`)
- Compose mounts `${REPOS_DIR}` read-only at `/workspace/repos`
- Only this service sees repository files; API/Worker do not mount the repos

Worker connects as an MCP client (`WORKSPACE_MCP_URL`, typically `http://workspace-mcp:8081/mcp` in Compose).

### How tools are implemented

They are **not** shelling out to ripgrep/cat as external system CLIs. They are Go library code over the filesystem:

| Tool | Implementation |
|---|---|
| `workspace.list_repositories` | `os.ReadDir` on workspace root; return directory aliases only |
| `code.search` | `filepath.WalkDir` + literal substring match in file contents; skips `.git`, `node_modules`, etc.; bounded `maxResults` |
| `file.read` | `os.Stat` / bounded read / return line range; caps on bytes and line count |

Path safety:

- resolve repo alias under workspace root
- `EvalSymlinks` + `ensureWithinRoot` to block path escape
- reject symlinks as repository roots in listing
- no write APIs registered

Allowlist is also enforced on the Worker/runtime side from Agent YAML (`allowed_tools`).

---

## Quick reference: responsibilities

```text
Frontend     Conversation snapshot + SSE consumer + draft assembly
API          Auth, CRUD, SSE replay from MySQL, Redis subscribe (hint)
Worker       Lease Run, Eino ReAct, MCP tools, persist boundaries, Redis publish
MySQL        Authoritative Conversations / Messages / Runs / Events / Checkpoints
Redis        Pub/Sub doorbell only (channel per runID)
Workspace MCP  Read-only FS tools behind MCP HTTP
```

## 10. Is `assistant.delta` from Eino or this platform?

**This platform.** Eino exposes model streaming as `schema.StreamReader[*schema.Message]` chunks. The platform maps those chunks into its own durable event types:

- runtime: `AssistantStreamEvent{Phase: "started"|"delta", ...}`
- persisted/SSE: `assistant.started` / `assistant.delta` / `assistant.completed`

Eino does not define the string `assistant.delta`.

## 11. If Run1 never finishes, what happens to Run2/Run3 SSE?

`next_executable_run_seq` advances only when a Run reaches a terminal state (`CompleteRun`). A stuck Run1 blocks Run2/Run3 from being claimed, even though they already exist as `queued`.

Lease reclaim only retries **the same** executable `queue_seq` after `lease_expires_at` (30s without renew). It does **not** skip to Run2.

SSE for Run2/Run3:

- Frontend may open EventSource while status is `queued`.
- API sends `: heartbeat` every 15s; there is **no application-level idle timeout** that closes waiting SSE.
- Practical disconnects come from browser, reverse proxy, or load balancer idle limits — not from Redis Pub/Sub TTL.
- Heartbeat is SSE keep-alive (API -> browser), not a protocol deadline for the Run.

Other safety nets:

- Agent `run_timeout_seconds` (600) can fail a Run that is still inside the runtime context.
- If a bug renews the lease forever while never completing, the Conversation queue stays blocked until cancel/ops intervention.

## 12. Who aggregates deltas? One flush = one `run_events.seq`?

Yes. The **Worker** `streamBuffer` aggregates raw stream text and flushes by size (256 bytes) or time (100ms). Each flush calls `AppendRunEvents`, which assigns **one** monotonic `seq` and inserts **one** `run_events` row of type `assistant.delta`. Redis then publishes that seq as a hint.

## 13. What tables does one LLM/tool boundary write? Local transaction? What is in `run_events`?

### Already transactional

`PersistBoundary` and `AppendRunEvents` each run inside a local MySQL transaction (`query.Transaction`) and lock the owned Run (`execution_token`).

Typical **tool/model boundary** (`PersistBoundary`) may touch:

| Table | When |
|---|---|
| `run_steps` | always for that boundary step |
| `tool_calls` | tool start/complete |
| `run_checkpoints` | when runtime emits a Checkpoint |
| `run_events` | progress / tool.started / tool.completed / assistant.completed, etc. |
| `runs` | `latest_checkpoint_id`, `next_event_seq` |

Typical **stream flush** (`AppendRunEvents`):

| Table | When |
|---|---|
| `run_events` | `assistant.delta` (and similar append-only events) |
| `runs` | `next_event_seq` |

### What `run_events` carries

| Event | Content |
|---|---|
| `progress.updated` | safe summary |
| `tool.started` / `tool.completed` | tool name, sanitized arguments, result **summary** (not necessarily full tool payload) |
| `assistant.*` | visible answer fragments / full final text |
| `run.completed` / failed / cancelled | terminal summary |

Tool **full results** also live in Checkpoints (for resume) and ToolCall `result_summary` (for Trace). Frontend Trace uses `/runs/{id}/trace`; live tool progress uses the same SSE channel via `tool.*` events.

**Chain-of-thought / private reasoning is not sent on `run_events`.** Runtime clears `ReasoningContent` in `sanitizeAssistantMessage` before persistence/UI emission. Design explicitly avoids exposing raw CoT as product data.

## 14. MCP sidecar vs Eino local `InvokableTool` (analysis only)

Eino supports both:

- local tools via `components/tool` (`InferTool` / `NewTool` / `InvokableTool`)
- MCP tools via MCP client integration

This MVP uses MCP so Worker has no repo mount and the security boundary stays in `workspace-mcp`.

Performance (same FS logic assumed):

| Factor | MCP sidecar | In-process Eino tool |
|---|---|---|
| Extra hop | HTTP + MCP framing Worker -> MCP | none |
| Serialization | JSON tool args/results over HTTP | in-memory |
| Isolation | separate process, RO mount only on MCP | Worker would need FS access |
| Typical cost vs LLM | usually small (ms–tens of ms) vs model seconds | slightly lower tool latency |

For this product, LLM time dominates; MCP overhead is rarely the bottleneck. The architectural win (blast-radius / least privilege) is the main reason to keep MCP.

## Related code

- SSE: `backend/internal/httpapi/sse.go`
- Redis notifier: `backend/internal/events/notifier.go`
- Message/Run create: `backend/internal/store/conversations.go`, `backend/internal/store/api.go`, `backend/internal/httpapi/conversations.go`
- Stream deltas: `backend/internal/worker/stream_buffer.go`
- CompleteRun / PersistBoundary: `backend/internal/store/execution.go`
- Logging / trace / async: `docs/tech/logging.md`
- Frontend subscribe: `frontend/src/api/events.ts`, `frontend/src/state/conversation.ts`
- Workspace MCP: `mcp-server/internal/workspace/`
