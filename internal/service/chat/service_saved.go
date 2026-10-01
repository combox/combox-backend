package chat

import (
	"context"
	"strings"
)

// SavedChatTitleDefault is the backend title of the self-chat. The sidebar
// always renders the localized label (chat.saved_messages) instead, so this
// is only what unmodified surfaces (forward picker, export filename) show.
const SavedChatTitleDefault = "Saved Messages"

// findSavedChat returns the caller's self-chat when it already exists.
// It scans ListChatsByUser instead of a dedicated repository method so the
// ChatRepository interface stays unchanged.
func (s *Service) findSavedChat(ctx context.Context, userID string) (*Chat, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil
	}
	chats, err := s.chats.ListChatsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range chats {
		if IsSavedChat(chats[i]) {
			found := chats[i]
			return &found, nil
		}
	}
	return nil, nil
}

// isSavedConflict reports a unique violation on uniq_chats_saved_per_user
// (a concurrent GetOrCreate racing this one).
func isSavedConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "uniq_chats_saved_per_user") {
		return true
	}
	return strings.Contains(msg, "duplicate key") && strings.Contains(msg, "saved")
}

// ensureSavedChat is the idempotent GetOrCreate of the self-chat: the first
// call creates the single-member 'saved' row, later calls return it. A title
// is only used at creation time; an empty title falls back to the default.
func (s *Service) ensureSavedChat(ctx context.Context, userID, title string) (Chat, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return Chat{}, invalidArg("error.chat.invalid_input")
	}
	if existing, err := s.findSavedChat(ctx, userID); err == nil && existing != nil {
		return *existing, nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = SavedChatTitleDefault
	}
	created, err := s.chats.CreateChat(ctx, title, []string{userID}, userID, ChatTypeStandard, ChatKindSaved)
	if err != nil {
		if isSavedConflict(err) {
			if existing, rerr := s.findSavedChat(ctx, userID); rerr == nil && existing != nil {
				return *existing, nil
			}
		}
		return Chat{}, internal(err)
	}
	return created, nil
}
