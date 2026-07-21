import { fireEvent, render, screen } from "@testing-library/react";
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

const running: Run = {
  ...run,
  status: "running",
  finishedAt: undefined,
};

function mockScrollMetrics(
  el: HTMLElement,
  metrics: { scrollTop: number; scrollHeight: number; clientHeight: number },
) {
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => metrics.scrollHeight });
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => metrics.clientHeight });
  el.scrollTop = metrics.scrollTop;
}

describe("Chat", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    const store = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => { store.set(key, value); },
      removeItem: (key: string) => { store.delete(key); },
      clear: () => { store.clear(); },
    });
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

  it("keeps Send enabled while a Run is active and shows Stop", () => {
    const onStop = vi.fn();
    render(<Chat
      messages={[userMessage]}
      runs={[running]}
      onSend={vi.fn()}
      onStop={onStop}
    />);

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Follow up" } });
    expect((screen.getByRole("button", { name: "Send" }) as HTMLButtonElement).disabled).toBe(false);
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalledOnce();
  });

  it("keeps the composer outside the scrollable messages region", () => {
    const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
    const messages = container.querySelector(".messages");
    const composer = container.querySelector("form.composer");
    expect(messages).toBeTruthy();
    expect(composer).toBeTruthy();
    expect(messages!.contains(composer)).toBe(false);
    expect(composer!.compareDocumentPosition(messages!) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
  });

  it("sends with the selected follow-up mode", () => {
    const onSend = vi.fn().mockResolvedValue(undefined);
    render(<Chat messages={[]} runs={[]} onSend={onSend} />);

    fireEvent.click(screen.getByRole("button", { name: "Steer" }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Redirect please" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    expect(onSend).toHaveBeenCalledWith("Redirect please", "steer");
    expect(window.localStorage.getItem("agent-platform.followUpMode")).toBe("steer");
  });

  it("hides Jump to latest while stuck to bottom", () => {
    const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
    const messages = container.querySelector(".messages") as HTMLElement;
    mockScrollMetrics(messages, { scrollTop: 920, scrollHeight: 1000, clientHeight: 80 });
    fireEvent.scroll(messages);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });

  it("shows Jump to latest after scrolling up and restores stick on click", () => {
    const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
    const messages = container.querySelector(".messages") as HTMLElement;
    mockScrollMetrics(messages, { scrollTop: 100, scrollHeight: 1000, clientHeight: 80 });
    fireEvent.scroll(messages);

    const jump = screen.getByRole("button", { name: "Jump to latest" });
    expect(jump).toBeTruthy();

    fireEvent.click(jump);
    expect(messages.scrollTop).toBe(messages.scrollHeight - messages.clientHeight);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });

  it("auto-scrolls while stuck when the draft grows", async () => {
    const { container, rerender } = render(
      <Chat messages={[userMessage]} runs={[running]} draft={draft} onSend={vi.fn()} />,
    );
    const messages = container.querySelector(".messages") as HTMLElement;
    mockScrollMetrics(messages, { scrollTop: 0, scrollHeight: 500, clientHeight: 80 });
    // start stuck near bottom
    mockScrollMetrics(messages, { scrollTop: 400, scrollHeight: 500, clientHeight: 80 });
    fireEvent.scroll(messages);

    mockScrollMetrics(messages, { scrollTop: 400, scrollHeight: 800, clientHeight: 80 });
    rerender(<Chat
      messages={[userMessage]}
      runs={[running]}
      draft={{ ...draft, content: draft.content + "\n\nmore tokens" }}
      onSend={vi.fn()}
    />);

    await vi.waitFor(() => {
      expect(messages.scrollTop).toBe(messages.scrollHeight - messages.clientHeight);
    });
  });

  it("does not auto-scroll when the user has scrolled up", () => {
    const { container, rerender } = render(
      <Chat messages={[userMessage]} runs={[running]} draft={draft} onSend={vi.fn()} />,
    );
    const messages = container.querySelector(".messages") as HTMLElement;
    mockScrollMetrics(messages, { scrollTop: 50, scrollHeight: 500, clientHeight: 80 });
    fireEvent.scroll(messages);
    const before = messages.scrollTop;

    mockScrollMetrics(messages, { scrollTop: before, scrollHeight: 800, clientHeight: 80 });
    rerender(<Chat
      messages={[userMessage]}
      runs={[running]}
      draft={{ ...draft, content: draft.content + "\n\nmore tokens" }}
      onSend={vi.fn()}
    />);

    expect(messages.scrollTop).toBe(before);
    expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();
  });

  it("forces stick after send", async () => {
    const onSend = vi.fn().mockResolvedValue(undefined);
    const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={onSend} />);
    const messages = container.querySelector(".messages") as HTMLElement;
    mockScrollMetrics(messages, { scrollTop: 10, scrollHeight: 500, clientHeight: 80 });
    fireEvent.scroll(messages);
    expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Follow up" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    expect(onSend).toHaveBeenCalled();
    expect(messages.scrollTop).toBe(messages.scrollHeight - messages.clientHeight);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });

  it("resets stick when conversationKey changes", () => {
    const { container, rerender } = render(
      <Chat conversationKey="a" messages={[userMessage]} runs={[run]} onSend={vi.fn()} />,
    );
    const messages = container.querySelector(".messages") as HTMLElement;
    mockScrollMetrics(messages, { scrollTop: 10, scrollHeight: 500, clientHeight: 80 });
    fireEvent.scroll(messages);
    expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();

    rerender(<Chat conversationKey="b" messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });
});
