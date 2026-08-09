package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestResponsesRequestUsesPlatformManagedNativeOutputItems(t *testing.T) {
	t.Parallel()
	reasoning := json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque"}`)
	functionCall := json.RawMessage(
		`{"type":"function_call","call_id":"call-1","name":"file_read","arguments":"{}"}`,
	)
	assistant, err := convertResponse(responsesResponse{
		Output: []json.RawMessage{reasoning, functionCall},
	})
	if err != nil {
		t.Fatalf("convertResponse() error = %v", err)
	}
	inputs, err := convertMessages([]*schema.Message{
		schema.UserMessage("inspect"),
		assistant,
		schema.ToolMessage("result", "call-1"),
	})
	if err != nil {
		t.Fatalf("convertMessages() error = %v", err)
	}
	if len(inputs) != 4 {
		t.Fatalf("Responses inputs = %d, want user + two native outputs + tool result", len(inputs))
	}
	if !bytes.Equal(inputs[1], reasoning) || !bytes.Equal(inputs[2], functionCall) {
		t.Fatalf("native output items changed: %s / %s", inputs[1], inputs[2])
	}
	model := &ResponsesModel{model: "gpt-5"}
	request, err := model.newRequest([]*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatalf("newRequest() error = %v", err)
	}
	if request.Store {
		t.Fatal("Responses request Store = true, want platform-managed store:false")
	}
}

func TestResponsesReplayKeepsNativeItemsOutOfSafeCheckpoint(t *testing.T) {
	t.Parallel()
	reasoning := json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque-private-state"}`)
	functionCall := json.RawMessage(
		`{"type":"function_call","call_id":"call-1","name":"file_read","arguments":"{}"}`,
	)
	assistant, err := convertResponse(responsesResponse{
		Output: []json.RawMessage{reasoning, functionCall},
	})
	if err != nil {
		t.Fatalf("convertResponse() error = %v", err)
	}
	codec, err := newReplayCodec(ReplayKeyringConfig{
		LegacyKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
	})
	if err != nil {
		t.Fatalf("newReplayCodec() error = %v", err)
	}
	runner := &EinoRunner{
		replay: codec,
		definition: Definition{Agent: AgentDefinition{Model: ModelDefinition{
			Provider: "openai", APIMode: "responses", Model: "gpt-5",
			ContinuationScope: "conversation", AdapterVersion: "openai-responses/v1",
		}}},
	}
	checkpoint, err := runner.newCheckpoint(
		"run-1", "conversation-1", 1,
		checkpointState{Messages: []*schema.Message{schema.UserMessage("inspect"), assistant}},
	)
	if err != nil {
		t.Fatalf("newCheckpoint() error = %v", err)
	}
	if bytes.Contains(checkpoint.Payload, []byte("opaque-private-state")) {
		t.Fatalf("safe checkpoint contains encrypted reasoning item: %s", checkpoint.Payload)
	}
	restored, err := codec.open(checkpoint.ProviderReplay)
	if err != nil {
		t.Fatalf("open Responses replay: %v", err)
	}
	inputs, err := convertMessages(restored)
	if err != nil {
		t.Fatalf("convert restored Responses messages: %v", err)
	}
	if len(inputs) != 3 || !bytes.Equal(inputs[1], reasoning) || !bytes.Equal(inputs[2], functionCall) {
		t.Fatalf("restored Responses items = %s", inputs)
	}
}
