package http

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	vkrepo "combox-backend/internal/repository/valkey"
	emailcodesvc "combox-backend/internal/service/emailcode"
)

// Legacy email binding for migrated boxchat users (migration 000044).
//
//	POST /api/private/v1/auth/legacy/bind-email/request {email}
//	POST /api/private/v1/auth/legacy/bind-email/verify  {email, code}
//
// Both endpoints require a migr-limited access token (see the auth middleware
// allowlist); the user id is taken from the verified token, never from the
// body. OTPs are 6 digits, stored as sha256(salt:code) in valkey under
// "legacy-bind:<userID>" with a 10 minute TTL.
//
// Sending: when a mail sender is wired (Resend, see run.go) the code is
// emailed; when NO sender is configured the code is written to the server log
// at warn level (dev mode) and the response stays {ok:true,resend_after:60}
// in both cases — the OTP slice never appears in any response.

const (
	legacyBindDefaultTTL  = 10 * time.Minute
	legacyBindResendAfter = 60
	// Cooldown window: entries younger than TTL-cooldown are not re-issued.
	legacyBindCooldown   = legacyBindDefaultTTL - legacyBindResendAfter*time.Second
	legacyBindCodeDigits = 6
)

// legacyBindStore is the valkey OTP backend; *vkrepo.LegacyBindRepository
// implements it. The interface keeps handlers unit-testable without valkey.
type legacyBindStore interface {
	Save(ctx context.Context, userID, email, salt, codeHash string, ttl time.Duration) error
	Get(ctx context.Context, userID string) (vkrepo.LegacyBindEntry, time.Duration, bool, error)
	Verify(ctx context.Context, userID, email, code string) (bool, error)
	Delete(ctx context.Context, userID string) error
}

var _ legacyBindStore = (*vkrepo.LegacyBindRepository)(nil)

type legacyBindEmailRequest struct {
	Email string `json:"email"`
}

type legacyBindVerifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func legacyBindTTLOrDefault(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return legacyBindDefaultTTL
	}
	return ttl
}

func normalizeBindEmail(raw string) (string, bool) {
	email := strings.TrimSpace(strings.ToLower(raw))
	if email == "" {
		return "", false
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", false
	}
	return email, true
}

func generateBindCode() (string, error) {
	buf := make([]byte, legacyBindCodeDigits)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	digits := make([]byte, legacyBindCodeDigits)
	for i := range buf {
		digits[i] = byte('0' + (buf[i] % 10))
	}
	return string(digits), nil
}

func generateBindSalt() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i, b := range buf {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out), nil
}

func newLegacyBindRequestHandler(auth AuthService, store legacyBindStore, sender emailcodesvc.Sender, logger *slog.Logger, ttl time.Duration, i18n Translator, defaultLocale string) http.HandlerFunc {
	ttl = legacyBindTTLOrDefault(ttl)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if store == nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		var req legacyBindEmailRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		email, ok := normalizeBindEmail(req.Email)
		if !ok {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}

		user, err := auth.GetProfile(r.Context(), userID)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		if !user.IsLegacyUnverified {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}

		exists, err := auth.EmailExists(r.Context(), email)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		if exists {
			writeAPIError(w, r, http.StatusConflict, "conflict", "error.auth.already_exists", nil, i18n, defaultLocale)
			return
		}

		// Cooldown: a fresh entry (younger than resend_after) is not
		// re-issued; the client counts down resend_after instead.
		if _, left, found, err := store.Get(r.Context(), userID); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		} else if found && left > legacyBindCooldown {
			remaining := int((left - legacyBindCooldown).Seconds())
			if remaining < 1 {
				remaining = 1
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":           true,
				"resend_after": remaining,
			})
			return
		}

		code, err := generateBindCode()
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}
		salt, err := generateBindSalt()
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}
		if err := store.Save(r.Context(), userID, email, salt, vkrepo.HashLegacyBindCode(salt, code), ttl); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		if sender != nil {
			subject := "ComBox: email binding code"
			text := fmt.Sprintf("Enter this code to bind your email to the migrated account:\n\n%s\n\nValid for 10 minutes.\nIf this wasn't you, ignore this message.", code)
			html := fmt.Sprintf(`<p>Enter this code to bind your email to the migrated account:</p><p style="font-size:24px;"><b>%s</b></p><p>Valid for 10 minutes. If this wasn't you, ignore this message.</p>`, code)
			if err := sender.Send(r.Context(), email, subject, html, text); err != nil {
				if logger != nil {
					logger.Error("legacy-bind: mail send failed",
						slog.String("user_id", userID),
						slog.String("request_id", RequestIDFromContext(r.Context())),
						slog.String("error", err.Error()))
				}
				writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
				return
			}
		} else if logger != nil {
			// Dev mode (no mailer configured): the OTP goes to the server
			// log at warn and never to the response.
			logger.Warn("legacy-bind code (no mailer configured; dev mode)",
				slog.String("user_id", userID),
				slog.String("email", email),
				slog.String("code", code))
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"message":      i18n.Translate(locale, "status.ok"),
			"ok":           true,
			"resend_after": legacyBindResendAfter,
		})
	}
}

func newLegacyBindVerifyHandler(auth AuthService, store legacyBindStore, logger *slog.Logger, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if store == nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		var req legacyBindVerifyRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		email, ok := normalizeBindEmail(req.Email)
		if !ok || strings.TrimSpace(req.Code) == "" {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}

		// Constant-time OTP check against the valkey entry; attempts are
		// capped (entry deleted at the cap) and every failure is the same
		// 401 with no hint which half was wrong.
		valid, err := store.Verify(r.Context(), userID, email, strings.TrimSpace(req.Code))
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}
		if !valid {
			if logger != nil {
				logger.Info("legacy-bind: invalid code",
					slog.String("user_id", userID),
					slog.String("request_id", RequestIDFromContext(r.Context())))
			}
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.invalid_email_code", nil, i18n, defaultLocale)
			return
		}

		// Uniqueness is re-checked inside CompleteLegacyBind (plus the DB
		// unique constraint); then the migr mark is lifted with a FULL
		// session, in the successful-login response shape.
		user, tokens, err := auth.CompleteLegacyBind(r.Context(), userID, email, r.UserAgent(), clientIP(r))
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		_ = store.Delete(r.Context(), userID)

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":            i18n.Translate(locale, "auth.login.success"),
			"user":               mapAuthUser(user),
			"tokens":             tokens,
			"migration_required": false,
		})
	}
}
