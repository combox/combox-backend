package botwebhook

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeAlreadyExists   = "already_exists"
	CodeInternal        = "internal"
)

var allowedEvents = map[string]struct{}{
	"message.created": {},
	"message.updated": {},
	"message.deleted": {},
	"message.read":    {},
}

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

type Webhook struct {
	ID          string    `json:"id"`
	BotUserID   string    `json:"bot_user_id"`
	EndpointURL string    `json:"endpoint_url"`
	Events      []string  `json:"events"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateInput struct {
	BotUserID   string
	EndpointURL string
	Events      []string
}

type Service struct {
	mu       sync.Mutex
	byID     map[string]Webhook
	byBotURL map[string]string
}

func New() *Service {
	return &Service{
		byID:     make(map[string]Webhook),
		byBotURL: make(map[string]string),
	}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Webhook, error) {
	_ = ctx

	botUserID := strings.TrimSpace(input.BotUserID)
	endpointURL := strings.TrimSpace(input.EndpointURL)
	if botUserID == "" || endpointURL == "" {
		return Webhook{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.bot.invalid_webhook_input"}
	}

	parsed, err := url.Parse(endpointURL)
	if err != nil || parsed == nil || strings.TrimSpace(parsed.Host) == "" {
		return Webhook{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.bot.invalid_webhook_url"}
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return Webhook{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.bot.invalid_webhook_url"}
	}

	events := make([]string, 0, len(input.Events))
	seen := make(map[string]struct{}, len(input.Events))
	for _, ev := range input.Events {
		ev = strings.TrimSpace(ev)
		if ev == "" {
			continue
		}
		if _, ok := allowedEvents[ev]; !ok {
			return Webhook{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.bot.invalid_webhook_event",
				Details:    map[string]string{"event": ev},
			}
		}
		if _, ok := seen[ev]; ok {
			continue
		}
		seen[ev] = struct{}{}
		events = append(events, ev)
	}
	if len(events) == 0 {
		return Webhook{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.bot.invalid_webhook_input"}
	}

	key := botUserID + "|" + endpointURL
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byBotURL[key]; exists {
		return Webhook{}, &Error{Code: CodeAlreadyExists, MessageKey: "error.bot.webhook_already_exists"}
	}

	now := time.Now().UTC()
	id := uuid.NewString()
	item := Webhook{
		ID:          id,
		BotUserID:   botUserID,
		EndpointURL: endpointURL,
		Events:      events,
		Enabled:     true,
		CreatedAt:   now,
	}
	s.byID[id] = item
	s.byBotURL[key] = id
	return item, nil
}
