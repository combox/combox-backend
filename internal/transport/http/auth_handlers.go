package http

import (
	authsvc "combox-backend/internal/service/auth"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type registerRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func newRegisterHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		var req registerRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		user, tokens, err := auth.Register(r.Context(), authsvc.RegisterInput{
			Email:     req.Email,
			Username:  req.Username,
			Password:  req.Password,
			UserAgent: r.UserAgent(),
			IPAddress: clientIP(r),
		})
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "auth.register.success"),
			"user": map[string]any{
				"id":       user.ID,
				"email":    user.Email,
				"username": user.Username,
			},
			"tokens": tokens,
		})
	}
}

func newLoginHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		var req loginRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		user, tokens, err := auth.Login(r.Context(), authsvc.LoginInput{
			Login:     req.Login,
			Password:  req.Password,
			UserAgent: r.UserAgent(),
			IPAddress: clientIP(r),
		})
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "auth.login.success"),
			"user": map[string]any{
				"id":       user.ID,
				"email":    user.Email,
				"username": user.Username,
			},
			"tokens": tokens,
		})
	}
}

func newRefreshHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		var req refreshRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		tokens, err := auth.Refresh(r.Context(), authsvc.RefreshInput{
			RefreshToken: req.RefreshToken,
			UserAgent:    r.UserAgent(),
			IPAddress:    clientIP(r),
		})
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "auth.refresh.success"),
			"tokens":  tokens,
		})
	}
}

func newLogoutHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		var req logoutRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		if err := auth.Logout(r.Context(), authsvc.LogoutInput{
			RefreshToken: req.RefreshToken,
		}); err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "auth.logout.success"),
		})
	}
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return nil
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request, i18n Translator, defaultLocale string) {
	writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.request.method_not_allowed", nil, i18n, defaultLocale)
}

func writeAuthServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var serviceErr *authsvc.Error
	if errors.As(err, &serviceErr) {
		status := http.StatusInternalServerError
		switch serviceErr.Code {
		case authsvc.CodeInvalidArgument:
			status = http.StatusBadRequest
		case authsvc.CodeInvalidCredential, authsvc.CodeUnauthorized:
			status = http.StatusUnauthorized
		case authsvc.CodeConflict:
			status = http.StatusConflict
		}
		writeAPIError(w, r, status, serviceErr.Code, serviceErr.MessageKey, serviceErr.Details, i18n, defaultLocale)
		return
	}

	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}

func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, key string, details map[string]string, i18n Translator, defaultLocale string) {
	locale := requestLocale(r, defaultLocale)
	writeJSON(w, status, map[string]any{
		"code":       code,
		"message":    i18n.Translate(locale, key),
		"details":    detailsOrEmpty(details),
		"request_id": RequestIDFromContext(r.Context()),
	})
}

func detailsOrEmpty(details map[string]string) map[string]string {
	if details == nil {
		return map[string]string{}
	}
	return details
}

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first := strings.Split(xff, ",")[0]
		return strings.TrimSpace(first)
	}
	return strings.TrimSpace(r.RemoteAddr)
}
