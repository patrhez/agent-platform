import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Message } from "../api/client";
import type { AssistantDraft } from "../state/conversation";
import { Chat } from "./Chat";

const draft: AssistantDraft = {
  runID: "run-1",
  streamID: "run-1:1:1",
  attempt: 1,
  stepNo: 1,
  nextOffset: 1,
  content: "## Streaming answer",
};

describe("Chat", () => {
  it("renders an active Markdown draft", () => {
    render(<Chat messages={[]} runs={[]} draft={draft} onSend={vi.fn()} />);
    expect(screen.getByRole("article", { name: "Agent is answering" }).getAttribute("aria-busy")).toBe("true");
    expect(screen.getByRole("heading", { name: "Streaming answer" })).toBeTruthy();
  });

  it("prefers the formal assistant Message for the same Run", () => {
    const messages: Message[] = [{
      id: "message", conversationId: "conversation", seq: 2, role: "assistant",
      content: "# Final answer", status: "final", runId: "run-1", createdAt: "now",
    }];
    render(<Chat messages={messages} runs={[]} draft={draft} onSend={vi.fn()} />);
    expect(screen.queryByRole("article", { name: "Agent is answering" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Final answer" })).toBeTruthy();
  });
});
