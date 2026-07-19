import { useEffect, useMemo, useState } from "react";
import { api, type Run, type RunTrace as RunTraceResponse } from "../api/client";
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

export function RunTrace({ run, events }: { run: Run; events: RunEvent[] }) {
  const [trace, setTrace] = useState<RunTraceResponse>();
  const toolEventEpoch = events.reduce(
    (count, event) => count + (event.type === "tool.started" || event.type === "tool.completed" ? 1 : 0),
    0,
  );
  useEffect(() => {
    let active = true;
    void api.getTrace(run.id).then((value) => { if (active) setTrace(value); });
    return () => { active = false; };
  }, [run.id, toolEventEpoch]);
  const timeline = useMemo(
    () => mergeTimeline(persistedTimeline(trace), liveTimeline(events)),
    [trace, events],
  );
  const liveActive = run.status === "queued" || run.status === "running" || run.status === "waiting";
  const summary = traceSummary(run, timeline);
  const activity = liveActivity(timeline, liveActive);

  return <div className="run-trace-panel">
    <details className="run-trace">
      <summary>{summary}</summary>
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
    </details>
    {activity && <p className="run-trace-activity" aria-live="polite">{activity}</p>}
  </div>;
}

function traceSummary(run: Run, timeline: TimelineItem[]): string {
  if (run.status === "failed") {
    return run.errorCode ? `Failed (${run.errorCode})` : "Failed";
  }
  if (run.status === "cancelled") return "Cancelled";

  const tools = timeline.filter((item) => item.kind === "tool");
  const runningTool = [...tools].reverse().find((item) => item.status === "running");
  const lastTool = [...tools].reverse().find((item) => item.toolName);
  const liveActive = run.status === "queued" || run.status === "running" || run.status === "waiting";
  if (liveActive) {
    if (tools.length === 0 && timeline.length === 0 && run.status === "queued") return "Queued";
    const count = toolCountLabel(tools.length);
    if (runningTool?.toolName) {
      return count ? `Running · ${count} · ${runningTool.toolName}…` : `Running · ${runningTool.toolName}…`;
    }
    if (lastTool?.toolName) {
      return count ? `Running · ${count} · last ${lastTool.toolName}` : `Running · last ${lastTool.toolName}`;
    }
    return "Running";
  }

  const duration = formatDuration(runDurationMs(run));
  const toolCount = tools.length;
  if (toolCount > 0) return `Used ${toolCount} tools · ${duration}`;
  return `Completed · ${duration}`;
}

function liveActivity(timeline: TimelineItem[], liveActive: boolean): string | undefined {
  if (!liveActive) return undefined;
  const tools = timeline.filter((item) => item.kind === "tool");
  const runningTool = [...tools].reverse().find((item) => item.status === "running");
  if (runningTool?.toolName) return `Calling ${runningTool.toolName}…`;
  const lastTool = [...tools].reverse().find((item) => item.toolName);
  if (lastTool?.toolName) return `Last tool: ${lastTool.toolName} · waiting for model`;
  if (timeline.some((item) => item.kind === "model")) return "Waiting for model…";
  return "Starting…";
}

function toolCountLabel(count: number): string | undefined {
  if (count <= 0) return undefined;
  return count === 1 ? "1 tool" : `${count} tools`;
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
    if (event.type === "progress.updated") {
      return [{
        key: `progress:${event.seq}`,
        order: 100_000 + event.seq,
        kind: "model",
        label: stringValue(event.payload.summary) ?? "Working",
        status: "running",
      }];
    }
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

function runDurationMs(run: Run): number {
  if (!run.finishedAt) return 0;
  return elapsedMilliseconds(run.createdAt, run.finishedAt);
}

function formatDuration(durationMs: number): string {
  if (durationMs < 1000) return `${Math.max(0, Math.round(durationMs))}ms`;
  const seconds = durationMs / 1000;
  return Number.isInteger(seconds) ? `${seconds}s` : `${seconds.toFixed(1)}s`;
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
