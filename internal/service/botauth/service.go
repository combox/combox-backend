package botauth

import (
	"context"
	"errors"
	"strings"
)

var ErrInvalidToken = errors.New("invalid bot token")

type TokenConfig struct {
	Token   string
	UserID  string
	Scopes  []string
	ChatIDs []string
}

type Principal struct {
	UserID          string
	Scopes          map[string]struct{}
	AllowedChatIDs  map[string]struct{}
	AllowAllChatIDs bool
}

func (p Principal) HasScope(scope string) bool {
	_, ok := p.Scopes[strings.TrimSpace(scope)]
	return ok
}

func (p Principal) CanAccessChat(chatID string) bool {
	if p.AllowAllChatIDs {
		return true
	}
	_, ok := p.AllowedChatIDs[strings.TrimSpace(chatID)]
	return ok
}

type Service struct {
	byToken map[string]Principal
}

func New(tokens []TokenConfig) (*Service, error) {
	byToken := make(map[string]Principal, len(tokens))
	for _, tk := range tokens {
		token := strings.TrimSpace(tk.Token)
		userID := strings.TrimSpace(tk.UserID)
		if token == "" || userID == "" {
			return nil, errors.New("bot token config requires token and user_id")
		}

		scopes := make(map[string]struct{}, len(tk.Scopes))
		for _, s := range tk.Scopes {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			scopes[s] = struct{}{}
		}
		if len(scopes) == 0 {
			return nil, errors.New("bot token config requires at least one scope")
		}

		allowedChats := make(map[string]struct{}, len(tk.ChatIDs))
		allowAll := false
		for _, cid := range tk.ChatIDs {
			cid = strings.TrimSpace(cid)
			if cid == "" {
				continue
			}
			if cid == "*" {
				allowAll = true
				break
			}
			allowedChats[cid] = struct{}{}
		}
		if !allowAll && len(allowedChats) == 0 {
			return nil, errors.New("bot token config requires chat_ids or wildcard *")
		}

		byToken[token] = Principal{
			UserID:          userID,
			Scopes:          scopes,
			AllowedChatIDs:  allowedChats,
			AllowAllChatIDs: allowAll,
		}
	}

	return &Service{byToken: byToken}, nil
}

func (s *Service) ValidateToken(_ context.Context, token string) (Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Principal{}, ErrInvalidToken
	}
	p, ok := s.byToken[token]
	if !ok {
		return Principal{}, ErrInvalidToken
	}
	return p, nil
}
