package worker

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
)

const (
	streamFlushInterval = 100 * time.Millisecond
	streamFlushBytes    = 256
)

type assistantEventPayload struct {
	StreamID string `json:"streamId"`
	Attempt  int    `json:"attempt"`
	StepNo   int    `json:"stepNo"`
	Offset   int    `json:"offset"`
	Text     string `json:"text,omitempty"`
}

type streamBuffer struct {
	persist    func([]domain.RunEvent) error
	now        func() time.Time
	streamID   string
	attempt    int
	stepNo     int
	nextOffset int
	pending    string
	lastFlush  time.Time
}

func newStreamBuffer(persist func([]domain.RunEvent) error, now func() time.Time) *streamBuffer {
	return &streamBuffer{persist: persist, now: now}
}

func (buffer *streamBuffer) Add(event runtime.AssistantStreamEvent) error {
	switch event.Phase {
	case "started":
		if err := buffer.Flush(); err != nil {
			return err
		}
		buffer.streamID = event.StreamID
		buffer.attempt = event.Attempt
		buffer.stepNo = event.StepNo
		buffer.nextOffset = 0
		buffer.lastFlush = buffer.now()
		return buffer.persistEvent("assistant.started", assistantEventPayload{
			StreamID: event.StreamID,
			Attempt:  event.Attempt,
			StepNo:   event.StepNo,
			Offset:   0,
		})
	case "delta":
		if buffer.streamID == "" || buffer.streamID != event.StreamID ||
			buffer.attempt != event.Attempt || buffer.stepNo != event.StepNo {
			return fmt.Errorf("assistant Delta does not match the active stream")
		}
		buffer.pending += event.Text
		if len(buffer.pending) >= streamFlushBytes || buffer.now().Sub(buffer.lastFlush) >= streamFlushInterval {
			return buffer.Flush()
		}
		return nil
	default:
		return fmt.Errorf("unsupported assistant stream phase %q", event.Phase)
	}
}

func (buffer *streamBuffer) Flush() error {
	if buffer.pending == "" {
		return nil
	}
	payload := assistantEventPayload{
		StreamID: buffer.streamID,
		Attempt:  buffer.attempt,
		StepNo:   buffer.stepNo,
		Offset:   buffer.nextOffset,
		Text:     buffer.pending,
	}
	if err := buffer.persistEvent("assistant.delta", payload); err != nil {
		return err
	}
	buffer.pending = ""
	buffer.nextOffset++
	buffer.lastFlush = buffer.now()
	return nil
}

func (buffer *streamBuffer) persistEvent(eventType string, payload assistantEventPayload) error {
	contents, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s event: %w", eventType, err)
	}
	if err := buffer.persist([]domain.RunEvent{{Type: eventType, SafePayload: contents}}); err != nil {
		return fmt.Errorf("persist %s event: %w", eventType, err)
	}
	return nil
}
