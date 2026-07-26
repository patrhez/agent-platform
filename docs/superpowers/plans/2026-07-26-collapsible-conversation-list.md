# Collapsible Conversation List Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users fully hide the left conversation list and restore it from a header toggle, persisting the preference in `localStorage`.

**Architecture:** `App` owns `sidebarOpen`, initialized from and written to `localStorage` via a tiny helper. A always-visible header button toggles the class on `.layout` and conditionally renders `ConversationList`. Collapsing clears selection mode.

**Tech Stack:** React 19, TypeScript, Vite, Vitest, Testing Library, CSS grid

**Spec:** `docs/superpowers/specs/2026-07-26-collapsible-conversation-list-design.md`

## Global Constraints

- All documentation, UI copy, commit messages, comments, and code identifiers for this feature must be in English.
- Frontend-only; no backend or API changes.
- `localStorage` key must be exactly `agent-platform.sidebarOpen` with string values `"true"` / `"false"`.
- Default when missing or invalid: open (`true`).
- Collapse must fully remove the sidebar from layout flow (no reserved width).
- Collapsing while in selection mode must exit selection mode and clear `selectedIDs`.
- Keep changes minimal; no unrelated refactors.
- Prefer instant collapse over fragile width animations if transition conflicts with `display: none` / conditional render.

## File Structure

| File | Responsibility |
|---|---|
| `frontend/src/state/sidebar.ts` | Read/write `sidebarOpen` preference |
| `frontend/src/state/sidebar.test.ts` | Unit tests for persistence helper |
| `frontend/src/components/ConversationList.tsx` | Stable `id="conversation-sidebar"` on `<aside>` |
| `frontend/src/App.tsx` | `sidebarOpen` state, toggle in chat header, conditional sidebar, clear selection on collapse |
| `frontend/src/App.test.tsx` | Toggle, persistence, selection-clear coverage |
| `frontend/src/app.css` | Collapsed layout + header toggle styles |

---

### Task 1: Sidebar Preference Helper

**Files:**
- Create: `frontend/src/state/sidebar.ts`
- Create: `frontend/src/state/sidebar.test.ts`

**Interfaces:**
- Consumes: `window.localStorage`
- Produces:
  - `export const SIDEBAR_OPEN_KEY = "agent-platform.sidebarOpen"`
  - `export function readSidebarOpen(): boolean`
  - `export function writeSidebarOpen(open: boolean): void`

- [ ] **Step 1: Write the failing unit tests**

Create `frontend/src/state/sidebar.test.ts`:

```ts
import { beforeEach, describe, expect, it } from "vitest";
import { readSidebarOpen, SIDEBAR_OPEN_KEY, writeSidebarOpen } from "./sidebar";

describe("sidebar preference", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("defaults to open when unset", () => {
    expect(readSidebarOpen()).toBe(true);
  });

  it("defaults to open when invalid", () => {
    localStorage.setItem(SIDEBAR_OPEN_KEY, "maybe");
    expect(readSidebarOpen()).toBe(true);
  });

  it("reads false when stored as false", () => {
    localStorage.setItem(SIDEBAR_OPEN_KEY, "false");
    expect(readSidebarOpen()).toBe(false);
  });

  it("writes string true/false", () => {
    writeSidebarOpen(false);
    expect(localStorage.getItem(SIDEBAR_OPEN_KEY)).toBe("false");
    writeSidebarOpen(true);
    expect(localStorage.getItem(SIDEBAR_OPEN_KEY)).toBe("true");
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd frontend && npm test -- --run src/state/sidebar.test.ts`

Expected: FAIL (module `./sidebar` not found)

- [ ] **Step 3: Implement the helper**

Create `frontend/src/state/sidebar.ts`:

```ts
export const SIDEBAR_OPEN_KEY = "agent-platform.sidebarOpen";

export function readSidebarOpen(): boolean {
  const raw = localStorage.getItem(SIDEBAR_OPEN_KEY);
  if (raw === "false") return false;
  if (raw === "true") return true;
  return true;
}

export function writeSidebarOpen(open: boolean): void {
  localStorage.setItem(SIDEBAR_OPEN_KEY, String(open));
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && npm test -- --run src/state/sidebar.test.ts`

Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/state/sidebar.ts frontend/src/state/sidebar.test.ts
git commit -m "$(cat <<'EOF'
feat: add sidebar open preference helper

EOF
)"
```

---

### Task 2: Collapsible Sidebar UI

**Files:**
- Modify: `frontend/src/components/ConversationList.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/App.test.tsx`
- Modify: `frontend/src/app.css`

**Interfaces:**
- Consumes: `readSidebarOpen`, `writeSidebarOpen` from `./state/sidebar`
- Produces: layout class `sidebar-collapsed` when closed; toggle button with labels `Hide conversations` / `Show conversations`; aside `id="conversation-sidebar"`

- [ ] **Step 1: Write the failing App tests**

Append a new describe block to `frontend/src/App.test.tsx` (reuse existing mocks / fixtures at top of file). Clear `localStorage` in `beforeEach`:

```ts
import { SIDEBAR_OPEN_KEY } from "./state/sidebar";

describe("App collapsible conversation list", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    vi.mocked(api.listConversations).mockResolvedValue({ conversations: [existing, second] });
    vi.mocked(api.getConversation).mockResolvedValue(existingDetail);
  });

  it("shows the sidebar by default and hides it when toggled", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("Existing");

    const sidebar = document.getElementById("conversation-sidebar");
    expect(sidebar).not.toBeNull();
    expect(screen.getByRole("button", { name: "Hide conversations" })).toHaveAttribute("aria-expanded", "true");

    await user.click(screen.getByRole("button", { name: "Hide conversations" }));

    expect(document.getElementById("conversation-sidebar")).toBeNull();
    expect(screen.getByRole("button", { name: "Show conversations" })).toHaveAttribute("aria-expanded", "false");
    expect(localStorage.getItem(SIDEBAR_OPEN_KEY)).toBe("false");
  });

  it("starts collapsed when localStorage says false", async () => {
    localStorage.setItem(SIDEBAR_OPEN_KEY, "false");
    render(<App />);
    await screen.findByRole("heading", { name: "Issue Troubleshooter Agent" });

    expect(document.getElementById("conversation-sidebar")).toBeNull();
    expect(screen.getByRole("button", { name: "Show conversations" })).toHaveAttribute("aria-expanded", "false");
  });

  it("clears selection mode when the sidebar collapses", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("Existing");

    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByText("Existing"));
    expect(screen.getByText("1 selected")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Hide conversations" }));
    await user.click(screen.getByRole("button", { name: "Show conversations" }));

    expect(screen.queryByText("1 selected")).toBeNull();
    expect(screen.getByRole("button", { name: "Select" })).toBeInTheDocument();
  });
});
```

Ensure `SIDEBAR_OPEN_KEY` is imported at the top of `App.test.tsx`.

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `cd frontend && npm test -- --run src/App.test.tsx`

Expected: FAIL — missing toggle / sidebar id / collapse behavior

- [ ] **Step 3: Add stable id on ConversationList aside**

In `frontend/src/components/ConversationList.tsx`, change the root element to:

```tsx
<aside id="conversation-sidebar">
```

- [ ] **Step 4: Wire App state, toggle, and conditional render**

In `frontend/src/App.tsx`:

1. Import helpers:

```ts
import { readSidebarOpen, writeSidebarOpen } from "./state/sidebar";
```

2. Add state near other `useState` calls:

```ts
const [sidebarOpen, setSidebarOpen] = useState(() => readSidebarOpen());
```

3. Add toggle handler (near `clearSelectionMode`):

```ts
const toggleSidebar = () => {
  const next = !sidebarOpen;
  if (!next) clearSelectionMode();
  setSidebarOpen(next);
  writeSidebarOpen(next);
};
```

4. Update the layout JSX:

```tsx
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
    <section className="chat-column">
      <header>
        <div className="chat-header-row">
          <button
            type="button"
            className="sidebar-toggle"
            aria-expanded={sidebarOpen}
            aria-controls="conversation-sidebar"
            aria-label={sidebarOpen ? "Hide conversations" : "Show conversations"}
            onClick={toggleSidebar}
          >
            {sidebarOpen ? "Hide" : "Show"}
          </button>
          <h1>Issue Troubleshooter Agent</h1>
        </div>
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
```

Visible button text can be short (`Hide` / `Show`); accessible name comes from `aria-label`.

- [ ] **Step 5: Add CSS for collapsed layout and toggle**

In `frontend/src/app.css`, after the existing `.layout` rules:

```css
.layout.sidebar-collapsed {
  grid-template-columns: 1fr;
}
```

Near `section.chat-column > header` styles, add:

```css
.chat-header-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
.sidebar-toggle {
  flex: 0 0 auto;
  margin: 0;
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 500;
  box-shadow: var(--shadow-xs);
  transition: background var(--duration) var(--ease), color var(--duration) var(--ease), border-color var(--duration) var(--ease);
}
.sidebar-toggle:hover {
  background: var(--surface-subtle);
  border-color: var(--border-strong);
  color: var(--text);
}
```

Do not change mobile `@media` rules beyond what still works with `sidebar-collapsed` (single-column already).

- [ ] **Step 6: Run App tests**

Run: `cd frontend && npm test -- --run src/App.test.tsx`

Expected: PASS (existing delete/new-conversation tests + 3 new sidebar tests)

- [ ] **Step 7: Run full frontend test suite**

Run: `cd frontend && npm test -- --run`

Expected: PASS (all frontend tests)

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/ConversationList.tsx frontend/src/App.tsx frontend/src/App.test.tsx frontend/src/app.css
git commit -m "$(cat <<'EOF'
feat: make conversation list collapsible

EOF
)"
```

---

## Spec Coverage Checklist

| Spec requirement | Task |
|---|---|
| Fully hide sidebar / no reserved width | Task 2 |
| Toggle in chat header, always visible | Task 2 |
| `aria-expanded` / `aria-controls` / button labels | Task 2 |
| `localStorage` key `agent-platform.sidebarOpen` | Task 1 + 2 |
| Default open on missing/invalid | Task 1 |
| Collapse clears selection mode | Task 2 |
| Frontend-only | Both tasks |
| Tests for toggle, persistence, selection clear | Task 1 + 2 |
