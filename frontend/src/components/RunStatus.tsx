import type { Run } from "../api/client";

const fallbackMessages: Record<string, string> = {
  cancelled: "Run cancelled.",
  step_limit_exceeded: "The agent reached its maximum step limit before finishing.",
  llm_invalid_temperature: "The language model rejected the temperature setting. Check the agent model configuration and retry.",
  llm_invalid_request: "The language model rejected the request. Check the agent model configuration and retry.",
  llm_overload: "The language model is currently overloaded. Please retry in a moment.",
  llm_rate_limited: "The language model rate limit was exceeded. Please retry shortly.",
  llm_auth_error: "The language model rejected the API credentials. Check LLM_API_KEY and retry.",
  llm_timeout: "The language model request timed out. Please retry.",
  llm_unavailable: "The language model is temporarily unavailable. Please retry shortly.",
  runtime_error: "The run failed due to a runtime error. Check the execution trace or retry.",
};

export function runFailureMessage(run: Pick<Run, "errorCode" | "errorMessage">): string {
  if (run.errorMessage?.trim()) return run.errorMessage.trim();
  const code = run.errorCode || "runtime_error";
  return fallbackMessages[code] ?? `Run failed (${code}). Check the execution trace or retry.`;
}

export function RunStatus({ run }: { run?: Run }) {
  if (run?.status === "failed") {
    return <p className="run-status failed" role="status">{runFailureMessage(run)}</p>;
  }
  if (run?.status === "cancelled") {
    return <p className="run-status cancelled" role="status">{run.errorMessage?.trim() || "Run cancelled."}</p>;
  }
  return null;
}
