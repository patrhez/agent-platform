# Streaming Responses, Markdown, and Safe Run Trace Design

## Goal

Make the Agent Platform visibly stream model output, render assistant Markdown, and show a live, safe execution timeline without exposing model reasoning or sensitive workspace data.

## Current Gap

The API already serves durable Run events through SSE and flushes every event, but the Agent runtime calls Eino `Generate`. It therefore waits for a complete model response and emits only an `assistant.completed` event. The frontend stores SSE events without projecting them into visible messages, then reloads the Conversation only after a terminal event. The resulting experience is a single final response rather than streaming output.

The existing Run trace endpoint persists model and Tool boundaries, but the frontend fetches it only when the Run ID changes. Tool progress is therefore neither live nor consistently refreshed.

## Architecture

The selected design uses durable, buffered Delta events:

```text
DeepSeek Chat Completions Stream
        -> Eino StreamReader
        -> Agent Runtime content filtering
        -> Worker Delta Buffer
        -> MySQL run_events
        -> Redis notification hint
        -> API SSE
        -> frontend Run projection
```

MySQL remains the source of truth. Redis carries only notification hints after committed events. The API remains stateless and can replay events from any instance.

## Runtime Streaming

The Eino runtime uses `ToolCallingChatModel.Stream` for every model turn and consumes the returned `StreamReader` until EOF. It concatenates chunks with Eino's supported message concatenation behavior so streamed Tool Calls remain valid.

Only `Message.Content` is eligible for user-visible streaming. `ReasoningContent` is discarded immediately and is never placed in a RuntimeEvent, Checkpoint, RunEvent, log, or API response.

The runtime emits these events for a text-producing model turn:

- `assistant.started`: identifies a new stream generation;
- `assistant.delta`: carries a bounded text fragment;
- `assistant.completed`: marks the model text stream complete and carries the finalized response boundary.

Tool-selection turns continue to emit model and Tool execution boundaries. Models normally return empty content when selecting Tools. If a provider sends content before a Tool Call, that content may be presented as a transient assistant update, but it never becomes the formal final Message unless the turn completes without Tool Calls.

## Delta Buffering and Persistence

The Worker buffers runtime content and flushes a Delta when any condition is met:

- 100 milliseconds have elapsed since the previous flush; or
- the buffer reaches 256 UTF-8 bytes; or
- the model stream ends.

Both thresholds are package constants and can be tuned without changing the public event contract.

Delta events use a separate Store method such as `AppendRunEvents`. It uses a GORM `Transaction` callback and only appends `run_events`; it does not create a RunStep, ToolCall, or Checkpoint. This prevents token streaming from turning into frequent Checkpoint writes.

Each assistant event contains:

```json
{
  "streamId": "run-id:attempt:step-number",
  "attempt": 1,
  "stepNo": 2,
  "offset": 0,
  "text": "next buffered fragment"
}
```

`offset` is the zero-based fragment ordinal within one stream, not a byte or string index. `assistant.started` resets the frontend draft for the same logical step. If a Worker loses its lease or exits during generation, a later Run attempt emits a new start event and replaces the incomplete prior draft rather than appending duplicate content.

If a Delta cannot be committed, the Worker stops the Run. It must never send text to the frontend before that text has been durably stored.

## Completion Semantics

The existing two completion levels remain distinct:

- `assistant.completed` means the model response stream finished and the complete response boundary was checkpointed;
- `run.completed` means the formal assistant Message, Run terminal state, and terminal Run event were committed atomically.

The formal assistant Message remains the authoritative final answer. When the frontend receives a terminal Run event, it reloads the Conversation and replaces any streaming draft with the formal Message.

## SSE and Recovery

SSE continues to use Run event sequence numbers as event IDs. On an automatic EventSource reconnect, `Last-Event-ID` takes precedence over an initial `after` query parameter so the API replays only events after the last browser-confirmed sequence.

On a full page refresh, the frontend restores the Conversation and Run trace from HTTP, then subscribes to active Runs. Replaying from sequence zero remains valid because frontend projection is idempotent by `(runId, eventSeq)`. A later optimization may persist the latest processed cursor in browser state, but it is not required for this MVP.

API restarts do not affect execution because all visible events are replayed from MySQL. Worker restarts resume from Checkpoints at durable model or Tool boundaries. Incomplete streamed drafts are superseded by a new attempt.

## Frontend Message Projection

The conversation reducer maintains, per Run:

- the last processed event sequence;
- the active stream ID and attempt;
- the accumulated assistant draft;
- live execution timeline items.

`assistant.delta` appends text only when its stream ID and attempt match the active stream. Duplicate event sequences and stale attempts are ignored. The Chat component renders the draft as a temporary assistant message with a generation indicator. The draft disappears after the authoritative Conversation snapshot includes the final assistant Message.

## Markdown Rendering

Assistant content uses `react-markdown` with `remark-gfm`. It supports headings, lists, tables, block quotes, links, inline code, and fenced code blocks.

Raw HTML is not enabled. During streaming, temporarily incomplete Markdown is allowed to render using the parser's best effort. The terminal Conversation snapshot causes a clean render of the complete final Markdown.

User-authored messages remain plain text for the MVP.

## Safe Execution Timeline

The latest Run displays a live timeline rather than model chain-of-thought. Timeline items include:

- queued, running, succeeded, failed, or cancelled Run state;
- safe model activity summaries such as `Analyzing the issue` or `Generating answer`;
- Tool start and completion state;
- Tool name, safe arguments, status, and duration;
- a collapsed Tool result summary.

Allowed Tool arguments are rendered by a Tool-specific whitelist:

- `workspace.list_repositories`: no arguments;
- `code.search`: repository alias, search term, relative path prefix, glob, and result limit;
- `file.read`: repository alias, relative file path, start line, and end line.

The UI never renders absolute Workspace paths, API keys, provider request bodies, `ReasoningContent`, or Chain-of-Thought. Tool results remain collapsed by default and show only the existing safe result summary.

Live SSE events update the timeline during execution. On refresh or initial Run selection, `GET /runs/{runId}/trace` restores persisted steps and Tool Calls before subsequent SSE events are projected.

## Error Handling

- SSE disconnect: EventSource reconnects and resumes using `Last-Event-ID`.
- API restart: durable events replay from MySQL.
- Worker restart: a new attempt resets an incomplete draft for the same step.
- Delta persistence failure: the Run fails; uncommitted text is never emitted.
- Tool failure: the timeline shows a safe failure state without underlying credentials, request bodies, or source contents.
- Markdown parse edge cases: incomplete streaming Markdown may render temporarily; the final snapshot replaces it.

## Testing

Backend tests cover:

- Eino stream consumption and message concatenation;
- suppression of `ReasoningContent`;
- Delta buffering thresholds and final flush;
- Delta persistence without Checkpoint or RunStep writes;
- GORM Transaction callback usage;
- new-attempt draft reset events;
- SSE `Last-Event-ID` precedence and replay;
- Tool event safe payloads and duration fields.

Frontend tests cover:

- ordered Delta concatenation and duplicate suppression;
- stale attempt rejection and new-attempt reset;
- draft replacement by a final snapshot;
- Markdown headings, lists, code blocks, tables, and raw HTML safety;
- live Tool timeline states, safe arguments, collapsed summaries, and failures.

End-to-end acceptance uses the configured DeepSeek Chat Completions API and verifies that:

1. answer text becomes visible before the Run reaches terminal state;
2. refreshing or restarting API resumes the same stream without duplicated text;
3. Tool Calls appear live and survive refresh;
4. the final Markdown answer replaces the draft;
5. Worker restart supersedes an incomplete draft and the Run still reaches a valid terminal state.

## Non-Goals

- exposing model Chain-of-Thought or raw reasoning tokens;
- streaming full Tool results or source files into the timeline;
- changing SSE to WebSocket or Streamable HTTP;
- using Redis as the authoritative stream store;
- syntax highlighting, copy buttons, or rich artifact previews in this iteration.
