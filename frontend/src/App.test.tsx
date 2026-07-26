import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { api, type ConversationDetail } from "./api/client";
import { SIDEBAR_OPEN_KEY } from "./state/sidebar";

vi.mock("./api/client", () => ({
  api: {
    listConversations: vi.fn(),
    getConversation: vi.fn(),
    createConversation: vi.fn(),
    sendMessage: vi.fn(),
    cancelActive: vi.fn(),
    getTrace: vi.fn(),
    cancelRun: vi.fn(),
    deleteConversation: vi.fn(),
    deleteConversations: vi.fn(),
  },
}));
vi.mock("./api/events", () => ({
  subscribeToRun: vi.fn(() => ({ close: vi.fn() })),
}));

const existing = { id: "existing", title: "Existing", createdAt: "2026-07-18T00:00:00Z", updatedAt: "2026-07-18T00:00:00Z" };
const second = { id: "second", title: "Second", createdAt: "2026-07-18T00:02:00Z", updatedAt: "2026-07-18T00:02:00Z" };
const created = { id: "created", title: "Created", createdAt: "2026-07-18T00:01:00Z", updatedAt: "2026-07-18T00:01:00Z" };
const existingDetail: ConversationDetail = {
  conversation: existing,
  messages: [{ id: "message", conversationId: "existing", seq: 1, role: "user", content: "existing message", status: "final", createdAt: "2026-07-18T00:00:00Z" }],
  runs: [],
};

describe("App new conversation flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listConversations)
      .mockResolvedValueOnce({ conversations: [existing] })
      .mockResolvedValue({ conversations: [created, existing] });
    vi.mocked(api.getConversation).mockImplementation(async (id) => id === existing.id
      ? existingDetail
      : { conversation: created, messages: [], runs: [] });
    vi.mocked(api.createConversation).mockResolvedValue({ conversation: created });
  });

  it("keeps a new conversation local until its first message", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("existing message");

    await user.click(screen.getByRole("button", { name: "New conversation" }));

    expect(screen.queryByText("existing message")).toBeNull();
    expect(api.createConversation).not.toHaveBeenCalled();

    await user.type(screen.getByPlaceholderText("Describe the issue to investigate"), "first draft question");
    await user.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() => expect(api.createConversation).toHaveBeenCalledWith("first draft question"));
  });
});

describe("App conversation delete flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listConversations)
      .mockResolvedValueOnce({ conversations: [existing, second] })
      .mockResolvedValue({ conversations: [second] });
    vi.mocked(api.getConversation).mockResolvedValue(existingDetail);
    vi.mocked(api.deleteConversation).mockResolvedValue(undefined);
    vi.mocked(api.deleteConversations).mockResolvedValue(undefined);
  });

  it("deletes the active conversation and enters draft mode", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("existing message");

    await user.click(screen.getAllByRole("button", { name: "Delete conversation" })[0]);
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(api.deleteConversation).toHaveBeenCalledWith(existing.id));
    expect(screen.queryByText("existing message")).toBeNull();
    expect(api.createConversation).not.toHaveBeenCalled();
  });

  it("bulk deletes selected conversations in one request", async () => {
    const user = userEvent.setup();
    vi.mocked(api.listConversations)
      .mockReset()
      .mockResolvedValueOnce({ conversations: [existing, second] })
      .mockResolvedValue({ conversations: [] });
    render(<App />);
    await screen.findByText("Existing");

    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByText("Existing"));
    await user.click(screen.getByText("Second"));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(api.deleteConversations).toHaveBeenCalledWith(
      expect.arrayContaining([existing.id, second.id]),
    ));
  });

  it("does not call delete twice on double confirm", async () => {
    const user = userEvent.setup();
    let resolveDelete!: () => void;
    vi.mocked(api.deleteConversation).mockImplementation(
      () => new Promise((resolve) => { resolveDelete = () => resolve(undefined); }),
    );

    render(<App />);
    await screen.findByText("existing message");

    await user.click(screen.getAllByRole("button", { name: "Delete conversation" })[0]);
    const dialog = await screen.findByRole("dialog");
    const deleteBtn = within(dialog).getByRole("button", { name: "Delete" });

    act(() => {
      deleteBtn.click();
      deleteBtn.click();
    });

    expect(api.deleteConversation).toHaveBeenCalledTimes(1);

    resolveDelete();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("closes confirm dialog when list refresh fails after successful delete", async () => {
    const user = userEvent.setup();
    vi.mocked(api.listConversations)
      .mockReset()
      .mockResolvedValueOnce({ conversations: [existing, second] })
      .mockRejectedValueOnce(new Error("list failed"));

    render(<App />);
    await screen.findByText("existing message");

    await user.click(screen.getAllByRole("button", { name: "Delete conversation" })[0]);
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(api.deleteConversation).toHaveBeenCalledWith(existing.id));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText(/list failed/)).toBeInTheDocument();
    expect(screen.queryByText("existing message")).toBeNull();
    expect(api.createConversation).not.toHaveBeenCalled();
  });
});

describe("App collapsible conversation list", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    vi.mocked(api.listConversations).mockResolvedValue({ conversations: [existing, second] });
    vi.mocked(api.getConversation).mockResolvedValue(existingDetail);
  });

  it("shows the sidebar by default and hides it when toggled", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("Existing");

    const sidebar = document.getElementById("conversation-sidebar");
    expect(sidebar).not.toBeNull();
    expect(screen.getByRole("button", { name: "Hide conversations" })).toHaveAttribute("aria-expanded", "true");

    await user.click(screen.getByRole("button", { name: "Hide conversations" }));

    expect(document.getElementById("conversation-sidebar")).toBeNull();
    expect(screen.getByRole("button", { name: "Show conversations" })).toHaveAttribute("aria-expanded", "false");
    expect(localStorage.getItem(SIDEBAR_OPEN_KEY)).toBe("false");
  });

  it("starts collapsed when localStorage says false", async () => {
    localStorage.setItem(SIDEBAR_OPEN_KEY, "false");
    render(<App />);
    await screen.findByRole("heading", { name: "Issue Troubleshooter Agent" });

    expect(document.getElementById("conversation-sidebar")).toBeNull();
    expect(screen.getByRole("button", { name: "Show conversations" })).toHaveAttribute("aria-expanded", "false");
  });

  it("clears selection mode when the sidebar collapses", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("Existing");

    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByText("Existing"));
    expect(screen.getByText("1 selected")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Hide conversations" }));
    await user.click(screen.getByRole("button", { name: "Show conversations" }));

    expect(screen.queryByText("1 selected")).toBeNull();
    expect(screen.getByRole("button", { name: "Select" })).toBeInTheDocument();
  });
});
