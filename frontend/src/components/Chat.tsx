import { useState } from "react";
import type { Message, Run } from "../api/client";
import type { AssistantDraft } from "../state/conversation";
import { MarkdownContent } from "./MarkdownContent";

type ChatProps = {
  messages: Message[];
  runs: Run[];
  draft?: AssistantDraft;
  onSend: (content: string) => Promise<void>;
};

export function Chat({ messages, runs, draft, onSend }: ChatProps) {
  const [content, setContent] = useState("");
  const sending = runs.some((run) => run.status === "queued" || run.status === "running");
  const hasFormalMessage = draft
    ? messages.some((message) => message.role === "assistant" && message.runId === draft.runID)
    : false;

  return <main>
    <div className="messages">
      {messages.map((message) => <article className={message.role} key={message.id}>
        <b>{message.role === "user" ? "You" : "Agent"}</b>
        {message.role === "assistant"
          ? <MarkdownContent content={message.content} />
          : <p>{message.content}</p>}
      </article>)}
      {draft && !hasFormalMessage && <article
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
