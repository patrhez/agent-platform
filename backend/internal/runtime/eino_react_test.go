package runtime

import (
	"bytes"
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type streamingTestModel struct {
	chunks []*schema.Message
}

func (streamingModel *streamingTestModel) Generate(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.Message, error) {
	return nil, nil
}

func (streamingModel *streamingTestModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray(streamingModel.chunks), nil
}

func (streamingModel *streamingTestModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return streamingModel, nil
}

func TestEinoRunnerStreamsAssistantContent(t *testing.T) {
	first := schema.AssistantMessage("hello ", nil)
	second := schema.AssistantMessage("world", nil)
	second.ReasoningContent = "private reasoning"
	second.Extra = map[string]any{
		"reasoning-content": "private reasoning metadata",
		"request-id":        "request-1",
	}
	events := make([]AssistantStreamEvent, 0, 3)

	response, err := streamModelResponse(
		context.Background(),
		&streamingTestModel{chunks: []*schema.Message{first, second}},
		[]*schema.Message{schema.UserMessage("test")},
		"run-1:2:1",
		2,
		1,
		func(event RuntimeEvent) error {
			if event.Assistant != nil {
				events = append(events, *event.Assistant)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("streamModelResponse() error = %v", err)
	}
	if response.Content != "hello world" {
		t.Errorf("response.Content = %q, want %q", response.Content, "hello world")
	}
	if response.ReasoningContent != "" {
		t.Errorf("response.ReasoningContent = %q, want empty", response.ReasoningContent)
	}
	if _, found := response.Extra["reasoning-content"]; found {
		t.Error("response.Extra contains private reasoning metadata")
	}
	if response.Extra["request-id"] != "request-1" {
		t.Errorf("response.Extra request-id = %v, want request-1", response.Extra["request-id"])
	}

	want := []AssistantStreamEvent{
		{StreamID: "run-1:2:1", Phase: "started", Attempt: 2, StepNo: 1, Offset: 0},
		{StreamID: "run-1:2:1", Phase: "delta", Attempt: 2, StepNo: 1, Offset: 0, Text: "hello "},
		{StreamID: "run-1:2:1", Phase: "delta", Attempt: 2, StepNo: 1, Offset: 1, Text: "world"},
	}
	if len(events) != len(want) {
		t.Fatalf("len(events) = %d, want %d: %#v", len(events), len(want), events)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Errorf("events[%d] = %#v, want %#v", index, events[index], want[index])
		}
	}
}

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

	state, ordinal, err := restoreState(input, nil, "test system prompt")
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

	state, ordinal, err := restoreState(input, checkpoint, "test system prompt")
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
	request, binding, err := toolRequest("run-id", call, toolset)
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
	_, _, err := toolRequest("run-id", &schema.ToolCall{
		ID: "provider-call-id",
		Function: schema.FunctionCall{
			Name:      "code.search",
			Arguments: `{}`,
		},
	}, toolset)
	if err == nil {
		t.Fatal("toolRequest() error = nil, want rejection for unsupported model Tool name")
	}
}
