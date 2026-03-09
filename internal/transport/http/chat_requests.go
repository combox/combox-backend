package http

import chatsvc "combox-backend/internal/service/chat"

type createChatRequest struct {
	Title     string   `json:"title"`
	MemberIDs []string `json:"member_ids"`
	Type      string   `json:"type"`
}

type createChannelRequest struct {
	Title       string `json:"title"`
	ChannelType string `json:"channel_type"`
}

type updateChatRequest struct {
	Title            *string `json:"title"`
	AvatarDataURL    *string `json:"avatar_data_url"`
	AvatarGradient   *string `json:"avatar_gradient"`
	CommentsEnabled  *bool   `json:"comments_enabled"`
	ReactionsEnabled *bool   `json:"reactions_enabled"`
	IsPublic         *bool   `json:"is_public"`
	PublicSlug       *string `json:"public_slug"`
}

type createInviteLinkRequest struct {
	Title string `json:"title"`
}

type addMembersRequest struct {
	MemberIDs []string `json:"member_ids"`
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

type createMessageRequest struct {
	Content          string   `json:"content"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	AttachmentIDs    []string `json:"attachment_ids"`
	E2E              *struct {
		SenderDeviceID string                `json:"sender_device_id"`
		Envelopes      []chatsvc.E2EEnvelope `json:"envelopes"`
	} `json:"e2e"`
}

type createDirectMessageRequest struct {
	RecipientUserID  string   `json:"recipient_user_id"`
	Content          string   `json:"content"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	AttachmentIDs    []string `json:"attachment_ids"`
}

type openDirectChatRequest struct {
	RecipientUserID string `json:"recipient_user_id"`
}

type upsertMessageStatusRequest struct {
	Status string `json:"status"`
}

type editMessageRequest struct {
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
}

type toggleReactionRequest struct {
	Emoji string `json:"emoji"`
}
