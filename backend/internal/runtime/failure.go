package runtime

import (
	"context"
	"errors"
	"strings"
)

// RunFailure is a safe, user-visible classification of a terminal Run error.
type RunFailure struct {
	Code    string
	Message string
}

const (
	failureCancelled          = "cancelled"
	failureStepLimit          = "step_limit_exceeded"
	failureInvalidTemperature = "llm_invalid_temperature"
	failureInvalidRequest     = "llm_invalid_request"
	failureOverload           = "llm_overload"
	failureRateLimited        = "llm_rate_limited"
	failureAuth               = "llm_auth_error"
	failureTimeout            = "llm_timeout"
	failureUnavailable        = "llm_unavailable"
	failureRuntime            = "runtime_error"
)

// ClassifyRunFailure maps an execution error to a safe code and English message.
// It never returns provider payloads, secrets, or chain-of-thought.
func ClassifyRunFailure(cause error) RunFailure {
	if cause == nil {
		return RunFailure{
			Code:    failureRuntime,
			Message: "The run failed due to a runtime error. Check the execution trace or retry.",
		}
	}
	if errors.Is(cause, ErrRunCancelled) {
		return RunFailure{Code: failureCancelled, Message: "Run cancelled."}
	}
	if errors.Is(cause, ErrStepLimit) {
		return RunFailure{
			Code:    failureStepLimit,
			Message: "The agent reached its maximum step limit before finishing.",
		}
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return RunFailure{
			Code:    failureTimeout,
			Message: "The language model request timed out. Please retry.",
		}
	}

	text := strings.ToLower(cause.Error())
	switch {
	case strings.Contains(text, "temperature"):
		return RunFailure{
			Code:    failureInvalidTemperature,
			Message: "The language model rejected the temperature setting. Check the agent model configuration and retry.",
		}
	case containsAny(text, "rate limit", "rate_limit", "too many requests", "status code: 429"):
		return RunFailure{
			Code:    failureRateLimited,
			Message: "The language model rate limit was exceeded. Please retry shortly.",
		}
	case containsAny(text, "overload", "overloaded", "capacity", "server_busy", "status code: 503"):
		return RunFailure{
			Code:    failureOverload,
			Message: "The language model is currently overloaded. Please retry in a moment.",
		}
	case containsAny(text, "unauthorized", "invalid api key", "authentication", "status code: 401", "status code: 403"):
		return RunFailure{
			Code:    failureAuth,
			Message: "The language model rejected the API credentials. Check LLM_API_KEY and retry.",
		}
	case containsAny(text, "timeout", "deadline exceeded", "i/o timeout"):
		return RunFailure{
			Code:    failureTimeout,
			Message: "The language model request timed out. Please retry.",
		}
	case containsAny(text, "invalid_request", "bad request", "status code: 400"):
		return RunFailure{
			Code:    failureInvalidRequest,
			Message: "The language model rejected the request. Check the agent model configuration and retry.",
		}
	case containsAny(text, "status code: 500", "status code: 502", "status code: 504", "service unavailable", "bad gateway"):
		return RunFailure{
			Code:    failureUnavailable,
			Message: "The language model is temporarily unavailable. Please retry shortly.",
		}
	default:
		return RunFailure{
			Code:    failureRuntime,
			Message: "The run failed due to a runtime error. Check the execution trace or retry.",
		}
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
