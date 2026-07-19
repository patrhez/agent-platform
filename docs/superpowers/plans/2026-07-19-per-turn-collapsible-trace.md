# Per-Turn Collapsible Execution Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a default-collapsed, per-turn Execution Trace under each user message and above that turn’s assistant answer, with English progress-style summaries.

**Architecture:** Refactor `Chat` to render turns bound by `Run.triggerMessageId`. Embed `RunTrace` (and turn-local `RunStatus`) inside each turn. Active runs keep using SSE events from `App` state; each `RunTrace` still loads `GET /runs/{id}/trace` for persisted steps.

**Tech Stack:** React + Vitest + Testing Library (existing frontend).

## Global Constraints

- All user-facing copy is English.
- Trace `<details>` must not set `open` by default (including while running).
- No backend API changes.
- Do not persist expand/collapse state.

---

### Task 1: RunTrace summary + default collapsed

**Files:**
- Modify: `frontend/src/components/RunTrace.tsx`
- Modify: `frontend/src/components/RunTrace.test.tsx`
- Modify: `frontend/src/app.css` (compact turn-level trace styles)

**Interfaces:**
- Consumes: `Run`, `RunEvent[]`, existing `api.getTrace`
- Produces: `RunTrace({ run, events })` with English `summaryLabel`; default collapsed

- [x] **Step 1: Write failing tests** for default-collapsed `<details>` and summary strings (`Running · code.search…`, `Used 1 tools · …`, `Failed`)
- [x] **Step 2: Implement** `run` prop, `traceSummary(...)`, remove `open`, keep timeline merge logic
- [x] **Step 3: Run** `npm test -- --run src/components/RunTrace.test.tsx` in `frontend/` — expect PASS
- [ ] **Step 4: Commit** (only if user asks)

---

### Task 2: Chat per-turn composition

**Files:**
- Modify: `frontend/src/components/Chat.tsx`
- Modify: `frontend/src/components/Chat.test.tsx`

**Interfaces:**
- Consumes: `messages`, `runs`, `draft`, `eventsByRunID: Record<string, RunEvent[]>`, `onSend`
- Produces: turns as user → `RunTrace` → `RunStatus?` → assistants/draft

- [x] **Step 1: Write failing tests** that a user message is followed by a collapsed trace summary before the assistant answer
- [x] **Step 2: Implement** turn assembly via `triggerMessageId` / `runId`
- [x] **Step 3: Run** Chat + RunTrace tests — expect PASS
- [ ] **Step 4: Commit** (only if user asks)

---

### Task 3: Wire App; remove page-level Trace

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/App.test.tsx` if needed

- [x] **Step 1: Pass** `eventsByRunID` into `Chat`; remove bottom `<RunTrace />` and page-footer-only `<RunStatus />`
- [x] **Step 2: Run** full frontend test suite — expect PASS
- [ ] **Step 3: Commit** (only if user asks)
