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
  it("renders a safe failed status from errorMessage", () => {
    render(<RunStatus run={{
      ...baseRun,
      status: "failed",
      errorCode: "llm_invalid_temperature",
      errorMessage: "The language model rejected the temperature setting. Check the agent model configuration and retry.",
    }} />);
    expect(screen.getByRole("status").textContent).toBe(
      "The language model rejected the temperature setting. Check the agent model configuration and retry.",
    );
  });

  it("falls back to a known errorCode message", () => {
    render(<RunStatus run={{ ...baseRun, status: "failed", errorCode: "llm_overload" }} />);
    expect(screen.getByRole("status").textContent).toBe(
      "The language model is currently overloaded. Please retry in a moment.",
    );
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
