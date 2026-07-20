package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestADKRunnerStreamsFinalAnswer(t *testing.T) {
	first := schema.AssistantMessage("hello ", nil)
	second := schema.AssistantMessage("world", nil)
	second.ReasoningContent = "private reasoning"

	executor := &recordingMCPExecutor{result: ToolResult{Content: "unused"}}
	runner := &EinoRunner{
		definition: Definition{
			Agent: AgentDefinition{
				ID:      "test-agent",
				Version: "test",
				Runtime: Name,
				Model: ModelDefinition{
					APIMode:     "chat_completions",
					Model:       "test-model",
					Temperature: 0.1,
				},
				Limits: LimitDefinition{MaxSteps: 5, RunTimeoutSeconds: 60},
				MCPServers: []MCPServer{{
					Key:           "workspace",
					URL:           "http://example",
					AllowedTools:  []string{"file.read"},
					SafeArguments: map[string][]string{"file.read": {"repo", "path"}},
				}},
				SkillsBundleVersion: "v1",
			},
			SystemPrompt: "test system prompt",
		},
		model: &streamingTestModel{chunks: []*schema.Message{first, second}},
		connect: func(context.Context, MCPServer) (mcpExecutor, error) {
			return executor, nil
		},
	}

	var events []RuntimeEvent
	result, err := runner.Run(
		context.Background(),
		AgentInput{
			RunID:   "run-1",
			Attempt: 2,
			Messages: []ConversationMessage{
				{Role: "user", Content: "hi"},
			},
		},
		nil,
		func(event RuntimeEvent) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Final != "hello world" {
		t.Fatalf("Final = %q, want %q", result.Final, "hello world")
	}

	var deltas []AssistantStreamEvent
	var finals []RuntimeEvent
	for _, event := range events {
		if event.Assistant != nil {
			deltas = append(deltas, *event.Assistant)
		}
		if event.Final != "" {
			finals = append(finals, event)
		}
	}
	wantDeltas := []AssistantStreamEvent{
		{StreamID: "run-1:2:1", Phase: "started", Attempt: 2, StepNo: 1},
		{StreamID: "run-1:2:1", Phase: "delta", Attempt: 2, StepNo: 1, Offset: 0, Text: "hello "},
		{StreamID: "run-1:2:1", Phase: "delta", Attempt: 2, StepNo: 1, Offset: 1, Text: "world"},
	}
	if len(deltas) != len(wantDeltas) {
		t.Fatalf("deltas = %#v, want %#v", deltas, wantDeltas)
	}
	for index := range wantDeltas {
		if deltas[index] != wantDeltas[index] {
			t.Errorf("deltas[%d] = %#v, want %#v", index, deltas[index], wantDeltas[index])
		}
	}
	if len(finals) != 1 || finals[0].Checkpoint == nil {
		t.Fatalf("final events = %#v, want one Checkpointed final", finals)
	}
	var state checkpointState
	if err := json.Unmarshal(finals[0].Checkpoint.Payload, &state); err != nil {
		t.Fatalf("decode Checkpoint: %v", err)
	}
	if len(state.Messages) != 3 {
		t.Fatalf("Checkpoint messages = %d, want 3 (system,user,assistant)", len(state.Messages))
	}
	if state.Messages[2].Content != "hello world" || state.Messages[2].ReasoningContent != "" {
		t.Fatalf("assistant checkpoint message = %#v", state.Messages[2])
	}
}

func TestADKEmitSessionSerializesParallelToolFinishes(t *testing.T) {
	t.Parallel()

	ordinal := 0
	state := checkpointState{Iteration: 1, Messages: nil}
	seen := make(map[int]struct{})
	var seenMu sync.Mutex
	session := &adkEmitSession{
		runID:   "run-parallel",
		attempt: 1,
		state:   &state,
		ordinal: &ordinal,
		emit: func(event RuntimeEvent) error {
			if event.Checkpoint == nil {
				return nil
			}
			seenMu.Lock()
			defer seenMu.Unlock()
			if _, exists := seen[event.Checkpoint.Ordinal]; exists {
				t.Errorf("duplicate Checkpoint ordinal %d", event.Checkpoint.Ordinal)
			}
			seen[event.Checkpoint.Ordinal] = struct{}{}
			return nil
		},
	}

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for index := 0; index < workers; index++ {
		go func(index int) {
			defer wg.Done()
			request := ToolRequest{
				ID:     fmt.Sprintf("%026d", index+1),
				CallID: fmt.Sprintf("call_%d", index),
				Name:   "file.list",
			}
			if err := session.emitToolFinish(request, ToolResult{Content: "ok", Summary: "ok"}); err != nil {
				t.Errorf("emitToolFinish(%d) error = %v", index, err)
			}
		}(index)
	}
	wg.Wait()

	if ordinal != workers {
		t.Fatalf("ordinal = %d, want %d", ordinal, workers)
	}
	if len(seen) != workers {
		t.Fatalf("unique ordinals = %d, want %d", len(seen), workers)
	}
	if len(state.Messages) != workers {
		t.Fatalf("messages = %d, want %d", len(state.Messages), workers)
	}
}
