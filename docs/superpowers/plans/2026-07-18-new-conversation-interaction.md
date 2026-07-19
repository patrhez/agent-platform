# New Conversation Interaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a sidebar action that enters a local blank conversation and creates the durable Conversation only when the first message is sent.

**Architecture:** `App` owns a boolean local-draft state and derives the visible conversation from it. `ConversationList` remains presentational and emits an `onCreate` action; the existing backend API remains unchanged.

**Tech Stack:** React 19, TypeScript, Vite, Vitest, Testing Library, jsdom

## Global Constraints

- Clicking **New conversation** must not make an API request or create a database row.
- Sending the first draft message must use the existing `POST /api/v1/conversations` client method.
- Selecting a saved conversation abandons the empty local draft.
- No backend, schema, deletion, title-editing, or browser-persistence changes are in scope.
- Do not use subagents; execute inline in the current session.

---

### Task 1: Frontend Test Harness

**Files:**
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Modify: `frontend/vite.config.ts`

**Interfaces:**
- Produces: `npm test -- --run` for jsdom-based React component tests.

- [ ] **Step 1: Install the focused test dependencies**

Run:

```bash
npm install --save-dev vitest @testing-library/react @testing-library/user-event jsdom
```

Expected: `package.json` and `package-lock.json` contain the four test dependencies.

- [ ] **Step 2: Add the test script and jsdom configuration**

Set the package script to:

```json
"test": "vitest"
```

Change `vite.config.ts` to use `defineConfig` from `vitest/config` and add:

```ts
test: {
  environment: "jsdom",
}
```

- [ ] **Step 3: Verify the test runner starts successfully**

Run: `npm test -- --run --passWithNoTests`

Expected: PASS with no test files found.

- [ ] **Step 4: Commit the test harness**

```bash
git add frontend/package.json frontend/package-lock.json frontend/vite.config.ts
git commit -m "test: add frontend component test harness"
```

### Task 2: Local Draft Interaction

**Files:**
- Create: `frontend/src/components/ConversationList.test.tsx`
- Create: `frontend/src/App.test.tsx`
- Modify: `frontend/src/components/ConversationList.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/app.css`

**Interfaces:**
- `ConversationList` consumes `onCreate: () => void` in addition to its existing props.
- `App` produces a local draft state where `activeID` and visible conversation detail are absent.

- [ ] **Step 1: Write the failing sidebar component test**

Create a test that renders one saved conversation, clicks the button named `New conversation`, and asserts `onCreate` was called exactly once:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ConversationList } from "./ConversationList";

describe("ConversationList", () => {
  it("reports a new local conversation request", async () => {
    const onCreate = vi.fn();
    render(<ConversationList conversations={[]} onCreate={onCreate} onSelect={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: "New conversation" }));
    expect(onCreate).toHaveBeenCalledOnce();
  });
});
```

- [ ] **Step 2: Run the sidebar test and verify RED**

Run: `npm test -- --run src/components/ConversationList.test.tsx`

Expected: FAIL because `ConversationList` does not accept or render `onCreate`.

- [ ] **Step 3: Implement the new sidebar action**

Replace `ConversationList` with:

```tsx
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
      <button className="new-conversation" onClick={onCreate}>+ New conversation</button>
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
```

- [ ] **Step 4: Run the sidebar test and verify GREEN**

Run: `npm test -- --run src/components/ConversationList.test.tsx`

Expected: PASS.

- [ ] **Step 5: Write the failing application interaction test**

Create `frontend/src/App.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { api, type ConversationDetail } from "./api/client";

vi.mock("./api/client", () => ({
  api: {
    listConversations: vi.fn(),
    getConversation: vi.fn(),
    createConversation: vi.fn(),
    sendMessage: vi.fn(),
    getTrace: vi.fn(),
    cancelRun: vi.fn(),
  },
}));
vi.mock("./api/events", () => ({
  subscribeToRun: vi.fn(() => ({ close: vi.fn() })),
}));

const existing = { id: "existing", title: "Existing", createdAt: "2026-07-18T00:00:00Z", updatedAt: "2026-07-18T00:00:00Z" };
const created = { id: "created", title: "Created", createdAt: "2026-07-18T00:01:00Z", updatedAt: "2026-07-18T00:01:00Z" };
const existingDetail: ConversationDetail = {
  conversation: existing,
  messages: [{ id: "message", conversationId: "existing", seq: 1, role: "user", content: "existing message", status: "final", createdAt: "2026-07-18T00:00:00Z" }],
  runs: [],
};

describe("App new conversation flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listConversations)
      .mockResolvedValueOnce({ conversations: [existing] })
      .mockResolvedValue({ conversations: [created, existing] });
    vi.mocked(api.getConversation).mockImplementation(async (id) => id === existing.id
      ? existingDetail
      : { conversation: created, messages: [], runs: [] });
    vi.mocked(api.createConversation).mockResolvedValue({ conversation: created });
  });

  it("keeps a new conversation local until its first message", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("existing message");

    await user.click(screen.getByRole("button", { name: "New conversation" }));

    expect(screen.queryByText("existing message")).toBeNull();
    expect(api.createConversation).not.toHaveBeenCalled();

    await user.type(screen.getByPlaceholderText("Describe the issue to investigate"), "first draft question");
    await user.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() => expect(api.createConversation).toHaveBeenCalledWith("first draft question"));
  });
});
```

- [ ] **Step 6: Run the application test and verify RED**

Run: `npm test -- --run src/App.test.tsx`

Expected: FAIL because `App` has no draft transition and does not pass `onCreate`.

- [ ] **Step 7: Implement local draft state in `App`**

Replace the `App` component body with this state flow while keeping the existing imports:

```tsx
export default function App() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeID, setActiveID] = useState<string>();
  const [drafting, setDrafting] = useState(false);
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

  const detail = state.events.snapshot?.at(-1)?.payload.detail as typeof state.detail;
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
  const send = async (content: string) => {
    try {
      if (drafting || !activeID) {
        const created = await api.createConversation(content);
        await loadConversationList();
        await loadConversation(created.conversation.id);
        return;
      }
      await api.sendMessage(activeID, content, newClientMessageID());
      await loadConversation(activeID);
    } catch (cause) {
      setError(String(cause));
    }
  };

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
        <Chat messages={visibleDetail?.messages ?? []} runs={visibleDetail?.runs ?? []} onSend={send} />
        <RunTrace runID={visibleDetail?.runs.at(-1)?.id} />
      </section>
    </div>
  );
}
```

- [ ] **Step 8: Add focused button styling**

Add after the existing `aside button` rule:

```css
aside .new-conversation {
  margin-bottom: 18px;
  border: 1px solid #6f8fbd;
  background: #30415f;
  font-weight: 600;
}

aside .new-conversation:hover {
  background: #3b5278;
}
```

- [ ] **Step 9: Run all frontend checks**

Run:

```bash
npm test -- --run
npm run build
```

Expected: all tests PASS and Vite production build completes.

- [ ] **Step 10: Rebuild and verify in the browser**

Run: `docker compose up -d --build web`

Verify:

- clicking `+ New conversation` clears the saved conversation display;
- no new sidebar item appears until the first message is sent;
- the first message creates and selects the new conversation;
- selecting an existing saved conversation exits draft mode.

- [ ] **Step 11: Commit the interaction**

```bash
git add frontend/src/App.tsx frontend/src/App.test.tsx frontend/src/components/ConversationList.tsx frontend/src/components/ConversationList.test.tsx frontend/src/app.css
git commit -m "feat: add local new conversation flow"
```
