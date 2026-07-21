import { useEffect, useState } from "react";
import type { FollowUpMode, Message, Run } from "../api/client";
import type { RunEvent } from "../api/events";
import type { AssistantDraft } from "../state/conversation";
import { MarkdownContent } from "./MarkdownContent";
import { RunStatus } from "./RunStatus";
import { RunTrace } from "./RunTrace";

const followUpModeKey = "agent-platform.followUpMode";

type ChatProps = {
  messages: Message[];
  runs: Run[];
  eventsByRunID?: Record<string, RunEvent[]>;
  draft?: AssistantDraft;
  busy?: boolean;
  onSend: (content: string, mode: FollowUpMode) => Promise<void>;
  onStop?: () => Promise<void>;
};

type Turn = {
  user: Message;
  run?: Run;
  assistants: Message[];
};

export function Chat({
  messages,
  runs,
  eventsByRunID = {},
  draft,
  busy = false,
  onSend,
  onStop,
}: ChatProps) {
  const [content, setContent] = useState("");
  const [mode, setMode] = useState<FollowUpMode>(readFollowUpMode);
  const active = runs.some((run) => ["queued", "running", "waiting"].includes(run.status));
  const turns = buildTurns(messages, runs);
  const draftAttached = draft
    ? turns.some((turn) => turn.run?.id === draft.runID)
    : false;

	useEffect(() => {
		try {
			window.localStorage.setItem(followUpModeKey, mode);
		} catch {
			// Ignore storage failures in restricted environments.
		}
	}, [mode]);

  return <main className="chat">
    <div className="messages-pane">
      <div className="messages">
      {turns.map((turn) => {
        const showDraft = Boolean(
          draft
          && turn.run?.id === draft.runID
          && !turn.assistants.some((message) => message.runId === draft.runID),
        );
        return <div className="turn" key={turn.user.id}>
          <article className="user">
            <b>You</b>
            <p>{turn.user.content}</p>
          </article>
          {turn.run && <RunTrace run={turn.run} events={eventsByRunID[turn.run.id] ?? []} />}
          {turn.run && <RunStatus run={turn.run} />}
          {turn.assistants.map((message) => <article className="assistant" key={message.id}>
            <b>Agent</b>
            <MarkdownContent content={message.content} />
          </article>)}
          {showDraft && draft && <article
            className="assistant streaming"
            aria-label="Agent is answering"
            aria-busy="true"
          >
            <b>Agent</b>
            <MarkdownContent content={draft.content} />
            <span className="generation-cursor" aria-hidden="true" />
          </article>}
        </div>;
      })}
      {draft && !draftAttached && <article
        className="assistant streaming"
        aria-label="Agent is answering"
        aria-busy="true"
      >
        <b>Agent</b>
        <MarkdownContent content={draft.content} />
        <span className="generation-cursor" aria-hidden="true" />
      </article>}
      </div>
    </div>
    <form className="composer" onSubmit={(event) => {
      event.preventDefault();
      if (!content.trim() || busy) return;
      const next = content.trim();
      setContent("");
      void onSend(next, mode);
    }}>
      <textarea
        value={content}
        onChange={(event) => setContent(event.target.value)}
        placeholder="Describe the issue to investigate"
        disabled={busy}
      />
      <div className="composer-actions">
        <div className="follow-up-mode" role="group" aria-label="Follow-up mode">
          <button
            type="button"
            className={mode === "queue" ? "selected" : undefined}
            aria-pressed={mode === "queue"}
            disabled={busy}
            onClick={() => setMode("queue")}
          >
            Queue
          </button>
          <button
            type="button"
            className={mode === "steer" ? "selected" : undefined}
            aria-pressed={mode === "steer"}
            disabled={busy}
            onClick={() => setMode("steer")}
          >
            Steer
          </button>
        </div>
        {active && onStop && <button type="button" className="stop" disabled={busy} onClick={() => void onStop()}>
          Stop
        </button>}
        <button type="submit" disabled={busy || !content.trim()}>Send</button>
      </div>
    </form>
  </main>;
}

function readFollowUpMode(): FollowUpMode {
  try {
    const value = window.localStorage.getItem(followUpModeKey);
    return value === "steer" ? "steer" : "queue";
  } catch {
    return "queue";
  }
}

function buildTurns(messages: Message[], runs: Run[]): Turn[] {
  const turns: Turn[] = [];
  for (const message of messages) {
    if (message.role === "user") {
      turns.push({
        user: message,
        run: runs.find((run) => run.triggerMessageId === message.id),
        assistants: [],
      });
      continue;
    }
    const turn = [...turns].reverse().find((candidate) => candidate.run?.id === message.runId)
      ?? turns.at(-1);
    turn?.assistants.push(message);
  }
  return turns;
}
