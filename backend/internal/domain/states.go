package domain

// RunStatus describes the durable lifecycle state of a Run.
type RunStatus string

const (
	// RunStatusQueued indicates a Run is waiting for a Worker lease.
	RunStatusQueued RunStatus = "queued"
	// RunStatusRunning indicates a Worker currently owns a Run lease.
	RunStatusRunning RunStatus = "running"
	// RunStatusWaiting indicates a Run is paused for a user response.
	RunStatusWaiting RunStatus = "waiting"
	// RunStatusSucceeded indicates a Run produced its final answer.
	RunStatusSucceeded RunStatus = "succeeded"
	// RunStatusFailed indicates a Run ended with a retained failure result.
	RunStatusFailed RunStatus = "failed"
	// RunStatusCancelled indicates cancellation ended a Run.
	RunStatusCancelled RunStatus = "cancelled"
)
