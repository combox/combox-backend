package http

import (
	vkrepo "combox-backend/internal/repository/valkey"
	authsvc "combox-backend/internal/service/auth"
	"net/http"
	"strings"
	"time"
)

type profileUpdateRequest struct {
	Username       *string `json:"username"`
	FirstName      *string `json:"first_name"`
	LastName       *string `json:"last_name"`
	BirthDate      *string `json:"birth_date"`
	AvatarDataURL  *string `json:"avatar_data_url"`
	AvatarGradient *string `json:"avatar_gradient"`
}

type emailCodeRequest struct {
	Code string `json:"code"`
}

type emailChangeNewRequest struct {
	Email string `json:"email"`
}

func newProfileHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPatch {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		if r.Method == http.MethodGet {
			user, err := auth.GetProfile(r.Context(), userID)
			if err != nil {
				writeAuthServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "status.ok"),
				"user":    mapAuthUser(user),
			})
			return
		}

		var req profileUpdateRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		input := authsvc.UpdateProfileInput{
			UserID:         userID,
			Username:       authsvc.OptionalString{Set: req.Username != nil, Value: req.Username},
			FirstName:      authsvc.OptionalString{Set: req.FirstName != nil, Value: req.FirstName},
			LastName:       authsvc.OptionalString{Set: req.LastName != nil, Value: req.LastName},
			BirthDate:      authsvc.OptionalString{Set: req.BirthDate != nil, Value: req.BirthDate},
			AvatarDataURL:  authsvc.OptionalString{Set: req.AvatarDataURL != nil, Value: req.AvatarDataURL},
			AvatarGradient: authsvc.OptionalString{Set: req.AvatarGradient != nil, Value: req.AvatarGradient},
		}

		user, err := auth.UpdateProfile(r.Context(), input)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"user":    mapAuthUser(user),
		})
	}
}

func newProfileEmailChangeStartHandler(auth AuthService, emailCode EmailCodeService, repo *vkrepo.EmailChangeRepository, ttl time.Duration, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if emailCode == nil || repo == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.auth.email_code_unavailable", nil, i18n, defaultLocale)
			return
		}

		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		user, err := auth.GetProfile(r.Context(), userID)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		if err := emailCode.SendCodeEmailOnly(r.Context(), user.Email, locale); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}
		_ = repo.Clear(r.Context(), userID)

		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
		})
	}
}

func newProfileEmailChangeVerifyOldHandler(auth AuthService, emailCode EmailCodeService, repo *vkrepo.EmailChangeRepository, ttl time.Duration, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if emailCode == nil || repo == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.auth.email_code_unavailable", nil, i18n, defaultLocale)
			return
		}

		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		var req emailCodeRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		user, err := auth.GetProfile(r.Context(), userID)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		ok, err := emailCode.VerifyCode(r.Context(), user.Email, req.Code)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if !ok {
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.invalid_email_code", nil, i18n, defaultLocale)
			return
		}
		if ttl <= 0 {
			ttl = 10 * time.Minute
		}
		if err := repo.MarkOldVerified(r.Context(), userID, user.Email, ttl); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message":  i18n.Translate(locale, "status.ok"),
			"verified": true,
		})
	}
}

func newProfileEmailChangeSendNewHandler(auth AuthService, emailCode EmailCodeService, repo *vkrepo.EmailChangeRepository, ttl time.Duration, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if emailCode == nil || repo == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.auth.email_code_unavailable", nil, i18n, defaultLocale)
			return
		}

		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		var req emailChangeNewRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		state, err := repo.Get(r.Context(), userID)
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}
		if !state.OldVerified {
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.invalid_email_code", nil, i18n, defaultLocale)
			return
		}

		user, err := auth.GetProfile(r.Context(), userID)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		if strings.EqualFold(strings.TrimSpace(req.Email), user.Email) {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if state.OldEmail != "" && !strings.EqualFold(state.OldEmail, user.Email) {
			_ = repo.Clear(r.Context(), userID)
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.invalid_email_code", nil, i18n, defaultLocale)
			return
		}

		exists, err := auth.EmailExists(r.Context(), req.Email)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		if exists {
			writeAPIError(w, r, http.StatusConflict, "conflict", "error.auth.already_exists", nil, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		if err := emailCode.SendCodeEmailOnly(r.Context(), req.Email, locale); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if ttl <= 0 {
			ttl = 10 * time.Minute
		}
		if err := repo.SetNewEmail(r.Context(), userID, req.Email, ttl); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
		})
	}
}

func newProfileEmailChangeConfirmHandler(auth AuthService, emailCode EmailCodeService, repo *vkrepo.EmailChangeRepository, ttl time.Duration, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		if emailCode == nil || repo == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "service_unavailable", "error.auth.email_code_unavailable", nil, i18n, defaultLocale)
			return
		}

		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		var req emailCodeRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		state, err := repo.Get(r.Context(), userID)
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
			return
		}
		if !state.OldVerified || strings.TrimSpace(state.NewEmail) == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.invalid_email_code", nil, i18n, defaultLocale)
			return
		}

		ok, err := emailCode.VerifyCode(r.Context(), state.NewEmail, req.Code)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.auth.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if !ok {
			writeAPIError(w, r, http.StatusUnauthorized, "invalid_credentials", "error.auth.invalid_email_code", nil, i18n, defaultLocale)
			return
		}

		user, err := auth.UpdateEmail(r.Context(), userID, state.NewEmail)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		_ = repo.Clear(r.Context(), userID)

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"user":    mapAuthUser(user),
		})
	}
}
