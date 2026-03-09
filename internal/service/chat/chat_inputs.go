package chat

type CreateChatInput struct {
	UserID    string
	Title     string
	MemberIDs []string
	Type      string
}

type CreateChannelInput struct {
	UserID      string
	GroupChatID string
	Title       string
	ChannelType string
}

type CreateStandaloneChannelInput struct {
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

type UpdateChatInput struct {
	UserID           string
	ChatID           string
	Title            OptionalString
	AvatarDataURL    OptionalString
	AvatarGradient   OptionalString
	CommentsEnabled  OptionalBool
	ReactionsEnabled OptionalBool
	IsPublic         OptionalBool
	PublicSlug       OptionalString
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

type UpsertMessageStatusInput struct {
	UserID    string
	ChatID    string
	MessageID string
	Status    string
}
