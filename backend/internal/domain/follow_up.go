package domain

import (
	"fmt"
	"strings"
)

// FollowUpMode controls how a new user message interacts with active Runs.
type FollowUpMode string

const (
	// FollowUpModeQueue appends a Run without interrupting active work.
	FollowUpModeQueue FollowUpMode = "queue"
	// FollowUpModeSteer cancels active Runs, then appends a new Run.
	FollowUpModeSteer FollowUpMode = "steer"
)

// ParseFollowUpMode accepts queue/steer; empty defaults to queue.
func ParseFollowUpMode(value string) (FollowUpMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(FollowUpModeQueue):
		return FollowUpModeQueue, nil
	case string(FollowUpModeSteer):
		return FollowUpModeSteer, nil
	default:
		return "", fmt.Errorf("mode must be %q or %q", FollowUpModeQueue, FollowUpModeSteer)
	}
}
