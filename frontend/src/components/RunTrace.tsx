import { useEffect, useMemo, useState } from "react";
import { api, type RunTrace as RunTraceResponse } from "../api/client";
import type { RunEvent } from "../api/events";

type TimelineItem = {
  key: string;
  order: number;
  kind: "model" | "tool" | "run";
  label: string;
  status: string;
  toolName?: string;
  arguments?: unknown;
  resultSummary?: string;
  durationMs?: number;
};

export function RunTrace({ runID, events }: { runID?: string; events: RunEvent[] }) {
  const [trace, setTrace] = useState<RunTraceResponse>();
  useEffect(() => {
    let active = true;
    setTrace(undefined);
    if (runID) {
      void api.getTrace(runID).then((value) => { if (active) setTrace(value); });
    }
    return () => { active = false; };
  }, [runID]);
  const timeline = useMemo(
    () => mergeTimeline(persistedTimeline(trace), liveTimeline(events)),
    [trace, events],
  );
  if (!runID) return null;
  return <details className="run-trace" open>
    <summary>Execution trace</summary>
    <ol>
      {timeline.map((item) => <li className={`timeline-item ${item.status}`} key={item.key}>
        <div className="timeline-heading">
          <span>{statusLabel(item.status)}</span>
          <strong>{item.toolName ?? item.label}</strong>
          {item.durationMs !== undefined && <small>{item.durationMs}ms</small>}
        </div>
        {item.toolName && <SafeArguments toolName={item.toolName} value={item.arguments} />}
        {item.resultSummary && <details className="tool-result">
          <summary>Result summary</summary>
          <p>{item.resultSummary}</p>
        </details>}
      </li>)}
    </ol>
  </details>;
}

function persistedTimeline(trace?: RunTraceResponse): TimelineItem[] {
  if (!trace) return [];
  const modelSteps = trace.steps
    .filter((step) => step.kind !== "tool")
    .map((step) => ({
      key: `step:${step.id}`,
      order: step.stepNo * 100,
      kind: "model" as const,
      label: step.safeSummary,
      status: step.status,
    }));
  const tools = trace.toolCalls.map((call, index) => ({
    key: `tool:${call.id}`,
    order: call.stepNo * 100 + index + 1,
    kind: "tool" as const,
    label: call.toolName,
    status: call.status,
    toolName: call.toolName,
    arguments: call.arguments,
    resultSummary: call.resultSummary,
    durationMs: elapsedMilliseconds(call.createdAt, call.updatedAt),
  }));
  return [...modelSteps, ...tools];
}

function liveTimeline(events: RunEvent[]): TimelineItem[] {
  return events.flatMap((event): TimelineItem[] => {
    if (event.type === "assistant.started") {
      return [{
        key: `stream:${String(event.payload.streamId ?? event.seq)}`,
        order: 100_000 + event.seq,
        kind: "model",
        label: "Generating answer",
        status: "running",
      }];
    }
    if (event.type === "assistant.completed") {
      return [{ key: `assistant:${event.seq}`, order: 100_000 + event.seq, kind: "model", label: "Answer generation complete", status: "completed" }];
    }
    if (event.type === "tool.started" || event.type === "tool.completed") {
      const toolCallID = stringValue(event.payload.toolCallId);
      const toolName = stringValue(event.payload.tool);
      if (!toolCallID || !toolName) return [];
      return [{
        key: `tool:${toolCallID}`,
        order: 100_000 + event.seq,
        kind: "tool",
        label: toolName,
        status: stringValue(event.payload.status) ?? (event.type === "tool.started" ? "running" : "completed"),
        toolName,
        arguments: event.payload.arguments,
        resultSummary: stringValue(event.payload.resultSummary),
        durationMs: numberValue(event.payload.durationMs),
      }];
    }
    if (event.type === "run.failed" || event.type === "run.cancelled" || event.type === "run.completed") {
      return [{
        key: `run:${event.seq}`,
        order: 100_000 + event.seq,
        kind: "run",
        label: stringValue(event.payload.summary) ?? "Run status updated",
        status: event.type === "run.completed" ? "completed" : event.type.replace("run.", ""),
      }];
    }
    return [];
  });
}

function mergeTimeline(persisted: TimelineItem[], live: TimelineItem[]): TimelineItem[] {
  const merged = new Map<string, TimelineItem>();
  for (const item of [...persisted, ...live]) {
    merged.set(item.key, { ...merged.get(item.key), ...item });
  }
  return [...merged.values()].sort((left, right) => left.order - right.order);
}

function SafeArguments({ toolName, value }: { toolName: string; value: unknown }) {
  const argumentsToShow = safeArguments(toolName, value);
  if (argumentsToShow.length === 0) return null;
  return <dl className="tool-arguments">
    {argumentsToShow.map(([label, content]) => <div key={label}><dt>{label}</dt><dd>{content}</dd></div>)}
  </dl>;
}

function safeArguments(toolName: string, value: unknown): Array<[string, string]> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return [];
  const source = value as Record<string, unknown>;
  const fields = toolName === "code.search"
    ? [["repo", "Repository"], ["query", "Query"], ["pathPrefix", "Path"], ["glob", "File scope"], ["maxResults", "Max results"]]
    : toolName === "file.read"
      ? [["repo", "Repository"], ["path", "File"], ["startLine", "Start line"], ["endLine", "End line"]]
      : [];
  return fields.flatMap(([key, label]): Array<[string, string]> => {
    const content = source[key];
    return typeof content === "string" || typeof content === "number"
      ? [[label, String(content)]]
      : [];
  });
}

function elapsedMilliseconds(start: string, end: string): number {
  const value = new Date(end).getTime() - new Date(start).getTime();
  return Number.isFinite(value) && value > 0 ? value : 0;
}

function statusLabel(status: string): string {
  if (status === "completed" || status === "succeeded") return "Completed";
  if (status === "failed") return "Failed";
  if (status === "cancelled") return "Cancelled";
  return "In progress";
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}
