import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ConversationList } from "./ConversationList";

const sample = [
  {
    id: "c1", title: "Alpha", createdAt: "2026-07-26T00:00:00Z",
    updatedAt: "2026-07-26T00:00:00Z", latestMessageAt: "2026-07-27T01:02:00Z",
  },
  { id: "c2", title: "Beta", createdAt: "2026-07-26T00:00:00Z", updatedAt: "2026-07-26T00:00:00Z" },
];

const baseProps = {
  conversations: sample,
  selecting: false,
  selectedIDs: new Set<string>(),
  onCreate: vi.fn(),
  onSelect: vi.fn(),
  onToggleSelectMode: vi.fn(),
  onToggleSelected: vi.fn(),
  onSelectAll: vi.fn(),
  onRequestDelete: vi.fn(),
  onRequestBulkDelete: vi.fn(),
};

describe("ConversationList", () => {
  it("reports a new local conversation request", async () => {
    const onCreate = vi.fn();
    render(
      <ConversationList
        conversations={[]}
        selecting={false}
        selectedIDs={new Set()}
        onCreate={onCreate}
        onSelect={vi.fn()}
        onToggleSelectMode={vi.fn()}
        onToggleSelected={vi.fn()}
        onSelectAll={vi.fn()}
        onRequestDelete={vi.fn()}
        onRequestBulkDelete={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "New conversation" }));

    expect(onCreate).toHaveBeenCalledOnce();
  });

  it("requests single delete from a conversation row", async () => {
    const onRequestDelete = vi.fn();
    render(
      <ConversationList
        {...baseProps}
        onRequestDelete={onRequestDelete}
      />,
    );
    const row = screen.getByText("Alpha").closest(".conversation-row");
    expect(row).toBeTruthy();
    await userEvent.click(within(row as HTMLElement).getByRole("button", { name: "Delete conversation" }));
    expect(onRequestDelete).toHaveBeenCalledWith("c1");
  });

  it("shows the latest activity time for every conversation", () => {
    const { container } = render(<ConversationList {...baseProps} />);
    const timestamps = container.querySelectorAll("time.conversation-time");
    expect(timestamps).toHaveLength(2);
    expect(timestamps[0]).toHaveAttribute("datetime", "2026-07-27T01:02:00.000Z");
    expect(timestamps[1]).toHaveAttribute("datetime", "2026-07-26T00:00:00.000Z");
    expect(timestamps[0].textContent).not.toBe("");
  });

  it("toggles selection instead of activating a conversation while selecting", async () => {
    const onSelect = vi.fn();
    const onToggleSelected = vi.fn();
    render(
      <ConversationList
        {...baseProps}
        selecting
        selectedIDs={new Set(["c1"])}
        onSelect={onSelect}
        onToggleSelected={onToggleSelected}
      />,
    );
    await userEvent.click(screen.getByText("Beta"));
    expect(onToggleSelected).toHaveBeenCalledWith("c2");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("disables bulk Delete at 0 selected and enables when selection is non-empty", () => {
    const { rerender } = render(
      <ConversationList
        {...baseProps}
        selecting
        selectedIDs={new Set()}
      />,
    );
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();

    rerender(
      <ConversationList
        {...baseProps}
        selecting
        selectedIDs={new Set(["c1"])}
      />,
    );
    expect(screen.getByRole("button", { name: "Delete" })).toBeEnabled();
  });

  it("hides Select control when conversations are empty", () => {
    render(
      <ConversationList
        {...baseProps}
        conversations={[]}
      />,
    );
    expect(screen.queryByRole("button", { name: "Select" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
  });

  it("shows checkboxes in select mode and hides per-item delete", () => {
    render(
      <ConversationList
        {...baseProps}
        selecting
        selectedIDs={new Set(["c1"])}
      />,
    );
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Select all" })).toBeInTheDocument();
    expect(screen.getByText("1 selected")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete conversation" })).not.toBeInTheDocument();
    const checkboxes = screen.getAllByRole("checkbox", { hidden: true });
    expect(checkboxes).toHaveLength(2);
    expect(checkboxes[0]).toBeChecked();
    expect(checkboxes[1]).not.toBeChecked();
  });
});
