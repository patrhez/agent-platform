# Conversation Delete Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users delete historical conversations from the sidebar via single delete and multi-select bulk delete, using existing soft-delete semantics plus a new bulk API.

**Architecture:** Backend adds transactional `DeleteConversations` and `POST /api/v1/conversations/delete`. Frontend keeps list state in `App`, adds a presentational selection mode in `ConversationList`, and uses a shared `ConfirmDialog`. Deleting the active conversation clears the main panel into local draft mode.

**Tech Stack:** Go (GORM / gorm-gen query), `net/http` ServeMux, React 19, TypeScript, Vite, Vitest, Testing Library

**Spec:** `docs/superpowers/specs/2026-07-26-conversation-delete-design.md`

## Global Constraints

- All documentation, UI copy, commit messages, comments, and code identifiers for this feature must be in English.
- Go code must follow `.superpowers/sdd/golang-coding-standards.md` (exported GoDoc, wrapped errors with `%w`, gofmt, go vet, boundary validation, no ignored errors, functions focused and preferably ≤80 lines / ≤120 chars per line).
- Soft delete only via existing `conversations.deleted_at` (`gorm.DeletedAt`). No schema migration.
- Do not cascade-delete Messages/Runs; do not cancel active runs on delete.
- Bulk delete is all-or-nothing in one transaction (no partial success).
- After deleting the active conversation, enter local draft mode; do not auto-select another conversation.
- No optimistic UI removal; wait for the server response.
- Keep changes minimal and reviewable; no unrelated refactors.

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/store/api.go` | Add `DeleteConversations`; optionally route single delete through it |
| `backend/internal/store/delete_test.go` | sqlmock coverage for bulk soft delete success/failure |
| `backend/internal/httpapi/server.go` | Extend `ConversationStore`; register bulk route |
| `backend/internal/httpapi/conversations.go` | Bulk handler + ID normalization helper |
| `backend/internal/httpapi/delete_ids_test.go` | Table-driven tests for ID normalization |
| `frontend/src/api/client.ts` | `deleteConversation` / `deleteConversations` |
| `frontend/src/components/ConfirmDialog.tsx` | Controlled confirm dialog |
| `frontend/src/components/ConfirmDialog.test.tsx` | Dialog interaction tests |
| `frontend/src/components/ConversationList.tsx` | Hover delete + selection mode UI |
| `frontend/src/components/ConversationList.test.tsx` | List interaction tests |
| `frontend/src/App.tsx` | Selection/confirm/delete orchestration |
| `frontend/src/App.test.tsx` | Active-conversation delete → draft; bulk flow |
| `frontend/src/app.css` | Sidebar selection + dialog styles |

---

### Task 1: Store Bulk Soft Delete

**Files:**
- Modify: `backend/internal/store/api.go`
- Create: `backend/internal/store/delete_test.go`

**Interfaces:**
- Consumes: existing `lockConversation`, `ErrUnauthorized`, `ErrConversationNotFound`, GORM soft `Delete`
- Produces: `func (store *Store) DeleteConversations(ctx context.Context, userID string, conversationIDs []string) error`

- [ ] **Step 1: Write the failing store tests**

Create `backend/internal/store/delete_test.go` using the same sqlmock + GORM MySQL pattern as `cancel_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDeleteConversationsSoftDeletesOwnedRows(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDatabase,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open GORM: %v", err)
	}

	now := time.Now().UTC()
	conversationColumns := []string{
		"id", "user_id", "title", "next_message_seq", "next_run_seq", "next_executable_run_seq",
		"created_at", "updated_at",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-1", "user-1", "One", int64(1), int64(1), int64(1), now, now))
	mock.ExpectExec("UPDATE `conversations` SET `deleted_at`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-2", "user-1", "Two", int64(1), int64(1), int64(1), now, now))
	mock.ExpectExec("UPDATE `conversations` SET `deleted_at`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := New(database).DeleteConversations(
		context.Background(),
		"user-1",
		[]string{"conversation-1", "conversation-2"},
	); err != nil {
		t.Fatalf("DeleteConversations() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestDeleteConversationsRejectsForeignOwnerWithoutPartialDelete(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDatabase,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open GORM: %v", err)
	}

	now := time.Now().UTC()
	conversationColumns := []string{
		"id", "user_id", "title", "next_message_seq", "next_run_seq", "next_executable_run_seq",
		"created_at", "updated_at",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-1", "user-1", "One", int64(1), int64(1), int64(1), now, now))
	mock.ExpectExec("UPDATE `conversations` SET `deleted_at`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .*FROM `conversations`.*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows(conversationColumns).
			AddRow("conversation-2", "other-user", "Two", int64(1), int64(1), int64(1), now, now))
	mock.ExpectRollback()

	err = New(database).DeleteConversations(
		context.Background(),
		"user-1",
		[]string{"conversation-1", "conversation-2"},
	)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("DeleteConversations() error = %v, want ErrUnauthorized", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
```

Adjust SQL regexes if GORM emits slightly different statements; keep the behavioral assertions (commit vs rollback, `ErrUnauthorized`).

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd backend && go test ./internal/store -run 'TestDeleteConversations' -count=1
```

Expected: FAIL because `DeleteConversations` is undefined.

- [ ] **Step 3: Implement `DeleteConversations` and reuse it from single delete**

In `backend/internal/store/api.go`, replace the body of `DeleteConversation` and add:

```go
// DeleteConversation soft-deletes a user-owned Conversation from the normal UI.
func (store *Store) DeleteConversation(ctx context.Context, userID string, conversationID string) error {
	return store.DeleteConversations(ctx, userID, []string{conversationID})
}

// DeleteConversations soft-deletes the given user-owned Conversations in one transaction.
func (store *Store) DeleteConversations(ctx context.Context, userID string, conversationIDs []string) error {
	queries := query.Use(store.database)
	err := queries.Transaction(func(transaction *query.Query) error {
		for _, conversationID := range conversationIDs {
			conversation, err := lockConversation(ctx, transaction, conversationID)
			if err != nil {
				return err
			}
			if conversation.UserID != userID {
				return ErrUnauthorized
			}
			if _, err := transaction.Conversation.WithContext(ctx).Delete(conversation); err != nil {
				return fmt.Errorf("soft-delete Conversation %s: %w", conversationID, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete Conversations: %w", err)
	}
	return nil
}
```

Notes:
- Keep GoDoc on both exported methods; end with a period.
- Do not validate empty/max here; HTTP boundary owns request validation.
- Preserve soft-delete behavior (GORM `Delete` sets `deleted_at`).

- [ ] **Step 4: Run store tests**

Run:

```bash
cd backend && gofmt -w internal/store/api.go internal/store/delete_test.go && go vet ./internal/store && go test ./internal/store -run 'TestDeleteConversations' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/api.go backend/internal/store/delete_test.go
git commit -m "$(cat <<'EOF'
feat(store): soft-delete conversations in bulk

EOF
)"
```

---

### Task 2: HTTP Bulk Delete Endpoint

**Files:**
- Modify: `backend/internal/httpapi/server.go`
- Modify: `backend/internal/httpapi/conversations.go`
- Create: `backend/internal/httpapi/delete_ids_test.go`

**Interfaces:**
- Consumes: `store.DeleteConversations(ctx, userID, ids)`
- Produces:
  - `POST /api/v1/conversations/delete` with body `{ "ids": string[] }` → `204`
  - `normalizeDeleteConversationIDs(ids []string) ([]string, error)`
  - `ConversationStore.DeleteConversations(context.Context, string, []string) error`

- [ ] **Step 1: Write failing ID normalization tests**

Create `backend/internal/httpapi/delete_ids_test.go`:

```go
package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizeDeleteConversationIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ids     []string
		want    []string
		wantErr string
	}{
		{
			name: "deduplicates preserving order",
			ids:  []string{"a", "b", "a"},
			want: []string{"a", "b"},
		},
		{
			name:    "rejects empty slice",
			ids:     nil,
			wantErr: "ids must be a non-empty array",
		},
		{
			name:    "rejects empty string",
			ids:     []string{"a", ""},
			wantErr: "ids must not contain empty values",
		},
		{
			name:    "rejects more than 100 unique ids",
			ids:     makeUniqueIDs(101),
			wantErr: "ids must contain at most 100 unique values",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeDeleteConversationIDs(testCase.ids)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("got %#v, want %#v", got, testCase.want)
			}
			for index := range testCase.want {
				if got[index] != testCase.want[index] {
					t.Fatalf("got %#v, want %#v", got, testCase.want)
				}
			}
		})
	}
}

func makeUniqueIDs(count int) []string {
	ids := make([]string, count)
	for index := 0; index < count; index++ {
		ids[index] = string(rune('a'+(index%26))) + strings.Repeat("x", index/26+1)
	}
	return ids
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd backend && go test ./internal/httpapi -run 'TestNormalizeDeleteConversationIDs' -count=1
```

Expected: FAIL because `normalizeDeleteConversationIDs` is undefined.

- [ ] **Step 3: Implement normalization, handler, route, and interface method**

In `backend/internal/httpapi/server.go`, add to `ConversationStore`:

```go
DeleteConversations(context.Context, string, []string) error
```

Register:

```go
mux.HandleFunc("POST /api/v1/conversations/delete", server.deleteConversations)
```

Place it next to the other conversation routes.

In `backend/internal/httpapi/conversations.go`, add:

```go
const maxDeleteConversationIDs = 100

type deleteConversationsRequest struct {
	IDs []string `json:"ids"`
}

// normalizeDeleteConversationIDs validates and deduplicates bulk-delete IDs.
func normalizeDeleteConversationIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("ids must be a non-empty array")
	}
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, fmt.Errorf("ids must not contain empty values")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	if len(normalized) > maxDeleteConversationIDs {
		return nil, fmt.Errorf("ids must contain at most %d unique values", maxDeleteConversationIDs)
	}
	return normalized, nil
}

func (server *Server) deleteConversations(responseWriter http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	principal, err := requestPrincipal(ctx)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusUnauthorized, "unauthenticated", err)
		return
	}
	body, ok := decodeJSON[deleteConversationsRequest](server.logger, responseWriter, request)
	if !ok {
		return
	}
	ids, err := normalizeDeleteConversationIDs(body.IDs)
	if err != nil {
		writeError(ctx, server.logger, responseWriter, http.StatusBadRequest, "invalid_ids", err)
		return
	}
	if err := server.store.DeleteConversations(ctx, principal.UserID(), ids); err != nil {
		writeStoreError(ctx, server.logger, responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run httpapi + store tests and vet**

Run:

```bash
cd backend && gofmt -w internal/httpapi/server.go internal/httpapi/conversations.go internal/httpapi/delete_ids_test.go && go vet ./internal/httpapi ./internal/store && go test ./internal/httpapi ./internal/store -count=1
```

Expected: PASS (fix `makeUniqueIDs` if uniqueness collisions appear).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpapi/server.go backend/internal/httpapi/conversations.go backend/internal/httpapi/delete_ids_test.go
git commit -m "$(cat <<'EOF'
feat(api): add bulk conversation delete endpoint

EOF
)"
```

---

### Task 3: Frontend API Client

**Files:**
- Modify: `frontend/src/api/client.ts`
- Modify: `frontend/src/App.test.tsx` (mock stubs only, so later App tests compile)

**Interfaces:**
- Produces:
  - `api.deleteConversation(id: string): Promise<void>`
  - `api.deleteConversations(ids: string[]): Promise<void>`

- [ ] **Step 1: Add client methods**

In `frontend/src/api/client.ts`, extend `api`:

```ts
deleteConversation: (id: string) =>
  request<void>(`/conversations/${id}`, { method: "DELETE" }),
deleteConversations: (ids: string[]) =>
  request<void>("/conversations/delete", {
    method: "POST",
    body: JSON.stringify({ ids }),
  }),
```

Existing `request` already maps HTTP 204 to `undefined`.

- [ ] **Step 2: Update App test mocks**

In `frontend/src/App.test.tsx`, add to the `api` mock object:

```ts
deleteConversation: vi.fn(),
deleteConversations: vi.fn(),
```

- [ ] **Step 3: Typecheck via tests/build**

Run:

```bash
cd frontend && npm test -- --run App.test.tsx
```

Expected: existing App tests still PASS.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/api/client.ts frontend/src/App.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): add conversation delete API client methods

EOF
)"
```

---

### Task 4: ConfirmDialog Component

**Files:**
- Create: `frontend/src/components/ConfirmDialog.tsx`
- Create: `frontend/src/components/ConfirmDialog.test.tsx`
- Modify: `frontend/src/app.css`

**Interfaces:**
- Produces:

```ts
type ConfirmDialogProps = {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
};
```

- [ ] **Step 1: Write failing dialog tests**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "./ConfirmDialog";

describe("ConfirmDialog", () => {
  it("confirms and cancels", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(
      <ConfirmDialog
        open
        title="Delete conversation?"
        message="Demo title"
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );

    expect(screen.getByRole("dialog", { name: "Delete conversation?" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("cancels on Escape and ignores actions while busy", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    const { rerender } = render(
      <ConfirmDialog
        open
        title="Delete conversation?"
        message="Demo title"
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );

    await user.keyboard("{Escape}");
    expect(onCancel).toHaveBeenCalledOnce();

    rerender(
      <ConfirmDialog
        open
        title="Delete conversation?"
        message="Demo title"
        busy
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd frontend && npm test -- --run ConfirmDialog.test.tsx
```

Expected: FAIL because the component does not exist.

- [ ] **Step 3: Implement `ConfirmDialog` and styles**

```tsx
import { useEffect } from "react";

type ConfirmDialogProps = {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
};

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = "Delete",
  busy = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) onCancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, busy, onCancel]);

  if (!open) return null;

  return (
    <div className="confirm-backdrop" onClick={() => { if (!busy) onCancel(); }}>
      <div
        className="confirm-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-dialog-title"
        onClick={(event) => event.stopPropagation()}
      >
        <h3 id="confirm-dialog-title">{title}</h3>
        <p>{message}</p>
        <div className="confirm-actions">
          <button type="button" disabled={busy} onClick={onCancel}>Cancel</button>
          <button type="button" className="danger" disabled={busy} onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
```

Add CSS using existing danger tokens:

```css
.confirm-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(28, 25, 23, 0.35);
  display: grid;
  place-items: center;
  z-index: 50;
}
.confirm-dialog {
  width: min(420px, calc(100vw - 32px));
  padding: 20px;
  border-radius: var(--radius-md);
  background: var(--surface);
  border: 1px solid var(--border);
  box-shadow: var(--shadow-md);
  color: var(--text);
}
.confirm-dialog h3 {
  margin: 0 0 8px;
  font-size: 16px;
}
.confirm-dialog p {
  margin: 0 0 18px;
  color: var(--text-secondary);
  font-size: 14px;
  line-height: 1.45;
  overflow-wrap: anywhere;
}
.confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.confirm-actions button {
  padding: 8px 12px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
}
.confirm-actions button.danger {
  border-color: var(--danger-border);
  background: var(--danger);
  color: #fff;
}
.confirm-actions button:disabled {
  opacity: 0.6;
}
```

- [ ] **Step 4: Run dialog tests**

Run:

```bash
cd frontend && npm test -- --run ConfirmDialog.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ConfirmDialog.tsx frontend/src/components/ConfirmDialog.test.tsx frontend/src/app.css
git commit -m "$(cat <<'EOF'
feat(web): add shared confirm dialog

EOF
)"
```

---

### Task 5: ConversationList Selection and Hover Delete

**Files:**
- Modify: `frontend/src/components/ConversationList.tsx`
- Modify: `frontend/src/components/ConversationList.test.tsx`
- Modify: `frontend/src/app.css`

**Interfaces:**
- Consumes: selection props from `App`
- Produces presentational callbacks:

```ts
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
```

- [ ] **Step 1: Extend failing list tests**

Update `ConversationList.test.tsx` to cover:

1. Hover/focus delete button calls `onRequestDelete` with the conversation id.
2. Select mode shows checkboxes; clicking a row calls `onToggleSelected`, not `onSelect`.
3. Bulk Delete is disabled at 0 selected and enabled when `selectedIDs` is non-empty.
4. Select control is absent when `conversations` is empty.

Example cases:

```tsx
const sample = [
  { id: "c1", title: "Alpha", createdAt: "2026-07-26T00:00:00Z", updatedAt: "2026-07-26T00:00:00Z" },
  { id: "c2", title: "Beta", createdAt: "2026-07-26T00:00:00Z", updatedAt: "2026-07-26T00:00:00Z" },
];

it("requests single delete from a conversation row", async () => {
  const onRequestDelete = vi.fn();
  render(
    <ConversationList
      conversations={sample}
      selecting={false}
      selectedIDs={new Set()}
      onCreate={vi.fn()}
      onSelect={vi.fn()}
      onToggleSelectMode={vi.fn()}
      onToggleSelected={vi.fn()}
      onSelectAll={vi.fn()}
      onRequestDelete={onRequestDelete}
      onRequestBulkDelete={vi.fn()}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Delete conversation" }));
  // If multiple delete buttons exist, scope to the Alpha row first.
  expect(onRequestDelete).toHaveBeenCalledWith("c1");
});

it("toggles selection instead of activating a conversation while selecting", async () => {
  const onSelect = vi.fn();
  const onToggleSelected = vi.fn();
  render(
    <ConversationList
      conversations={sample}
      selecting
      selectedIDs={new Set(["c1"])}
      onCreate={vi.fn()}
      onSelect={onSelect}
      onToggleSelectMode={vi.fn()}
      onToggleSelected={onToggleSelected}
      onSelectAll={vi.fn()}
      onRequestDelete={vi.fn()}
      onRequestBulkDelete={vi.fn()}
    />,
  );
  await userEvent.click(screen.getByText("Beta"));
  expect(onToggleSelected).toHaveBeenCalledWith("c2");
  expect(onSelect).not.toHaveBeenCalled();
});
```

Use accessible names:
- Select mode toggle: `Select` / `Cancel`
- Bulk action: `Delete`
- Select all: `Select all`
- Per-item delete: `Delete conversation` (hide these buttons when `selecting` is true)

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd frontend && npm test -- --run ConversationList.test.tsx
```

Expected: FAIL on missing props/UI.

- [ ] **Step 3: Implement list UI**

Rewrite `ConversationList` to:

- Show header row with `Conversations` + `Select`/`Cancel` when `conversations.length > 0`
- In normal mode: row button selects conversation; include an inner delete button that stops propagation and calls `onRequestDelete`
- In selection mode: hide per-item delete; show checkbox + row toggle via `onToggleSelected`
- Show action bar when selecting: `Select all`, `${n} selected`, `Delete` (disabled when `n === 0`)

Suggested structure:

```tsx
export function ConversationList(props: ConversationListProps) {
  const selectedCount = props.selectedIDs.size;
  return (
    <aside>
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
          <button type="button" className="text-button" onClick={props.onSelectAll}>Select all</button>
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
          <div key={item.id} className={item.id === props.activeID ? "selected conversation-row" : "conversation-row"}>
            <button type="button" className="conversation-main" onClick={() => props.onSelect(item.id)}>
              <span className="conversation-title">{item.title}</span>
            </button>
            <button
              type="button"
              className="conversation-delete"
              aria-label="Delete conversation"
              onClick={() => props.onRequestDelete(item.id)}
            >
              ×
            </button>
          </div>
        );
      })}
    </aside>
  );
}
```

Style so `.conversation-delete` is visible on `.conversation-row:hover` and `.conversation-row:focus-within`. Keep warm-gray sidebar look; use `--danger` for destructive text actions.

If multiple `Delete conversation` buttons make the first test brittle, scope with `within(row)`.

- [ ] **Step 4: Run list tests**

Run:

```bash
cd frontend && npm test -- --run ConversationList.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ConversationList.tsx frontend/src/components/ConversationList.test.tsx frontend/src/app.css
git commit -m "$(cat <<'EOF'
feat(web): add conversation list select and delete controls

EOF
)"
```

---

### Task 6: App Orchestration

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/App.test.tsx`

**Interfaces:**
- Consumes: `api.deleteConversation`, `api.deleteConversations`, `ConfirmDialog`, updated `ConversationList`
- Produces App-owned state:

```ts
selecting: boolean
selectedIDs: Set<string>
confirm: null | { kind: "single"; id: string } | { kind: "bulk"; ids: string[] }
deleting: boolean
```

- [ ] **Step 1: Write failing App tests for delete flows**

Extend `App.test.tsx`:

```tsx
describe("App conversation delete flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listConversations)
      .mockResolvedValueOnce({ conversations: [existing, second] })
      .mockResolvedValue({ conversations: [second] });
    vi.mocked(api.getConversation).mockResolvedValue(existingDetail);
    vi.mocked(api.deleteConversation).mockResolvedValue(undefined);
    vi.mocked(api.deleteConversations).mockResolvedValue(undefined);
  });

  it("deletes the active conversation and enters draft mode", async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText("existing message");

    await user.click(screen.getAllByRole("button", { name: "Delete conversation" })[0]);
    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(api.deleteConversation).toHaveBeenCalledWith(existing.id));
    expect(screen.queryByText("existing message")).toBeNull();
    expect(api.createConversation).not.toHaveBeenCalled();
  });

  it("bulk deletes selected conversations in one request", async () => {
    const user = userEvent.setup();
    vi.mocked(api.listConversations)
      .mockReset()
      .mockResolvedValueOnce({ conversations: [existing, second] })
      .mockResolvedValue({ conversations: [] });
    render(<App />);
    await screen.findByText("Existing");

    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByText("Existing"));
    await user.click(screen.getByText("Second"));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("dialog").querySelector("button.danger")!); // or getByRole Delete inside dialog

    await waitFor(() => expect(api.deleteConversations).toHaveBeenCalledWith(
      expect.arrayContaining([existing.id, second.id]),
    ));
  });
});
```

Define `second` fixture symmetrically with `existing`. For dialog Delete vs bar Delete, prefer:

```ts
const dialog = await screen.findByRole("dialog");
await user.click(within(dialog).getByRole("button", { name: "Delete" }));
```

- [ ] **Step 2: Run App tests to verify new cases fail**

Run:

```bash
cd frontend && npm test -- --run App.test.tsx
```

Expected: FAIL on missing wiring.

- [ ] **Step 3: Wire App state and handlers**

In `App.tsx`:

```tsx
const [selecting, setSelecting] = useState(false);
const [selectedIDs, setSelectedIDs] = useState<Set<string>>(new Set());
const [confirm, setConfirm] = useState<
  null | { kind: "single"; id: string } | { kind: "bulk"; ids: string[] }
>(null);
const [deleting, setDeleting] = useState(false);

const clearSelectionMode = () => {
  setSelecting(false);
  setSelectedIDs(new Set());
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
  if (!confirm || deleting) return;
  setDeleting(true);
  setError("");
  try {
    const deletedIDs = confirm.kind === "single" ? [confirm.id] : confirm.ids;
    if (confirm.kind === "single") {
      await api.deleteConversation(confirm.id);
    } else {
      await api.deleteConversations(confirm.ids);
    }
    await loadConversationList();
    if (activeID && deletedIDs.includes(activeID)) {
      startDraft();
    }
    setConfirm(null);
    clearSelectionMode();
  } catch (cause) {
    setError(String(cause));
  } finally {
    setDeleting(false);
  }
};
```

Confirm dialog copy:

- Single: title `Delete conversation?`; message = conversation title or `This conversation`
- Bulk: title `Delete N conversations?`; message `Deleted conversations are removed from your list and cannot be restored.`

Pass new props into `ConversationList`. Render:

```tsx
<ConfirmDialog
  open={confirm !== null}
  title={/* as above */}
  message={/* as above */}
  busy={deleting}
  onCancel={() => { if (!deleting) setConfirm(null); }}
  onConfirm={() => { void confirmDelete(); }}
/>
```

Selection helpers:

```tsx
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
```

- [ ] **Step 4: Run frontend tests**

Run:

```bash
cd frontend && npm test -- --run
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.tsx frontend/src/App.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): wire conversation delete and multi-select flows

EOF
)"
```

---

### Task 7: Verification

**Files:**
- None expected unless tests/build reveal gaps

- [ ] **Step 1: Backend full package checks**

Run:

```bash
cd backend && gofmt -l . && go vet ./... && go test ./internal/store ./internal/httpapi -count=1
```

Expected: no gofmt diffs; vet clean; tests PASS.

- [ ] **Step 2: Frontend production build**

Run:

```bash
cd frontend && npm test -- --run && npm run build
```

Expected: tests PASS; TypeScript build succeeds.

- [ ] **Step 3: Manual acceptance checklist**

Verify against the spec:

1. Single delete with confirm removes one conversation from the list.
2. Multi-select bulk delete sends one `POST /conversations/delete` and clears selection mode.
3. Deleting the active conversation opens blank draft (composer available, no auto-select).
4. Cancel/Escape leaves data unchanged.
5. Soft-deleted rows remain in DB with `deleted_at` set (optional DB check if local MySQL is available).

- [ ] **Step 4: Final commit only if verification required fixes**

If fixes were needed, commit them with a focused message such as:

```bash
git commit -m "$(cat <<'EOF'
fix: harden conversation delete edge cases

EOF
)"
```

Otherwise skip.

---

## Spec Coverage Check

| Spec requirement | Task |
|---|---|
| Soft delete via existing `deleted_at` | Task 1 |
| Keep single `DELETE /conversations/{id}` | Task 1 (reuse) + existing handler |
| Bulk `POST /conversations/delete` all-or-nothing | Task 1 + Task 2 |
| ID validation / max 100 / dedupe | Task 2 |
| Frontend API methods | Task 3 |
| In-app confirm dialog | Task 4 |
| Hover single delete + Select mode + Select all | Task 5 |
| Active delete → draft, no auto-select | Task 6 |
| No optimistic update; header errors | Task 6 |
| Tests for store/HTTP/UI | Tasks 1–6 |
| Out of scope (cascade, cancel runs, undo, pagination) | Not implemented |

## Execution Notes

- Prefer sqlmock expectation tweaks over changing product behavior if GORM SQL shape differs slightly.
- Keep UI English strings exactly as in the spec.
- Do not introduce React Query/Zustand; continue with `useState` in `App`.
