import type { Run } from "../api/client";

export function RunStatus({ run }: { run?: Run }) {
  if (run?.status === "failed") {
    const code = run.errorCode || "runtime_error";
    return <p className="run-status failed" role="status">Run failed ({code}). Check the execution trace or retry.</p>;
  }
  if (run?.status === "cancelled") {
    return <p className="run-status cancelled" role="status">Run cancelled.</p>;
  }
  return null;
}
