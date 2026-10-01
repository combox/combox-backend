package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	ChatTypeStandard  = "standard"
	ChatTypeSecretE2E = "secret_e2e"
)

// Chat kind values accepted by CreateChatInput.Kind. An empty value keeps the
// legacy behaviour (the repository derives the kind from the member count).
const (
	ChatKindGroup  = "group"
	ChatKindDirect = "direct"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	// CodeConflict is returned for state conflicts that are not the caller's
	// syntax (poll already voted, poll already closed); it maps to HTTP 409.
	CodeConflict = "conflict"
	// CodeAlreadyExists is returned when a UNIQUE constraint rejects the
	// write (a chat folder name taken by the same user); it maps to HTTP 409.
	CodeAlreadyExists = "already_exists"
	CodeInternal      = "internal"
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

type ReactionActor struct {
	UserID string `json:"user_id"`
	At     string `json:"at"`
}

type MessageReaction struct {
	Emoji   string          `json:"emoji"`
	Count   int             `json:"count"`
	UserIDs []string        `json:"user_ids"`
	Actors  []ReactionActor `json:"actors,omitempty"`
}

type Chat struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Description is the human readable chat blurb (migration 000039); it is
	// always serialised, empty when unset.
	Description string `json:"description"`
	// IconEmoji is the short icon shown next to the title (migration 000039);
	// any text up to 8 characters, not emoji-validated.
	IconEmoji             string     `json:"icon_emoji"`
	IsDirect              bool       `json:"is_direct"`
	Type                  string     `json:"type"`
	Kind                  string     `json:"kind"`
	IsPublic              bool       `json:"is_public"`
	PublicSlug            *string    `json:"public_slug,omitempty"`
	ParentChatID          *string    `json:"parent_chat_id,omitempty"`
	ParentTitle           *string    `json:"parent_title,omitempty"`
	ChannelType           *string    `json:"channel_type,omitempty"`
	TopicNumber           *int       `json:"topic_number,omitempty"`
	IsGeneral             *bool      `json:"is_general,omitempty"`
	BotID                 *string    `json:"bot_id,omitempty"`
	PeerUserID            *string    `json:"peer_user_id,omitempty"`
	ViewerRole            *string    `json:"viewer_role,omitempty"`
	SendPermission        *string    `json:"send_permission,omitempty"`
	SubscriberCount       *int       `json:"subscriber_count,omitempty"`
	CommentsEnabled       bool       `json:"comments_enabled"`
	ReactionsEnabled      *bool      `json:"reactions_enabled,omitempty"`
	SignMessages          *bool      `json:"sign_messages,omitempty"`
	ShowAuthorsProfiles   *bool      `json:"show_authors_profiles,omitempty"`
	AutoTranslate         *bool      `json:"auto_translate,omitempty"`
	SlowModeSeconds       *int       `json:"slow_mode_seconds,omitempty"`
	DiscussionChatID      *string    `json:"discussion_chat_id,omitempty"`
	Archived              bool       `json:"archived"`
	Pinned                bool       `json:"pinned"`
	PinScope              string     `json:"pin_scope"`
	PinOrder              int64      `json:"pin_order"`
	AvatarURL             *string    `json:"avatar_data_url,omitempty"`
	AvatarBg              *string    `json:"avatar_gradient,omitempty"`
	LastMessagePreview    *string    `json:"last_message_preview,omitempty"`
	LastMessageSenderName *string    `json:"last_message_sender_name,omitempty"`
	LastMessageAt         *time.Time `json:"last_message_at,omitempty"`
	// Wallpaper is the chat's own background (migration 000038). Both fields
	// are always serialised; nil means "no wallpaper set".
	WallpaperKind  *string   `json:"wallpaper_kind"`
	WallpaperValue *string   `json:"wallpaper_value"`
	CreatedAt      time.Time `json:"created_at"`
}

type ChatInviteLink struct {
	ID        string  `json:"id"`
	ChatID    string  `json:"chat_id"`
	CreatedBy string  `json:"created_by"`
	Token     string  `json:"token"`
	Title     *string `json:"title"`
	IsPrimary bool    `json:"is_primary"`
	UseCount  int     `json:"use_count"`
	RevokedAt *string `json:"revoked_at"`
	CreatedAt string  `json:"created_at"`
}

type PublicChannelModerationEntry struct {
	UserID    string `json:"user_id"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

type ChatMember struct {
	UserID   string    `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// ChatUserState holds the per-viewer state of a chat (archive, pin, history watermark).
// It is stored per (chat_id, user_id) and is not visible to other members.
type ChatUserState struct {
	Archived         bool
	Pinned           bool
	PinScope         string
	PinOrder         int64
	HistoryClearedAt *time.Time
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
	ForwardOriginUserID      *string           `json:"forward_origin_user_id,omitempty"`
	ForwardOriginName        *string           `json:"forward_origin_name,omitempty"`
	// ForwardOriginRedacted is true when the origin owner's
	// forwarded_messages privacy rule forbids this viewer from seeing who
	// forwarded the message; the name is blanked and the user id dropped.
	ForwardOriginRedacted bool `json:"forward_origin_redacted,omitempty"`
	// ForwardOriginAvatarURL is the origin user's avatar for the Telegram
	// style forward card; it is only present when the origin is visible.
	ForwardOriginAvatarURL *string    `json:"forward_origin_avatar_data_url,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	EditedAt               *time.Time `json:"edited_at,omitempty"`
	// Poll is attached to every message that carries a poll, for every read
	// path (list, get, pin, edit, create-poll, export). It is viewer scoped:
	// tallies honour hide_results (see buildPollView).
	Poll *Poll `json:"poll,omitempty"`
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
	Items      []Message       `json:"items"`
	Statuses   []MessageStatus `json:"statuses,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// PollMaxOptions is the number of options a poll may carry. It is mirrored in
// the SDK (combox-api maxPollOptions) and reported as max_options on every
// poll endpoint response so the client can resynchronise.
const PollMaxOptions = 13

const (
	WallpaperKindNone   = "none"
	WallpaperKindPreset = "preset"
	WallpaperKindImage  = "image"
)

// PollOption is one answer choice of a poll. IDs are generated by the server,
// which is why CREATE accepts 0-based indices instead of ids.
type PollOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// PollResult is a tally row for one option, always aligned with Poll.Options.
type PollResult struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Votes   int    `json:"votes"`
	Percent int    `json:"percent"`
}

// Poll is the stored poll plus the fields computed for one viewer. The stored
// part is identical for everyone; TotalVotes/Results/Voters/ResultsHidden are
// viewer scoped (see buildPollView).
type Poll struct {
	ID               string       `json:"id"`
	ChatID           string       `json:"chat_id"`
	MessageID        string       `json:"message_id"`
	Question         string       `json:"question"`
	Description      *string      `json:"description,omitempty"`
	Options          []PollOption `json:"options"`
	ShowWhoVoted     bool         `json:"show_who_voted"`
	Multiple         bool         `json:"multiple"`
	AllowAddOptions  bool         `json:"allow_add_options"`
	AllowRevoting    bool         `json:"allow_revoting"`
	ShuffleOptions   bool         `json:"shuffle_options"`
	CorrectOptionIDs []string     `json:"correct_option_ids"`
	Explanation      *string      `json:"explanation,omitempty"`
	ClosesAt         *time.Time   `json:"closes_at,omitempty"`
	HideResults      bool         `json:"hide_results"`
	IsClosed         bool         `json:"is_closed"`
	CreatedBy        string       `json:"created_by"`
	CreatedAt        time.Time    `json:"created_at"`

	// Closed is the effective state: is_closed || closes_at elapsed.
	Closed bool `json:"closed"`
	// ResultsHidden is true when hide_results is set, the poll is still open
	// and this viewer has not voted; Results/TotalVotes/Voters are then absent.
	ResultsHidden bool                `json:"results_hidden"`
	MyOptionIDs   []string            `json:"my_option_ids"`
	TotalVotes    *int                `json:"total_votes,omitempty"`
	Results       []PollResult        `json:"results,omitempty"`
	Voters        map[string][]string `json:"voters,omitempty"`
}

// PollVote is one stored ballot.
type PollVote struct {
	UserID    string    `json:"user_id"`
	OptionIDs []string  `json:"option_ids"`
	VotedAt   time.Time `json:"voted_at"`
}

type CreatePollInput struct {
	UserID          string
	ChatID          string
	Question        string
	Description     string
	Options         []string
	ShowWhoVoted    bool
	Multiple        bool
	AllowAddOptions bool
	AllowRevoting   bool
	ShuffleOptions  bool
	// CorrectOptionIDs are 0-based option indices encoded as strings; the
	// server generates the option ids and stores those instead.
	CorrectOptionIDs []string
	Explanation      string
	ClosesAt         *time.Time
	HideResults      bool
}

type VotePollInput struct {
	UserID    string
	PollID    string
	OptionIDs []string
}

type ChatExportMessage struct {
	ID                string              `json:"id"`
	CreatedAt         time.Time           `json:"created_at"`
	EditedAt          *time.Time          `json:"edited_at,omitempty"`
	SenderUserID      string              `json:"sender_user_id"`
	SenderBotID       *string             `json:"sender_bot_id,omitempty"`
	Text              string              `json:"text"`
	ReplyToMessageID  *string             `json:"reply_to_message_id,omitempty"`
	ForwardOriginName *string             `json:"forward_origin_name,omitempty"`
	Attachments       []AttachmentSummary `json:"attachments,omitempty"`
	Reactions         []MessageReaction   `json:"reactions,omitempty"`
	Poll              *Poll               `json:"poll,omitempty"`
}

type ChatExport struct {
	ExportedAt   time.Time           `json:"exported_at"`
	Chat         Chat                `json:"chat"`
	MessageCount int                 `json:"message_count"`
	Truncated    bool                `json:"truncated"`
	Messages     []ChatExportMessage `json:"messages"`
}

// AttachmentSummary is the export-only view of a message attachment: metadata
// only, no download URLs (the export is a plain JSON archive).
type AttachmentSummary struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	MIMEType   string `json:"mime_type"`
	Kind       string `json:"kind"`
	SizeBytes  *int64 `json:"size_bytes,omitempty"`
	Width      *int   `json:"width,omitempty"`
	Height     *int   `json:"height,omitempty"`
	DurationMS *int   `json:"duration_ms,omitempty"`
}

type CreateChatInput struct {
	UserID    string
	Title     string
	MemberIDs []string
	Type      string
	// Kind is optional: "group" forces a group chat even with a single
	// participant, "direct" (or empty) keeps the legacy member-count logic.
	Kind string
}

type CreateChannelInput struct {
	UserID      string
	GroupChatID string
	Title       string
	ChannelType string
}

type CreatePublicChannelInput struct {
	UserID     string
	Title      string
	PublicSlug string
	IsPublic   bool
}

type DeleteChannelInput struct {
	UserID        string
	GroupChatID   string
	ChannelChatID string
}

type OptionalString struct {
	Set   bool
	Value *string
}

type OptionalBool struct {
	Set   bool
	Value bool
}

type OptionalInt struct {
	Set   bool
	Value int
}

type UpdateChatInput struct {
	UserID              string
	ChatID              string
	Title               OptionalString
	AvatarDataURL       OptionalString
	AvatarGradient      OptionalString
	CommentsEnabled     OptionalBool
	ReactionsEnabled    OptionalBool
	SignMessages        OptionalBool
	ShowAuthorsProfiles OptionalBool
	AutoTranslate       OptionalBool
	SlowModeSeconds     OptionalInt
	DiscussionChatID    OptionalString
	IsPublic            OptionalBool
	PublicSlug          OptionalString
	SendPermission      OptionalString
	Description         OptionalString
	IconEmoji           OptionalString
	ChannelType         OptionalString
}

// ChannelType values accepted by UpdateChatInput.ChannelType; they mirror the
// chk_chats_channel_type CHECK constraint (migration 000021).
const (
	ChannelTypeText  = "text"
	ChannelTypeVoice = "voice"
)

// Length caps enforced by UpdateChat for the free-text settings added in
// migration 000039; both are counted in runes, not bytes.
const (
	ChatDescriptionMaxLen = 255
	ChatIconEmojiMaxLen   = 8
)

// SendPermission values accepted by UpdateChatInput.SendPermission.
const (
	SendPermissionAll    = "all"
	SendPermissionAdmins = "admins"
)

// NormalizeSendPermission maps an arbitrary value onto the supported set.
// An empty value keeps the current behaviour (everyone may post).
func NormalizeSendPermission(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SendPermissionAdmins, "admin", "admins_only", "moderators":
		return SendPermissionAdmins
	case "", SendPermissionAll:
		return SendPermissionAll
	default:
		return ""
	}
}

type CreateMessageInput struct {
	UserID           string
	BotID            string
	ChatID           string
	Content          string
	ReplyToMessageID string
	AttachmentIDs    []string
	SenderDeviceID   string
	Envelopes        []E2EEnvelope
}

type ListMessagesInput struct {
	UserID   string
	ChatID   string
	Limit    int
	Cursor   string
	DeviceID string
}

type CreateDirectMessageInput struct {
	UserID           string
	RecipientUserID  string
	Content          string
	ReplyToMessageID string
	AttachmentIDs    []string
}

type OpenDirectChatInput struct {
	UserID          string
	RecipientUserID string
}

type CreateInviteLinkInput struct {
	UserID string
	ChatID string
	Title  string
}

type ChatRepository interface {
	CreateChat(ctx context.Context, title string, memberIDs []string, creatorID string, chatType string, kind string) (Chat, error)
	CreateChannel(ctx context.Context, parentChatID, title, channelType, creatorID string) (Chat, error)
	CreatePublicChannel(ctx context.Context, title, publicSlug, creatorID string, isPublic bool) (Chat, error)
	GetOrCreateCommentThread(ctx context.Context, channelChatID, rootMessageID, creatorUserID string) (threadChatID string, err error)
	IsPublicChannelBanned(ctx context.Context, channelChatID, userID string) (bool, error)
	IsPublicChannelMuted(ctx context.Context, channelChatID, userID string) (bool, error)
	UpsertPublicChannelBan(ctx context.Context, channelChatID, userID, actorUserID string) error
	DeletePublicChannelBan(ctx context.Context, channelChatID, userID string) error
	UpsertPublicChannelMute(ctx context.Context, channelChatID, userID, actorUserID string) error
	DeletePublicChannelMute(ctx context.Context, channelChatID, userID string) error
	ListPublicChannelBans(ctx context.Context, channelChatID string, limit int) ([]PublicChannelModerationEntry, error)
	ListPublicChannelMutes(ctx context.Context, channelChatID string, limit int) ([]PublicChannelModerationEntry, error)
	DeleteChannel(ctx context.Context, parentChatID, channelChatID string) error
	DeleteChat(ctx context.Context, chatID string) error
	FindDirectChatByMembers(ctx context.Context, userAID, userBID, chatType string) (Chat, bool, error)
	ListChatsByUser(ctx context.Context, userID string) ([]Chat, error)
	ListChannelsByParent(ctx context.Context, parentChatID, userID string) ([]Chat, error)
	GetChat(ctx context.Context, chatID string) (Chat, error)
	UpdateChat(ctx context.Context, input UpdateChatInput) (Chat, error)
	ListChatInviteLinks(ctx context.Context, chatID string) ([]ChatInviteLink, error)
	CreateChatInviteLink(ctx context.Context, chatID, createdBy, title string, isPrimary bool) (ChatInviteLink, error)
	GetChatInviteLinkByToken(ctx context.Context, token string) (ChatInviteLink, error)
	IncrementChatInviteLinkUse(ctx context.Context, linkID string) error
	ListChatMembers(ctx context.Context, chatID string, includeBanned bool) ([]ChatMember, error)
	AddChatMembers(ctx context.Context, chatID string, memberIDs []string) error
	UpdateChatMemberRole(ctx context.Context, chatID, userID, role string) error
	RemoveChatMember(ctx context.Context, chatID, userID string) error
	ListChatMemberIDs(ctx context.Context, chatID string) ([]string, error)
	GetChatMemberRole(ctx context.Context, chatID, userID string) (string, error)
	// CountChannelSubscribers counts the unique subscriber rows of a channel
	// (every chat_members row except bans; rows are unique by the
	// PRIMARY KEY (chat_id, user_id)). It backs the subscriber_count of
	// public standalone channels, including the subscribe response.
	CountChannelSubscribers(ctx context.Context, chatID string) (int, error)
	IsChatMember(ctx context.Context, chatID, userID string) (bool, error)
	GetChatUserState(ctx context.Context, userID, chatID string) (ChatUserState, error)
	ListChatUserStates(ctx context.Context, userID string) (map[string]ChatUserState, error)
	SetChatArchived(ctx context.Context, userID, chatID string, archived bool) error
	SetChatPinned(ctx context.Context, userID, chatID string, pinned bool, pinScope string, pinOrder int64) error
	SetChatHistoryCleared(ctx context.Context, userID, chatID string, at time.Time) error
	DeleteChatUserState(ctx context.Context, userID, chatID string) error
	SetChatPinnedMessage(ctx context.Context, chatID, messageID string) error
	GetChatPinnedMessageID(ctx context.Context, chatID string) (string, error)
	SetChatWallpaper(ctx context.Context, chatID, wallpaperKind, wallpaperValue string) error
}

type MessageRepository interface {
	CreateMessage(ctx context.Context, chatID, userID, content, replyToMessageID string) (Message, error)
	CreateMessageAsBot(ctx context.Context, chatID, botID, content, replyToMessageID string) (Message, error)
	CreateMessageWithAttachments(ctx context.Context, chatID, userID, content, replyToMessageID string, attachmentIDs []string) (Message, error)
	CreateMessageE2E(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, replyToMessageID string) (Message, error)
	CreateMessageE2EWithAttachments(ctx context.Context, chatID, userID, senderDeviceID string, envelopes []E2EEnvelope, replyToMessageID string, attachmentIDs []string) (Message, error)
	CreateForwardedMessage(ctx context.Context, chatID, sourceMessageID, userID string) (Message, error)
	ListMessages(ctx context.Context, chatID string, limit int, cursor string) (MessagePage, error)
	ListMessagesForDevice(ctx context.Context, chatID, deviceID string, limit int, cursor string) (MessagePage, error)
	UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string) (MessageStatus, error)
	UpdateMessageContent(ctx context.Context, chatID, messageID, editorUserID, newContent string, attachmentIDs []string, allowForeign bool) (Message, error)
	GetMessageByID(ctx context.Context, chatID, messageID string) (Message, error)
	GetMessageMeta(ctx context.Context, messageID string) (MessageMeta, error)
	SoftDeleteMessage(ctx context.Context, chatID, messageID, deleterUserID string, allowForeign bool) error
	ToggleMessageReaction(ctx context.Context, chatID, messageID, userID, emoji string) ([]MessageReaction, string, error)
	// CountVisibleMessages counts the live (not soft-deleted) messages of a
	// chat that a viewer can still see: created after visibleAfter when set.
	// It backs the per-user history clear acknowledgement.
	CountVisibleMessages(ctx context.Context, chatID string, visibleAfter *time.Time) (int, error)
	// ListAttachmentSummaries returns attachment metadata keyed by message id
	// for the given messages (used by the chat export).
	ListAttachmentSummaries(ctx context.Context, messageIDs []string) (map[string][]AttachmentSummary, error)
}

// PollRepository stores polls and ballots. Polls hang off an ordinary chat
// message; votes are one row per (poll, voter).
type PollRepository interface {
	CreatePoll(ctx context.Context, poll Poll) (Poll, error)
	GetPoll(ctx context.Context, pollID string) (Poll, error)
	GetPollsByMessageIDs(ctx context.Context, messageIDs []string) ([]Poll, error)
	// ListVotesForPolls returns every ballot of the given polls in one query.
	ListVotesForPolls(ctx context.Context, pollIDs []string) (map[string][]PollVote, error)
	UpsertVote(ctx context.Context, pollID, userID string, optionIDs []string, at time.Time) error
	ClosePoll(ctx context.Context, pollID string) error
}

type StatusRepository interface {
	UpsertMessageStatus(ctx context.Context, chatID, messageID, userID, status string, at time.Time) (MessageStatus, error)
	ListLatestMessageStatuses(ctx context.Context, messageIDs []string) ([]MessageStatus, error)
}

type MessageEventPublisher interface {
	PublishDeviceMessageCreated(ctx context.Context, ev DeviceMessageCreatedEvent) error
	PublishUserMessageCreated(ctx context.Context, ev UserMessageCreatedEvent) error
	PublishMessageStatus(ctx context.Context, ev MessageStatusEvent) error
	PublishMessageUpdated(ctx context.Context, ev MessageUpdatedEvent) error
	PublishMessageDeleted(ctx context.Context, ev MessageDeletedEvent) error
	PublishMessageReaction(ctx context.Context, ev MessageReactionEvent) error
	PublishChatUpdated(ctx context.Context, ev ChatUpdatedEvent) error
}

type NotificationRepository interface {
	IncrementChatUnread(ctx context.Context, userID, chatID string, delta int) (int, error)
	ResetChatUnread(ctx context.Context, userID, chatID string) error
}

type InviteRepository interface {
	Create(ctx context.Context, chatID, inviterID, inviteeID string, ttl time.Duration) (ChatInvite, error)
	Consume(ctx context.Context, token string) (ChatInvite, bool, error)
}

type UserMessageCreatedEvent struct {
	MessageID       string
	ChatID          string
	SenderUserID    string
	RecipientUserID string
	CreatedAt       time.Time
	// Preview is the plaintext message head used for chat previews and the
	// desktop notification body; it is empty for encrypted payloads.
	Preview string
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

type MessageStatus struct {
	MessageID string    `json:"message_id"`
	ChatID    string    `json:"chat_id"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
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

// ChatUpdatedEvent is broadcast to every current member after shared chat
// fields changed. Chat is the chat list JSON shape; per-viewer archive/pin
// flags are still zero because this snapshot precedes attachChatUserState.
type ChatUpdatedEvent struct {
	ChatID          string
	RecipientUserID string
	Chat            Chat
	UpdatedAt       time.Time
}

type EditMessageInput struct {
	UserID        string
	ChatID        string
	MessageID     string
	Content       string
	AttachmentIDs []string
}

type ForwardMessageInput struct {
	UserID          string
	ChatID          string
	SourceMessageID string
}

type PinMessageInput struct {
	UserID    string
	ChatID    string
	MessageID string
	Pinned    bool
}

type UpsertMessageStatusInput struct {
	UserID    string
	ChatID    string
	MessageID string
	Status    string
}

type Service struct {
	chats      ChatRepository
	messages   MessageRepository
	polls      PollRepository
	publisher  MessageEventPublisher
	statusRepo StatusRepository
	// folders is the chat folder store (migration 000041_chat_folders); it is
	// required by every chat folder entry point.
	folders ChatFolderRepository
	// folderInvites is the chat folder share-link store (migration
	// 000043_chat_folder_invites); it is required by the folder invite entry
	// points. A nil repo answers "not configured" instead of panicking.
	folderInvites ChatFolderInviteRepository
	notifications NotificationRepository
	avatars       AvatarStore
	avatarTTL     time.Duration
	invites       InviteRepository
	inviteTTL     time.Duration
	photoHistory  ProfilePhotoRecorder
	// events is the recent-actions journal (migration 000040). Writes through
	// it are best effort; a nil repo silently disables recording.
	events ChatEventRepository
	// forwardPrivacy enforces the origin owner's forwarded_messages rule when
	// a message is serialised for a viewer; nil keeps the legacy behaviour.
	forwardPrivacy ForwardPrivacy
	// forwardOriginProfiles resolves the origin user's avatar for the forward
	// card; it is only consulted when the origin is visible.
	forwardOriginProfiles ForwardOriginProfiles
	// publicAppBaseURL is used to generate human-friendly invite links sent in messages.
	// If empty, we fall back to a relative URL (works inside the web app origin).
	publicAppBaseURL string
}

var ErrChatNotFound = errors.New("chat not found")
var ErrMessageNotFound = errors.New("message not found")
var ErrInvalidAttachments = errors.New("invalid attachments")
var ErrPollNotFound = errors.New("poll not found")

// Chat folder sentinel errors returned by ChatFolderRepository implementations
// and translated into typed service errors by the chat folder entry points.
var (
	// ErrChatFolderNotFound is returned for a missing folder or one that
	// belongs to somebody else (both must read as 404).
	ErrChatFolderNotFound = errors.New("chat folder not found")
	// ErrChatFolderNameTaken is returned when UNIQUE (user_id, name) rejects
	// the name.
	ErrChatFolderNameTaken = errors.New("chat folder name already exists")
	// ErrChatFolderLimit is returned when the user already owns the maximum
	// number of folders.
	ErrChatFolderLimit = errors.New("chat folder limit reached")
	// ErrChatFolderInviteNotFound is returned for a missing or revoked folder
	// invite, or when a folder has no live invite to revoke. It always reads
	// as 404, never leaking whether the token ever existed.
	ErrChatFolderInviteNotFound = errors.New("chat folder invite not found")
	// ErrChatFolderInviteTaken is returned when minting an invite collides
	// with an existing row (token reuse or a concurrent create racing the
	// single-active-invite guard). Callers re-read the active invite or retry
	// with a fresh token.
	ErrChatFolderInviteTaken = errors.New("chat folder invite already exists")
)

const (
	avatarRefPrefix  = "s3key:"
	defaultAvatarTTL = time.Hour * 24 * 7
	defaultInviteTTL = time.Hour * 24 * 7
)

type AvatarStore interface {
	PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
}

// ForwardPrivacy evaluates one privacy parameter of a message origin owner
// (implemented by the privacy service). WithMemo starts a memoised batch so a
// page of messages costs at most one settings read per distinct origin.
type ForwardPrivacy interface {
	WithMemo(ctx context.Context) context.Context
	Evaluate(ctx context.Context, viewerID, targetID, param string) (bool, error)
}

// ForwardOriginProfiles resolves profile data of a forward origin for the
// forward card (implemented over the auth profile reader, which already
// presigns avatar object keys).
type ForwardOriginProfiles interface {
	GetAvatarDataURL(ctx context.Context, userID string) (*string, error)
}

func New(chats ChatRepository, messages MessageRepository) (*Service, error) {
	if chats == nil {
		return nil, errors.New("chat repository is required")
	}
	if messages == nil {
		return nil, errors.New("message repository is required")
	}
	return &Service{chats: chats, messages: messages}, nil
}

func NewWithPublisher(chats ChatRepository, messages MessageRepository, publisher MessageEventPublisher) (*Service, error) {
	svc, err := New(chats, messages)
	if err != nil {
		return nil, err
	}
	svc.publisher = publisher
	return svc, nil
}

func NewWithPublisherAndStatusRepo(chats ChatRepository, messages MessageRepository, publisher MessageEventPublisher, statusRepo StatusRepository) (*Service, error) {
	svc, err := NewWithPublisher(chats, messages, publisher)
	if err != nil {
		return nil, err
	}
	svc.statusRepo = statusRepo
	return svc, nil
}

func (s *Service) SetAvatarStore(store AvatarStore, ttl time.Duration) {
	s.avatars = store
	if ttl <= 0 {
		ttl = defaultAvatarTTL
	}
	s.avatarTTL = ttl
}

func (s *Service) SetNotificationRepository(repo NotificationRepository) {
	s.notifications = repo
}

func (s *Service) SetInviteRepository(repo InviteRepository, ttl time.Duration) {
	s.invites = repo
	if ttl <= 0 {
		ttl = defaultInviteTTL
	}
	s.inviteTTL = ttl
}

func (s *Service) SetPublicAppBaseURL(base string) {
	base = strings.TrimSpace(base)
	base = strings.TrimRight(base, "/")
	s.publicAppBaseURL = base
}

// SetForwardPrivacy installs the forwarded_messages privacy gate used while
// serialising messages for a viewer.
func (s *Service) SetForwardPrivacy(privacy ForwardPrivacy) {
	s.forwardPrivacy = privacy
}

// SetForwardOriginProfiles installs the avatar resolver of forward origins.
func (s *Service) SetForwardOriginProfiles(profiles ForwardOriginProfiles) {
	s.forwardOriginProfiles = profiles
}

func (s *Service) inviteURL(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if s.publicAppBaseURL == "" {
		return "/#invite:" + token
	}
	return s.publicAppBaseURL + "/#invite:" + token
}

func (s *Service) resolveAvatarURL(ctx context.Context, raw *string) *string {
	if raw == nil {
		return nil
	}
	ref := strings.TrimSpace(*raw)
	if ref == "" {
		return nil
	}
	if !strings.HasPrefix(ref, avatarRefPrefix) {
		return &ref
	}
	if s.avatars == nil {
		return nil
	}
	objectKey := strings.TrimSpace(strings.TrimPrefix(ref, avatarRefPrefix))
	if objectKey == "" {
		return nil
	}
	ttl := s.avatarTTL
	if ttl <= 0 {
		ttl = defaultAvatarTTL
	}
	presigned, err := s.avatars.PresignGetObject(ctx, objectKey, ttl)
	if err != nil {
		return nil
	}
	return &presigned
}

func (s *Service) nowUTC() time.Time {
	return time.Now().UTC()
}
