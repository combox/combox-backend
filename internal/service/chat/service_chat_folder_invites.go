package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// chatFolderInviteTokenRetries caps how many fresh tokens the service mints
// when creation collides (token reuse is ~2^-128 likely, the realistic case
// is two concurrent creates racing the single-active-invite guard).
const chatFolderInviteTokenRetries = 3

// ChatFolderInvite is one share link of a chat folder: the owner mints it,
// any signed-in user holding the token may resolve (preview) the folder, and
// the owner revokes it by stamping revoked_at.
type ChatFolderInvite struct {
	ID        string     `json:"id"`
	FolderID  string     `json:"folder_id"`
	Token     string     `json:"token"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	UseCount  int        `json:"use_count"`
}

// ResolvedFolderInviteChat is one chat of a shared folder as seen by the
// resolving user: public metadata plus whether they already belong to it.
// The client uses is_member to render "joined" state and offer import.
type ResolvedFolderInviteChat struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
	PublicSlug *string `json:"public_slug"`
	IsMember   bool    `json:"is_member"`
}

// ResolvedFolderInvite is the preview payload of GET .../invite/{token}.
// folder_emoji carries the folder's icon column (chat_folders has name/icon,
// no separate emoji column).
type ResolvedFolderInvite struct {
	FolderName  string                     `json:"folder_name"`
	FolderEmoji string                     `json:"folder_emoji"`
	Chats       []ResolvedFolderInviteChat `json:"chats"`
}

// ChatFolderInviteRepository persists folder share links (see
// postgres.ChatFolderRepository, migration 000043_chat_folder_invites).
// Creates scope writes by folder ownership enforced one layer up: every entry
// point loads the folder through ChatFolderRepository first, so a folder of
// somebody else reports ErrChatFolderNotFound rather than leaking existence.
type ChatFolderInviteRepository interface {
	// GetActiveChatFolderInvite returns the live (revoked_at IS NULL) invite
	// of a folder, or ErrChatFolderInviteNotFound when there is none.
	GetActiveChatFolderInvite(ctx context.Context, folderID string) (ChatFolderInvite, error)
	// CreateChatFolderInvite mints a live invite. A UNIQUE collision (token
	// reuse or a concurrent create racing the single-active guard) reports
	// ErrChatFolderInviteTaken.
	CreateChatFolderInvite(ctx context.Context, folderID, createdBy, token string) (ChatFolderInvite, error)
	// RevokeActiveChatFolderInvite stamps revoked_at=now() on the live invite
	// of a folder, or reports ErrChatFolderInviteNotFound when there is none.
	RevokeActiveChatFolderInvite(ctx context.Context, folderID string) error
	// GetChatFolderInviteByToken loads an invite by token regardless of owner
	// or revocation state; callers decide what a revoked row means.
	GetChatFolderInviteByToken(ctx context.Context, token string) (ChatFolderInvite, error)
	// GetChatFolderByID loads a folder by id without an owner scope, for
	// invite resolve (the resolver is usually not the owner).
	GetChatFolderByID(ctx context.Context, folderID string) (ChatFolder, error)
	// IncrementChatFolderInviteUse bumps use_count; best effort.
	IncrementChatFolderInviteUse(ctx context.Context, inviteID string) error
}

// SetChatFolderInviteRepository wires the folder share-link store.
func (s *Service) SetChatFolderInviteRepository(repo ChatFolderInviteRepository) {
	s.folderInvites = repo
}

func (s *Service) inviteRepo() (ChatFolderInviteRepository, error) {
	if s.folderInvites == nil {
		return nil, internal(errors.New("chat folder invite repository is not configured"))
	}
	return s.folderInvites, nil
}

// CreateChatFolderInvite mints a share link for a folder of the caller, or
// returns the still-live one when it exists. Only the folder owner may call
// it: a foreign or missing folder reads as 404.
func (s *Service) CreateChatFolderInvite(ctx context.Context, userID, folderID string) (ChatFolderInvite, error) {
	userID = strings.TrimSpace(userID)
	folderID = strings.TrimSpace(folderID)
	if userID == "" {
		return ChatFolderInvite{}, invalidArg("error.chat.folder.invalid_input")
	}
	if folderID == "" || !validChatFolderID(folderID) {
		return ChatFolderInvite{}, notFound("error.chat.folder.not_found", nil)
	}
	folders, err := s.folderRepo()
	if err != nil {
		return ChatFolderInvite{}, err
	}
	if _, err := folders.GetChatFolder(ctx, userID, folderID); err != nil {
		return ChatFolderInvite{}, mapChatFolderRepoError(err)
	}
	invites, err := s.inviteRepo()
	if err != nil {
		return ChatFolderInvite{}, err
	}
	if active, err := invites.GetActiveChatFolderInvite(ctx, folderID); err == nil {
		return active, nil
	} else if !errors.Is(err, ErrChatFolderInviteNotFound) {
		return ChatFolderInvite{}, internal(err)
	}
	for attempt := 0; attempt < chatFolderInviteTokenRetries; attempt++ {
		token, err := newChatFolderInviteToken()
		if err != nil {
			return ChatFolderInvite{}, internal(err)
		}
		created, err := invites.CreateChatFolderInvite(ctx, folderID, userID, token)
		if err == nil {
			return created, nil
		}
		if !errors.Is(err, ErrChatFolderInviteTaken) {
			return ChatFolderInvite{}, internal(err)
		}
		// Somebody raced us (or, astronomically unlikely, the token
		// collided): a live invite may exist now, return it instead of
		// minting a second one.
		if active, err := invites.GetActiveChatFolderInvite(ctx, folderID); err == nil {
			return active, nil
		} else if !errors.Is(err, ErrChatFolderInviteNotFound) {
			return ChatFolderInvite{}, internal(err)
		}
	}
	return ChatFolderInvite{}, internal(errors.New("chat folder invite token collision"))
}

// RevokeChatFolderInvite revokes the live share link of a folder of the
// caller. Only the owner may call it; a folder without a live invite reads
// as 404.
func (s *Service) RevokeChatFolderInvite(ctx context.Context, userID, folderID string) error {
	userID = strings.TrimSpace(userID)
	folderID = strings.TrimSpace(folderID)
	if userID == "" {
		return invalidArg("error.chat.folder.invalid_input")
	}
	if folderID == "" || !validChatFolderID(folderID) {
		return notFound("error.chat.folder.not_found", nil)
	}
	folders, err := s.folderRepo()
	if err != nil {
		return err
	}
	if _, err := folders.GetChatFolder(ctx, userID, folderID); err != nil {
		return mapChatFolderRepoError(err)
	}
	invites, err := s.inviteRepo()
	if err != nil {
		return err
	}
	if err := invites.RevokeActiveChatFolderInvite(ctx, folderID); err != nil {
		return mapChatFolderInviteRepoError(err)
	}
	return nil
}

// ResolveChatFolderInvite previews a shared folder for a signed-in user
// holding the token. A missing or revoked token reads as 404; there is no
// auto-join, the client renders an import dialog from the payload.
func (s *Service) ResolveChatFolderInvite(ctx context.Context, userID, token string) (ResolvedFolderInvite, error) {
	userID = strings.TrimSpace(userID)
	token = strings.TrimSpace(token)
	if userID == "" || token == "" {
		return ResolvedFolderInvite{}, invalidArg("error.chat.folder.invalid_input")
	}
	invites, err := s.inviteRepo()
	if err != nil {
		return ResolvedFolderInvite{}, err
	}
	invite, err := invites.GetChatFolderInviteByToken(ctx, token)
	if err != nil {
		return ResolvedFolderInvite{}, mapChatFolderInviteRepoError(err)
	}
	if invite.RevokedAt != nil {
		return ResolvedFolderInvite{}, notFound("error.chat.folder.invite_not_found", ErrChatFolderInviteNotFound)
	}
	folder, err := invites.GetChatFolderByID(ctx, invite.FolderID)
	if err != nil {
		return ResolvedFolderInvite{}, mapChatFolderRepoError(err)
	}
	out := ResolvedFolderInvite{
		FolderName:  folder.Name,
		FolderEmoji: folder.Icon,
		Chats:       []ResolvedFolderInviteChat{},
	}
	for _, chatID := range folder.ChatIDs {
		meta, err := s.chats.GetChat(ctx, chatID)
		if err != nil {
			if errors.Is(err, ErrChatNotFound) {
				continue
			}
			return ResolvedFolderInvite{}, internal(err)
		}
		isMember, err := s.chats.IsChatMember(ctx, chatID, userID)
		if err != nil {
			return ResolvedFolderInvite{}, internal(err)
		}
		out.Chats = append(out.Chats, ResolvedFolderInviteChat{
			ID:         meta.ID,
			Title:      meta.Title,
			Kind:       meta.Kind,
			PublicSlug: meta.PublicSlug,
			IsMember:   isMember,
		})
	}
	_ = invites.IncrementChatFolderInviteUse(ctx, invite.ID)
	return out, nil
}

// newChatFolderInviteToken mints 16 bytes of crypto/rand as 32 hex chars.
func newChatFolderInviteToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// mapChatFolderInviteRepoError translates the invite store sentinels onto the
// typed service errors the handlers already know how to serialise.
func mapChatFolderInviteRepoError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrChatFolderInviteNotFound):
		return notFound("error.chat.folder.invite_not_found", err)
	case errors.Is(err, ErrChatFolderInviteTaken):
		return conflict("error.chat.folder.invite_conflict")
	default:
		return internal(err)
	}
}
