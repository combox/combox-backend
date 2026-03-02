package valkey

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const EventTypeDeviceMessageCreated = "message.created"

const EventTypeUserMessageCreated = "message.created"

const EventTypeMessageStatus = "message.status"

const EventTypeMessageUpdated = "message.updated"

const EventTypeMessageDeleted = "message.deleted"

const EventTypeMessageReaction = "message.reaction"
const EventTypePresence = "presence.update"
const EventTypeNotification = "notification"

type DeviceMessageCreatedEvent struct {
	Type              string    `json:"type"`
	MessageID         string    `json:"message_id"`
	ChatID            string    `json:"chat_id"`
	SenderUserID      string    `json:"sender_user_id"`
	SenderDeviceID    string    `json:"sender_device_id"`
	RecipientDeviceID string    `json:"recipient_device_id"`
	Alg               string    `json:"alg"`
	Header            string    `json:"header"`
	Ciphertext        string    `json:"ciphertext"`
	CreatedAt         time.Time `json:"created_at"`
}

type UserMessageCreatedEvent struct {
	Type            string    `json:"type"`
	MessageID       string    `json:"message_id"`
	ChatID          string    `json:"chat_id"`
	SenderUserID    string    `json:"sender_user_id"`
	RecipientUserID string    `json:"recipient_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

type MessageStatusEvent struct {
	Type      string    `json:"type"`
	MessageID string    `json:"message_id"`
	ChatID    string    `json:"chat_id"`
	UserID    string    `json:"user_id"`
	DeviceID  string    `json:"device_id,omitempty"`
	Status    string    `json:"status"`
	At        time.Time `json:"at"`
}

type MessageUpdatedEvent struct {
	Type            string    `json:"type"`
	MessageID       string    `json:"message_id"`
	ChatID          string    `json:"chat_id"`
	EditorUserID    string    `json:"editor_user_id"`
	RecipientUserID string    `json:"recipient_user_id"`
	Content         string    `json:"content"`
	EditedAt        time.Time `json:"edited_at"`
}

type MessageDeletedEvent struct {
	Type            string    `json:"type"`
	MessageID       string    `json:"message_id"`
	ChatID          string    `json:"chat_id"`
	ActorUserID     string    `json:"actor_user_id"`
	RecipientUserID string    `json:"recipient_user_id"`
	At              time.Time `json:"at"`
}

type MessageReaction struct {
	Emoji   string   `json:"emoji"`
	UserIDs []string `json:"user_ids"`
}

type MessageReactionEvent struct {
	Type            string            `json:"type"`
	MessageID       string            `json:"message_id"`
	ChatID          string            `json:"chat_id"`
	ActorUserID     string            `json:"actor_user_id"`
	RecipientUserID string            `json:"recipient_user_id"`
	Emoji           string            `json:"emoji"`
	Action          string            `json:"action"`
	Reactions       []MessageReaction `json:"reactions"`
	At              time.Time         `json:"at"`
}

type PresenceEvent struct {
	Type      string    `json:"type"`
	UserID    string    `json:"user_id"`
	Online    bool      `json:"online"`
	LastSeen  time.Time `json:"last_seen"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NotificationEvent struct {
	Type      string    `json:"type"`
	UserID    string    `json:"user_id"`
	Kind      string    `json:"kind"`
	Payload   any       `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

type EventPublisher struct {
	c *Client
}

func NewEventPublisher(c *Client) *EventPublisher {
	return &EventPublisher{c: c}
}

func NewEventPublisherFromRedis(rdb *redis.Client) *EventPublisher {
	if rdb == nil {
		return &EventPublisher{}
	}
	return &EventPublisher{c: &Client{rdb: rdb}}
}

func deviceChannel(deviceID string) string {
	return "device:" + deviceID
}

func userChannel(userID string) string {
	return "user:" + userID
}

func presenceChannel(userID string) string {
	return "presence:" + userID
}

func (p *EventPublisher) PublishDeviceMessageCreated(ctx context.Context, ev DeviceMessageCreatedEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeDeviceMessageCreated
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, deviceChannel(ev.RecipientDeviceID), payload).Err()
}

func (p *EventPublisher) PublishUserMessageCreated(ctx context.Context, ev UserMessageCreatedEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeUserMessageCreated
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.RecipientUserID), payload).Err()
}

func (p *EventPublisher) PublishMessageStatus(ctx context.Context, ev MessageStatusEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeMessageStatus
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.UserID), payload).Err()
}

func (p *EventPublisher) PublishMessageUpdated(ctx context.Context, ev MessageUpdatedEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeMessageUpdated
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.RecipientUserID), payload).Err()
}

func (p *EventPublisher) PublishMessageDeleted(ctx context.Context, ev MessageDeletedEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeMessageDeleted
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.RecipientUserID), payload).Err()
}

func (p *EventPublisher) PublishMessageReaction(ctx context.Context, ev MessageReactionEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeMessageReaction
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.RecipientUserID), payload).Err()
}

func (p *EventPublisher) PublishPresence(ctx context.Context, ev PresenceEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypePresence
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, presenceChannel(ev.UserID), payload).Err()
}

func (p *EventPublisher) PublishNotification(ctx context.Context, ev NotificationEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeNotification
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.UserID), payload).Err()
}
