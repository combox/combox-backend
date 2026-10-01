package http

import (
	"net/http"
	"strings"
	"time"

	chatsvc "combox-backend/internal/service/chat"
)

type createPollRequest struct {
	Question    string   `json:"question"`
	Description *string  `json:"description"`
	Options     []string `json:"options"`
	// ShowWhoVoted exposes the voter list (option id -> user ids).
	ShowWhoVoted bool `json:"show_who_voted"`
	Multiple     bool `json:"multiple"`
	// AllowAddOptions is stored for the composer UI; there is no
	// "add option" endpoint yet.
	AllowAddOptions bool `json:"allow_add_options"`
	AllowRevoting   bool `json:"allow_revoting"`
	ShuffleOptions  bool `json:"shuffle_options"`
	// CorrectOptionIDs are 0-based option indices encoded as strings: the
	// server generates the option ids at creation time.
	CorrectOptionIDs []string `json:"correct_option_ids"`
	Explanation      *string  `json:"explanation"`
	ClosesAt         *string  `json:"closes_at"`
	HideResults      bool     `json:"hide_results"`
}

type votePollRequest struct {
	OptionIDs []string `json:"option_ids"`
}

// newChatPollsHandler serves POST /api/private/v1/chats/{chatID}/polls.
func newChatPollsHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		targetChatID, ok := chatSubpath(r.URL.Path, "polls")
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.chat.not_found", nil, i18n, defaultLocale)
			return
		}
		var req createPollRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		input := chatsvc.CreatePollInput{
			UserID:           userID,
			ChatID:           targetChatID,
			Question:         strings.TrimSpace(req.Question),
			Options:          req.Options,
			ShowWhoVoted:     req.ShowWhoVoted,
			Multiple:         req.Multiple,
			AllowAddOptions:  req.AllowAddOptions,
			AllowRevoting:    req.AllowRevoting,
			ShuffleOptions:   req.ShuffleOptions,
			CorrectOptionIDs: req.CorrectOptionIDs,
			HideResults:      req.HideResults,
		}
		if req.Description != nil {
			input.Description = strings.TrimSpace(*req.Description)
		}
		if req.Explanation != nil {
			input.Explanation = strings.TrimSpace(*req.Explanation)
		}
		if req.ClosesAt != nil && strings.TrimSpace(*req.ClosesAt) != "" {
			parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ClosesAt))
			if err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.poll.invalid_input", nil, i18n, defaultLocale)
				return
			}
			input.ClosesAt = &parsed
		}
		created, err := chat.CreatePoll(r.Context(), input)
		if err != nil {
			writeChatServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"message":     i18n.Translate(requestLocale(r, defaultLocale), "status.ok"),
			"poll":        created.Poll,
			"item":        created,
			"max_options": chatsvc.PollMaxOptions,
		})
	}
}

// newPollsHandler serves the poll routes:
//
//	GET  /api/private/v1/polls/{pollID}
//	POST /api/private/v1/polls/{pollID}/vote
//	POST /api/private/v1/polls/{pollID}/close
func newPollsHandler(chat ChatService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		pollID, action, ok := pollPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.poll.not_found", nil, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)

		switch {
		case action == "" && r.Method == http.MethodGet:
			poll, err := chat.GetPoll(r.Context(), userID, pollID)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message":     i18n.Translate(locale, "status.ok"),
				"poll":        poll,
				"max_options": chatsvc.PollMaxOptions,
			})
		case action == "vote" && r.Method == http.MethodPost:
			var req votePollRequest
			if err := decodeJSON(r, &req); err != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
				return
			}
			poll, err := chat.VotePoll(r.Context(), chatsvc.VotePollInput{
				UserID:    userID,
				PollID:    pollID,
				OptionIDs: req.OptionIDs,
			})
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message":     i18n.Translate(locale, "status.ok"),
				"poll":        poll,
				"max_options": chatsvc.PollMaxOptions,
			})
		case action == "close" && r.Method == http.MethodPost:
			poll, err := chat.ClosePoll(r.Context(), userID, pollID)
			if err != nil {
				writeChatServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"message":     i18n.Translate(locale, "status.ok"),
				"poll":        poll,
				"max_options": chatsvc.PollMaxOptions,
			})
		default:
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
		}
	}
}

// pollPath splits /api/private/v1/polls/{pollID}[/{action}].
func pollPath(path string) (string, string, bool) {
	const prefix = "/api/private/v1/polls/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || strings.TrimSpace(rest) == "" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	switch len(parts) {
	case 1:
		if parts[0] == "" {
			return "", "", false
		}
		return parts[0], "", true
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}
