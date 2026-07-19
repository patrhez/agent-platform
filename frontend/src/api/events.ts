import { eventURL } from "./client";

export type RunEvent = { runID: string; seq: number; type: string; payload: Record<string, unknown> };

export function subscribeToRun(runID: string, onEvent: (event: RunEvent) => void, onTerminal: () => void): EventSource {
  const source = new EventSource(eventURL(runID, 0));
  const eventTypes = ["progress.updated", "tool.started", "tool.completed", "assistant.started", "assistant.delta", "assistant.completed", "run.completed", "run.failed", "run.cancelled"];
  eventTypes.forEach((type) => source.addEventListener(type, (message) => {
    const item = message as MessageEvent<string>;
    onEvent({ runID, seq: Number(item.lastEventId), type, payload: JSON.parse(item.data) });
    if (["run.completed", "run.failed", "run.cancelled"].includes(type)) { source.close(); onTerminal(); }
  }));
  return source;
}
