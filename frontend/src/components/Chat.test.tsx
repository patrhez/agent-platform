import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, type Message, type Run } from "../api/client";
import type { AssistantDraft } from "../state/conversation";
import { Chat } from "./Chat";

vi.mock("../api/client", () => ({ api: { getTrace: vi.fn() } }));

const draft: AssistantDraft = {
  runID: "run-1",
  streamID: "run-1:1:1",
  attempt: 1,
  stepNo: 1,
  nextOffset: 1,
  content: "## Streaming answer",
};

const userMessage: Message = {
  id: "user-1", conversationId: "conversation", seq: 1, role: "user",
  content: "Where is the 500 thrown?", status: "final", createdAt: "now",
};

const assistantMessage: Message = {
  id: "assistant-1", conversationId: "conversation", seq: 2, role: "assistant",
  content: "# Final answer", status: "final", runId: "run-1", createdAt: "now",
};

const run: Run = {
  id: "run-1",
  conversationId: "conversation",
  triggerMessageId: "user-1",
  queueSeq: 1,
  status: "completed",
  attempt: 1,
  createdAt: "2026-07-18T00:00:00.000Z",
  finishedAt: "2026-07-18T00:00:01.000Z",
};

describe("Chat", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTrace).mockResolvedValue({ steps: [], toolCalls: [] });
  });

  it("renders an active Markdown draft", () => {
    render(<Chat messages={[]} runs={[]} draft={draft} onSend={vi.fn()} />);
    expect(screen.getByRole("article", { name: "Agent is answering" }).getAttribute("aria-busy")).toBe("true");
    expect(screen.getByRole("heading", { name: "Streaming answer" })).toBeTruthy();
  });

  it("prefers the formal assistant Message for the same Run", () => {
    render(<Chat
      messages={[userMessage, assistantMessage]}
      runs={[run]}
      draft={draft}
      onSend={vi.fn()}
    />);
    expect(screen.queryByRole("article", { name: "Agent is answering" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Final answer" })).toBeTruthy();
  });

  it("places a collapsed execution trace between the user question and assistant answer", async () => {
    vi.mocked(api.getTrace).mockResolvedValue({
      steps: [],
      toolCalls: [{
        id: "tool-1", stepNo: 1, serverKey: "workspace", toolName: "code.search",
        arguments: { repo: "agent-platform", query: "stream" },
        resultSummary: "2 matches", status: "completed",
        createdAt: "2026-07-18T00:00:00Z", updatedAt: "2026-07-18T00:00:00.100Z",
      }],
    });

    render(<Chat
      messages={[userMessage, assistantMessage]}
      runs={[run]}
      onSend={vi.fn()}
    />);

    expect(await screen.findByText("Used 1 tools · 1s")).toBeTruthy();
    const user = screen.getByText("Where is the 500 thrown?");
    const trace = document.querySelector(".run-trace");
    const answer = screen.getByRole("heading", { name: "Final answer" });
    expect(trace?.hasAttribute("open")).toBe(false);
    expect(user.compareDocumentPosition(trace!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(trace!.compareDocumentPosition(answer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
