import type { Conversation } from "../api/client";

type ConversationListProps = {
  conversations: Conversation[];
  activeID?: string;
  selecting: boolean;
  selectedIDs: Set<string>;
  onCreate: () => void;
  onSelect: (id: string) => void;
  onToggleSelectMode: () => void;
  onToggleSelected: (id: string) => void;
  onSelectAll: () => void;
  onRequestDelete: (id: string) => void;
  onRequestBulkDelete: () => void;
};

export function ConversationList(props: ConversationListProps) {
  const selectedCount = props.selectedIDs.size;

  return (
    <aside id="conversation-sidebar">
      <div className="conversation-list-header">
        <h2>Conversations</h2>
        {props.conversations.length > 0 && (
          <button type="button" className="text-button" onClick={props.onToggleSelectMode}>
            {props.selecting ? "Cancel" : "Select"}
          </button>
        )}
      </div>
      <button aria-label="New conversation" className="new-conversation" onClick={props.onCreate}>
        + New conversation
      </button>
      {props.selecting && (
        <div className="selection-bar">
          <button type="button" className="text-button" onClick={props.onSelectAll}>
            Select all
          </button>
          <span>{selectedCount} selected</span>
          <button
            type="button"
            className="danger-text"
            disabled={selectedCount === 0}
            onClick={props.onRequestBulkDelete}
          >
            Delete
          </button>
        </div>
      )}
      {props.conversations.map((item) => {
        const selected = props.selectedIDs.has(item.id);
        if (props.selecting) {
          return (
            <button
              key={item.id}
              type="button"
              className={selected ? "selected conversation-row" : "conversation-row"}
              onClick={() => props.onToggleSelected(item.id)}
            >
              <input type="checkbox" readOnly checked={selected} tabIndex={-1} aria-hidden />
              <span className="conversation-title">{item.title}</span>
            </button>
          );
        }
        return (
          <div
            key={item.id}
            className={item.id === props.activeID ? "selected conversation-row" : "conversation-row"}
          >
            <button type="button" className="conversation-main" onClick={() => props.onSelect(item.id)}>
              <span className="conversation-title">{item.title}</span>
            </button>
            <button
              type="button"
              className="conversation-delete"
              aria-label="Delete conversation"
              onClick={(event) => {
                event.stopPropagation();
                props.onRequestDelete(item.id);
              }}
            >
              ×
            </button>
          </div>
        );
      })}
    </aside>
  );
}
