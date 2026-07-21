# Pinned Composer and Smart Auto-Scroll Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pin the chat composer to the bottom of the chat column and auto-scroll the message list only while the user is following the latest output, with an English **Jump to latest** control when they scroll up.

**Architecture:** Convert the right-hand chat column into a viewport-height flex stack (`header` + scrollable `messages` + pinned `composer`). `Chat` owns a messages container ref, `stuckToBottom` state, scroll-threshold detection (~80px), rAF-coalesced scroll-to-bottom while stuck, and the floating **Jump to latest** button. `App` passes a `conversationKey` so stick resets on conversation switch or new draft.

**Tech Stack:** React 19, TypeScript, Vite, Vitest, Testing Library, existing `frontend/src/app.css`

## Global Constraints

- UI copy and `aria-label`s for this feature are English (`Jump to latest`).
- Composer is always pinned; do not use viewport-`fixed` or form-only `position: sticky`.
- Stick threshold is ~80px from the bottom of the messages container.
- Coalesce scroll-to-bottom with `requestAnimationFrame` during streaming.
- No API or backend changes.
- Do not change follow-up modes, Stop, or streaming protocol.

## File Structure

| File | Responsibility |
|------|----------------|
| `frontend/src/app.css` | Viewport-height chat column; messages as sole scroll region; composer pin; jump button styles |
| `frontend/src/App.tsx` | Flex column on `<section>`; pass `conversationKey` into `Chat` |
| `frontend/src/components/Chat.tsx` | Messages/composer split; stick state; scroll effects; Jump to latest |
| `frontend/src/components/Chat.test.tsx` | Layout, stick, jump, send-force, conversation-reset coverage |

---

### Task 1: Pin composer with flex chat column

**Files:**
- Modify: `frontend/src/app.css`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/components/Chat.tsx`
- Test: `frontend/src/components/Chat.test.tsx`

**Interfaces:**
- Consumes: existing `Chat` props (`messages`, `runs`, `draft`, `busy`, `onSend`, `onStop`, `eventsByRunID`)
- Produces: DOM structure `main.chat > .messages-pane > .messages` + `form.composer`; CSS classes `.chat`, `.messages-pane`, `.composer`

- [ ] **Step 1: Write the failing layout test**

Add to `frontend/src/components/Chat.test.tsx`:

```tsx
it("keeps the composer outside the scrollable messages region", () => {
  const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
  const messages = container.querySelector(".messages");
  const composer = container.querySelector("form.composer");
  expect(messages).toBeTruthy();
  expect(composer).toBeTruthy();
  expect(messages!.contains(composer)).toBe(false);
  expect(composer!.compareDocumentPosition(messages!) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
});
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```sh
cd frontend && npm test -- --run src/components/Chat.test.tsx -t "keeps the composer outside"
```

Expected: FAIL because `form` has no `composer` class and/or still sits in a flat `main` without the new structure.

- [ ] **Step 3: Update CSS for the pinned column**

In `frontend/src/app.css`, replace/extend the relevant rules so the layout fills the viewport and only messages scroll:

```css
.layout { display: grid; grid-template-columns: 260px 1fr; height: 100vh; min-height: 100vh; }
section.chat-column {
  max-width: 960px;
  width: calc(100% - 48px);
  margin: 0 auto;
  padding: 24px 24px 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
  box-sizing: border-box;
}
section.chat-column > header { flex: 0 0 auto; }
.chat {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.messages-pane {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.messages {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: 16px;
}
form.composer {
  flex: 0 0 auto;
  display: flex;
  gap: 12px;
  align-items: stretch;
  padding: 12px 0 24px;
  background: #f5f7fb;
}
```

Remove or stop relying on the old `.messages { min-height: 55vh; }` and bare `form { ... }` / `section { ... }` rules that fight this layout (keep other `section`-unrelated rules intact; retarget the old `section` block to `section.chat-column`).

- [ ] **Step 4: Wire App section class and Chat structure**

In `frontend/src/App.tsx`, change the chat `<section>` to:

```tsx
<section className="chat-column">
```

In `frontend/src/components/Chat.tsx`, wrap output as:

```tsx
return <main className="chat">
  <div className="messages-pane">
    <div className="messages">
      {/* existing turns / draft rendering unchanged */}
    </div>
  </div>
  <form className="composer" onSubmit={/* existing handler */}>
    {/* existing textarea + actions unchanged */}
  </form>
</main>;
```

- [ ] **Step 5: Run the layout test and verify GREEN**

Run:

```sh
cd frontend && npm test -- --run src/components/Chat.test.tsx -t "keeps the composer outside"
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add frontend/src/app.css frontend/src/App.tsx frontend/src/components/Chat.tsx frontend/src/components/Chat.test.tsx
git commit -m "$(cat <<'EOF'
feat: pin chat composer below a scrollable messages pane

EOF
)"
```

---

### Task 2: Stick detection and Jump to latest

**Files:**
- Modify: `frontend/src/components/Chat.tsx`
- Modify: `frontend/src/app.css`
- Test: `frontend/src/components/Chat.test.tsx`

**Interfaces:**
- Consumes: Task 1 DOM (`.messages` scroll container inside `.messages-pane`)
- Produces:
  - `stuckToBottom: boolean` (default `true`)
  - `STICK_THRESHOLD_PX = 80`
  - `isStuckToBottom(el: HTMLElement): boolean`
  - Button role with accessible name `Jump to latest`, visible only when `!stuckToBottom`

- [ ] **Step 1: Write failing stick / jump tests**

Add helpers and tests to `Chat.test.tsx`:

```tsx
function mockScrollMetrics(
  el: HTMLElement,
  metrics: { scrollTop: number; scrollHeight: number; clientHeight: number },
) {
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => metrics.scrollHeight });
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => metrics.clientHeight });
  el.scrollTop = metrics.scrollTop;
}

it("hides Jump to latest while stuck to bottom", () => {
  const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
  const messages = container.querySelector(".messages") as HTMLElement;
  mockScrollMetrics(messages, { scrollTop: 920, scrollHeight: 1000, clientHeight: 80 });
  fireEvent.scroll(messages);
  expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
});

it("shows Jump to latest after scrolling up and restores stick on click", () => {
  const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
  const messages = container.querySelector(".messages") as HTMLElement;
  mockScrollMetrics(messages, { scrollTop: 100, scrollHeight: 1000, clientHeight: 80 });
  fireEvent.scroll(messages);

  const jump = screen.getByRole("button", { name: "Jump to latest" });
  expect(jump).toBeTruthy();

  fireEvent.click(jump);
  expect(messages.scrollTop).toBe(messages.scrollHeight - messages.clientHeight);
  expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
});
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```sh
cd frontend && npm test -- --run src/components/Chat.test.tsx -t "Jump to latest"
```

Expected: FAIL because the button and stick state do not exist.

- [ ] **Step 3: Implement stick state and Jump to latest**

In `frontend/src/components/Chat.tsx`:

```tsx
import { useEffect, useRef, useState } from "react";

const STICK_THRESHOLD_PX = 80;

function isStuckToBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= STICK_THRESHOLD_PX;
}

// inside Chat:
const messagesRef = useRef<HTMLDivElement>(null);
const [stuckToBottom, setStuckToBottom] = useState(true);

const scrollToBottom = () => {
  const el = messagesRef.current;
  if (!el) return;
  el.scrollTop = el.scrollHeight;
  setStuckToBottom(true);
};

// on messages:
<div
  className="messages"
  ref={messagesRef}
  onScroll={(event) => {
    setStuckToBottom(isStuckToBottom(event.currentTarget));
  }}
>
```

Inside `.messages-pane`, after `.messages`:

```tsx
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
```

Add CSS:

```css
.jump-to-latest {
  position: absolute;
  right: 12px;
  bottom: 12px;
  z-index: 1;
  padding: 8px 12px;
  border: 1px solid #cbd5e1;
  border-radius: 999px;
  background: white;
  color: #175cd3;
  font-size: 13px;
  font-weight: 600;
  box-shadow: 0 2px 8px rgba(23, 32, 51, 0.12);
}
```

- [ ] **Step 4: Run stick / jump tests and verify GREEN**

Run:

```sh
cd frontend && npm test -- --run src/components/Chat.test.tsx -t "Jump to latest"
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Chat.tsx frontend/src/components/Chat.test.tsx frontend/src/app.css
git commit -m "$(cat <<'EOF'
feat: add Jump to latest when chat scroll leaves the bottom

EOF
)"
```

---

### Task 3: Auto-scroll while stuck, force on send, reset on conversation change

**Files:**
- Modify: `frontend/src/components/Chat.tsx`
- Modify: `frontend/src/App.tsx`
- Test: `frontend/src/components/Chat.test.tsx`

**Interfaces:**
- Consumes: Task 2 `stuckToBottom`, `messagesRef`, `scrollToBottom`
- Produces:
  - New prop `conversationKey?: string` on `Chat`
  - rAF-coalesced `scheduleScrollToBottom()` used when stuck and content changes
  - Send handler forces stick + scroll before/after clearing input
  - Effect resets stick and scrolls when `conversationKey` changes

- [ ] **Step 1: Write failing auto-scroll / send / reset tests**

Add to `Chat.test.tsx`:

```tsx
it("auto-scrolls while stuck when the draft grows", () => {
  const { container, rerender } = render(
    <Chat messages={[userMessage]} runs={[running]} draft={draft} onSend={vi.fn()} />,
  );
  const messages = container.querySelector(".messages") as HTMLElement;
  mockScrollMetrics(messages, { scrollTop: 0, scrollHeight: 500, clientHeight: 80 });
  // start stuck near bottom
  mockScrollMetrics(messages, { scrollTop: 400, scrollHeight: 500, clientHeight: 80 });
  fireEvent.scroll(messages);

  mockScrollMetrics(messages, { scrollTop: 400, scrollHeight: 800, clientHeight: 80 });
  rerender(<Chat
    messages={[userMessage]}
    runs={[running]}
    draft={{ ...draft, content: draft.content + "\n\nmore tokens" }}
    onSend={vi.fn()}
  />);

  expect(messages.scrollTop).toBe(messages.scrollHeight - messages.clientHeight);
});

it("does not auto-scroll when the user has scrolled up", () => {
  const { container, rerender } = render(
    <Chat messages={[userMessage]} runs={[running]} draft={draft} onSend={vi.fn()} />,
  );
  const messages = container.querySelector(".messages") as HTMLElement;
  mockScrollMetrics(messages, { scrollTop: 50, scrollHeight: 500, clientHeight: 80 });
  fireEvent.scroll(messages);
  const before = messages.scrollTop;

  mockScrollMetrics(messages, { scrollTop: before, scrollHeight: 800, clientHeight: 80 });
  rerender(<Chat
    messages={[userMessage]}
    runs={[running]}
    draft={{ ...draft, content: draft.content + "\n\nmore tokens" }}
    onSend={vi.fn()}
  />);

  expect(messages.scrollTop).toBe(before);
  expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();
});

it("forces stick after send", async () => {
  const onSend = vi.fn().mockResolvedValue(undefined);
  const { container } = render(<Chat messages={[userMessage]} runs={[run]} onSend={onSend} />);
  const messages = container.querySelector(".messages") as HTMLElement;
  mockScrollMetrics(messages, { scrollTop: 10, scrollHeight: 500, clientHeight: 80 });
  fireEvent.scroll(messages);
  expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();

  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Follow up" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));

  expect(onSend).toHaveBeenCalled();
  expect(messages.scrollTop).toBe(messages.scrollHeight - messages.clientHeight);
  expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
});

it("resets stick when conversationKey changes", () => {
  const { container, rerender } = render(
    <Chat conversationKey="a" messages={[userMessage]} runs={[run]} onSend={vi.fn()} />,
  );
  const messages = container.querySelector(".messages") as HTMLElement;
  mockScrollMetrics(messages, { scrollTop: 10, scrollHeight: 500, clientHeight: 80 });
  fireEvent.scroll(messages);
  expect(screen.getByRole("button", { name: "Jump to latest" })).toBeTruthy();

  rerender(<Chat conversationKey="b" messages={[userMessage]} runs={[run]} onSend={vi.fn()} />);
  expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
});
```

If rAF timing makes the draft-growth assertion flaky under Vitest, flush with:

```tsx
await vi.waitFor(() => {
  expect(messages.scrollTop).toBe(messages.scrollHeight - messages.clientHeight);
});
```

and make those tests `async`.

- [ ] **Step 2: Run tests and verify RED**

Run:

```sh
cd frontend && npm test -- --run src/components/Chat.test.tsx -t "auto-scroll|forces stick|resets stick"
```

Expected: FAIL (no auto-scroll / `conversationKey` behavior yet).

- [ ] **Step 3: Implement scroll scheduling, send force, and conversation reset**

Extend `ChatProps`:

```tsx
type ChatProps = {
  conversationKey?: string;
  // ...existing props
};
```

Inside `Chat`:

```tsx
const rafRef = useRef<number | null>(null);

const scheduleScrollToBottom = () => {
  if (rafRef.current != null) return;
  rafRef.current = window.requestAnimationFrame(() => {
    rafRef.current = null;
    const el = messagesRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
    setStuckToBottom(true);
  });
};

useEffect(() => () => {
  if (rafRef.current != null) window.cancelAnimationFrame(rafRef.current);
}, []);

useEffect(() => {
  setStuckToBottom(true);
  scheduleScrollToBottom();
}, [conversationKey]);

useEffect(() => {
  if (!stuckToBottom) return;
  scheduleScrollToBottom();
}, [draft?.content, messages, turns.length, stuckToBottom]);
```

In the form `onSubmit`, after validating content and before/when calling `onSend`, call `scrollToBottom()` (immediate, not only rAF) so stick restores even if the user had scrolled up:

```tsx
onSubmit={(event) => {
  event.preventDefault();
  if (!content.trim() || busy) return;
  const next = content.trim();
  setContent("");
  scrollToBottom();
  void onSend(next, mode);
}}
```

In `frontend/src/App.tsx`:

```tsx
<Chat
  conversationKey={drafting ? "draft" : activeID}
  messages={visibleDetail?.messages ?? []}
  // ...unchanged props
/>
```

- [ ] **Step 4: Run Chat tests and verify GREEN**

Run:

```sh
cd frontend && npm test -- --run src/components/Chat.test.tsx
```

Expected: all Chat tests PASS.

- [ ] **Step 5: Verify production build**

Run:

```sh
cd frontend && npm run build
```

Expected: TypeScript compile and Vite build succeed.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/Chat.tsx frontend/src/components/Chat.test.tsx frontend/src/App.tsx
git commit -m "$(cat <<'EOF'
feat: smart auto-scroll chat output while the composer stays pinned

EOF
)"
```

---

## Spec Coverage Checklist

| Spec requirement | Task |
|------------------|------|
| Flex column: header / scroll messages / pinned composer | Task 1 |
| Messages-only scroll; bottom padding | Task 1 |
| Stick within ~80px; auto-scroll while stuck | Tasks 2–3 |
| Stop auto-scroll when scrolled up | Task 2 |
| **Jump to latest** English label + aria-label | Task 2 |
| Force stick on send | Task 3 |
| Reset stick on conversation / draft switch | Task 3 |
| rAF coalesce during streaming | Task 3 |
| Tests + production build | Tasks 1–3 |
| No API / mode / Stop changes | All tasks |
