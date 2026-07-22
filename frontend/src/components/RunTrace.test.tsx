import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Run, type RunTrace as RunTraceResponse } from "../api/client";
import type { RunEvent } from "../api/events";
import { RunTrace } from "./RunTrace";

vi.mock("../api/client", () => ({ api: { getTrace: vi.fn() } }));

const run: Run = {
  id: "run-1",
  conversationId: "conversation",
  triggerMessageId: "user-1",
  queueSeq: 1,
  status: "running",
  attempt: 1,
  createdAt: "2026-07-18T00:00:00.000Z",
};

const persisted: RunTraceResponse = {
  steps: [{
    id: "step", stepNo: 1, kind: "model", status: "completed",
    safeSummary: "Agent selected read-only repository Tools",
    createdAt: "2026-07-18T00:00:00Z",
  }],
  toolCalls: [{
    id: "tool-1", stepNo: 1, serverKey: "workspace", toolName: "code.search",
    arguments: { repo: "agent-platform", query: "stream", workspaceRoot: "/private/root" },
    resultSummary: "2 matches", status: "completed",
    createdAt: "2026-07-18T00:00:00Z", updatedAt: "2026-07-18T00:00:00.034Z",
  }],
};

function event(seq: number, type: string, payload: Record<string, unknown>): RunEvent {
  return { runID: "run-1", seq, type, payload };
}

describe("RunTrace", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTrace).mockResolvedValue(persisted);
  });

  it("stays collapsed by default and shows a live progress summary", async () => {
    vi.mocked(api.getTrace).mockResolvedValue({ steps: [], toolCalls: [] });
    const toolStarted = event(2, "tool.started", {
      toolCallId: "tool-2", tool: "file.read", status: "running",
      arguments: { repo: "agent-platform", path: "backend/main.go", startLine: 1, endLine: 20 },
      durationMs: 0,
    });
    render(<RunTrace run={run} events={[toolStarted]} />);

    expect(await screen.findByText("Running · 1 tool · file.read…")).toBeTruthy();
    expect(screen.getByText("Calling file.read…")).toBeTruthy();
    const details = document.querySelector(".run-trace");
    expect(details?.tagName).toBe("DETAILS");
    expect(details?.hasAttribute("open")).toBe(false);
  });

  it("keeps the latest tool visible after it finishes while waiting for the model", async () => {
    vi.mocked(api.getTrace).mockResolvedValue({ steps: [], toolCalls: [] });
    const toolStarted = event(1, "tool.started", {
      toolCallId: "tool-2", tool: "code.search", status: "running",
      arguments: { repo: "tiny-llm", query: "README" }, durationMs: 0,
    });
    const toolCompleted = event(2, "tool.completed", {
      toolCallId: "tool-2", tool: "code.search", status: "completed",
      arguments: { repo: "tiny-llm", query: "README" },
      resultSummary: "2 matches", durationMs: 40,
    });
    const { rerender } = render(<RunTrace run={run} events={[toolStarted]} />);
    expect(await screen.findByText("Running · 1 tool · code.search…")).toBeTruthy();

    rerender(<RunTrace run={run} events={[toolStarted, toolCompleted]} />);
    expect(await screen.findByText("Running · 1 tool · last code.search")).toBeTruthy();
    expect(screen.getByText("Last tool: code.search · waiting for model")).toBeTruthy();
  });

  it("updates the summary from live events even when the snapshot Run is still queued", async () => {
    vi.mocked(api.getTrace).mockResolvedValue({ steps: [], toolCalls: [] });
    const queued: Run = { ...run, status: "queued" };
    const progress = event(1, "progress.updated", { summary: "Agent selected read-only repository Tools" });
    const toolStarted = event(2, "tool.started", {
      toolCallId: "tool-2", tool: "code.search", status: "running",
      arguments: { repo: "tiny-llm", query: "README" },
      durationMs: 0,
    });
    const { rerender } = render(<RunTrace run={queued} events={[]} />);
    expect(await screen.findByText("Queued")).toBeTruthy();

    rerender(<RunTrace run={queued} events={[progress]} />);
    expect(await screen.findByText("Running")).toBeTruthy();
    expect(screen.getByText("Waiting for model…")).toBeTruthy();

    rerender(<RunTrace run={queued} events={[progress, toolStarted]} />);
    expect(await screen.findByText("Running · 1 tool · code.search…")).toBeTruthy();
  });

  it("summarizes completed runs with tool count and duration", async () => {
    const completedRun: Run = {
      ...run,
      status: "completed",
      finishedAt: "2026-07-18T00:00:01.200Z",
    };
    render(<RunTrace run={completedRun} events={[]} />);
    expect(await screen.findByText("Used 1 tools · 1.2s")).toBeTruthy();
    expect(document.querySelector(".run-trace")?.hasAttribute("open")).toBe(false);
    expect(screen.queryByText(/Calling |Last tool:/)).toBeNull();
  });

  it("merges persisted trace with live safe Tool progress", async () => {
    const started = event(1, "assistant.started", {
      streamId: "run-1:1:1", attempt: 1, stepNo: 1, reasoning: "private thought",
    });
    const toolStarted = event(2, "tool.started", {
      toolCallId: "tool-2", tool: "file.read", status: "running",
      arguments: { repo: "agent-platform", path: "backend/main.go", startLine: 1, endLine: 20, apiKey: "secret" },
      durationMs: 0,
    });
    const { rerender } = render(<RunTrace run={run} events={[started, toolStarted]} />);

    await screen.findByText(/code\.search/);
    expect(screen.getAllByText(/agent-platform/)).toHaveLength(2);
    expect(screen.getByText(/backend\/main\.go/)).toBeTruthy();
    expect(screen.queryByText(/private thought|private\/root|secret/)).toBeNull();
    const persistedResult = screen.getByText("2 matches").closest("details");
    expect(persistedResult?.hasAttribute("open")).toBe(false);

    const completed = event(3, "tool.completed", {
      toolCallId: "tool-2", tool: "file.read", status: "completed",
      arguments: { repo: "agent-platform", path: "backend/main.go", startLine: 1, endLine: 20 },
      resultSummary: "read 20 lines", durationMs: 12,
    });
    rerender(<RunTrace run={run} events={[started, toolStarted, completed]} />);
    await waitFor(() => expect(screen.getByText(/12ms/)).toBeTruthy());
    expect(screen.getByText("read 20 lines")).toBeTruthy();
  });

  it("renders scalar arguments for unknown tools while hiding credential-like keys", async () => {
    vi.mocked(api.getTrace).mockResolvedValue({ steps: [], toolCalls: [] });
    const toolStarted = event(1, "tool.started", {
      toolCallId: "tool-3", tool: "docs.lookup", status: "running",
      arguments: { topic: "deployment", limit: 5, authToken: "secret", nested: { skip: true } },
      durationMs: 0,
    });
    render(<RunTrace run={run} events={[toolStarted]} />);

    expect(await screen.findByText("deployment")).toBeTruthy();
    expect(screen.getByText("5")).toBeTruthy();
    expect(screen.queryByText(/secret/)).toBeNull();
    expect(screen.queryByText(/skip/)).toBeNull();
  });

  it("shows a compact failure summary without the detailed status message", async () => {
    const failedRun: Run = {
      ...run,
      status: "failed",
      errorCode: "llm_overload",
      errorMessage: "The language model is currently overloaded. Please retry in a moment.",
    };
    render(<RunTrace run={failedRun} events={[event(4, "tool.completed", {
      toolCallId: "tool-failed", tool: "code.search", status: "failed",
      arguments: { repo: "agent-platform", query: "missing" }, resultSummary: "Tool execution failed", durationMs: 5,
    })]} />);
    expect(await screen.findByText("Failed (llm_overload)")).toBeTruthy();
    expect(screen.queryByText("The language model is currently overloaded. Please retry in a moment.")).toBeNull();
    expect(screen.getByText("Tool execution failed")).toBeTruthy();
  });
});
