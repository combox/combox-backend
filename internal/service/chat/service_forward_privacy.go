package chat

import (
	"context"
	"strings"
)

// forwardPrivacyParam is the privacy parameter enforced on forward origins.
// It mirrors privacy.ParamForwardedMessages without importing that package,
// keeping this package free of a service-level dependency.
const forwardPrivacyParam = "forwarded_messages"

// applyForwardPrivacyToMessages rewrites the forward-origin block of a page of
// messages for the current viewer. One memo covers the whole batch: at most
// one privacy settings read per distinct origin user per call.
func (s *Service) applyForwardPrivacyToMessages(ctx context.Context, viewerID string, items []Message) {
	if !s.forwardPrivacyReady(viewerID) {
		return
	}
	ctx = s.forwardPrivacy.WithMemo(ctx)
	for i := range items {
		s.redactForwardOrigin(ctx, viewerID, &items[i])
	}
}

// applyForwardPrivacyToMessage is the single-message variant of
// applyForwardPrivacyToMessages.
func (s *Service) applyForwardPrivacyToMessage(ctx context.Context, viewerID string, item *Message) {
	if item == nil || !s.forwardPrivacyReady(viewerID) {
		return
	}
	ctx = s.forwardPrivacy.WithMemo(ctx)
	s.redactForwardOrigin(ctx, viewerID, item)
}

func (s *Service) forwardPrivacyReady(viewerID string) bool {
	return s.forwardPrivacy != nil && strings.TrimSpace(viewerID) != ""
}

// redactForwardOrigin applies the origin owner's forwarded_messages rule to
// one message:
//
//   - origin == viewer -> left as it is (the owner always sees themselves),
//     the avatar is attached so their own forward card renders;
//   - allowed -> origin kept, plus forward_origin_avatar_data_url;
//   - denied (or the lookup failed, fail closed) -> the name becomes an empty
//     string, the user id and avatar are dropped and
//     forward_origin_redacted: true is added, so the client cannot navigate
//     to the origin profile from the message.
func (s *Service) redactForwardOrigin(ctx context.Context, viewerID string, item *Message) {
	origin := strings.TrimSpace(derefString(item.ForwardOriginUserID))
	if origin == "" {
		return
	}
	allowed, err := s.forwardPrivacy.Evaluate(ctx, viewerID, origin, forwardPrivacyParam)
	if err != nil || !allowed {
		empty := ""
		item.ForwardOriginName = &empty
		item.ForwardOriginUserID = nil
		item.ForwardOriginAvatarURL = nil
		item.ForwardOriginRedacted = true
		return
	}
	item.ForwardOriginRedacted = false
	if s.forwardOriginProfiles == nil {
		return
	}
	if avatar, avatarErr := s.forwardOriginProfiles.GetAvatarDataURL(ctx, origin); avatarErr == nil {
		item.ForwardOriginAvatarURL = avatar
	}
}
