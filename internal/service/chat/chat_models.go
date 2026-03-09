package chat

import (
	"fmt"
	"time"
)

const (
	ChatTypeStandard  = "standard"
	ChatTypeSecretE2E = "secret_e2e"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
)

type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

type MessageMeta struct {
	ID               string
	ChatID           string
	UserID           string
	ReplyToMessageID string
	SenderBotID      *string
	IsE2E            bool
}

type MessageReaction struct {
	Emoji   string   `json:"emoji"`
	Count   int      `json:"count"`
	UserIDs []string `json:"user_ids"`
}

type Chat struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	IsDirect           bool      `json:"is_direct"`
	Type               string    `json:"type"`
	Kind               string    `json:"kind"`
	IsPublic           bool      `json:"is_public"`
	PublicSlug         *string   `json:"public_slug,omitempty"`
	ParentChatID       *string   `json:"parent_chat_id,omitempty"`
	ChannelType        *string   `json:"channel_type,omitempty"`
	TopicNumber        *int      `json:"topic_number,omitempty"`
	IsGeneral          *bool     `json:"is_general,omitempty"`
	BotID              *string   `json:"bot_id,omitempty"`
	PeerUserID         *string   `json:"peer_user_id,omitempty"`
	ViewerRole         *string   `json:"viewer_role,omitempty"`
	SubscriberCount    *int      `json:"subscriber_count,omitempty"`
	CommentsEnabled    bool      `json:"comments_enabled"`
	ReactionsEnabled   bool      `json:"reactions_enabled"`
	AvatarURL          *string   `json:"avatar_data_url,omitempty"`
	AvatarBg           *string   `json:"avatar_gradient,omitempty"`
	LastMessagePreview *string   `json:"last_message_preview,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type ChatInviteLink struct {
	ID        string     `json:"id"`
	ChatID    string     `json:"chat_id"`
	CreatedBy string     `json:"created_by"`
	Token     string     `json:"token"`
	Title     *string    `json:"title,omitempty"`
	IsPrimary bool       `json:"is_primary"`
	UseCount  int        `json:"use_count"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type ChatMember struct {
	UserID   string    `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type ChatInvite struct {
	Token     string
	ChatID    string
	InviterID string
	InviteeID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Message struct {
	ID                       string            `json:"id"`
	ChatID                   string            `json:"chat_id"`
	UserID                   string            `json:"user_id"`
	SenderBotID              *string           `json:"sender_bot_id,omitempty"`
	Content                  string            `json:"content"`
	ReplyToMessageID         *string           `json:"reply_to_message_id,omitempty"`
	ReplyToMessagePreview    *string           `json:"reply_to_message_preview,omitempty"`
	ReplyToMessageSenderName *string           `json:"reply_to_message_sender_name,omitempty"`
	IsE2E                    bool              `json:"is_e2e"`
	E2E                      *E2EPayload       `json:"e2e,omitempty"`
	Reactions                []MessageReaction `json:"reactions,omitempty"`
	CreatedAt                time.Time         `json:"created_at"`
	EditedAt                 *time.Time        `json:"edited_at,omitempty"`
}

type E2EEnvelope struct {
	RecipientDeviceID string `json:"recipient_device_id"`
	Alg               string `json:"alg"`
	Header            string `json:"header"`
	Ciphertext        string `json:"ciphertext"`
}

type E2EPayload struct {
	SenderDeviceID string       `json:"sender_device_id"`
	Envelope       *E2EEnvelope `json:"envelope,omitempty"`
}

type MessagePage struct {
	Items      []Message `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type MessageStatus struct {
	MessageID string    `json:"message_id"`
	ChatID    string    `json:"chat_id"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}
