package runtime

import (
	"context"
	"testing"
)

func TestBuildContextHandlersRespectsEnabledFlags(t *testing.T) {
	t.Parallel()

	session := &adkEmitSession{
		state: &checkpointState{},
		model: &streamingTestModel{},
	}

	disabled, err := buildContextHandlers(context.Background(), Definition{}, session)
	if err != nil {
		t.Fatalf("buildContextHandlers() error = %v", err)
	}
	if len(disabled) != 1 {
		t.Fatalf("disabled handlers = %d, want sync only", len(disabled))
	}

	enabled, err := buildContextHandlers(context.Background(), Definition{
		Agent: AgentDefinition{
			Context: ContextDefinition{
				Reduction: ReductionDefinition{
					Enabled:           true,
					MaxTokensForClear: 1000,
					MaxLengthForTrunc: 500,
					SkipOffload:       true,
				},
				Summarization: SummarizationDefinition{
					Enabled:         true,
					ContextMessages: 4,
				},
			},
		},
	}, session)
	if err != nil {
		t.Fatalf("buildContextHandlers(enabled) error = %v", err)
	}
	if len(enabled) != 3 {
		t.Fatalf("enabled handlers = %d, want reduction+summarization+sync", len(enabled))
	}
}
