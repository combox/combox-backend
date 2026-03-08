package search

import (
	"context"
	"strings"
	"time"
)

const (
	ScopeAll   = "all"
	ScopeUsers = "users"
	ScopeChats = "chats"
)

type UserResult struct {
	ID            string  `json:"id"`
	Email         string  `json:"email"`
	Username      string  `json:"username"`
	FirstName     string  `json:"first_name"`
	LastName      *string `json:"last_name,omitempty"`
	BirthDate     *string `json:"birth_date,omitempty"`
	AvatarDataURL *string `json:"avatar_data_url,omitempty"`
	AvatarGradient *string `json:"avatar_gradient,omitempty"`
}

type ChatResult struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Kind           string  `json:"kind"`
	PublicSlug     *string `json:"public_slug,omitempty"`
	AvatarDataURL  *string `json:"avatar_data_url,omitempty"`
	AvatarGradient *string `json:"avatar_gradient,omitempty"`
}

type Results struct {
	Users []UserResult `json:"users"`
	Chats []ChatResult `json:"chats"`
}

type Repository interface {
	SearchUsers(ctx context.Context, q string, limit int) ([]UserResult, error)
	SearchPublicChats(ctx context.Context, q string, limit int) ([]ChatResult, error)
}

type Service struct {
	repo       Repository
	avatars    AvatarStore
	avatarTTL  time.Duration
}

func New(repo Repository) *Service {
	if repo == nil {
		return nil
	}
	return &Service{repo: repo}
}

const (
	avatarRefPrefix  = "s3key:"
	defaultAvatarTTL = time.Hour * 24 * 7
)

type AvatarStore interface {
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
}

func (s *Service) SetAvatarStore(store AvatarStore, ttl time.Duration) {
	s.avatars = store
	if ttl <= 0 {
		ttl = defaultAvatarTTL
	}
	s.avatarTTL = ttl
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

func (s *Service) resolveUsersAvatars(ctx context.Context, users []UserResult) []UserResult {
	if len(users) == 0 {
		return users
	}
	out := make([]UserResult, len(users))
	copy(out, users)
	for i := range out {
		out[i].AvatarDataURL = s.resolveAvatarURL(ctx, out[i].AvatarDataURL)
	}
	return out
}

func (s *Service) resolveChatsAvatars(ctx context.Context, chats []ChatResult) []ChatResult {
	if len(chats) == 0 {
		return chats
	}
	out := make([]ChatResult, len(chats))
	copy(out, chats)
	for i := range out {
		out[i].AvatarDataURL = s.resolveAvatarURL(ctx, out[i].AvatarDataURL)
	}
	return out
}

func (s *Service) Search(ctx context.Context, q string, scope string, limit int) (Results, error) {
	q = strings.TrimSpace(q)
	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		scope = ScopeAll
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	var out Results
	if q == "" {
		return out, nil
	}

	switch scope {
	case ScopeUsers:
		items, err := s.repo.SearchUsers(ctx, q, limit)
		if err != nil {
			return Results{}, err
		}
		out.Users = s.resolveUsersAvatars(ctx, items)
		return out, nil
	case ScopeChats:
		items, err := s.repo.SearchPublicChats(ctx, q, limit)
		if err != nil {
			return Results{}, err
		}
		out.Chats = s.resolveChatsAvatars(ctx, items)
		return out, nil
	case ScopeAll:
		users, err := s.repo.SearchUsers(ctx, q, limit)
		if err != nil {
			return Results{}, err
		}
		chats, err := s.repo.SearchPublicChats(ctx, q, limit)
		if err != nil {
			return Results{}, err
		}
		out.Users = s.resolveUsersAvatars(ctx, users)
		out.Chats = s.resolveChatsAvatars(ctx, chats)
		return out, nil
	default:
		users, err := s.repo.SearchUsers(ctx, q, limit)
		if err != nil {
			return Results{}, err
		}
		chats, err := s.repo.SearchPublicChats(ctx, q, limit)
		if err != nil {
			return Results{}, err
		}
		out.Users = s.resolveUsersAvatars(ctx, users)
		out.Chats = s.resolveChatsAvatars(ctx, chats)
		return out, nil
	}
}
