// Package events provides non-durable Redis notifications for committed Run events.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/pkg/async"
	"github.com/redis/go-redis/v9"
)

const channelPrefix = "agent-platform:run-events:"

// Notifier prompts API instances to replay durable Run events from MySQL.
type Notifier interface {
	Publish(context.Context, string, int64) error
	Subscribe(context.Context, string) (Subscription, error)
}

// Subscription receives Run event sequence hints from Redis Pub/Sub.
type Subscription interface {
	Notifications() <-chan int64
	Close() error
}

// Open connects a Redis-backed Notifier using a redis:// URL.
func Open(ctx context.Context, address string, logger logging.Logger) (Notifier, error) {
	if logger == nil {
		logger = logging.Nop()
	}
	options, err := redis.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping Redis: %w", err)
	}
	return &redisNotifier{client: client, logger: logger}, nil
}

// Noop returns a Notifier that keeps SSE recovery functional without Redis hints.
func Noop() Notifier {
	return noopNotifier{}
}

type redisNotifier struct {
	client *redis.Client
	logger logging.Logger
}

type notification struct {
	RunID string `json:"runId"`
	Seq   int64  `json:"seq"`
}

func (notifier *redisNotifier) Publish(ctx context.Context, runID string, seq int64) error {
	contents, err := json.Marshal(notification{RunID: runID, Seq: seq})
	if err != nil {
		return fmt.Errorf("encode Run event notification: %w", err)
	}
	if err := notifier.client.Publish(ctx, channelName(runID), contents).Err(); err != nil {
		return fmt.Errorf("publish Run event notification: %w", err)
	}
	return nil
}

func (notifier *redisNotifier) Subscribe(ctx context.Context, runID string) (Subscription, error) {
	pubsub := notifier.client.Subscribe(ctx, channelName(runID))
	if _, err := pubsub.Receive(ctx); err != nil {
		if closeErr := pubsub.Close(); closeErr != nil {
			return nil, fmt.Errorf("subscribe to Run events: %w; close subscription: %v", err, closeErr)
		}
		return nil, fmt.Errorf("subscribe to Run events: %w", err)
	}
	return &redisSubscription{
		pubsub:   pubsub,
		messages: pubsub.Channel(),
		logger:   notifier.logger,
	}, nil
}

type redisSubscription struct {
	pubsub   *redis.PubSub
	messages <-chan *redis.Message
	logger   logging.Logger
}

func (subscription *redisSubscription) Notifications() <-chan int64 {
	sequences := make(chan int64, 1)
	go func() {
		defer async.Recover(context.Background(), subscription.logger)
		defer close(sequences)
		for message := range subscription.messages {
			sequence, ok := decodeSequence(message.Payload)
			if ok {
				select {
				case sequences <- sequence:
				default:
				}
			}
		}
	}()
	return sequences
}

func (subscription *redisSubscription) Close() error {
	if err := subscription.pubsub.Close(); err != nil {
		return fmt.Errorf("close Run event subscription: %w", err)
	}
	return nil
}

func (noopNotifier) Publish(context.Context, string, int64) error {
	return nil
}

func (noopNotifier) Subscribe(context.Context, string) (Subscription, error) {
	return noopSubscription{}, nil
}

type noopNotifier struct{}

type noopSubscription struct{}

func (noopSubscription) Notifications() <-chan int64 {
	return make(<-chan int64)
}

func (noopSubscription) Close() error {
	return nil
}

func channelName(runID string) string {
	return channelPrefix + runID
}

func decodeSequence(payload string) (int64, bool) {
	message := notification{}
	if err := json.Unmarshal([]byte(payload), &message); err == nil && message.Seq > 0 {
		return message.Seq, true
	}
	sequence, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || sequence < 1 {
		return 0, false
	}
	return sequence, true
}
