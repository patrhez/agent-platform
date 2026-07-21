# Pinned Composer and Smart Auto-Scroll Design

## Goal

Keep the chat composer visually stable while the agent streams output, and auto-scroll the message list only when the user is following the latest content.

Today the whole page grows with streaming text, which pushes the composer down and interrupts typing. The composer must stay fixed at the bottom of the chat column; only the message list scrolls.

## User Experience

### Layout

- The right-hand chat column is a viewport-height flex column:
  1. **Header** — title and error; does not scroll.
  2. **Messages** — `flex: 1; overflow-y: auto`; the only scrolling region for chat content.
  3. **Composer** — always pinned to the bottom of the column; never moves when agent output grows.
- The sidebar is unchanged.
- Message list bottom padding keeps the last bubble from feeling cramped against the composer.

### Smart auto-scroll

- The message list is **stuck** when the user is within ~80px of the bottom.
- While stuck, streaming draft updates and new messages scroll to the bottom.
- When the user scrolls up past the threshold, stick turns off and auto-scroll stops.
- Stick turns back on when the user scrolls to the bottom again, or clicks **Jump to latest**.

### Jump to latest

- A floating control sits at the bottom-right of the message area, above the composer.
- It is visible only while the list is not stuck.
- Label and `aria-label` are English: **Jump to latest**.
- Clicking it scrolls to the bottom and restores stick.

### Forced stick moments

- After the user sends a message, always scroll to bottom and stick, even if they had scrolled up.
- Switching conversations (or starting a new local draft) resets to stuck and scrolls to the bottom of that view.

## Approach

Use an internal scroll container (flex column), not `position: sticky` on the form and not viewport-`fixed` composer.

Rationale: keeps the composer aligned with the existing `max-width: 960px` chat column, avoids fighting document scroll with the header/sidebar, and makes stick detection a simple `scrollTop` / `scrollHeight` check on one element.

## State and Component Boundaries

- Logic lives in `Chat.tsx`:
  - ref on the messages container;
  - `stuckToBottom` boolean (default `true`);
  - scroll listener to update stick from the distance-to-bottom threshold;
  - effect (or equivalent) that scrolls when stuck and content changes (`draft`, messages/turns);
  - **Jump to latest** button rendered over the messages region.
- Layout/CSS changes in `app.css` (and any minimal `App.tsx` height wiring needed so the chat column fills the viewport under the header).
- No API or backend changes.
- No new global app state beyond what `Chat` already receives.

## Performance

- Coalesce scroll-to-bottom calls during rapid token streaming (e.g. `requestAnimationFrame`) so each frame at most one `scrollTop` write.
- Do not force scroll while not stuck.

## Error Handling / Edge Cases

- Empty conversation or draft-only view: composer remains pinned; scroll behavior still applies when content appears.
- Composer height growth (multi-line textarea): flex shrinks the messages region; if stuck, remain at bottom after layout.
- Window resize: same as composer height change.
- Restricted environments where scroll APIs behave oddly: prefer no-op over throwing; stick state remains consistent with the last successful measurement.

## Testing

- Composer is outside the scrollable messages region and stays at the bottom of the chat column.
- While stuck, draft growth scrolls the messages container to the bottom.
- Scrolling up disables auto-scroll and shows **Jump to latest**.
- Clicking **Jump to latest** scrolls to bottom and restores stick.
- Sending a message forces stick and scrolls to bottom.
- Conversation switch resets to stuck.
- Existing Chat tests continue to pass; extend `Chat.test.tsx` for the new behaviors.
- Production frontend build still typechecks and bundles.

## Out of Scope

- Changing follow-up modes, Stop, or streaming event protocol.
- Persisting stick preference across reloads.
- Virtualized message lists.
- Mobile-specific gesture handling beyond the shared responsive layout.
- Redesigning header or sidebar chrome.
