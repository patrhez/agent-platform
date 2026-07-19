import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Run } from "../api/client";
import { RunStatus } from "./RunStatus";

const baseRun: Run = {
  id: "run",
  conversationId: "conversation",
  triggerMessageId: "message",
  queueSeq: 1,
  status: "running",
  attempt: 1,
  createdAt: "2026-07-18T00:00:00Z",
};

describe("RunStatus", () => {
  it("renders a safe failed status", () => {
    render(<RunStatus run={{ ...baseRun, status: "failed", errorCode: "runtime_error" }} />);
    expect(screen.getByRole("status").textContent).toBe("Run failed (runtime_error). Check the execution trace or retry.");
  });

  it("renders a cancelled status", () => {
    render(<RunStatus run={{ ...baseRun, status: "cancelled" }} />);
    expect(screen.getByRole("status").textContent).toBe("Run cancelled.");
  });

  it("renders nothing for a successful Run", () => {
    const { container } = render(<RunStatus run={{ ...baseRun, status: "succeeded" }} />);
    expect(container.textContent).toBe("");
  });
});
