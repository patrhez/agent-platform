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
	runner := EinoRunner{model: &streamingTestModel{chunks: []*schema.Message{first, second}}}
	events := make([]AssistantStreamEvent, 0, 3)

	response, err := runner.streamModelResponse(
		context.Background(),
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

	state, ordinal, err := restoreState(input, nil)
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
			schema.SystemMessage(systemPrompt),
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

	state, ordinal, err := restoreState(input, checkpoint)
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

func TestToolRequestMapsModelToolNamesToWorkspaceMCPTools(t *testing.T) {
	testCases := []struct {
		name      string
		modelTool string
		mcpTool   string
	}{
		{name: "search", modelTool: "code_search", mcpTool: "code.search"},
		{name: "read", modelTool: "file_read", mcpTool: "file.read"},
		{name: "list repositories", modelTool: "workspace_list_repositories", mcpTool: "workspace.list_repositories"},
	}

	runner := EinoRunner{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			call := &schema.ToolCall{
				ID: "provider-call-id",
				Function: schema.FunctionCall{
					Name:      testCase.modelTool,
					Arguments: `{}`,
				},
			}

			request, err := runner.toolRequest("run-id", call)
			if err != nil {
				t.Fatalf("toolRequest() error = %v", err)
			}
			if request.Name != testCase.mcpTool {
				t.Errorf("request.Name = %q, want %q", request.Name, testCase.mcpTool)
			}
		})
	}
}

func TestWorkspaceToolInfosUseProviderCompatibleNames(t *testing.T) {
	for _, tool := range workspaceToolInfos() {
		for _, character := range tool.Name {
			valid := character >= 'a' && character <= 'z' ||
				character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || character == '_' || character == '-'
			if !valid {
				t.Errorf("Workspace Tool name %q contains unsupported character %q", tool.Name, character)
			}
		}
	}
}

func TestToolRequestRejectsUnknownModelTool(t *testing.T) {
	runner := EinoRunner{}
	_, err := runner.toolRequest("run-id", &schema.ToolCall{
		ID: "provider-call-id",
		Function: schema.FunctionCall{
			Name:      "code.search",
			Arguments: `{}`,
		},
	})
	if err == nil {
		t.Fatal("toolRequest() error = nil, want rejection for unsupported model Tool name")
	}
}
