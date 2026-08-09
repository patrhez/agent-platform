package runtime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/patrhez/agent-platform/backend/internal/domain"
)

const (
	replayArtifactSchemaVersion = 1
	replayEncryptionAlgorithm   = "AES-256-GCM"
	replayEncoding              = "json"
	replayCompression           = "none"
	legacyReplayKeyID           = "legacy"
)

// ReplayKeyringConfig selects the active encryption key and all readable historical keys.
type ReplayKeyringConfig struct {
	ActiveKeyID string
	KeysJSON    string
	LegacyKey   string
}

type replayCodec struct {
	activeKeyID string
	keys        map[string]cipher.AEAD
}

type replayContext struct {
	RunID             string
	CheckpointID      string
	ConversationID    string
	Provider          string
	APIMode           string
	Model             string
	ContinuationScope string
	AdapterVersion    string
}

type providerReplayEnvelope struct {
	SchemaVersion     int               `json:"schema_version"`
	Provider          string            `json:"provider"`
	APIMode           string            `json:"api_mode"`
	Model             string            `json:"model"`
	ContinuationScope string            `json:"continuation_scope"`
	AdapterVersion    string            `json:"adapter_version"`
	Items             []json.RawMessage `json:"items"`
}

// chatReplayMessage is the durable Chat Completions wire projection. It is kept
// independent of Eino's Message JSON representation so library upgrades cannot
// silently redefine persisted artifacts.
type chatReplayMessage struct {
	Role             schema.RoleType   `json:"role"`
	Content          string            `json:"content"`
	Name             string            `json:"name,omitempty"`
	ToolCalls        []schema.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ToolName         string            `json:"tool_name,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	Extra            map[string]any    `json:"extra,omitempty"`
}

type replayAAD struct {
	Version               int    `json:"version"`
	RunID                 string `json:"run_id"`
	CheckpointID          string `json:"checkpoint_id"`
	ConversationID        string `json:"conversation_id"`
	Provider              string `json:"provider"`
	APIMode               string `json:"api_mode"`
	Model                 string `json:"model"`
	ContinuationScope     string `json:"continuation_scope"`
	ArtifactSchemaVersion int    `json:"artifact_schema_version"`
	AdapterVersion        string `json:"adapter_version"`
	EncryptionAlgorithm   string `json:"encryption_algorithm"`
	KeyID                 string `json:"key_id"`
}

func newReplayCodec(configuration ReplayKeyringConfig) (*replayCodec, error) {
	activeKeyID, encodedKeys, err := parseReplayKeys(configuration)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]cipher.AEAD, len(encodedKeys))
	for keyID, encodedKey := range encodedKeys {
		aead, keyErr := newReplayAEAD(keyID, encodedKey)
		if keyErr != nil {
			return nil, keyErr
		}
		keys[keyID] = aead
	}
	if _, found := keys[activeKeyID]; !found {
		return nil, fmt.Errorf("active replay encryption key %q is not configured", activeKeyID)
	}
	return &replayCodec{activeKeyID: activeKeyID, keys: keys}, nil
}

func parseReplayKeys(configuration ReplayKeyringConfig) (string, map[string]string, error) {
	if configuration.KeysJSON == "" {
		if configuration.LegacyKey == "" {
			return "", nil, fmt.Errorf("replay encryption keyring is required")
		}
		return legacyReplayKeyID, map[string]string{legacyReplayKeyID: configuration.LegacyKey}, nil
	}
	keys := map[string]string{}
	if err := json.Unmarshal([]byte(configuration.KeysJSON), &keys); err != nil {
		return "", nil, fmt.Errorf("decode replay encryption keyring: %w", err)
	}
	if configuration.ActiveKeyID == "" {
		return "", nil, fmt.Errorf("REPLAY_ENCRYPTION_ACTIVE_KEY_ID is required with REPLAY_ENCRYPTION_KEYS")
	}
	return configuration.ActiveKeyID, keys, nil
}

func newReplayAEAD(keyID string, encodedKey string) (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("replay encryption key %q must be base64-encoded 32 bytes", keyID)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create replay cipher for key %q: %w", keyID, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create replay AEAD for key %q: %w", keyID, err)
	}
	return aead, nil
}

func (codec *replayCodec) seal(
	context replayContext,
	messages []*schema.Message,
) (*domain.ProviderReplay, error) {
	items, err := captureReplayItems(context.APIMode, messages)
	if err != nil {
		return nil, err
	}
	envelope := providerReplayEnvelope{
		SchemaVersion: replayArtifactSchemaVersion, Provider: context.Provider,
		APIMode: context.APIMode, Model: context.Model,
		ContinuationScope: context.ContinuationScope, AdapterVersion: context.AdapterVersion,
		Items: items,
	}
	plaintext, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode provider replay envelope: %w", err)
	}
	aead := codec.keys[codec.activeKeyID]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate provider replay nonce: %w", err)
	}
	replay := replayMetadata(context, codec.activeKeyID, nonce)
	aad, err := marshalReplayAAD(replay)
	if err != nil {
		return nil, err
	}
	replay.Ciphertext = aead.Seal(nil, nonce, plaintext, aad)
	return replay, nil
}

func replayMetadata(context replayContext, keyID string, nonce []byte) *domain.ProviderReplay {
	return &domain.ProviderReplay{
		CheckpointID: context.CheckpointID, RunID: context.RunID,
		ConversationID: context.ConversationID, Provider: context.Provider,
		APIMode: context.APIMode, Model: context.Model,
		ContinuationScope:     context.ContinuationScope,
		ArtifactSchemaVersion: replayArtifactSchemaVersion,
		AdapterVersion:        context.AdapterVersion, Encoding: replayEncoding,
		Compression: replayCompression, EncryptionAlgorithm: replayEncryptionAlgorithm,
		KeyID: keyID, Nonce: append([]byte(nil), nonce...), CreatedAt: time.Now().UTC(),
	}
}

func (codec *replayCodec) open(replay *domain.ProviderReplay) ([]*schema.Message, error) {
	if err := validateReplayMetadata(replay); err != nil {
		return nil, err
	}
	aead, found := codec.keys[replay.KeyID]
	if !found {
		return nil, fmt.Errorf("key ID %q: %w", replay.KeyID, ErrProviderReplayKeyUnavailable)
	}
	aad, err := marshalReplayAAD(replay)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, replay.Nonce, replay.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: authenticate ciphertext", ErrProviderReplayDecryptFailed)
	}
	envelope := providerReplayEnvelope{}
	if err := json.Unmarshal(plaintext, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode provider replay envelope", ErrProviderReplayIncompatible)
	}
	if err := validateReplayEnvelope(replay, envelope); err != nil {
		return nil, err
	}
	messages, err := restoreReplayItems(replay.APIMode, envelope.Items)
	if err != nil {
		return nil, fmt.Errorf("%w: restore provider replay items: %w", ErrProviderReplayIncompatible, err)
	}
	return messages, nil
}

func validateReplayMetadata(replay *domain.ProviderReplay) error {
	if replay == nil || replay.ArtifactSchemaVersion != replayArtifactSchemaVersion {
		return fmt.Errorf("%w: unsupported artifact schema", ErrProviderReplayIncompatible)
	}
	if replay.Encoding != replayEncoding || replay.Compression != replayCompression {
		return fmt.Errorf("%w: unsupported encoding", ErrProviderReplayIncompatible)
	}
	if replay.EncryptionAlgorithm != replayEncryptionAlgorithm || len(replay.Nonce) != 12 {
		return fmt.Errorf("%w: unsupported encryption metadata", ErrProviderReplayIncompatible)
	}
	return nil
}

func validateReplayEnvelope(replay *domain.ProviderReplay, envelope providerReplayEnvelope) error {
	if envelope.SchemaVersion != replay.ArtifactSchemaVersion || envelope.Provider != replay.Provider ||
		envelope.APIMode != replay.APIMode || envelope.Model != replay.Model ||
		envelope.ContinuationScope != replay.ContinuationScope ||
		envelope.AdapterVersion != replay.AdapterVersion {
		return fmt.Errorf("%w: envelope metadata mismatch", ErrProviderReplayIncompatible)
	}
	return nil
}

func marshalReplayAAD(replay *domain.ProviderReplay) ([]byte, error) {
	aad := replayAAD{
		Version: 1, RunID: replay.RunID, CheckpointID: replay.CheckpointID,
		ConversationID: replay.ConversationID, Provider: replay.Provider,
		APIMode: replay.APIMode, Model: replay.Model,
		ContinuationScope:     replay.ContinuationScope,
		ArtifactSchemaVersion: replay.ArtifactSchemaVersion,
		AdapterVersion:        replay.AdapterVersion,
		EncryptionAlgorithm:   replay.EncryptionAlgorithm, KeyID: replay.KeyID,
	}
	encoded, err := json.Marshal(aad)
	if err != nil {
		return nil, fmt.Errorf("encode provider replay AAD: %w", err)
	}
	return encoded, nil
}

func captureReplayItems(apiMode string, messages []*schema.Message) ([]json.RawMessage, error) {
	if apiMode == "responses" {
		items, err := convertMessages(messages)
		if err != nil {
			return nil, fmt.Errorf("capture Responses replay items: %w", err)
		}
		return items, nil
	}
	items := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		item := chatReplayMessage{
			Role: message.Role, Content: message.Content, Name: message.Name,
			ToolCalls: message.ToolCalls, ToolCallID: message.ToolCallID,
			ToolName: message.ToolName, ReasoningContent: message.ReasoningContent,
			Extra: message.Extra,
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("encode Chat Completions replay item: %w", err)
		}
		items = append(items, encoded)
	}
	return items, nil
}

func restoreReplayItems(apiMode string, items []json.RawMessage) ([]*schema.Message, error) {
	if apiMode == "responses" {
		return restoreResponsesReplayItems(items)
	}
	messages := make([]*schema.Message, 0, len(items))
	for _, encoded := range items {
		item := chatReplayMessage{}
		if err := json.Unmarshal(encoded, &item); err != nil {
			return nil, fmt.Errorf("%w: decode Chat Completions replay item", ErrProviderReplayIncompatible)
		}
		messages = append(messages, &schema.Message{
			Role: item.Role, Content: item.Content, Name: item.Name,
			ToolCalls: item.ToolCalls, ToolCallID: item.ToolCallID,
			ToolName: item.ToolName, ReasoningContent: item.ReasoningContent,
			Extra: item.Extra,
		})
	}
	return messages, nil
}

func hasReasoning(messages []*schema.Message) bool {
	for _, message := range messages {
		if message != nil && message.Role == schema.Assistant && message.ReasoningContent != "" {
			return true
		}
	}
	return false
}
