package http

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	botauthsvc "combox-backend/internal/service/botauth"

	"github.com/google/uuid"
)

type contextKey string

const requestIDKey contextKey = "request_id"
const botPrincipalKey contextKey = "bot_principal"

type BotPrincipal = botauthsvc.Principal

func BotPrincipalFromContext(ctx context.Context) (botauthsvc.Principal, bool) {
	value, ok := ctx.Value(botPrincipalKey).(botauthsvc.Principal)
	return value, ok
}

func BotAuthMiddleware(botAuth BotAuthService, i18n Translator, defaultLocale string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if !strings.HasPrefix(path, "/api/public/v1/bot/") {
				next.ServeHTTP(w, r)
				return
			}
			if botAuth == nil {
				writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.bot.invalid_token", nil, i18n, defaultLocale)
				return
			}

			authz := strings.TrimSpace(r.Header.Get("Authorization"))
			const prefix = "Bearer "
			if !strings.HasPrefix(authz, prefix) {
				writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.bot.invalid_token", nil, i18n, defaultLocale)
				return
			}
			token := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
			if token == "" {
				writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.bot.invalid_token", nil, i18n, defaultLocale)
				return
			}

			principal, err := botAuth.ValidateToken(r.Context(), token)
			if err != nil {
				writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.bot.invalid_token", nil, i18n, defaultLocale)
				return
			}

			ctx := context.WithValue(r.Context(), botPrincipalKey, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func AuthMiddleware(accessSecret string, i18n Translator, defaultLocale string) func(http.Handler) http.Handler {
	allowed := map[string]struct{}{
		"/api/private/v1/auth/email-exists":      {},
		"/api/private/v1/auth/email-code/send":   {},
		"/api/private/v1/auth/email-code/verify": {},
		"/api/private/v1/auth/register":          {},
		"/api/private/v1/auth/login":             {},
		"/api/private/v1/auth/refresh":           {},
		"/api/private/v1/auth/logout":            {},
		"/api/private/v1/ws":                     {},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if !strings.HasPrefix(path, "/api/private/v1/") {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := allowed[path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			authz := strings.TrimSpace(r.Header.Get("Authorization"))
			const prefix = "Bearer "
			if !strings.HasPrefix(authz, prefix) {
				writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.invalid_credentials", nil, i18n, defaultLocale)
				return
			}
			token := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
			if token == "" {
				writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.invalid_credentials", nil, i18n, defaultLocale)
				return
			}
			userID, err := verifyAccessToken(token, accessSecret)
			if err != nil {
				writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.invalid_credentials", nil, i18n, defaultLocale)
				return
			}

			r2 := r.Clone(r.Context())
			r2.Header.Set("X-User-ID", userID)
			next.ServeHTTP(w, r2)
		})
	}
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}

		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RecoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						slog.Any("panic", rec),
						slog.String("stack", string(debug.Stack())),
						slog.String("request_id", RequestIDFromContext(r.Context())),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
					)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func AccessLogMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)

			logger.Info("request completed",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.status),
				slog.Int("bytes", ww.bytes),
				slog.Duration("duration", time.Since(startedAt)),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

func RequestIDFromContext(ctx context.Context) string {
	value, ok := ctx.Value(requestIDKey).(string)
	if !ok {
		return ""
	}
	return value
}

type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	written, err := w.ResponseWriter.Write(body)
	w.bytes += written
	return written, err
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hj.Hijack()
}

func (w *responseWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}
