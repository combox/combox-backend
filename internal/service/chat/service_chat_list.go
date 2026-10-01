package chat

import (
	"context"
	"errors"
	"strings"
)

// DefaultPinScope is the sidebar category a chat is pinned into when the
// client does not specify one ("All").
const DefaultPinScope = "all"

var pinScopes = map[string]bool{
	"all":     true,
	"direct":  true,
	"channel": true,
	"group":   true,
}

func normalizePinScope(raw string) string {
	scope := strings.TrimSpace(strings.ToLower(raw))
	if pinScopes[scope] {
		return scope
	}
	return DefaultPinScope
}

// ensureChatListAccess mirrors what reading a chat requires: members of a
// normal chat can manage their own per-user state, public standalone channels
// are readable (and manageable) without membership, and comment threads are
// readable by anyone who can read their parent channel.
func (s *Service) ensureChatListAccess(ctx context.Context, chatID, userID string) error {
	chatMeta, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return mapChatOrMessageRepoError(err)
	}
	kind := strings.TrimSpace(strings.ToLower(chatMeta.Kind))

	if isStandaloneChannel(chatMeta) && chatMeta.IsPublic {
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, chatID, userID); banErr != nil {
			return internal(banErr)
		} else if banned {
			return forbidden("error.chat.forbidden")
		}
		role, roleErr := s.chats.GetChatMemberRole(ctx, chatID, userID)
		switch {
		case roleErr == nil:
			if strings.EqualFold(role, "banned") {
				return forbidden("error.chat.forbidden")
			}
		case errors.Is(roleErr, ErrChatNotFound):
			// Not a member yet: allowed for public standalone channels.
		default:
			return internal(roleErr)
		}
		return nil
	}

	if kind == "comment_thread" {
		parentID := ""
		if chatMeta.ParentChatID != nil {
			parentID = strings.TrimSpace(*chatMeta.ParentChatID)
		}
		if parentID == "" {
			return forbidden("error.chat.forbidden")
		}
		if banned, banErr := s.chats.IsPublicChannelBanned(ctx, parentID, userID); banErr != nil {
			return internal(banErr)
		} else if banned {
			return forbidden("error.chat.forbidden")
		}
		return nil
	}

	return s.ensureChatMember(ctx, chatID, userID)
}

func (s *Service) attachChatUserState(ctx context.Context, userID string, target *Chat) {
	if target == nil {
		return
	}
	cleanUserID := strings.TrimSpace(userID)
	if cleanUserID == "" {
		return
	}
	state, err := s.chats.GetChatUserState(ctx, cleanUserID, target.ID)
	if err != nil {
		return
	}
	target.Archived = state.Archived
	target.Pinned = state.Pinned
	target.PinScope = strings.TrimSpace(state.PinScope)
	if target.PinScope == "" {
		target.PinScope = DefaultPinScope
	}
	target.PinOrder = state.PinOrder
}

func (s *Service) ArchiveChat(ctx context.Context, userID, chatID string, archived bool) (Chat, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatListAccess(ctx, chatID, userID); err != nil {
		return Chat{}, err
	}
	if err := s.chats.SetChatArchived(ctx, userID, chatID, archived); err != nil {
		return Chat{}, internal(err)
	}
	return s.GetChat(ctx, userID, chatID)
}

func (s *Service) PinChat(ctx context.Context, userID, chatID string, pinned bool, pinScope string, pinOrder int64) (Chat, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	scope := normalizePinScope(pinScope)
	if !pinned {
		pinOrder = 0
	}
	if err := s.ensureChatListAccess(ctx, chatID, userID); err != nil {
		return Chat{}, err
	}
	if err := s.chats.SetChatPinned(ctx, userID, chatID, pinned, scope, pinOrder); err != nil {
		return Chat{}, internal(err)
	}
	return s.GetChat(ctx, userID, chatID)
}

func (s *Service) MarkChatRead(ctx context.Context, userID, chatID string) error {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatListAccess(ctx, chatID, userID); err != nil {
		return err
	}
	if s.notifications == nil {
		return nil
	}
	if err := s.notifications.ResetChatUnread(ctx, userID, chatID); err != nil {
		return internal(err)
	}
	return nil
}

// ClearChatHistory hides the caller's copy of a chat's history by moving their
// own watermark forward. It returns how many messages the caller could still
// see before the call; other members are unaffected.
func (s *Service) ClearChatHistory(ctx context.Context, userID, chatID string) (int, error) {
	userID = strings.TrimSpace(userID)
	chatID = strings.TrimSpace(chatID)
	if userID == "" || chatID == "" {
		return 0, invalidArg("error.chat.invalid_input")
	}
	if err := s.ensureChatListAccess(ctx, chatID, userID); err != nil {
		return 0, err
	}
	state, err := s.chats.GetChatUserState(ctx, userID, chatID)
	if err != nil {
		return 0, internal(err)
	}
	cleared, err := s.messages.CountVisibleMessages(ctx, chatID, state.HistoryClearedAt)
	if err != nil {
		return 0, internal(err)
	}
	if err := s.chats.SetChatHistoryCleared(ctx, userID, chatID, s.nowUTC()); err != nil {
		return 0, internal(err)
	}
	if s.notifications != nil {
		if err := s.notifications.ResetChatUnread(ctx, userID, chatID); err != nil {
			return 0, internal(err)
		}
	}
	return cleared, nil
}
