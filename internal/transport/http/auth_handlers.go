package http

import (
	authsvc "combox-backend/internal/service/auth"
	emailcodesvc "combox-backend/internal/service/emailcode"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type registerRequest struct {
	Email          string  `json:"email"`
	Username       string  `json:"username"`
	Password       string  `json:"password"`
	FirstName      string  `json:"first_name"`
	LastName       *string `json:"last_name"`
	BirthDate      *string `json:"birth_date"`
	AvatarDataURL  *string `json:"avatar_data_url"`
	AvatarGradient *string `json:"avatar_gradient"`
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	LoginKey string `json:"login_key"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type emailExistsRequest struct {
	Email string `json:"email"`
}

type emailCodeSendRequest struct {
	Email string `json:"email"`
}

type emailCodeVerifyRequest struct {
	Email   string `json:"email"`
	Code    string `json:"code"`
	Purpose string `json:"purpose"`
}

func newEmailExistsHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		var req emailExistsRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		exists, err := auth.EmailExists(r.Context(), req.Email)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"exists":  exists,
		})
	}
}

func newEmailCodeSendHandler(svc EmailCodeService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if svc == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.auth.email_code_unavailable", nil, i18n, defaultLocale)
			return
		}

		var req emailCodeSendRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		if err := svc.SendCode(r.Context(), req.Email, locale); err != nil {
			if errors.Is(err, emailcodesvc.ErrInvalidEmail) {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
				return
			}
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "auth.email_code.sent"),
		})
	}
}

func newEmailCodeVerifyHandler(svc EmailCodeService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if svc == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.auth.email_code_unavailable", nil, i18n, defaultLocale)
			return
		}

		var req emailCodeVerifyRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		ok, err := svc.VerifyCode(r.Context(), req.Email, req.Code)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		if !ok {
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.invalid_email_code", nil, i18n, defaultLocale)
			return
		}

		payload := map[string]any{
			"message":  i18n.Translate(locale, "auth.email_code.verified"),
			"verified": true,
		}

		if strings.EqualFold(strings.TrimSpace(req.Purpose), "login") {
			loginKey, keyErr := svc.IssueLoginKey(r.Context(), req.Email)
			if keyErr != nil {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
				return
			}
			if strings.TrimSpace(loginKey) == "" {
				writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.email_code_required", nil, i18n, defaultLocale)
				return
			}
			payload["login_key"] = loginKey
		}

		writeJSON(w, http.StatusOK, payload)
	}
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
			Email:          req.Email,
			Username:       req.Username,
			Password:       req.Password,
			FirstName:      req.FirstName,
			LastName:       req.LastName,
			BirthDate:      req.BirthDate,
			AvatarDataURL:  req.AvatarDataURL,
			AvatarGradient: req.AvatarGradient,
			UserAgent:      r.UserAgent(),
			IPAddress:      clientIP(r),
		})
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusCreated, map[string]any{
			"message": i18n.Translate(locale, "auth.register.success"),
			"user":    mapAuthUser(user),
			"tokens":  tokens,
		})
	}
}

func newLoginHandler(auth AuthService, emailCode EmailCodeService, i18n Translator, defaultLocale string) http.HandlerFunc {
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
		if emailCode == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.auth.email_code_unavailable", nil, i18n, defaultLocale)
			return
		}
		loginKey := strings.TrimSpace(req.LoginKey)
		if loginKey == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.login_key_required", nil, i18n, defaultLocale)
			return
		}
		ok, err := emailCode.ValidateLoginKey(r.Context(), req.Login, loginKey)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if !ok {
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.login_key_invalid", nil, i18n, defaultLocale)
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
		_, _ = emailCode.ConsumeLoginKey(r.Context(), req.Login, loginKey)

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "auth.login.success"),
			"user":    mapAuthUser(user),
			"tokens":  tokens,
		})
	}
}

func mapAuthUser(user authsvc.User) map[string]any {
	return map[string]any{
		"id":                       user.ID,
		"email":                    user.Email,
		"username":                 user.Username,
		"first_name":               user.FirstName,
		"last_name":                user.LastName,
		"birth_date":               user.BirthDate,
		"avatar_data_url":          user.AvatarDataURL,
		"avatar_gradient":          user.AvatarGradient,
		"session_idle_ttl_seconds": user.SessionIdleTTLSeconds,
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
