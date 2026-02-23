package search

import (
	"context"
	"strings"
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
	AvatarDataURL *string `json:"avatar_data_url,omitempty"`
	AvatarGradient *string `json:"avatar_gradient,omitempty"`
}

type ChatResult struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
	PublicSlug *string `json:"public_slug,omitempty"`
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
	repo Repository
}

func New(repo Repository) *Service {
	if repo == nil {
		return nil
	}
	return &Service{repo: repo}
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
		out.Users = items
		return out, nil
	case ScopeChats:
		items, err := s.repo.SearchPublicChats(ctx, q, limit)
		if err != nil {
			return Results{}, err
		}
		out.Chats = items
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
		out.Users = users
		out.Chats = chats
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
		out.Users = users
		out.Chats = chats
		return out, nil
	}
}
