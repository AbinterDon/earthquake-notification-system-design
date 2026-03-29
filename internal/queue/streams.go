// Package queue provides thin wrappers around Redis Streams for reliable,
// low-latency message passing between services.
//
// Why Redis Streams over Kafka?
//   - Memory-first: sub-millisecond enqueue latency
//   - No ZooKeeper / broker cluster overhead for a demo deployment
//   - Consumer groups provide at-least-once delivery with per-message ACK
//
// The Outbox pattern (notification_outbox table) ensures idempotency so
// at-least-once delivery is safe to use here.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// StreamNames for each channel.
const (
	StreamEarthquakes = "stream:earthquakes"
	StreamAPNs        = "stream:apns"
	StreamFCM         = "stream:fcm"
)

// Message wraps a single stream entry delivered to a consumer.
type Message struct {
	ID      string // Redis stream message ID (e.g. "1700000000000-0")
	Payload string // raw JSON payload
}

// --- Producer ----------------------------------------------------------------

// Producer publishes JSON-encoded messages onto a Redis Stream.
type Producer struct {
	rdb    *redis.Client
	stream string
}

func NewProducer(rdb *redis.Client, stream string) *Producer {
	return &Producer{rdb: rdb, stream: stream}
}

// Publish encodes v as JSON and appends it to the stream.
func (p *Producer) Publish(ctx context.Context, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("queue: marshal: %w", err)
	}
	return p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]interface{}{"payload": string(data)},
	}).Err()
}

// --- Consumer ----------------------------------------------------------------

// Consumer reads messages from a Redis Stream consumer group.
type Consumer struct {
	rdb      *redis.Client
	stream   string
	group    string
	consumer string // unique name within the group (e.g. hostname+PID)
}

func NewConsumer(rdb *redis.Client, stream, group, consumer string) *Consumer {
	return &Consumer{rdb: rdb, stream: stream, group: group, consumer: consumer}
}

// EnsureGroup creates the consumer group if it does not yet exist.
// Uses offset "0" so that on a fresh stream the consumer reads from the
// beginning, and unacknowledged messages survive restarts.
func (c *Consumer) EnsureGroup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, c.stream, c.group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("queue: ensure group %s/%s: %w", c.stream, c.group, err)
	}
	return nil
}

// ReadMessages returns up to count messages, blocking up to 5 s if the stream
// is empty. It first tries to autoclaim messages that have been pending for
// more than 2 minutes (handles crashed consumers), then reads new deliveries.
func (c *Consumer) ReadMessages(ctx context.Context, count int) ([]Message, error) {
	// 1. Autoclaim stale pending messages from any consumer in the group.
	autoclaim, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   c.stream,
		Group:    c.group,
		Consumer: c.consumer,
		MinIdle:  2 * time.Minute,
		Start:    "0",
		Count:    int64(count),
	}).Result()
	if err == nil && len(autoclaim.Messages) > 0 {
		return toMessages(autoclaim.Messages), nil
	}

	// 2. Read newly delivered messages.
	entries, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.consumer,
		Streams:  []string{c.stream, ">"},
		Count:    int64(count),
		Block:    5 * time.Second,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // timeout with no messages
		}
		return nil, fmt.Errorf("queue: xreadgroup %s: %w", c.stream, err)
	}

	var msgs []Message
	for _, s := range entries {
		msgs = append(msgs, toMessages(s.Messages)...)
	}
	return msgs, nil
}

// Ack acknowledges a message so it is removed from the PEL.
func (c *Consumer) Ack(ctx context.Context, msgID string) error {
	return c.rdb.XAck(ctx, c.stream, c.group, msgID).Err()
}

func toMessages(entries []redis.XMessage) []Message {
	msgs := make([]Message, 0, len(entries))
	for _, e := range entries {
		payload, _ := e.Values["payload"].(string)
		msgs = append(msgs, Message{ID: e.ID, Payload: payload})
	}
	return msgs
}
