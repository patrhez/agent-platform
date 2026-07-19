import type { Conversation } from "../api/client";

type ConversationListProps = {
  conversations: Conversation[];
  activeID?: string;
  onCreate: () => void;
  onSelect: (id: string) => void;
};

export function ConversationList({ conversations, activeID, onCreate, onSelect }: ConversationListProps) {
  return (
    <aside>
      <h2>Conversations</h2>
      <button aria-label="New conversation" className="new-conversation" onClick={onCreate}>+ New conversation</button>
      {conversations.map((item) => (
        <button
          className={item.id === activeID ? "selected" : ""}
          key={item.id}
          onClick={() => onSelect(item.id)}
        >
          {item.title}
        </button>
      ))}
    </aside>
  );
}
