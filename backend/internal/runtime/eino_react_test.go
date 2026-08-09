package runtime

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/patrhez/agent-platform/backend/internal/domain"
)

func TestRestoreStateIncludesOrderedConversationHistory(t *testing.T) {
	t.Parallel()

	input := AgentInput{
		RunID:   "run-2",
		Attempt: 1,
		Messages: []ConversationMessage{
			{Role: "user", Content: "Find the bugs."},
			{Role: "assistant", Content: "I found two bugs."},
			{Role: "user", Content: "Explain those bugs."},
		},
	}

	runner := &EinoRunner{definition: Definition{SystemPrompt: "test system prompt"}}
	state, ordinal, err := runner.restoreState(input, nil)
	if err != nil {
		t.Fatalf("restoreState() error = %v", err)
	}
	if ordinal != 0 {
		t.Fatalf("restoreState() ordinal = %d, want 0", ordinal)
	}
	wantRoles := []schema.RoleType{schema.System, schema.User, schema.Assistant, schema.User}
	if len(state.Messages) != len(wantRoles) {
		t.Fatalf("restoreState() messages = %d, want %d", len(state.Messages), len(wantRoles))
	}
	for index, role := range wantRoles {
		if state.Messages[index].Role != role {
			t.Errorf("message %d role = %s, want %s", index, state.Messages[index].Role, role)
		}
	}
	if state.Messages[2].Content != "I found two bugs." {
		t.Errorf("prior assistant content = %q", state.Messages[2].Content)
	}
}

func TestRestoreStateUsesCheckpointWithoutAppendingConversationHistory(t *testing.T) {
	t.Parallel()

	checkpoint, err := newCheckpoint("run-2", 4, checkpointState{
		Iteration: 2,
		Messages: []*schema.Message{
			schema.SystemMessage("test system prompt"),
			schema.UserMessage("original question"),
			schema.AssistantMessage("checkpointed answer", nil),
		},
	})
	if err != nil {
		t.Fatalf("newCheckpoint() error = %v", err)
	}
	input := AgentInput{
		RunID:   "run-2",
		Attempt: 2,
		Messages: []ConversationMessage{
			{Role: "user", Content: "must not be appended during retry"},
		},
	}

	runner := &EinoRunner{definition: Definition{SystemPrompt: "test system prompt"}}
	state, ordinal, err := runner.restoreState(input, checkpoint)
	if err != nil {
		t.Fatalf("restoreState() error = %v", err)
	}
	if ordinal != 4 || len(state.Messages) != 3 {
		t.Fatalf("restoreState() = ordinal %d, %d messages", ordinal, len(state.Messages))
	}
	if state.Messages[2].Content != "checkpointed answer" {
		t.Errorf("restored answer = %q", state.Messages[2].Content)
	}
}

func TestRestoreStateContinuesCompatibleConversationReplayForNewRun(t *testing.T) {
	t.Parallel()
	runner := testReplayRunner(t, "conversation")
	replay, err := runner.replay.seal(testReplayContext("conversation"), []*schema.Message{
		schema.SystemMessage("test system prompt"),
		schema.UserMessage("first question"),
		schema.AssistantMessage("first answer", nil),
	})
	if err != nil {
		t.Fatalf("seal replay: %v", err)
	}
	input := replayTestInput(replay)
	state, ordinal, err := runner.restoreState(input, nil)
	if err != nil {
		t.Fatalf("restoreState() error = %v", err)
	}
	if ordinal != 0 || len(state.Messages) != 4 {
		t.Fatalf("restoreState() = ordinal %d, %d messages", ordinal, len(state.Messages))
	}
	if state.Messages[2].Content != "first answer" || state.Messages[3].Content != "next question" {
		t.Fatalf("restored messages = %#v", state.Messages)
	}
}

func TestRestoreStateResetsInvalidConversationReplayForNewRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*domain.ProviderReplay)
	}{
		{name: "provider mismatch", mutate: func(replay *domain.ProviderReplay) { replay.Provider = "other" }},
		{name: "missing key", mutate: func(replay *domain.ProviderReplay) { replay.KeyID = "missing" }},
		{name: "authentication failure", mutate: func(replay *domain.ProviderReplay) {
			replay.Ciphertext[0] ^= 0xff
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := testReplayRunner(t, "conversation")
			replay, err := runner.replay.seal(testReplayContext("conversation"), []*schema.Message{
				schema.SystemMessage("test system prompt"), schema.UserMessage("old question"),
			})
			if err != nil {
				t.Fatalf("seal replay: %v", err)
			}
			test.mutate(replay)
			state, _, err := runner.restoreState(replayTestInput(replay), nil)
			if err != nil {
				t.Fatalf("restoreState() error = %v", err)
			}
			if len(state.Messages) != 4 || state.Messages[3].Content != "next question" {
				t.Fatalf("new Run did not reset to safe history: %#v", state.Messages)
			}
		})
	}
}

func TestRestoreStateIgnoresConversationHeadForRunScope(t *testing.T) {
	t.Parallel()
	runner := testReplayRunner(t, "run")
	replay := &domain.ProviderReplay{ContinuationScope: "conversation"}
	state, _, err := runner.restoreState(replayTestInput(replay), nil)
	if err != nil {
		t.Fatalf("restoreState() error = %v", err)
	}
	if len(state.Messages) != 4 || state.Messages[1].Content != "first question" {
		t.Fatalf("run-scope new Run consumed Conversation head: %#v", state.Messages)
	}
}

func TestRestoreStateFailsClosedWhenActiveRunReplayIsMissing(t *testing.T) {
	t.Parallel()
	runner := testReplayRunner(t, "run")
	checkpoint, err := newCheckpoint("run-1", 1, checkpointState{
		Messages: []*schema.Message{schema.SystemMessage("test system prompt")},
	})
	if err != nil {
		t.Fatalf("newCheckpoint() error = %v", err)
	}
	_, _, err = runner.restoreState(AgentInput{RunID: "run-1", ConversationID: "conversation-1"}, checkpoint)
	if !errors.Is(err, ErrProviderReplayMissing) {
		t.Fatalf("restoreState() error = %v, want missing replay", err)
	}
}

func TestRestoreStateFailsClosedForInvalidActiveRunReplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*domain.ProviderReplay)
		wantErr error
	}{
		{
			name: "provider mismatch",
			mutate: func(replay *domain.ProviderReplay) {
				replay.Provider = "other"
			},
			wantErr: ErrProviderReplayIncompatible,
		},
		{
			name: "missing key",
			mutate: func(replay *domain.ProviderReplay) {
				replay.KeyID = "missing"
			},
			wantErr: ErrProviderReplayKeyUnavailable,
		},
		{
			name: "authentication failure",
			mutate: func(replay *domain.ProviderReplay) {
				replay.Ciphertext[0] ^= 0xff
			},
			wantErr: ErrProviderReplayDecryptFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := testReplayRunner(t, "run")
			checkpoint, err := runner.newCheckpoint(
				"run-1", "conversation-1", 1,
				checkpointState{Messages: []*schema.Message{schema.SystemMessage("test system prompt")}},
			)
			if err != nil {
				t.Fatalf("newCheckpoint() error = %v", err)
			}
			test.mutate(checkpoint.ProviderReplay)
			_, _, err = runner.restoreState(
				AgentInput{RunID: "run-1", ConversationID: "conversation-1"}, checkpoint,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("restoreState() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func testReplayRunner(t *testing.T, scope string) *EinoRunner {
	t.Helper()
	return &EinoRunner{
		replay: testReplayCodec(t),
		definition: Definition{
			SystemPrompt: "test system prompt",
			Agent: AgentDefinition{Model: ModelDefinition{
				Provider: "deepseek", APIMode: "chat_completions", Model: "deepseek-v4-pro",
				ContinuationScope: scope, AdapterVersion: "deepseek-chat-completions/v1",
			}},
		},
	}
}

func replayTestInput(replay *domain.ProviderReplay) AgentInput {
	return AgentInput{
		RunID: "run-2", ConversationID: "conversation-1", ProviderReplay: replay,
		Messages: []ConversationMessage{
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "next question"},
		},
	}
}

func TestCheckpointDoesNotPersistReasoningMetadata(t *testing.T) {
	t.Parallel()

	message := schema.AssistantMessage("safe answer", nil)
	message.ReasoningContent = "private reasoning"
	message.Extra = map[string]any{
		"reasoning_content": "private metadata",
		"request-id":        "request-1",
	}
	checkpoint, err := newCheckpoint("run-1", 1, checkpointState{
		Messages: []*schema.Message{message},
	})
	if err != nil {
		t.Fatalf("newCheckpoint() error = %v", err)
	}
	if bytes.Contains(checkpoint.Payload, []byte("private")) || bytes.Contains(checkpoint.Payload, []byte("reasoning")) {
		t.Fatalf("Checkpoint payload contains reasoning metadata: %s", checkpoint.Payload)
	}
	if !bytes.Contains(checkpoint.Payload, []byte("request-1")) {
		t.Fatalf("Checkpoint payload removed safe provider metadata: %s", checkpoint.Payload)
	}
}

func TestToolRequestRoutesModelToolNamesToConfiguredServers(t *testing.T) {
	executor := &fakeMCPExecutor{tools: map[string]toolMetadata{
		"code.search": {Description: "search code"},
	}}
	toolset, err := connectToolset(
		context.Background(),
		[]MCPServer{{
			Key:           "workspace",
			URL:           "http://example",
			AllowedTools:  []string{"code.search"},
			SafeArguments: map[string][]string{"code.search": {"repo", "query"}},
		}},
		func(context.Context, MCPServer) (mcpExecutor, error) { return executor, nil },
	)
	if err != nil {
		t.Fatalf("connectToolset() error = %v", err)
	}
	defer func() { _ = toolset.Close() }()

	call := &schema.ToolCall{
		ID: "provider-call-id",
		Function: schema.FunctionCall{
			Name:      "code_search",
			Arguments: `{"repo":"agent-platform","query":"stream","workspaceRoot":"/private"}`,
		},
	}
	request, binding, err := toolRequestFromCall("run-id", call, toolset)
	if err != nil {
		t.Fatalf("toolRequest() error = %v", err)
	}
	if request.Name != "code.search" || request.ServerKey != "workspace" {
		t.Errorf("request = %q on %q, want code.search on workspace", request.Name, request.ServerKey)
	}
	if binding.executor != executor {
		t.Error("binding.executor is not the configured executor")
	}
	if request.SafeArguments["repo"] != "agent-platform" || request.SafeArguments["workspaceRoot"] != nil {
		t.Errorf("request.SafeArguments = %#v, want only allowlisted keys", request.SafeArguments)
	}
}

func TestToolRequestRejectsUnknownModelTool(t *testing.T) {
	toolset := &mcpToolset{bindings: map[string]toolBinding{}}
	_, _, err := toolRequestFromCall("run-id", &schema.ToolCall{
		ID: "provider-call-id",
		Function: schema.FunctionCall{
			Name:      "code.search",
			Arguments: `{}`,
		},
	}, toolset)
	if err == nil {
		t.Fatal("toolRequestFromCall() error = nil, want rejection for unsupported model Tool name")
	}
}
