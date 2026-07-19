import { describe, expect, it } from "vitest";
import type { ConversationDetail } from "../api/client";
import type { RunEvent } from "../api/events";
import { applyEvent, assistantDraft, initialState } from "./conversation";

const runID = "run-1";

function runEvent(seq: number, type: string, payload: Record<string, unknown>): RunEvent {
  return { runID, seq, type, payload };
}

describe("conversation event projection", () => {
  it("projects ordered assistant Deltas and replaces stale attempts with the final snapshot", () => {
    let state = applyEvent(initialState, runEvent(1, "assistant.started", {
      streamId: "run-1:1:1", attempt: 1, stepNo: 1, offset: 0,
    }));
    expect(assistantDraft(state, runID)?.content).toBe("");

    state = applyEvent(state, runEvent(2, "assistant.delta", {
      streamId: "run-1:1:1", attempt: 1, stepNo: 1, offset: 0, text: "hello ",
    }));
    state = applyEvent(state, runEvent(3, "assistant.delta", {
      streamId: "run-1:1:1", attempt: 1, stepNo: 1, offset: 1, text: "world",
    }));
    expect(assistantDraft(state, runID)?.content).toBe("hello world");

    const duplicate = applyEvent(state, runEvent(3, "assistant.delta", {
      streamId: "run-1:1:1", attempt: 1, stepNo: 1, offset: 1, text: "world",
    }));
    expect(duplicate).toBe(state);

    state = applyEvent(state, runEvent(4, "assistant.delta", {
      streamId: "run-1:1:1", attempt: 1, stepNo: 1, offset: 3, text: " ignored",
    }));
    state = applyEvent(state, runEvent(5, "assistant.delta", {
      streamId: "run-1:0:1", attempt: 0, stepNo: 1, offset: 0, text: "stale",
    }));
    expect(assistantDraft(state, runID)?.content).toBe("hello world");

    state = applyEvent(state, runEvent(6, "assistant.started", {
      streamId: "run-1:2:1", attempt: 2, stepNo: 1, offset: 0,
    }));
    expect(assistantDraft(state, runID)).toMatchObject({ attempt: 2, nextOffset: 0, content: "" });

    state = applyEvent(state, runEvent(7, "assistant.delta", {
      streamId: "run-1:2:1", attempt: 2, stepNo: 1, offset: 0, text: "replacement",
    }));
    const detail: ConversationDetail = {
      conversation: { id: "conversation", title: "title", createdAt: "now", updatedAt: "now" },
      messages: [{
        id: "assistant", conversationId: "conversation", seq: 2, role: "assistant",
        content: "final", status: "final", runId: runID, createdAt: "now",
      }],
      runs: [{
        id: runID, conversationId: "conversation", triggerMessageId: "message", queueSeq: 1,
        status: "succeeded", attempt: 2, createdAt: "now",
      }],
    };
    state = applyEvent(state, { runID: "snapshot", seq: 8, type: "snapshot", payload: { detail } });

    expect(state.detail).toEqual(detail);
    expect(assistantDraft(state, runID)).toBeUndefined();
  });
});
