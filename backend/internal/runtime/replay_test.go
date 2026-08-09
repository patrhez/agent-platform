package runtime

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestProviderReplayRestoresReasoningWithoutExposingItInCheckpoint(t *testing.T) {
	t.Parallel()
	codec, err := newReplayCodec(ReplayKeyringConfig{
		LegacyKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
	})
	if err != nil {
		t.Fatalf("newReplayCodec() error = %v", err)
	}
	assistant := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "call-1", Function: schema.FunctionCall{Name: "file_list", Arguments: `{}`},
	}})
	assistant.ReasoningContent = "private DeepSeek reasoning"
	runner := &EinoRunner{
		replay: codec,
		definition: Definition{Agent: AgentDefinition{Model: ModelDefinition{
			Provider: "deepseek", APIMode: "chat_completions", Model: "deepseek-v4-pro",
			ContinuationScope: "run", AdapterVersion: "deepseek-chat-completions/v1",
		}}},
	}
	checkpoint, err := runner.newCheckpoint(
		"run-1", "conversation-1", 1,
		checkpointState{Messages: []*schema.Message{assistant}},
	)
	if err != nil {
		t.Fatalf("newCheckpoint() error = %v", err)
	}
	if bytes.Contains(checkpoint.Payload, []byte("private DeepSeek reasoning")) {
		t.Fatalf("checkpoint exposed reasoning: %s", checkpoint.Payload)
	}
	state, _, err := runner.restoreState(
		AgentInput{RunID: "run-1", ConversationID: "conversation-1"}, checkpoint,
	)
	if err != nil {
		t.Fatalf("restoreState() error = %v", err)
	}
	if got := state.Messages[0].ReasoningContent; got != "private DeepSeek reasoning" {
		t.Fatalf("restored ReasoningContent = %q", got)
	}
}

func TestProviderReplayKeyRotationNonceAndAAD(t *testing.T) {
	t.Parallel()
	oldKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	activeKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	keys, err := json.Marshal(map[string]string{"old": oldKey, "active": activeKey})
	if err != nil {
		t.Fatalf("encode test keyring: %v", err)
	}
	codec, err := newReplayCodec(ReplayKeyringConfig{
		ActiveKeyID: "active", KeysJSON: string(keys),
	})
	if err != nil {
		t.Fatalf("newReplayCodec() error = %v", err)
	}
	context := replayContext{
		RunID: "run-1", CheckpointID: "checkpoint-1", ConversationID: "conversation-1",
		Provider: "deepseek", APIMode: "chat_completions", Model: "deepseek-v4-pro",
		ContinuationScope: "run", AdapterVersion: "deepseek-chat-completions/v1",
	}
	message := schema.AssistantMessage("", nil)
	message.ReasoningContent = "private"
	first, err := codec.seal(context, []*schema.Message{message})
	if err != nil {
		t.Fatalf("seal first replay: %v", err)
	}
	context.CheckpointID = "checkpoint-2"
	second, err := codec.seal(context, []*schema.Message{message})
	if err != nil {
		t.Fatalf("seal second replay: %v", err)
	}
	if first.KeyID != "active" || second.KeyID != "active" {
		t.Fatalf("new replay key IDs = %q, %q", first.KeyID, second.KeyID)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("two replay encryptions reused a nonce")
	}
	oldCodec, err := newReplayCodec(ReplayKeyringConfig{
		ActiveKeyID: "old", KeysJSON: string(keys),
	})
	if err != nil {
		t.Fatalf("new old replay codec: %v", err)
	}
	context.CheckpointID = "checkpoint-old"
	oldReplay, err := oldCodec.seal(context, []*schema.Message{message})
	if err != nil {
		t.Fatalf("seal old replay: %v", err)
	}
	if _, err := codec.open(oldReplay); err != nil {
		t.Fatalf("open historical replay with rotated keyring: %v", err)
	}
	first.Model = "tampered-model"
	if _, err := codec.open(first); !errors.Is(err, ErrProviderReplayDecryptFailed) {
		t.Fatalf("open tampered replay error = %v", err)
	}
}

func TestRequiredReplayRejectsMissingKey(t *testing.T) {
	t.Parallel()
	if _, err := newReplayCodec(ReplayKeyringConfig{}); err == nil {
		t.Fatal("newReplayCodec() error = nil, want missing-key rejection")
	}
}

func TestAuthenticatedMalformedReplayIsIncompatible(t *testing.T) {
	t.Parallel()
	codec := testReplayCodec(t)
	replay := replayMetadata(testReplayContext("conversation"), codec.activeKeyID, nil)
	aead := codec.keys[codec.activeKeyID]
	replay.Nonce = make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, replay.Nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	aad, err := marshalReplayAAD(replay)
	if err != nil {
		t.Fatalf("marshal replay AAD: %v", err)
	}
	replay.Ciphertext = aead.Seal(nil, replay.Nonce, []byte("not-json"), aad)
	if _, err := codec.open(replay); !errors.Is(err, ErrProviderReplayIncompatible) {
		t.Fatalf("open malformed replay error = %v, want incompatible", err)
	}
}

func testReplayCodec(t *testing.T) *replayCodec {
	t.Helper()
	codec, err := newReplayCodec(ReplayKeyringConfig{
		LegacyKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
	})
	if err != nil {
		t.Fatalf("newReplayCodec() error = %v", err)
	}
	return codec
}

func testReplayContext(scope string) replayContext {
	return replayContext{
		RunID: "run-1", CheckpointID: "checkpoint-1", ConversationID: "conversation-1",
		Provider: "deepseek", APIMode: "chat_completions", Model: "deepseek-v4-pro",
		ContinuationScope: scope, AdapterVersion: "deepseek-chat-completions/v1",
	}
}
