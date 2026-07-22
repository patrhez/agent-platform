package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyRunFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cause   error
		code    string
		message string
	}{
		{
			name:    "cancelled",
			cause:   ErrRunCancelled,
			code:    "cancelled",
			message: "Run cancelled.",
		},
		{
			name:    "step limit",
			cause:   ErrStepLimit,
			code:    "step_limit_exceeded",
			message: "The agent reached its maximum step limit before finishing.",
		},
		{
			name:  "temperature",
			cause: errors.New(`error, status code: 400, status: 400 Bad Request, message: invalid temperature: only 1 is allowed for this model`),
			code:  "llm_invalid_temperature",
			message: "The language model rejected the temperature setting. Check the agent model configuration and retry.",
		},
		{
			name:    "overload",
			cause:   errors.New("The engine is currently overloaded, please try again later"),
			code:    "llm_overload",
			message: "The language model is currently overloaded. Please retry in a moment.",
		},
		{
			name:    "rate limit",
			cause:   errors.New("error, status code: 429, status: 429 Too Many Requests, message: Rate limit reached"),
			code:    "llm_rate_limited",
			message: "The language model rate limit was exceeded. Please retry shortly.",
		},
		{
			name:    "auth",
			cause:   errors.New("error, status code: 401, status: 401 Unauthorized, message: Invalid Authentication"),
			code:    "llm_auth_error",
			message: "The language model rejected the API credentials. Check LLM_API_KEY and retry.",
		},
		{
			name:    "deadline",
			cause:   context.DeadlineExceeded,
			code:    "llm_timeout",
			message: "The language model request timed out. Please retry.",
		},
		{
			name:    "wrapped responses 400",
			cause:   fmt.Errorf("call model: %w", errors.New("Responses API returned 400 Bad Request: {\"error\":{\"message\":\"bad schema\"}}")),
			code:    "llm_invalid_request",
			message: "The language model rejected the request. Check the agent model configuration and retry.",
		},
		{
			name:    "unknown keeps provider details private",
			cause:   errors.New("private provider response with secret sk-abc"),
			code:    "runtime_error",
			message: "The run failed due to a runtime error. Check the execution trace or retry.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failure := ClassifyRunFailure(test.cause)
			if failure.Code != test.code {
				t.Fatalf("Code = %q, want %q", failure.Code, test.code)
			}
			if failure.Message != test.message {
				t.Fatalf("Message = %q, want %q", failure.Message, test.message)
			}
			if test.cause != nil && failure.Message == test.cause.Error() {
				t.Fatalf("Message leaked provider error: %q", failure.Message)
			}
		})
	}
}
