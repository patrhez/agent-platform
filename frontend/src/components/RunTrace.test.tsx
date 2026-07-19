import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type RunTrace as RunTraceResponse } from "../api/client";
import type { RunEvent } from "../api/events";
import { RunTrace } from "./RunTrace";

vi.mock("../api/client", () => ({ api: { getTrace: vi.fn() } }));

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

  it("merges persisted trace with live safe Tool progress", async () => {
    const started = event(1, "assistant.started", {
      streamId: "run-1:1:1", attempt: 1, stepNo: 1, reasoning: "private thought",
    });
    const toolStarted = event(2, "tool.started", {
      toolCallId: "tool-2", tool: "file.read", status: "running",
      arguments: { repo: "agent-platform", path: "backend/main.go", startLine: 1, endLine: 20, apiKey: "secret" },
      durationMs: 0,
    });
    const { rerender } = render(<RunTrace runID="run-1" events={[started, toolStarted]} />);

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
    rerender(<RunTrace runID="run-1" events={[started, toolStarted, completed]} />);
    await waitFor(() => expect(screen.getByText(/12ms/)).toBeTruthy());
    expect(screen.getByText("read 20 lines")).toBeTruthy();
  });

  it("shows a safe live failure state", async () => {
    render(<RunTrace runID="run-1" events={[event(4, "tool.completed", {
      toolCallId: "tool-failed", tool: "code.search", status: "failed",
      arguments: { repo: "agent-platform", query: "missing" }, resultSummary: "Tool execution failed", durationMs: 5,
    })]} />);
    expect(await screen.findByText("Failed")).toBeTruthy();
    expect(screen.getByText("Tool execution failed")).toBeTruthy();
  });
});
