package http

import (
	"bytes"
	vkrepo "combox-backend/internal/repository/valkey"
	authsvc "combox-backend/internal/service/auth"
	privacysvc "combox-backend/internal/service/privacy"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type profileUpdateRequest struct {
	Username              *string               `json:"username"`
	FirstName             *string               `json:"first_name"`
	LastName              *string               `json:"last_name"`
	BirthDate             *string               `json:"birth_date"`
	AvatarDataURL         *string               `json:"avatar_data_url"`
	AvatarGradient        *string               `json:"avatar_gradient"`
	Bio                   *string               `json:"bio"`
	PhoneNumber           *string               `json:"phone_number"`
	NameColor             *string               `json:"name_color"`
	PlaylistTitle         *string               `json:"playlist_title"`
	PlaylistIsPublic      *bool                 `json:"playlist_is_public"`
	SavedTracks           *[]authsvc.SavedTrack `json:"saved_tracks"`
	SessionIdleTTLSeconds *int64                `json:"session_idle_ttl_seconds"`
}

type profilePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func derefTracks(tracks *[]authsvc.SavedTrack) []authsvc.SavedTrack {
	if tracks == nil {
		return nil
	}
	return *tracks
}

func userIDFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/users/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
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

		// PATCH /profile carries session_idle_ttl_seconds alongside the
		// profile fields. encoding/json decodes both "key absent" and
		// "key: null" into a nil *int64, but the SDK sends explicit null
		// for "Forever" (see combox-api updateSessionIdleTTL and the
		// PrivacySecuritySettings SESSION_TTL_OPTIONS value: null).
		// Read the raw body once to tell them apart: absent = no TTL
		// change, null = forever (mapped to 0, the stored sentinel for
		// no auto-logout), number = custom TTL seconds.
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		var rawMap map[string]json.RawMessage
		_ = json.Unmarshal(body, &rawMap)
		var req profileUpdateRequest
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		ttlSet := false
		if raw, ok := rawMap["session_idle_ttl_seconds"]; ok {
			ttlSet = true
			if strings.TrimSpace(string(raw)) == "null" {
				forever := authsvc.SessionIdleTTLForeverSeconds
				req.SessionIdleTTLSeconds = &forever
			}
		}

		input := authsvc.UpdateProfileInput{
			UserID:           userID,
			Username:         authsvc.OptionalString{Set: req.Username != nil, Value: req.Username},
			FirstName:        authsvc.OptionalString{Set: req.FirstName != nil, Value: req.FirstName},
			LastName:         authsvc.OptionalString{Set: req.LastName != nil, Value: req.LastName},
			BirthDate:        authsvc.OptionalString{Set: req.BirthDate != nil, Value: req.BirthDate},
			AvatarDataURL:    authsvc.OptionalString{Set: req.AvatarDataURL != nil, Value: req.AvatarDataURL},
			AvatarGradient:   authsvc.OptionalString{Set: req.AvatarGradient != nil, Value: req.AvatarGradient},
			Bio:              authsvc.OptionalString{Set: req.Bio != nil, Value: req.Bio},
			PhoneNumber:      authsvc.OptionalString{Set: req.PhoneNumber != nil, Value: req.PhoneNumber},
			NameColor:        authsvc.OptionalString{Set: req.NameColor != nil, Value: req.NameColor},
			PlaylistTitle:    authsvc.OptionalString{Set: req.PlaylistTitle != nil, Value: req.PlaylistTitle},
			PlaylistIsPublic: authsvc.OptionalBool{Set: req.PlaylistIsPublic != nil, Value: req.PlaylistIsPublic != nil && *req.PlaylistIsPublic},
			SavedTracks:      authsvc.OptionalTracks{Set: req.SavedTracks != nil, Value: derefTracks(req.SavedTracks)},
		}

		hasProfileFields := req.Username != nil || req.FirstName != nil || req.LastName != nil || req.BirthDate != nil || req.AvatarDataURL != nil || req.AvatarGradient != nil || req.Bio != nil || req.PhoneNumber != nil || req.NameColor != nil || req.PlaylistTitle != nil || req.PlaylistIsPublic != nil || req.SavedTracks != nil
		var user authsvc.User
		if hasProfileFields {
			user, err = auth.UpdateProfile(r.Context(), input)
			if err != nil {
				writeAuthServiceError(w, r, err, i18n, defaultLocale)
				return
			}
		}
		if ttlSet {
			user, err = auth.UpdateSessionIdleTTL(r.Context(), userID, req.SessionIdleTTLSeconds)
			if err != nil {
				writeAuthServiceError(w, r, err, i18n, defaultLocale)
				return
			}
		}
		if !hasProfileFields && !ttlSet {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_argument", "error.request.invalid_input", nil, i18n, defaultLocale)
			return
		}
		if user.ID == "" {
			user, err = auth.GetProfile(r.Context(), userID)
			if err != nil {
				writeAuthServiceError(w, r, err, i18n, defaultLocale)
				return
			}
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
			"user":    mapAuthUser(user),
		})
	}
}

func newProfilePasswordHandler(auth AuthService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		var req profilePasswordRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}
		if err := auth.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "status.ok"),
		})
	}
}

func newUserByIDHandler(auth AuthService, privacy PrivacyService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}
		requesterID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if requesterID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		targetID, ok := userIDFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}
		user, err := auth.GetProfile(r.Context(), targetID)
		if err != nil {
			writeAuthServiceError(w, r, err, i18n, defaultLocale)
			return
		}
		// A private playlist is nobody else's business: only its owner sees
		// the tracks (the UI hides the block too, this is the server side).
		if !user.PlaylistIsPublic && targetID != requesterID {
			user.SavedTracks = []authsvc.SavedTrack{}
		}
		// Privacy: phone_number and bio are masked unless the owner's rule
		// allows this viewer. One memo covers both evaluations. The profile
		// payload carries no last_seen field, so last_seen has nothing to
		// mask here (it is enforced on /presence and the WS presence frames).
		if privacy != nil && targetID != requesterID {
			ctx := privacysvc.WithMemo(r.Context())
			empty := ""
			if allowed, perr := privacy.Evaluate(ctx, requesterID, targetID, privacysvc.ParamPhoneNumber); perr != nil || !allowed {
				user.PhoneNumber = &empty
			}
			if allowed, perr := privacy.Evaluate(ctx, requesterID, targetID, privacysvc.ParamBio); perr != nil || !allowed {
				user.Bio = &empty
			}
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
