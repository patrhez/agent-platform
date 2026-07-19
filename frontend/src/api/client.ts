export type Conversation = { id: string; title: string; createdAt: string; updatedAt: string; latestMessageAt?: string };
export type Message = { id: string; conversationId: string; seq: number; role: "user" | "assistant"; content: string; status: string; runId?: string; createdAt: string };
export type Run = { id: string; conversationId: string; triggerMessageId: string; queueSeq: number; status: string; attempt: number; errorCode?: string; finishedAt?: string; createdAt: string };
export type ConversationDetail = { conversation: Conversation; messages: Message[]; runs: Run[] };
export type RunTrace = { steps: TraceStep[]; toolCalls: ToolCall[] };
export type TraceStep = { id: string; stepNo: number; kind: string; status: string; safeSummary: string; startedAt?: string; finishedAt?: string; createdAt: string };
export type ToolCall = { id: string; stepNo: number; serverKey: string; toolName: string; arguments: unknown; resultSummary: string; status: string; createdAt: string; updatedAt: string };

const apiRoot = "/api/v1";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${apiRoot}${path}`, { ...init, headers: { "Content-Type": "application/json", ...init?.headers } });
  if (!response.ok) throw new Error((await response.json().catch(() => null))?.message ?? `Request failed (${response.status})`);
  return response.status === 204 ? (undefined as T) : (response.json() as Promise<T>);
}

export const api = {
  listConversations: () => request<{ conversations: Conversation[] }>("/conversations"),
  getConversation: (id: string) => request<ConversationDetail>(`/conversations/${id}`),
  createConversation: (content: string) => request<{ conversation: Conversation; runId?: string }>("/conversations", { method: "POST", body: JSON.stringify({ content }) }),
  sendMessage: (conversationID: string, content: string, clientMessageId: string) => request<{ messageId: string; runId: string; status: string }>(`/conversations/${conversationID}/messages`, { method: "POST", body: JSON.stringify({ content, clientMessageId }) }),
  getTrace: (runID: string) => request<RunTrace>(`/runs/${runID}/trace`),
  cancelRun: (runID: string) => request(`/runs/${runID}/cancel`, { method: "POST" }),
};

export function eventURL(runID: string, after = 0): string { return `${apiRoot}/runs/${runID}/events?after=${after}`; }
