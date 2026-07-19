import type { ConversationDetail, Run } from "../api/client";
import type { RunEvent } from "../api/events";

export type AssistantDraft = {
  runID: string;
  streamID: string;
  attempt: number;
  stepNo: number;
  nextOffset: number;
  content: string;
};

export type ConversationState = {
  detail?: ConversationDetail;
  events: Record<string, RunEvent[]>;
  drafts: Record<string, AssistantDraft>;
  seen: Set<string>;
};

export const initialState: ConversationState = { events: {}, drafts: {}, seen: new Set() };

export function applyEvent(state: ConversationState, event: RunEvent): ConversationState {
  const key = `${event.runID}:${event.seq}`;
  if (state.seen.has(key)) return state;
  const seen = new Set(state.seen);
  seen.add(key);

  if (event.type === "snapshot") {
    const detail = event.payload.detail as ConversationDetail;
    const completedRuns = new Set(detail.messages
      .filter((message) => message.role === "assistant" && message.runId)
      .map((message) => message.runId as string));
    const drafts = Object.fromEntries(Object.entries(state.drafts)
      .filter(([runID]) => !completedRuns.has(runID)));
    return { ...state, detail, drafts, seen };
  }

  const events = { ...state.events, [event.runID]: [...(state.events[event.runID] ?? []), event] };
  const drafts = projectDraft(state.drafts, event);
  return { ...state, drafts, seen, events };
}

function projectDraft(drafts: Record<string, AssistantDraft>, event: RunEvent): Record<string, AssistantDraft> {
  if (event.type === "assistant.started") {
    const streamID = stringValue(event.payload.streamId);
    const attempt = numberValue(event.payload.attempt);
    const stepNo = numberValue(event.payload.stepNo);
    if (!streamID || attempt === undefined || stepNo === undefined) return drafts;
    const current = drafts[event.runID];
    if (current && (attempt < current.attempt || (attempt === current.attempt && stepNo < current.stepNo))) {
      return drafts;
    }
    return {
      ...drafts,
      [event.runID]: { runID: event.runID, streamID, attempt, stepNo, nextOffset: 0, content: "" },
    };
  }
  if (event.type !== "assistant.delta") return drafts;
  const current = drafts[event.runID];
  if (!current) return drafts;
  const streamID = stringValue(event.payload.streamId);
  const attempt = numberValue(event.payload.attempt);
  const stepNo = numberValue(event.payload.stepNo);
  const offset = numberValue(event.payload.offset);
  const text = stringValue(event.payload.text);
  if (streamID !== current.streamID || attempt !== current.attempt || stepNo !== current.stepNo ||
    offset !== current.nextOffset || text === undefined) {
    return drafts;
  }
  return {
    ...drafts,
    [event.runID]: { ...current, content: current.content + text, nextOffset: current.nextOffset + 1 },
  };
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) ? value : undefined;
}

export function assistantDraft(state: ConversationState, runID: string): AssistantDraft | undefined {
  return state.drafts[runID];
}

export function runEvents(state: ConversationState, runID: string): RunEvent[] {
  return state.events[runID] ?? [];
}

export function activeRuns(detail?: ConversationDetail): Run[] {
  return detail?.runs.filter((run) => ["queued", "running", "waiting"].includes(run.status)) ?? [];
}
