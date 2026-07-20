import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { api, type ConversationDetail } from "./api/client";

vi.mock("./api/client", () => ({
  api: {
    listConversations: vi.fn(),
    getConversation: vi.fn(),
    createConversation: vi.fn(),
    sendMessage: vi.fn(),
    cancelActive: vi.fn(),
    getTrace: vi.fn(),
    cancelRun: vi.fn(),
  },
}));
vi.mock("./api/events", () => ({
  subscribeToRun: vi.fn(() => ({ close: vi.fn() })),
}));

const existing = { id: "existing", title: "Existing", createdAt: "2026-07-18T00:00:00Z", updatedAt: "2026-07-18T00:00:00Z" };
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
