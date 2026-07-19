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

Worker logs use the same `Logger`; they only include `trace_id` when a context carries one (for example future job metadata). Run lifecycle logs always include `run_id`.

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

## What to log / not log

Log: stable ids, statuses, durations, tool names, event seq, queue_seq, safe summaries, JSON of **safe** structs via `MustJSON`.

Do not log: API keys, raw LLM prompts, full tool file bodies, unsanitized PII, or provider error payloads that may contain secrets.
