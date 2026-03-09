package chat

import "time"

type UserMessageCreatedEvent struct {
	MessageID       string
	ChatID          string
	SenderUserID    string
	RecipientUserID string
	CreatedAt       time.Time
}

type DeviceMessageCreatedEvent struct {
	MessageID         string
	ChatID            string
	SenderUserID      string
	SenderDeviceID    string
	RecipientDeviceID string
	Alg               string
	Header            string
	Ciphertext        string
	CreatedAt         time.Time
}

type MessageStatusEvent struct {
	MessageID       string
	ChatID          string
	UserID          string
	RecipientUserID string
	Status          string
	At              time.Time
}

type MessageUpdatedEvent struct {
	MessageID       string
	ChatID          string
	EditorUserID    string
	RecipientUserID string
	Content         string
	EditedAt        time.Time
}

type MessageReactionEvent struct {
	MessageID       string
	ChatID          string
	ActorUserID     string
	RecipientUserID string
	Emoji           string
	Action          string
	Reactions       []MessageReaction
	At              time.Time
}

type MessageDeletedEvent struct {
	MessageID       string
	ChatID          string
	ActorUserID     string
	RecipientUserID string
	At              time.Time
}
