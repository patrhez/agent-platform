# Collapsible Conversation List Design

## Goal

Let users fully hide the left conversation list so the chat column can use the full viewport width, and restore the list with the same control. Remember the open/closed preference across reloads.

## Non-Goals

- Icon-only collapsed rail or floating drawer overlay
- Backend or API changes
- Changing conversation list / delete / selection behavior beyond exiting selection mode when the sidebar closes
- Mobile-specific drawer redesign (existing responsive rules stay)

## User Experience

### Toggle control

- Place a toggle button in the chat column header, left of the title (`Issue Troubleshooter Agent`).
- The button is always visible whether the sidebar is open or closed.
- Labels / accessible names:
  - When open: collapse action (e.g. `aria-label="Hide conversations"`)
  - When closed: expand action (e.g. `aria-label="Show conversations"`)
- Expose `aria-expanded` reflecting sidebar visibility and `aria-controls` pointing at the sidebar region id.

### Open state

- Layout matches today: ~272px sidebar + chat column.
- Conversation list, new conversation, select/delete flows unchanged.

### Closed state

- Sidebar is fully removed from layout flow (no reserved width).
- Chat column expands to use the available width (still centered with its existing max-width rules).
- Conversation list content is not interactive while hidden.
- If multi-select mode is active when the user collapses, exit selection mode and clear `selectedIDs` so hidden bulk-delete state cannot linger.

### Persistence

- Store preference in `localStorage` under key `agent-platform.sidebarOpen`.
- Value: `"true"` | `"false"` (string).
- On first visit / missing / invalid value: default to open (`true`).
- Toggle updates React state and writes `localStorage` immediately.

### Motion

- Prefer a short width transition on the layout if cheap to add with CSS.
- Instant open/close is acceptable if transition conflicts with `display: none` or grid collapse; correctness and accessibility come first.

## Architecture

| Layer | Change |
|---|---|
| `App` | Own `sidebarOpen` state; initialize from `localStorage`; toggle handler; pass open state / sidebar id into layout; exit selection mode on collapse |
| Chat header (`App` markup) | Render always-visible toggle next to `h1` |
| `ConversationList` / `aside` | Add stable `id` for `aria-controls`; optionally accept `hidden` / omit from DOM when closed |
| `app.css` | `.layout.sidebar-collapsed` (or equivalent) uses single-column grid; style header toggle |
| Tests | Cover default open, toggle, persistence key write, collapse clears selection mode |

### Recommended collapse mechanism

Use a class on `.layout` driven by `sidebarOpen`:

- Open: `grid-template-columns: 272px 1fr` (current)
- Closed: `grid-template-columns: 1fr` and hide the aside (`display: none` or do not render)

Either hiding via CSS or conditional render is fine; prefer conditional render or `hidden` + `display: none` so focus cannot land inside a visually removed sidebar.

### Data flow

```
load App
  → read localStorage agent-platform.sidebarOpen
  → sidebarOpen = parsed or true

click toggle
  → next = !sidebarOpen
  → if !next && selecting → clear selection mode
  → setSidebarOpen(next)
  → localStorage.setItem(..., String(next))
```

## Accessibility

- Toggle is a real `<button type="button">`.
- `aria-expanded={sidebarOpen}`.
- `aria-controls="conversation-sidebar"` (or equivalent id on the aside).
- When closed, sidebar is not focusable / not in tab order.
- Focus remains on the toggle after collapse/expand (default button behavior; no forced focus jump required).

## Testing

- Unit/component: header toggle flips layout class / sidebar presence.
- Persistence: toggling writes the expected `localStorage` key; initial render respects a pre-seeded value.
- Selection: collapsing while `selecting === true` clears selection mode and `selectedIDs`.
- Existing ConversationList / delete tests remain green; no API mock changes required.

## Rollout

Frontend-only change. No migration, feature flag, or backend deploy dependency.
