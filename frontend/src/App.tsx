import { useEffect, useReducer, useRef, useState } from "react";
import { api, type Conversation, type FollowUpMode } from "./api/client";
import { subscribeToRun } from "./api/events";
import { activeRuns, applyEvent, assistantDraft, initialState, runEvents } from "./state/conversation";
import { readSidebarOpen, writeSidebarOpen } from "./state/sidebar";
import { Chat } from "./components/Chat";
import { ConfirmDialog } from "./components/ConfirmDialog";
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
  const [selecting, setSelecting] = useState(false);
  const [selectedIDs, setSelectedIDs] = useState<Set<string>>(new Set());
  const [confirm, setConfirm] = useState<
    null | { kind: "single"; id: string } | { kind: "bulk"; ids: string[] }
  >(null);
  const [deleting, setDeleting] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(() => readSidebarOpen());
  const deletingRef = useRef(false);

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

  const clearSelectionMode = () => {
    setSelecting(false);
    setSelectedIDs(new Set());
  };

  const toggleSidebar = () => {
    const next = !sidebarOpen;
    if (!next) clearSelectionMode();
    setSidebarOpen(next);
    writeSidebarOpen(next);
  };

  const requestDelete = (id: string) => {
    if (deleting) return;
    setConfirm({ kind: "single", id });
  };

  const requestBulkDelete = () => {
    if (deleting || selectedIDs.size === 0) return;
    setConfirm({ kind: "bulk", ids: [...selectedIDs] });
  };

  const confirmDelete = async () => {
    if (!confirm || deletingRef.current) return;
    deletingRef.current = true;
    setDeleting(true);
    setError("");
    const deletedIDs = confirm.kind === "single" ? [confirm.id] : confirm.ids;
    const wasActiveDeleted = Boolean(activeID && deletedIDs.includes(activeID));
    try {
      if (confirm.kind === "single") {
        await api.deleteConversation(confirm.id);
      } else {
        await api.deleteConversations(confirm.ids);
      }
      setConfirm(null);
      clearSelectionMode();
      if (wasActiveDeleted) {
        startDraft();
      }
      try {
        await loadConversationList();
      } catch (cause) {
        setError(String(cause));
      }
    } catch (cause) {
      setError(String(cause));
    } finally {
      deletingRef.current = false;
      setDeleting(false);
    }
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

  const confirmTitle = confirm?.kind === "bulk"
    ? `Delete ${confirm.ids.length} conversations?`
    : "Delete conversation?";
  const confirmMessage = confirm?.kind === "bulk"
    ? "Deleted conversations are removed from your list and cannot be restored."
    : (conversations.find((item) => item.id === confirm?.id)?.title ?? "This conversation");

  return (
    <div className={sidebarOpen ? "layout" : "layout sidebar-collapsed"}>
      {sidebarOpen && (
        <ConversationList
          conversations={conversations}
          activeID={activeID}
          selecting={selecting}
          selectedIDs={selectedIDs}
          onCreate={startDraft}
          onSelect={(id) => void loadConversation(id)}
          onToggleSelectMode={() => {
            if (selecting) clearSelectionMode();
            else setSelecting(true);
          }}
          onToggleSelected={(id) => {
            setSelectedIDs((current) => {
              const next = new Set(current);
              if (next.has(id)) next.delete(id);
              else next.add(id);
              return next;
            });
          }}
          onSelectAll={() => {
            setSelectedIDs((current) =>
              current.size === conversations.length
                ? new Set()
                : new Set(conversations.map((item) => item.id)),
            );
          }}
          onRequestDelete={requestDelete}
          onRequestBulkDelete={requestBulkDelete}
        />
      )}
      <div className="sidebar-edge">
        <button
          type="button"
          className="sidebar-toggle"
          aria-expanded={sidebarOpen}
          aria-controls="conversation-sidebar"
          aria-label={sidebarOpen ? "Hide conversations" : "Show conversations"}
          title={sidebarOpen ? "Hide conversations" : "Show conversations"}
          onClick={toggleSidebar}
        >
          <svg
            className="sidebar-toggle-icon"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden
          >
            <path d="m11 17-5-5 5-5" />
            <path d="m18 17-5-5 5-5" />
          </svg>
        </button>
      </div>
      <section className="chat-column">
        <header>
          <h1>Issue Troubleshooter Agent</h1>
          {error && <p className="error">{error}</p>}
        </header>
        <Chat
          conversationKey={drafting ? "draft" : activeID}
          messages={visibleDetail?.messages ?? []}
          runs={visibleDetail?.runs ?? []}
          eventsByRunID={eventsByRunID}
          draft={draft}
          busy={busy}
          onSend={send}
          onStop={activeID ? stop : undefined}
        />
      </section>
      <ConfirmDialog
        open={confirm !== null}
        title={confirmTitle}
        message={confirmMessage}
        busy={deleting}
        onCancel={() => { if (!deleting) setConfirm(null); }}
        onConfirm={() => { void confirmDelete(); }}
      />
    </div>
  );
}
