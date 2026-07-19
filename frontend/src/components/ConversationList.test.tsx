import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ConversationList } from "./ConversationList";

describe("ConversationList", () => {
  it("reports a new local conversation request", async () => {
    const onCreate = vi.fn();
    render(<ConversationList conversations={[]} onCreate={onCreate} onSelect={vi.fn()} />);

    await userEvent.click(screen.getByRole("button", { name: "New conversation" }));

    expect(onCreate).toHaveBeenCalledOnce();
  });
});
