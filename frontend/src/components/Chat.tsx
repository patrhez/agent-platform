import { useState } from "react";
import type { Message, Run } from "../api/client";
import type { RunEvent } from "../api/events";
import type { AssistantDraft } from "../state/conversation";
import { MarkdownContent } from "./MarkdownContent";
import { RunStatus } from "./RunStatus";
import { RunTrace } from "./RunTrace";

type ChatProps = {
  messages: Message[];
  runs: Run[];
  eventsByRunID?: Record<string, RunEvent[]>;
  draft?: AssistantDraft;
  onSend: (content: string) => Promise<void>;
};

type Turn = {
  user: Message;
  run?: Run;
  assistants: Message[];
};

export function Chat({ messages, runs, eventsByRunID = {}, draft, onSend }: ChatProps) {
  const [content, setContent] = useState("");
  const sending = runs.some((run) => run.status === "queued" || run.status === "running");
  const turns = buildTurns(messages, runs);
  const draftAttached = draft
    ? turns.some((turn) => turn.run?.id === draft.runID)
    : false;

  return <main>
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
    <form onSubmit={(event) => {
      event.preventDefault();
      if (content.trim()) {
        void onSend(content.trim());
        setContent("");
      }
    }}>
      <textarea
        value={content}
        onChange={(event) => setContent(event.target.value)}
        placeholder="Describe the issue to investigate"
      />
      <button disabled={sending}>Send</button>
    </form>
  </main>;
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
