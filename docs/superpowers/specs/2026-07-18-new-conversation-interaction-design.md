# New Conversation Interaction Design

## Goal

Add an explicit **New conversation** action to the MVP web UI without creating permanent empty conversations.

## User Experience

- The conversation sidebar shows a prominent **+ New conversation** button above the saved conversation list.
- Clicking it switches the UI into a local draft state:
  - no saved conversation is selected;
  - the message area is empty;
  - the composer remains available for the first question.
- The first submitted message calls the existing `POST /api/v1/conversations` endpoint with the message content.
- After the API returns, the UI refreshes the conversation list, selects the new saved conversation, loads its messages and Run, and leaves the local draft state.
- Clicking an existing conversation while drafting abandons the empty local draft. Because the draft contains no persisted data, no cleanup is required.

## State and Component Boundaries

- `App` owns whether the UI is displaying a saved conversation or a local draft.
- `ConversationList` renders the new action and reports it through an `onCreate` callback.
- The API contract and backend remain unchanged.
- A local draft contains no title, ID, messages, Run, or server-side resource.

## Error Handling

- If first-message creation fails, the UI remains in draft state and shows the existing error message so the user can retry.
- The draft text continues to be owned by the composer during submission; this feature does not add persistent browser draft storage.
- Repeated clicks on **New conversation** are idempotent from the server’s perspective because they perform no network request.

## Testing

- A component-level test verifies that the sidebar exposes the new action and invokes `onCreate` when clicked.
- An application-state test verifies that entering draft mode clears the selected saved conversation without creating a server resource.
- A production frontend build verifies TypeScript and bundling.
- Manual browser verification covers creating a draft, sending its first message, and switching back to an existing conversation.

## Out of Scope

- Draft persistence across refreshes.
- User-provided conversation titles.
- Deleting empty or saved conversations.
- Multiple simultaneous local drafts.
