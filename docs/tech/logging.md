# Logging, Trace ID, and Safe Goroutines

## Stack

| Piece | Choice |
|---|---|
| Logger API | Injectable `logging.Logger` (context-first methods) |
| Implementation | `go.uber.org/zap` JSON core |
| File rotation | `lestrrat-go/file-rotatelogs`, one file per calendar day |
| Struct fields in logs | `jsonutil.MustJSON` (`samber/mo` Result helper) |
| Goroutines | `defer async.Recover(ctx, logger)` inside each `go func()` |

Zap does not read `context.Context` for fields. Our `Logger` implementation attaches `trace_id` from context before calling zap.

## Configuration

| Env | Default | Meaning |
|---|---|---|
| `LOG_DIR` | `logs` | Directory for rotated files |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_MAX_AGE_DAYS` | `14` | Retention for rotated files |

Set these in the repo `.env` (see `.env.example`). `compose.full.yaml` passes them into API/Worker containers. For the HA lab, `./scripts/ha/up.sh` overlays `LOG_LEVEL` and `LOG_MAX_AGE_DAYS` onto [`deploy/ha/config.yaml`](../../deploy/ha/config.yaml) before apply/restart — set `LOG_LEVEL=debug` then rerun `up.sh` to enable message-path debug logs.

Files:

- `logs/agent-api.YYYYMMDD.log` / symlink `logs/agent-api.log`
- `logs/agent-worker.YYYYMMDD.log` / symlink `logs/agent-worker.log`

Stdout also receives the same JSON lines for `docker logs` / local terminals.

## Dependency injection

Construct once in `cmd/api` and `cmd/worker`, then pass `logging.Logger` into structs (`httpapi.Server`, `worker.Worker`, Redis notifier). Do not call package-level log helpers for business logs.

```go
logger.Info(ctx, "run claimed",
    zap.String("run_id", run.ID),
    zap.Int64("queue_seq", run.QueueSeq),
)
logger.Info(ctx, "tool args",
    zap.String("args_json", jsonutil.MustJSON(safeArgs)),
)
```

## Trace ID

API middleware `withTraceID`:

1. Prefer inbound `X-Trace-Id`.
2. Otherwise generate a ULID and store it on the request context.
3. Echo the id on the response header `X-Trace-Id`.

Worker `Execute` calls `EnsureTraceID` so each claim gets a worker-side `trace_id`. Join API and Worker logs with `conversation_id` / `run_id` (HTTP and Worker traces differ across the async claim boundary).

For the HA lab, collect a greppable api+worker slice for one conversation with
[`scripts/ha/collect-conversation-logs.sh`](../../scripts/ha/collect-conversation-logs.sh)
(see the [HA scripts handbook](../../scripts/ha/README.md)).

## HTTP access log

`accessLog` records one `http_request` line per request with:

- `method`, `path`, `status`
- `duration` / `duration_ms` (`time.Since`)
- `request_body` / `response_body` (UTF-8 text, truncated at 4 KiB)
- `trace_id` when present on the context

Skipped response bodies: `text/event-stream`, `application/octet-stream`, and common binary content types, so SSE and artifact downloads are not buffered into logs.

## Async / panic recovery

Every goroutine must recover panics with `defer async.Recover`:

```go
go func() {
    defer async.Recover(ctx, logger)
    // work
}()
```

Recovered panics are logged at error level with `panic` and `stack` fields and do not crash the process.

## Runtime setup logs (debug)

When `LOG_LEVEL=debug`, each Run also emits the MCP → Eino tool wiring scene:

| msg | Meaning |
|---|---|
| `mcp server connecting` | About to open a Streamable HTTP MCP session |
| `mcp tools listed` | `ListTools` result vs configured allowlist |
| `mcp server connected` | Allowlisted tools bound for that server |
| `mcp toolset ready` | Per-Run toolset assembled (`server_key`, `mcp_name`, `model_name`, schema flag) |
| `eino tools bound` | Model-visible tool names handed to ADK `ToolsNode` |
| `eino input` / `eino output` | Messages and boundaries after ADK starts |

## What to log / not log

Log: stable ids, statuses, durations, tool names, event seq, queue_seq, safe summaries, JSON of **safe** structs via `MustJSON`.

Do not log: API keys, raw LLM prompts, full tool file bodies, unsanitized PII, or provider error payloads that may contain secrets.
