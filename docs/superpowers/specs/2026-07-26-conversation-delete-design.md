# Conversation Delete Design

## Goal

Let users remove historical conversations from the sidebar list via single delete and multi-select bulk delete. Deleted conversations leave the normal UI; durable rows remain via existing soft delete.

## Soft Delete Semantics

- Reuse the existing `conversations.deleted_at` column (`gorm.DeletedAt` on `model.Conversation`).
- No schema migration.
- Single delete already soft-deletes via `store.DeleteConversation` and `DELETE /api/v1/conversations/{conversationID}`.
- Bulk delete uses the same soft-delete semantics inside one transaction.
- Related `Message` / `Run` rows are not cascade-deleted in this change (MVP retention policy unchanged).
- Active runs are not cancelled on delete in this change.

## User Experience

### Normal sidebar

- When the list is non-empty, show a **Select** text button near the Conversations header.
- On hover or keyboard focus of a list item, show a delete icon button (`aria-label="Delete conversation"`).
- Clicking delete opens an in-app confirmation dialog; no network request yet.

### Selection mode

- Clicking **Select** enters selection mode; the control becomes **Cancel**.
- Each item shows a checkbox. Clicking a row toggles selection and does not switch the active conversation.
- Per-item hover delete is hidden in selection mode; deletion goes only through the bulk action bar.
- An action bar shows `N selected` and a **Delete** button (disabled when `N = 0`).
- Provide **Select all** / clear-all for the current loaded list.
- **Cancel** exits selection mode and clears the selection set.

### Confirmation dialog

- Shared lightweight in-app dialog consistent with existing UI (warm gray palette, existing button styles).
- Single delete:
  - Title: `Delete conversation?`
  - Body: conversation title (truncated if long)
- Bulk delete:
  - Title: `Delete N conversations?`
  - Body: explain that deleted conversations are removed from the list and cannot be restored from the UI
- Actions: **Cancel** (secondary) and **Delete** (primary/destructive)
- Escape and backdrop click equal Cancel
- While the delete request is in flight, both actions are disabled and the dialog shows busy state

### After successful delete

- Close the dialog and refresh the conversation list.
- If the deleted set includes `activeID`:
  - clear the main panel
  - enter local draft state (`drafting = true`, `activeID = undefined`)
  - do not auto-select another conversation
- After bulk delete, exit selection mode and clear `selectedIDs`.
- On failure: keep the list unchanged, allow closing the dialog, surface the error in the existing header error area.

## Architecture

| Layer | Change |
|---|---|
| Backend store | Add `DeleteConversations(ctx, userID, ids []string)` — one transaction, ownership checks, soft delete per ID |
| Backend HTTP | Keep single `DELETE /api/v1/conversations/{conversationID}`; add bulk `POST /api/v1/conversations/delete` |
| Frontend API | Add `deleteConversation(id)` and `deleteConversations(ids)` |
| `App` | Own selection mode, confirm intent, delete calls, list refresh, active-conversation fallback to draft |
| `ConversationList` | Render hover delete, selection mode UI, emit callbacks only |
| `ConfirmDialog` | New shared controlled confirm component |

### Data flow (bulk)

```
Select items → Delete → ConfirmDialog
  → api.deleteConversations(ids)
  → store soft-delete in one transaction
  → refresh list; if activeID deleted → draft mode
  → exit selection mode
```

## API Contract

### Single delete (existing)

`DELETE /api/v1/conversations/{conversationID}`

- Success: `204 No Content`
- Errors: `401 unauthenticated`, `403 forbidden`, `404 not_found`, `500 internal_error`

### Bulk delete (new)

`POST /api/v1/conversations/delete`

Request:

```json
{ "ids": ["01H...", "01H..."] }
```

Rules:

- `ids` required and non-empty; reject empty strings and non-string values with `400 invalid_ids`
- Deduplicate before processing
- Max 100 unique IDs per request; over limit → `400 invalid_ids`
- All-or-nothing transaction: if any ID is missing, already deleted, or not owned by the caller, roll back the entire batch

Success: `204 No Content`

Errors:

| Case | Status | Code |
|---|---|---|
| Unauthenticated | 401 | `unauthenticated` |
| Invalid JSON | 400 | `invalid_json` |
| Empty / over-limit / invalid ids | 400 | `invalid_ids` |
| Any ID not owned | 403 | `forbidden` |
| Any ID missing or already deleted | 404 | `not_found` |
| Other | 500 | `internal_error` |

`POST .../delete` is preferred over `DELETE /conversations` so the JSON body has a clear, non-ambiguous route.

### Frontend client

```ts
deleteConversation: (id: string) =>
  request<void>(`/conversations/${id}`, { method: "DELETE" })

deleteConversations: (ids: string[]) =>
  request<void>(`/conversations/delete`, {
    method: "POST",
    body: JSON.stringify({ ids }),
  })
```

## State and Component Boundaries

`App` owns:

```ts
selecting: boolean
selectedIDs: Set<string>
confirm: null
  | { kind: "single"; id: string }
  | { kind: "bulk"; ids: string[] }
deleting: boolean
```

`ConversationList` remains presentational: it receives selection props and callbacks (`onRequestDelete`, `onToggleSelectMode`, `onToggleSelected`, `onSelectAll`, `onRequestBulkDelete`, existing `onCreate` / `onSelect`).

`ConfirmDialog` is controlled and reusable; it does not call the API.

Selection mode is independent of draft mode. Deleting the active conversation enters draft mode and exits selection mode.

While `deleting` is true, the UI must not open another confirm dialog.

## Error Handling

- Frontend waits for the server response; no optimistic list removal.
- Bulk delete never partially applies from the UI’s perspective (server transaction guarantees this).
- Failed deletes leave `conversations` and `activeID` unchanged.
- Error message uses the existing App header error surface.

## Testing

### Backend

- Store: bulk soft-delete succeeds; mixed foreign/missing IDs fail with no `deleted_at` changes.
- HTTP: empty ids → 400; happy path → 204 and subsequent list omits deleted rows; ownership mismatch → 403.

### Frontend

- `ConversationList`: hover delete invokes `onRequestDelete`; selection mode toggles checkboxes and enables bulk delete when `N > 0`.
- `ConfirmDialog`: Confirm / Cancel / Escape behavior.
- App-level: deleting the active conversation enters drafting; bulk delete refreshes the list and exits selection mode.
- Production frontend build verifies TypeScript and bundling.

## Acceptance Criteria

1. User can delete a single historical conversation after confirming; it disappears from the list.
2. User can multi-select and bulk-delete after confirming; one request completes the batch.
3. Deleting the currently viewed conversation clears the main panel into blank draft state and does not auto-select another conversation.
4. Cancelling the confirm dialog changes no data.
5. Soft-deleted conversations are invisible to list/get, while the DB row remains with `deleted_at` set.

## Out of Scope

- Hard delete or cascade deletion of messages/runs
- Cancelling active runs on conversation delete
- Undo / recycle bin
- Conversation list pagination
- Draft persistence across refresh
- Context-menu or long-press delete as an alternate entry point
