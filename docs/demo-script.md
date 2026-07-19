# Agent Platform MVP Demo Script

1. Start Docker Desktop, copy `.env.example` to `.env`, and fill in local Workspace and LLM settings.
2. Run `./scripts/up.sh`. Only MySQL and Redis run in Docker; application roles run on the host macOS.
3. Open `http://127.0.0.1:5173` and click **+ New conversation**. Note that no database row is created yet.
4. Enter `List the projects in the current workspace.` Show that the first send creates a Conversation and a queued Run, and that the Agent returns repository aliases via `workspace.list_repositories`.
5. Before the Run finishes, point out that Markdown text keeps appearing in the answer area. Explain that these fragments are written to MySQL `run_events` first, then delivered to the browser over SSE.
6. Expand the execution trace. Show safe parameters, status, and duration for `code.search` / `file.read`, then expand a collapsed result summary.
7. Explain that model `ReasoningContent` and Chain-of-Thought are not stored, not transmitted, and not displayed.
8. Refresh the browser. Conversation history, the execution trace, and any active draft recover through the HTTP snapshot and SSE replay.
9. Run `tests/e2e/durable-streaming.sh` and verify deltas appear before the Run terminal state.
10. Run `tests/e2e/recovery.sh` and show the same Run reaching a terminal state after a Worker restart.
11. Run `tests/e2e/stream-reconnect.sh` and show recovery from the next event after `Last-Event-ID` when the API restarts.
12. Inspect `.local/logs/api.log`, `worker.log`, and `mcp.log`, and note that logs omit API keys, source-code bodies, and model reasoning.

During the demo, emphasize: MySQL stores Conversations, Runs, Checkpoints, and events; Redis is only an event hint channel. Workspace MCP reads `REPOS_DIR` directly in read-only mode; the API and frontend cannot access that directory.
