import { useEffect, useReducer, useState } from "react";
import { api, type Conversation, type FollowUpMode } from "./api/client";
import { subscribeToRun } from "./api/events";
import { activeRuns, applyEvent, assistantDraft, initialState, runEvents } from "./state/conversation";
import { Chat } from "./components/Chat";
import { ConversationList } from "./components/ConversationList";
import { newClientMessageID } from "./id";
import "./app.css";

export default function App() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeID, setActiveID] = useState<string>();
  const [drafting, setDrafting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [state, dispatch] = useReducer(applyEvent, initialState);
  const [error, setError] = useState("");

  const loadConversation = async (id: string) => {
    const detail = await api.getConversation(id);
    dispatch({ runID: "snapshot", seq: Date.now(), type: "snapshot", payload: { detail } });
    setActiveID(id);
    setDrafting(false);
  };

  const loadConversationList = async () => {
    const { conversations: items } = await api.listConversations();
    setConversations(items);
    return items;
  };

  const refresh = async () => {
    const items = await loadConversationList();
    if (items[0]) await loadConversation(items[0].id);
  };

  useEffect(() => {
    void refresh().catch((cause) => setError(String(cause)));
  }, []);

  const detail = state.detail;
  const visibleDetail = drafting ? undefined : detail;

  useEffect(() => {
    const sources = activeRuns(visibleDetail).map((run) => subscribeToRun(
      run.id,
      (event) => dispatch(event),
      () => { if (activeID) void loadConversation(activeID); },
    ));
    return () => sources.forEach((source) => source.close());
  }, [visibleDetail, activeID]);

  const startDraft = () => {
    setDrafting(true);
    setActiveID(undefined);
    setError("");
  };

  const send = async (content: string, mode: FollowUpMode) => {
    setBusy(true);
    setError("");
    try {
      if (drafting || !activeID) {
        const created = await api.createConversation(content);
        await loadConversationList();
        await loadConversation(created.conversation.id);
        return;
      }
      await api.sendMessage(activeID, content, newClientMessageID(), mode);
      await loadConversation(activeID);
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  };

  const stop = async () => {
    if (!activeID) return;
    setBusy(true);
    setError("");
    try {
      await api.cancelActive(activeID);
      await loadConversation(activeID);
    } catch (cause) {
      setError(String(cause));
    } finally {
      setBusy(false);
    }
  };

  const latestRun = visibleDetail?.runs.at(-1);
  const draft = latestRun ? assistantDraft(state, latestRun.id) : undefined;
  const eventsByRunID = Object.fromEntries(
    (visibleDetail?.runs ?? []).map((run) => [run.id, runEvents(state, run.id)]),
  );

  return (
    <div className="layout">
      <ConversationList
        conversations={conversations}
        activeID={activeID}
        onCreate={startDraft}
        onSelect={(id) => void loadConversation(id)}
      />
      <section>
        <header>
          <h1>Issue Troubleshooter Agent</h1>
          {error && <p className="error">{error}</p>}
        </header>
        <Chat
          messages={visibleDetail?.messages ?? []}
          runs={visibleDetail?.runs ?? []}
          eventsByRunID={eventsByRunID}
          draft={draft}
          busy={busy}
          onSend={send}
          onStop={activeID ? stop : undefined}
        />
      </section>
    </div>
  );
}
