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
const EventTypeCallStarted = "call.started"
const EventTypeCallEnded = "call.ended"
const EventTypeProfileUpdate = "profile.update"
const EventTypeChatUpdated = "chat.updated"

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
	Preview         string    `json:"preview,omitempty"`
}

type MessageStatusEvent struct {
	Type            string    `json:"type"`
	MessageID       string    `json:"message_id"`
	ChatID          string    `json:"chat_id"`
	UserID          string    `json:"user_id"`
	RecipientUserID string    `json:"recipient_user_id,omitempty"`
	DeviceID        string    `json:"device_id,omitempty"`
	Status          string    `json:"status"`
	At              time.Time `json:"at"`
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
	// LastSeenVisible carries the owner's "show last seen" setting. A nil value
	// leaves the visibility the client already fetched over REST untouched.
	LastSeenVisible *bool `json:"last_seen_visible,omitempty"`
}

type NotificationEvent struct {
	Type      string    `json:"type"`
	UserID    string    `json:"user_id"`
	Kind      string    `json:"kind"`
	Muted     bool      `json:"muted,omitempty"`
	Payload   any       `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// CallEvent tells a chat member that a call started or ended so clients that
// are not connected to the call signaling channel can ring / dismiss.
type CallEvent struct {
	Type      string    `json:"type"`
	UserID    string    `json:"user_id"`
	CallID    string    `json:"call_id"`
	ChatID    string    `json:"chat_id"`
	Kind      string    `json:"kind"`
	E2EE      bool      `json:"e2ee,omitempty"`
	StartedBy string    `json:"started_by"`
	StartedAt time.Time `json:"started_at"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ProfileUpdateEvent carries the public directory shape of a user whose own
// profile fields (name, avatar, username) changed, so connected clients can
// refresh cached names/avatars without a reload.
type ProfileUpdateEvent struct {
	Type            string  `json:"type"`
	UserID          string  `json:"user_id"`
	RecipientUserID string  `json:"recipient_user_id,omitempty"`
	ID              string  `json:"id"`
	Email           string  `json:"email"`
	Username        string  `json:"username"`
	FirstName       string  `json:"first_name"`
	LastName        *string `json:"last_name,omitempty"`
	BirthDate       *string `json:"birth_date,omitempty"`
	AvatarDataURL   *string `json:"avatar_data_url,omitempty"`
	AvatarGradient  *string `json:"avatar_gradient,omitempty"`
}

// ChatUpdatedEvent tells every member of a chat that shared chat fields
// changed. Chat holds the chat list JSON shape.
type ChatUpdatedEvent struct {
	Type            string    `json:"type"`
	ChatID          string    `json:"chat_id"`
	RecipientUserID string    `json:"recipient_user_id,omitempty"`
	Chat            any       `json:"chat"`
	UpdatedAt       time.Time `json:"updated_at"`
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
	recipientID := ev.RecipientUserID
	if recipientID == "" {
		recipientID = ev.UserID
	}
	return p.c.Client().Publish(ctx, userChannel(recipientID), payload).Err()
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

// PublishCallStarted informs a single chat member about a fresh call.
func (p *EventPublisher) PublishCallStarted(ctx context.Context, ev CallEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	ev.Type = EventTypeCallStarted
	return p.publishCall(ctx, ev)
}

// PublishCallEnded informs a single chat member that a call finished.
func (p *EventPublisher) PublishCallEnded(ctx context.Context, ev CallEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	ev.Type = EventTypeCallEnded
	return p.publishCall(ctx, ev)
}

func (p *EventPublisher) publishCall(ctx context.Context, ev CallEvent) error {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.UserID), payload).Err()
}

// PublishProfileUpdate delivers a refreshed public user shape to one recipient.
func (p *EventPublisher) PublishProfileUpdate(ctx context.Context, ev ProfileUpdateEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeProfileUpdate
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	recipientID := ev.RecipientUserID
	if recipientID == "" {
		recipientID = ev.UserID
	}
	return p.c.Client().Publish(ctx, userChannel(recipientID), payload).Err()
}

// PublishChatUpdated delivers a refreshed chat to one member.
func (p *EventPublisher) PublishChatUpdated(ctx context.Context, ev ChatUpdatedEvent) error {
	if p == nil || p.c == nil {
		return nil
	}
	if ev.Type == "" {
		ev.Type = EventTypeChatUpdated
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return p.c.Client().Publish(ctx, userChannel(ev.RecipientUserID), payload).Err()
}
