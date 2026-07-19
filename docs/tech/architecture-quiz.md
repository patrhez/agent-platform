# Architecture Understanding Quiz

Self-assessment questions covering this repository's MVP architecture principles, design trade-offs, field semantics, critical logic, and API design.

Suggested usage: answer closed-book first, then compare against the "Reference Answers" at the end. Answers are based on the current code and `docs/tech/` and `docs/superpowers/specs/`; if implementation and documentation conflict, the code wins.

Difficulty tags: `[Basic]` / `[Advanced]` / `[Trap]`.

---

## A. Overall Architecture and Responsibility Boundaries

**A1. `[Basic]`** What is the service shape of this project? Why choose "Modular monolith + API/Worker dual-role deployment" instead of splitting into multiple independent microservices from the start?

**A2. `[Basic]`** Draw (or list in text) the main components on the request path, and mark where each component's "authoritative state" lives: Web / API / Worker / MySQL / Redis / Workspace MCP.

**A3. `[Advanced]`** What does the API role explicitly **not** do? Give at least three examples. What systemic problems arise if any of these is violated?

**A4. `[Advanced]`** Why can a Worker "claim any Run on any healthy instance"? What mechanisms prevent workers from stepping on each other?

**A5. `[Basic]`** The MVP explicitly does not use Kafka / Temporal. What replaces the "reliable task queue" and the "workflow engine"? What responsibilities does each cover?

**A6. `[Trap]`** After Redis goes down or is wiped, if the user refreshes the page, can an in-progress Run still recover its progress? What is the basis for your answer?

---

## B. Domain Model and Field Semantics

**B1. `[Basic]`** In one sentence each, distinguish: `Conversation`, `Message`, `Run`, `RunStep`, `Checkpoint`, `RunEvent`, `ToolCall`, `Artifact`.

**B2. `[Basic]`** For a normal user follow-up, is it "continue the same Run" or "create a new Run"? Under what conditions would it be the same Run?

**B3. `[Advanced]`** Explain what each of the following `conversations` fields controls, and who increments it when:

| Field | Your explanation |
|---|---|
| `next_message_seq` | |
| `next_run_seq` | |
| `next_executable_run_seq` | |

**B4. `[Advanced]`** What is the relationship between `runs.queue_seq` and `conversations.next_executable_run_seq`? Write the key Worker claim predicate (conceptual SQL / pseudocode is fine).

**B5. `[Advanced]`** When does `execution_token` increment? How does the Worker use it when writing Step / Checkpoint / RunEvent / Complete? What happens if a late, stale Worker ignores this field?

**B6. `[Basic]`** What are the renewal period and expiration semantics of `lease_owner` / `lease_expires_at`? What happens after a lease expires? Will it skip a stuck Run and execute the next one?

**B7. `[Basic]`** What does Message `status=final` mean? Why is a user Message `final` at creation time? In which table / event type does streaming assistant text live, and when does it become a formal Message?

**B8. `[Advanced]`** What is the purpose of `client_message_id`? What is the granularity of the uniqueness constraint? When does the frontend generate it? What does the Store do on retry?

**B9. `[Basic]`** What design constraint does the name `RunEvent.SafePayload` imply? What content is **not allowed** in `run_events`?

**B10. `[Advanced]`** Who are the consumers served by `ToolCall.result_summary`, `Artifact`, and Checkpoint payload respectively? Where is the full tool result more likely to land?

**B11. `[Trap]`** Both `messages` and `run_events` can carry "what the assistant said." After a refresh, which should the frontend trust first? Why can't it rely on draft / delta alone?

---

## C. Execution, Checkpoint, and Runtime

**C1. `[Basic]`** What is the MVP Agent control-flow loop? Which Tools are enabled? Does the Worker process directly mount the repository filesystem?

**C2. `[Advanced]`** What is the principle for when Checkpoints are written? Why are Checkpoints **not** written for every token / `assistant.delta`?

**C3. `[Advanced]`** How do `PersistBoundary` and `AppendRunEvents` differ in transactions and the set of tables they touch? Which tables does each typically write to?

**C4. `[Trap]`** For the current MVP follow-up Run, which messages are loaded into model context by default? Is full multi-turn history injected automatically? What product behavior does this imply?

**C5. `[Basic]`** At which step in the runtime is `ReasoningContent` / Chain-of-Thought cleared? Can it appear in RuntimeEvent, Checkpoint, RunEvent, logs, or API responses?

**C6. `[Advanced]`** What is the security boundary between the Workspace MCP Sidecar and the Worker? What is the MCP transport? Why is Tool business logic not placed inside the Worker?

---

## D. Streaming: SSE / Redis / MySQL

**D1. `[Basic]`** Is SSE subscription scoped to Conversation or Run? What is the Redis channel naming pattern?

**D2. `[Basic]`** Describe the full path of one `assistant.delta` from an LLM token to the browser. Does Redis carry the full text or a hint?

**D3. `[Advanced]`** What is the `streamBuffer` flush strategy? What does "43 deltas" mean—is it equal to 43 raw tokens?

**D4. `[Advanced]`** In the API SSE handler, is Phase-2 order "subscribe first, then replay" or the opposite? Use a timeline to explain why this avoids losing events.

**D5. `[Basic]`** Where does the SSE cursor come from? What is the priority of `Last-Event-ID` vs `?after=`? Which database column does the SSE `id` correspond to?

**D6. `[Advanced]`** Who writes the Heartbeat (`: heartbeat`) to whom? Does it relate to Redis / Worker? Besides keep-alive, what else does it do?

**D7. `[Trap]`** If `ListRunEvents` were pointed at an async read replica in the future, what symptoms might appear? Why is the current Compose MVP fine for now? What mitigation is recommended?

**D8. `[Advanced]`** What are the terminal events? After `CompleteRun` / receiving a terminal event, what does Worker, API, and the frontend each clean up? Who is responsible for closing the browser EventSource?

**D9. `[Basic]`** Why is the design "MySQL authoritative + Redis doorbell" instead of having Redis Pub/Sub carry the full event payload directly?

---

## E. HTTP / API Design

**E1. `[Basic]`** List the main REST endpoints (method + path), and say whether each returns a "snapshot" or a "stream."

**E2. `[Advanced]`** Why put live progress on `GET /runs/{id}/events`, Steps/ToolCalls on `GET /runs/{id}/trace`, and full large results on `GET /runs/{id}/artifacts/{artifactId}`? What is the benefit of this split?

**E3. `[Advanced]`** When creating a conversation via `POST /conversations`, why is non-empty `content` required? Where does an empty draft live? Are Conversation + first Message + Run in the same local transaction? On crash, can an orphan Conversation be left behind?

**E4. `[Basic]`** After a successful `POST .../messages`, what does the frontend usually still need to do to see streaming output?

**E5. `[Advanced]`** On which Run states does the Cancel API act? How is Cancel semantically distinct from "lease expired and claimed by another Worker"?

**E6. `[Trap]`** If SSE were mounted at Conversation level (`/conversations/{id}/events`), what extra problems would that create compared to the current design?

---

## F. Identity, Idempotency, and Concurrency

**F1. `[Basic]`** Why use ULID (`CHAR(26)`) as primary keys instead of UUIDv4? Compared to UUIDv7, what trade-off does this project make?

**F2. `[Advanced]`** How do two concurrent POSTs on the same Conversation (different `clientMessageId`) guarantee seq does not conflict? What about retry with the same `clientMessageId`?

**F3. `[Advanced]`** If Run1 is stuck in `running` and the lease keeps getting renewed, and the user has already sent Run2/Run3, what happens to them? What if the Run1 Worker crashes and stops renewing?

**F4. `[Trap]`** In what scenarios do `attempt` increment and `execution_token` increment respectively? Can the two substitute for each other?

---

## G. Frontend Projection and UX Contract

**G1. `[Basic]`** How does the frontend project `assistant.started` / `delta` / `completed` into a chat draft? When does it discard the draft and trust the formal Message?

**G2. `[Advanced]`** Which data sources do the Trace timeline and chat area consume respectively? After refresh, how is an in-progress tool-call view restored?

**G3. `[Basic]`** What content is the UI explicitly forbidden to display? (List at least four categories.)

**G4. `[Trap]`** The current frontend often opens SSE with `after=0` and deduplicates in memory. What is a more reasonable cursor strategy for a production client? What still guarantees authority?

---

## H. Observability and Safe Logging

**H1. `[Basic]`** Why is Logger implemented as an injectable `logging.Logger` (ctx-first) instead of global `zap.L()`? How does `trace_id` enter log fields?

**H2. `[Advanced]`** Access logs record request/response bodies, but which response bodies are skipped? Why?

**H3. `[Basic]`** What is the conventional pattern for background goroutines? What does `async.Recover` solve, and what does it not solve?

**H4. `[Trap]`** Which fields/content should not be written to logs even in debug? (Answer in light of this project's security boundaries.)

---

## I. Design Trade-offs and "Why"

Answer in 2–5 sentences; encourage stating "what breaks if we do not do it this way."

**I1. `[Advanced]`** Why "at most one executable Run per Conversation at a time" instead of running multiple Agents in parallel?

**I2. `[Advanced]`** Why does the API not execute Eino, and why does the Worker not open SSE directly to the browser?

**I3. `[Advanced]`** Why does delta persistence go through `AppendRunEvents`, while tool/model boundaries go through `PersistBoundary`?

**I4. `[Advanced]`** Why are Artifact and the event stream separated? What would happen if full `file.read` content were stuffed into every `tool.completed` SafePayload?

**I5. `[Trap]`** What does "Eino is the first runtime adapter, not the owner of the business model" mean for the data model and package boundaries? Give one example of "wrong coupling."

**I6. `[Advanced]`** Is MVP read-only safety enforced by prompt constraints, or by mount / MCP scope / allowlist? Why?

---

## J. Scenario Walkthroughs (Integrated)

**J1. `[Advanced]`** The user sends a message and immediately double-clicks retry (same `clientMessageId`). Step by step from Store to Worker to frontend, explain the path that prevents a duplicate Run.

**J2. `[Advanced]`** The Worker is killed after flushing an `assistant.delta` but before Redis PUBLISH. The user's SSE is still connected. What happens next? Can the user permanently lose text?

**J3. `[Advanced]`** API instance A is SSE-pushing a Run; that API process restarts. After the browser reconnects, how does it avoid loss and reordering? Does Redis need to retain old messages?

**J4. `[Advanced]`** Mid-run, user confirmation is needed (future `waiting`). How is that different in domain semantics from "the user sent another new question"? What impact on `next_executable_run_seq` / whether a new Run is created?

**J5. `[Trap]`** Someone proposes: use Redis Streams as the Run queue and store only final results in MySQL. Refute or adapt that proposal using this project's authoritative-state principles.

---

## Scoring Guide (Self-Use)

| Tier | Guidance |
|---|---|
| Can answer most A/B/D/E basic questions correctly | Can follow code and streams; no global picture yet |
| Can consistently answer C/F/I advanced questions | Understands execution fencing and persistence boundaries |
| Can explain J scenarios and spot trap-question pitfalls | Ready to review design before changing core paths |

Related reading: `execution-and-streaming-faq.md`, `logging.md`, `docs/superpowers/specs/2026-07-17-agent-platform-mvp-design.md`, `2026-07-17-agent-platform-mvp-detailed-design.md`, `2026-07-18-streaming-markdown-trace-design.md`.

---

## Reference Answers

<details>
<summary>Expand to view reference answers (try on your own first)</summary>

### A. Overall Architecture

**A1.** Modular monolith: same domain/store, split by deployment role into `agent-api` / `agent-worker`. Benefits: shared consistency model, avoids premature distributed transactions; isolates SSE/read QPS from long-running Agent execution for independent scaling.

**A2.** Web = transient UI → API = no in-memory session authority → MySQL = authority; Worker = no cross-boundary in-memory authority, relies on Checkpoint/Run; Redis = non-authoritative fan-out; Workspace = external read-only resources, references persisted in DB.

**A3.** API does not run Eino, does not hold Conversation execution state in memory, does not own Worker session. Violating these couples API to execution, makes horizontal scaling hard, and loses execution state on instance failure.

**A4.** Runs are not bound to instances; claim uses row lock + lease; all writes carry `execution_token` fencing; recovery reads the latest Checkpoint.

**A5.** MySQL `runs` state machine + lease replaces the queue; Checkpoint + Step boundaries replace Temporal-style workflow persistence.

**A6.** Yes. Progress lives in MySQL `run_events` / Checkpoint / Run; Redis loss only affects real-time wake-up—reconnect replay and heartbeat polling are enough.

### B. Domain and Fields

**B1.** Conversation = multi-turn container; Message = visible chat turn; Run = one background execution; Step = explainable boundary; Checkpoint = recoverable snapshot; RunEvent = ordered visible event; ToolCall = safe tool summary; Artifact = large immutable full-text result.

**B2.** Normal follow-up → new Run. Agent proactively pauses for user input/approval → same Run enters `waiting` then resume (MVP follows design; product path may not be fully open yet).

**B3.** `next_message_seq` / `next_run_seq`: allocated and incremented when creating Message/Run. `next_executable_run_seq`: advanced only when a Run **reaches a terminal state**, ensuring serial executability.

**B4.** Claim condition conceptually: `status` claimable and `runs.queue_seq = conversations.next_executable_run_seq` (plus due/lease, etc.).

**B5.** Increments on ownership/claim change; write paths use `WHERE execution_token = ?`; stale Worker writes fail, preventing split-brain overwrite.

**B6.** ~30s lease, renewed about every 10s; after expiry another Worker can claim the **same** `queue_seq`; it will not skip a stuck executable sequence to run later Runs.

**B7.** `final` = content complete and eligible for history. User messages are written in one shot so they are final immediately. Streaming lives in `run_events` (`assistant.delta`); `CompleteRun` inserts the formal assistant Message.

**B8.** Client idempotency key; granularity `(conversation_id, client_message_id)`; frontend generates before POST; if it already exists, return the existing Run without duplicate insert.

**B9.** Carries only user-safe visible data; forbids CoT/ReasoningContent, secrets, absolute paths, raw provider body, unsanitized full sensitive tool output, etc.

**B10.** `result_summary` → Trace/UI; Artifact → on-demand full download; Checkpoint → Worker recovery. Full results lean toward Checkpoint/Artifact, not full SSE payload.

**B11.** After refresh, trust formal Messages in the Conversation snapshot; delta is an execution-time projection; terminal authority is the Message written by `CompleteRun`.

### C. Execution and Runtime

**C1.** Eino ReAct tool-calling; MVP Tools: `code.search`, `file.read`; Worker does not directly mount the repo, only via Workspace MCP.

**C2.** Write Checkpoints at boundaries with side effects / recoverable semantics, not on every token, to avoid high-frequency write amplification and meaningless snapshots.

**C3.** Both run in local transactions and validate ownership. `PersistBoundary` can write steps/tool_calls/checkpoints/events/runs; `AppendRunEvents` mainly appends events + advances `next_event_seq`.

**C4.** Currently often loads only trigger user content (+ system), not full history automatically—follow-ups may "forget" unless the user carries context in the question or history assembly is added later.

**C5.** Cleared by `sanitizeAssistantMessage` on the stream/concat path; by design must not enter RuntimeEvent/Checkpoint/RunEvent/log/API.

**C6.** Repo mounted only on sidecar; Streamable HTTP (loopback); Worker is MCP Client only; security and FS policy centralized in sidecar.

### D. Streaming

**D1.** Per Run; `agent-platform:run-events:{runID}`.

**D2.** LLM → streamBuffer → MySQL `run_events` → Redis PUBLISH `{runId,seq}` → API SELECT again → SSE → frontend reducer. Redis does not carry body text.

**D3.** Flush at ≥256 bytes or ≥100ms; 43 deltas = 43 flush rows, not 43 tokens.

**D4.** Subscribe first, then replay, to avoid a PUBLISH gap between replay and subscribe; worst case duplicate wake-up, idempotent via seq cursor.

**D5.** Prefer `Last-Event-ID`, else `after`, else 0; SSE `id` = `run_events.seq`.

**D6.** API → browser, 15s; unrelated to Redis/Worker; also triggers MySQL replay.

**D7.** Hint may arrive before replica catches up, causing brief empty reads; Compose single primary avoids this for now; mitigation: read primary on that path / retry.

**D8.** `run.completed` / `failed` / `cancelled`. Worker persists terminal state and publishes; API ends handler after writing terminal and unsubscribes Redis; frontend `EventSource.close()` and reloads conversation.

**D9.** Pub/Sub does not reliably retain; resume still needs MySQL; single authority means Redis loss does not lose business data.

### E. API

**E1.** Conversation CRUD/send message, Run snapshot, trace, artifacts, cancel = request/response snapshots; `GET .../events` = SSE stream. See `httpapi/server.go`.

**E2.** Split: high-frequency small events / structured summaries / large on-demand payloads; controls bandwidth, authorization, and default disclosure surface.

**E3.** Avoid polluting with empty Conversations; empty draft stays client-side only; `CreateConversationWithFirstMessage` is one transaction, rollback on failure leaves no orphans.

**E4.** Fetch conversation snapshot and `EventSource`-subscribe to the new `runId`'s events.

**E5.** Designed to cancel queued/waiting/running; distinct from lease reclaim: cancel is user-intent terminal state; lease expiry is ownership transfer and may continue the same Run.

**E6.** Mixed multi-Run event streams, harder auth and cursors, Redis channels misaligned with "single active Run" model.

### F. Identity and Concurrency

**F1.** Time-sortable, shorter, URL-friendly; UUIDv7 is more "standard UUID shape"; this project chooses ULID string primary keys.

**F2.** Lock Conversation row then allocate seq; same clientMessageId is idempotent return.

**F3.** Run2/3 stay queued until executable pointer advances; crash with no renewal lets another Worker reclaim the same queue_seq.

**F4.** `attempt` = retry/claim count; `execution_token` = write fencing. Not interchangeable.

### G. Frontend

**G1.** Reducer concatenates draft by streamId/attempt; discard draft once conversation snapshot shows that run's final assistant Message.

**G2.** Chat consumes Message + draft; timeline consumes trace + live `tool.*`/`progress.*`; after refresh, `getTrace` first then SSE.

**G3.** Reasoning/CoT, API key, raw provider request body, absolute Workspace paths, etc. (see streaming-markdown-trace design).

**G4.** Carry last applied seq; dedup still needed; authority is always MySQL replay.

### H. Observability

**H1.** DI for testing and multi-instance config; middleware puts `X-Trace-Id` in context, Logger reads it into zap fields.

**H2.** SSE / octet-stream / common binary skipped to avoid buffering large streams or polluting logs.

**H3.** `go func(){ defer async.Recover(ctx, logger); ...}()`; prevents panic from killing process; does not replace business error handling or timeout cancellation.

**H4.** Secrets, full tool raw output, large source snippets, CoT, unsanitized paths, etc.

### I. Trade-offs

**I1.** Ensures serial consistency of multi-turn context and tool explanations; parallel Runs in one Conversation would contradict each other.

**I2.** Isolates long tasks from request surface; SSE on stateless API eases multi-instance and reconnect; Worker focuses on execution and persistence.

**I3.** High-frequency deltas only append events; boundaries write step/tool/checkpoint, controlling write amplification.

**I4.** Large results on demand, with auth and rate limits; stuffing events would blow up SSE/DB rows and over-disclose by default.

**I5.** Conversation/Run/auth/persistence belong to platform domain, must not leak Eino types; wrong example: using Eino message structs directly as MySQL schema or public API.

**I6.** Enforced by read-only mount, MCP scope, and tool allowlist; prompt alone is not a security boundary.

### J. Scenarios

**J1.** Store hit on `(conversation_id, client_message_id)` returns original Run; Worker never sees a second queued twin; frontend keeps subscribing to the same runId.

**J2.** Committed delta is in MySQL; missing PUBLISH delays at most until heartbeat/reconnect replay; committed text is not permanently lost for lack of PUBLISH.

**J3.** Reconnect with cursor continues from MySQL `seq > after`; Redis does not need to retain historical messages.

**J4.** `waiting` = resume same Run; new user message = new Run queued. Latter does not advance executable until current executable Run is terminal.

**J5.** Violates "MySQL authoritative, Redis non-authoritative"; Redis loss loses queue/events. If using Streams, only as acceleration layer—resume and audit still anchored on MySQL Run/Event/Checkpoint.

</details>
