package botauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
)

var (
	ErrInvalidToken  = errors.New("invalid bot token")
	ErrTokenNotFound = errors.New("bot token not found")
)

type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.MessageKey
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

type GenerateTokenInput struct {
	BotUserID string
	Name      string
	Scopes    []string
	ChatIDs   []string
	ExpiresAt *time.Time
}

type GeneratedToken struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	BotUserID string     `json:"bot_user_id"`
	Scopes    []string   `json:"scopes"`
	ChatIDs   []string   `json:"chat_ids"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Token     string     `json:"token"`
}

type CreateTokenRecordInput struct {
	BotUserID  string
	Name       string
	SecretHash string
	Scopes     []string
	ChatIDs    []string
	ExpiresAt  *time.Time
}

type StoredToken struct {
	ID         string
	BotUserID  string
	SecretHash string
	Scopes     []string
	ChatIDs    []string
	IsRevoked  bool
	ExpiresAt  *time.Time
}

type Repository interface {
	Create(ctx context.Context, input CreateTokenRecordInput) (StoredToken, error)
	FindActiveByID(ctx context.Context, id string) (StoredToken, error)
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}

type Service struct {
	repo   Repository
	pepper string
}

func New(repo Repository, pepper string) (*Service, error) {
	if repo == nil {
		return nil, errors.New("bot token repository is required")
	}
	pepper = strings.TrimSpace(pepper)
	if pepper == "" {
		return nil, errors.New("bot token pepper is required")
	}
	return &Service{repo: repo, pepper: pepper}, nil
}

func (s *Service) GenerateToken(ctx context.Context, input GenerateTokenInput) (GeneratedToken, error) {
	botUserID := strings.TrimSpace(input.BotUserID)
	if botUserID == "" {
		return GeneratedToken{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.bot.invalid_token_input"}
	}

	scopes := normalizeList(input.Scopes)
	if len(scopes) == 0 {
		return GeneratedToken{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.bot.invalid_token_input"}
	}
	chatIDs := normalizeList(input.ChatIDs)
	if len(chatIDs) == 0 {
		return GeneratedToken{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.bot.invalid_token_input"}
	}

	secretRaw := make([]byte, 32)
	if _, err := rand.Read(secretRaw); err != nil {
		return GeneratedToken{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	secret := base64.RawURLEncoding.EncodeToString(secretRaw)
	secretHash := s.hashSecret(secret)

	stored, err := s.repo.Create(ctx, CreateTokenRecordInput{
		BotUserID:  botUserID,
		Name:       strings.TrimSpace(input.Name),
		SecretHash: secretHash,
		Scopes:     scopes,
		ChatIDs:    chatIDs,
		ExpiresAt:  input.ExpiresAt,
	})
	if err != nil {
		return GeneratedToken{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	token := "bt_" + stored.ID + "." + secret
	return GeneratedToken{
		ID:        stored.ID,
		Name:      strings.TrimSpace(input.Name),
		BotUserID: stored.BotUserID,
		Scopes:    append([]string(nil), stored.Scopes...),
		ChatIDs:   append([]string(nil), stored.ChatIDs...),
		ExpiresAt: stored.ExpiresAt,
		Token:     token,
	}, nil
}

func (s *Service) ValidateToken(ctx context.Context, token string) (Principal, error) {
	id, secret, ok := parseToken(token)
	if !ok {
		return Principal{}, ErrInvalidToken
	}

	stored, err := s.repo.FindActiveByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return Principal{}, ErrInvalidToken
		}
		return Principal{}, err
	}

	expected := s.hashSecret(secret)
	if !hmac.Equal([]byte(expected), []byte(stored.SecretHash)) {
		return Principal{}, ErrInvalidToken
	}

	_ = s.repo.TouchLastUsed(ctx, stored.ID, time.Now().UTC())

	scopes := make(map[string]struct{}, len(stored.Scopes))
	for _, scope := range stored.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		scopes[scope] = struct{}{}
	}

	chats := make(map[string]struct{}, len(stored.ChatIDs))
	allowAll := false
	for _, cid := range stored.ChatIDs {
		cid = strings.TrimSpace(cid)
		if cid == "" {
			continue
		}
		if cid == "*" {
			allowAll = true
			break
		}
		chats[cid] = struct{}{}
	}

	return Principal{
		UserID:          strings.TrimSpace(stored.BotUserID),
		Scopes:          scopes,
		AllowedChatIDs:  chats,
		AllowAllChatIDs: allowAll,
	}, nil
}

func (s *Service) hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(s.pepper + ":" + strings.TrimSpace(secret)))
	return hex.EncodeToString(sum[:])
}

func parseToken(raw string) (id string, secret string, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "bt_") {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, "bt_"), ".", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	id = strings.TrimSpace(parts[0])
	secret = strings.TrimSpace(parts[1])
	if id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

func normalizeList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
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
