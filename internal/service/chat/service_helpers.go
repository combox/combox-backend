package chat

import (
	"context"
	"errors"
	"strings"
)

func invalidArg(messageKey string) *Error {
	return &Error{Code: CodeInvalidArgument, MessageKey: messageKey}
}

func forbidden(messageKey string) *Error {
	return &Error{Code: CodeForbidden, MessageKey: messageKey}
}

func internal(cause error) *Error {
	return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: cause}
}

func notFound(messageKey string, cause error) *Error {
	return &Error{Code: CodeNotFound, MessageKey: messageKey, Cause: cause}
}

func conflict(messageKey string) *Error {
	return &Error{Code: CodeConflict, MessageKey: messageKey}
}

// alreadyExists reports a UNIQUE constraint rejection (409 already_exists),
// e.g. a chat folder name the user already uses.
func alreadyExists(messageKey string) *Error {
	return &Error{Code: CodeAlreadyExists, MessageKey: messageKey}
}

// messagePreviewHead collapses a message body into a single short line used for
// chat list previews and the desktop notification body.
func messagePreviewHead(content string, limit int) string {
	collapsed := strings.Join(strings.Fields(content), " ")
	runes := []rune(collapsed)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return collapsed
}

func dedupeMembers(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func normalizeChatType(raw string) (string, bool) {
	chatType := strings.TrimSpace(raw)
	if chatType == "" {
		chatType = ChatTypeStandard
	}
	switch chatType {
	case ChatTypeStandard, ChatTypeSecretE2E:
		return chatType, true
	default:
		return "", false
	}
}

func normalizeStatus(raw string) (string, bool) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "delivered", "read":
		return status, true
	default:
		return "", false
	}
}

func isStandaloneChannel(chatMeta Chat) bool {
	kind := strings.TrimSpace(strings.ToLower(chatMeta.Kind))
	if kind == "standalone_channel" {
		return true
	}
	if kind != "channel" {
		return false
	}
	if chatMeta.ParentChatID == nil {
		return true
	}
	return strings.TrimSpace(*chatMeta.ParentChatID) == ""
}

func isGroupChannel(chatMeta Chat) bool {
	kind := strings.TrimSpace(strings.ToLower(chatMeta.Kind))
	if kind != "channel" {
		return false
	}
	if chatMeta.ParentChatID == nil {
		return false
	}
	return strings.TrimSpace(*chatMeta.ParentChatID) != ""
}

func isBotChat(chatMeta Chat) bool {
	return strings.TrimSpace(strings.ToLower(chatMeta.Kind)) == "bot"
}

// isSelfServiceChat reports whether a chat is a private 1:1 conversation that
// is owned by its members (direct chats and bot chats): leaving it means
// removing only the caller's membership.
func isSelfServiceChat(chatMeta Chat) bool {
	return chatMeta.IsDirect || isBotChat(chatMeta)
}

func canHaveCommentThread(chatMeta Chat) bool {
	kind := strings.TrimSpace(strings.ToLower(chatMeta.Kind))
	switch kind {
	case "standalone_channel", "channel":
		return !chatMeta.IsDirect
	default:
		return false
	}
}

func (s *Service) ensureChatMember(ctx context.Context, chatID, userID string) error {
	member, err := s.chats.IsChatMember(ctx, chatID, userID)
	if err != nil {
		return internal(err)
	}
	if !member {
		chatMeta, metaErr := s.chats.GetChat(ctx, chatID)
		if metaErr != nil {
			return mapChatOrMessageRepoError(metaErr)
		}
		if isGroupChannel(chatMeta) {
			parentID := strings.TrimSpace(*chatMeta.ParentChatID)
			parentMember, parentErr := s.chats.IsChatMember(ctx, parentID, userID)
			if parentErr != nil {
				return internal(parentErr)
			}
			if parentMember {
				return nil
			}
		}
		return forbidden("error.chat.forbidden")
	}
	return nil
}

func mapChatOrMessageRepoError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrChatNotFound) {
		return notFound("error.chat.not_found", err)
	}
	if errors.Is(err, ErrMessageNotFound) {
		return notFound("error.message.not_found", err)
	}
	if errors.Is(err, ErrPollNotFound) {
		return notFound("error.poll.not_found", err)
	}
	if errors.Is(err, ErrInvalidAttachments) {
		return invalidArg("error.message.invalid_input")
	}
	return internal(err)
}
