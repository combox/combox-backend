package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxChatFoldersPerUser caps how many folders a single account owns.
	MaxChatFoldersPerUser = 12
	// MaxChatFolderChats caps the chats one folder may reference.
	MaxChatFolderChats = 1000
	// chatFolderNameMaxRunes / chatFolderIconMaxRunes are the field limits,
	// counted in runes after trimming.
	chatFolderNameMaxRunes = 32
	// Folder icons are MDI glyph names (e.g. "mdi-folder-outline") or a single
	// emoji for legacy folders: allow up to 64 runes for the names.
	chatFolderIconMaxRunes = 64
)

// ChatFolder is one Telegram style dialog filter: a named, optionally iconed
// bucket of chats shown as a tab above the chat list.
type ChatFolder struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	// ChatIDs preserves the order of chat_folder_chats.position and is never
	// null in responses.
	ChatIDs []string `json:"chat_ids"`
}

type CreateChatFolderInput struct {
	UserID  string
	Name    string
	Icon    string
	ChatIDs []string
}

// UpdateChatFolderInput patches a folder; every field is optional and an
// unset field keeps its stored value.
type UpdateChatFolderInput struct {
	UserID   string
	FolderID string
	Name     OptionalString
	Icon     OptionalString
	Position OptionalInt
}

// ChatFolderRepository persists chat folders (see
// postgres.NewChatFolderRepository). Implementations scope every read and
// write by user id so a folder of somebody else reports
// ErrChatFolderNotFound rather than leaking its existence.
type ChatFolderRepository interface {
	// ListChatFolders returns every folder of a user ordered by
	// (position, created_at, id), each with its chat ids in folder order.
	ListChatFolders(ctx context.Context, userID string) ([]ChatFolder, error)
	// GetChatFolder loads one folder owned by userID.
	GetChatFolder(ctx context.Context, userID, folderID string) (ChatFolder, error)
	// CreateChatFolder appends a folder at position max+1. It returns
	// ErrChatFolderLimit when the user already owns maxFolders folders and
	// ErrChatFolderNameTaken when the name collides.
	CreateChatFolder(ctx context.Context, input CreateChatFolderInput, maxFolders int) (ChatFolder, error)
	// UpdateChatFolderMeta renames and/or re-icons a folder; a nil pointer
	// keeps the stored value.
	UpdateChatFolderMeta(ctx context.Context, userID, folderID string, name, icon *string) (ChatFolder, error)
	// MoveChatFolder moves the folder to position, shifting the others so the
	// sequence stays a dense permutation.
	MoveChatFolder(ctx context.Context, userID, folderID string, position int) (ChatFolder, error)
	// ReplaceChatFolderChats swaps the whole chat set, keeping chatIDs order.
	ReplaceChatFolderChats(ctx context.Context, userID, folderID string, chatIDs []string) (ChatFolder, error)
	// DeleteChatFolder removes the folder (and, via cascade, its links).
	DeleteChatFolder(ctx context.Context, userID, folderID string) error
}

// SetChatFolderRepository wires the dialog filter store.
func (s *Service) SetChatFolderRepository(repo ChatFolderRepository) {
	s.folders = repo
}

func (s *Service) folderRepo() (ChatFolderRepository, error) {
	if s.folders == nil {
		return nil, internal(errors.New("chat folder repository is not configured"))
	}
	return s.folders, nil
}

// ListChatFolders returns the folders of a user, ordered for display.
func (s *Service) ListChatFolders(ctx context.Context, userID string) ([]ChatFolder, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, invalidArg("error.chat.folder.invalid_input")
	}
	repo, err := s.folderRepo()
	if err != nil {
		return nil, err
	}
	items, err := repo.ListChatFolders(ctx, userID)
	if err != nil {
		return nil, internal(err)
	}
	if items == nil {
		items = []ChatFolder{}
	}
	for i := range items {
		if items[i].ChatIDs == nil {
			items[i].ChatIDs = []string{}
		}
	}
	return items, nil
}

// CreateChatFolder validates and stores a new folder at the end of the list.
func (s *Service) CreateChatFolder(ctx context.Context, input CreateChatFolderInput) (ChatFolder, error) {
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		return ChatFolder{}, invalidArg("error.chat.folder.invalid_input")
	}
	name, err := normalizeChatFolderName(input.Name)
	if err != nil {
		return ChatFolder{}, err
	}
	icon, err := normalizeChatFolderIcon(input.Icon)
	if err != nil {
		return ChatFolder{}, err
	}
	chatIDs, err := s.normalizeChatFolderChatIDs(ctx, userID, input.ChatIDs)
	if err != nil {
		return ChatFolder{}, err
	}
	repo, err := s.folderRepo()
	if err != nil {
		return ChatFolder{}, err
	}
	created, err := repo.CreateChatFolder(ctx, CreateChatFolderInput{
		UserID:  userID,
		Name:    name,
		Icon:    icon,
		ChatIDs: chatIDs,
	}, MaxChatFoldersPerUser)
	if err != nil {
		return ChatFolder{}, mapChatFolderRepoError(err)
	}
	if created.ChatIDs == nil {
		created.ChatIDs = []string{}
	}
	return created, nil
}

// UpdateChatFolder applies a partial patch. Only the fields the caller set are
// touched; a Position patch renumbers the whole folder list of the user.
func (s *Service) UpdateChatFolder(ctx context.Context, input UpdateChatFolderInput) (ChatFolder, error) {
	userID := strings.TrimSpace(input.UserID)
	folderID := strings.TrimSpace(input.FolderID)
	if userID == "" {
		return ChatFolder{}, invalidArg("error.chat.folder.invalid_input")
	}
	if folderID == "" || !validChatFolderID(folderID) {
		return ChatFolder{}, notFound("error.chat.folder.not_found", nil)
	}

	var nameArg, iconArg *string
	if input.Name.Set {
		name, err := normalizeChatFolderName(derefString(input.Name.Value))
		if err != nil {
			return ChatFolder{}, err
		}
		nameArg = &name
	}
	if input.Icon.Set {
		icon, err := normalizeChatFolderIcon(derefString(input.Icon.Value))
		if err != nil {
			return ChatFolder{}, err
		}
		iconArg = &icon
	}

	repo, err := s.folderRepo()
	if err != nil {
		return ChatFolder{}, err
	}

	if nameArg == nil && iconArg == nil && !input.Position.Set {
		item, err := repo.GetChatFolder(ctx, userID, folderID)
		if err != nil {
			return ChatFolder{}, mapChatFolderRepoError(err)
		}
		return item, nil
	}

	var updated ChatFolder
	if nameArg != nil || iconArg != nil {
		updated, err = repo.UpdateChatFolderMeta(ctx, userID, folderID, nameArg, iconArg)
		if err != nil {
			return ChatFolder{}, mapChatFolderRepoError(err)
		}
	}
	if input.Position.Set {
		updated, err = repo.MoveChatFolder(ctx, userID, folderID, input.Position.Value)
		if err != nil {
			return ChatFolder{}, mapChatFolderRepoError(err)
		}
	}
	if updated.ChatIDs == nil {
		updated.ChatIDs = []string{}
	}
	return updated, nil
}

// SetChatFolderChats replaces the whole chat set of a folder. Chat ids the
// caller cannot read or does not belong to are rejected, never dropped
// silently.
func (s *Service) SetChatFolderChats(ctx context.Context, userID, folderID string, chatIDs []string) (ChatFolder, error) {
	userID = strings.TrimSpace(userID)
	folderID = strings.TrimSpace(folderID)
	if userID == "" {
		return ChatFolder{}, invalidArg("error.chat.folder.invalid_input")
	}
	if folderID == "" || !validChatFolderID(folderID) {
		return ChatFolder{}, notFound("error.chat.folder.not_found", nil)
	}
	normalized, err := s.normalizeChatFolderChatIDs(ctx, userID, chatIDs)
	if err != nil {
		return ChatFolder{}, err
	}
	repo, err := s.folderRepo()
	if err != nil {
		return ChatFolder{}, err
	}
	item, err := repo.ReplaceChatFolderChats(ctx, userID, folderID, normalized)
	if err != nil {
		return ChatFolder{}, mapChatFolderRepoError(err)
	}
	if item.ChatIDs == nil {
		item.ChatIDs = []string{}
	}
	return item, nil
}

// DeleteChatFolder removes a folder of the caller; the chat links are dropped
// by ON DELETE CASCADE.
func (s *Service) DeleteChatFolder(ctx context.Context, userID, folderID string) error {
	userID = strings.TrimSpace(userID)
	folderID = strings.TrimSpace(folderID)
	if userID == "" {
		return invalidArg("error.chat.folder.invalid_input")
	}
	if folderID == "" || !validChatFolderID(folderID) {
		return notFound("error.chat.folder.not_found", nil)
	}
	repo, err := s.folderRepo()
	if err != nil {
		return err
	}
	if err := repo.DeleteChatFolder(ctx, userID, folderID); err != nil {
		return mapChatFolderRepoError(err)
	}
	return nil
}

// normalizeChatFolderName trims the name and enforces 1..32 runes.
func normalizeChatFolderName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len([]rune(name)) > chatFolderNameMaxRunes {
		return "", &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.chat.folder.invalid_input",
			Details:    map[string]string{"field": "name"},
		}
	}
	return name, nil
}

// normalizeChatFolderIcon trims the icon and allows at most 64 runes (MDI
// glyph names like "mdi-folder-outline"); an empty icon is valid (the folder
// simply shows no glyph).
func normalizeChatFolderIcon(raw string) (string, error) {
	icon := strings.TrimSpace(raw)
	if len([]rune(icon)) > chatFolderIconMaxRunes {
		return "", &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.chat.folder.invalid_input",
			Details:    map[string]string{"field": "icon"},
		}
	}
	return icon, nil
}

// normalizeChatFolderChatIDs trims, drops empties, de-duplicates (keeping the
// first occurrence), enforces the per-folder cap and verifies that the caller
// may reference every chat.
func (s *Service) normalizeChatFolderChatIDs(ctx context.Context, userID string, chatIDs []string) ([]string, error) {
	out := make([]string, 0, len(chatIDs))
	seen := make(map[string]struct{}, len(chatIDs))
	for _, raw := range chatIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			return nil, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.chat.folder.invalid_chat_id",
				Details:    map[string]string{"chat_id": raw},
			}
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) > MaxChatFolderChats {
		return nil, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.chat.folder.too_many_chats",
			Details:    map[string]string{"limit": "1000"},
		}
	}
	for _, id := range out {
		if err := s.ensureChatMember(ctx, id, userID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// validChatFolderID reports whether a path segment can address a folder.
func validChatFolderID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

// mapChatFolderRepoError translates the folder store sentinels onto the typed
// service errors the handlers already know how to serialise.
func mapChatFolderRepoError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrChatFolderNotFound):
		return notFound("error.chat.folder.not_found", err)
	case errors.Is(err, ErrChatFolderNameTaken):
		return alreadyExists("error.chat.folder.already_exists")
	case errors.Is(err, ErrChatFolderLimit):
		return &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.chat.folder.limit_reached",
			Details:    map[string]string{"limit": "12"},
		}
	default:
		return internal(err)
	}
}
