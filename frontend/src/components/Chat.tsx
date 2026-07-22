import { useEffect, useRef, useState } from "react";
import type { FollowUpMode, Message, Run } from "../api/client";
import type { RunEvent } from "../api/events";
import type { AssistantDraft } from "../state/conversation";
import { MarkdownContent } from "./MarkdownContent";
import { RunStatus } from "./RunStatus";
import { RunTrace } from "./RunTrace";

const followUpModeKey = "agent-platform.followUpMode";
const STICK_THRESHOLD_PX = 80;

function isStuckToBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= STICK_THRESHOLD_PX;
}

type ChatProps = {
  conversationKey?: string;
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
  conversationKey,
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
  const messagesRef = useRef<HTMLDivElement>(null);
  const rafRef = useRef<number | null>(null);
  const stuckToBottomRef = useRef(true);
  const [stuckToBottom, setStuckToBottom] = useState(true);

  const scrollToBottom = () => {
    const el = messagesRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight - el.clientHeight;
    stuckToBottomRef.current = true;
    setStuckToBottom(true);
  };

  const scheduleScrollToBottom = () => {
    if (rafRef.current != null) return;
    rafRef.current = window.requestAnimationFrame(() => {
      rafRef.current = null;
      if (!stuckToBottomRef.current) return;
      const el = messagesRef.current;
      if (!el) return;
      el.scrollTop = el.scrollHeight - el.clientHeight;
      setStuckToBottom(true);
    });
  };

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

  useEffect(() => () => {
    if (rafRef.current != null) {
      window.cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }
  }, []);

  useEffect(() => {
    stuckToBottomRef.current = true;
    setStuckToBottom(true);
    scheduleScrollToBottom();
  }, [conversationKey]);

  useEffect(() => {
    if (!stuckToBottom) return;
    scheduleScrollToBottom();
  }, [draft?.content, messages, turns.length, stuckToBottom]);

  return <main className="chat">
    <div className="messages-pane">
      <div
        className="messages"
        ref={messagesRef}
        onScroll={(event) => {
          const stuck = isStuckToBottom(event.currentTarget);
          stuckToBottomRef.current = stuck;
          setStuckToBottom(stuck);
        }}
      >
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
            <DraftContent draft={draft} />
          </article>}
        </div>;
      })}
      {draft && !draftAttached && <article
        className="assistant streaming"
        aria-label="Agent is answering"
        aria-busy="true"
      >
        <b>Agent</b>
        <DraftContent draft={draft} />
      </article>}
      </div>
      {!stuckToBottom && (
        <button
          type="button"
          className="jump-to-latest"
          aria-label="Jump to latest"
          onClick={scrollToBottom}
        >
          Jump to latest
        </button>
      )}
    </div>
    <form className="composer" onSubmit={(event) => {
      event.preventDefault();
      if (!content.trim() || busy) return;
      const next = content.trim();
      setContent("");
      scrollToBottom();
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

function DraftContent({ draft }: { draft: AssistantDraft }) {
  if (!draft.content) {
    return <span className="typing-indicator" aria-hidden="true"><span /><span /><span /></span>;
  }
  return <>
    <MarkdownContent content={draft.content} />
    <span className="generation-cursor" aria-hidden="true" />
  </>;
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
