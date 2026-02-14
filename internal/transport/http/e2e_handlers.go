package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	e2esvc "combox-backend/internal/service/e2e"
)

type upsertUserKeyBackupRequest struct {
	Alg        string          `json:"alg"`
	KDF        string          `json:"kdf"`
	Salt       string          `json:"salt"`
	Params     json.RawMessage `json:"params"`
	Ciphertext string          `json:"ciphertext"`
}

type upsertDeviceKeysRequest struct {
	IdentityKey    string                 `json:"identity_key"`
	SignedPreKey   e2esvc.SignedPreKey    `json:"signed_prekey"`
	OneTimePreKeys []e2esvc.OneTimePreKey `json:"one_time_prekeys"`
}

func newE2EDeviceKeysHandler(svc E2EService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		deviceID, ok := e2eDeviceIDFromPath(r.URL.Path)
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
			return
		}

		if r.Method != http.MethodPut {
			writeMethodNotAllowed(w, r, i18n, defaultLocale)
			return
		}

		var req upsertDeviceKeysRequest
		if err := decodeJSON(r, &req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
			return
		}

		created, err := svc.UpsertDeviceKeys(r.Context(), e2esvc.UpsertDeviceKeysInput{
			UserID:         userID,
			DeviceID:       deviceID,
			IdentityKey:    req.IdentityKey,
			SignedPreKey:   req.SignedPreKey,
			OneTimePreKeys: req.OneTimePreKeys,
		})
		if err != nil {
			writeE2EServiceError(w, r, err, i18n, defaultLocale)
			return
		}

		locale := requestLocale(r, defaultLocale)
		writeJSON(w, http.StatusOK, map[string]any{
			"message": i18n.Translate(locale, "e2e.device_keys.upsert.success"),
			"device":  created,
		})
	}
}

func newE2EUsersHandler(svc E2EService, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requester := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if requester == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}

		// Route 1: list devices.
		if userID, ok := e2eUserIDFromDevicesPath(r.URL.Path); ok {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			items, err := svc.ListUserDevices(r.Context(), userID)
			if err != nil {
				writeE2EServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "e2e.devices.list.success"),
				"items":   items,
			})
			return
		}

		// Route 2: claim bundle.
		if userID, deviceID, ok := e2eBundleClaimFromPath(r.URL.Path); ok {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
			bundle, err := svc.ClaimPreKeyBundle(r.Context(), userID, deviceID)
			if err != nil {
				writeE2EServiceError(w, r, err, i18n, defaultLocale)
				return
			}
			locale := requestLocale(r, defaultLocale)
			writeJSON(w, http.StatusOK, map[string]any{
				"message": i18n.Translate(locale, "e2e.bundle.claim.success"),
				"bundle":  bundle,
			})
			return
		}

		// Route 3: user key backup.
		if userID, ok := e2eUserKeyBackupFromPath(r.URL.Path); ok {
			if requester != userID {
				writeAPIError(w, r, http.StatusForbidden, "forbidden", "error.chat.forbidden", nil, i18n, defaultLocale)
				return
			}
			switch r.Method {
			case http.MethodGet:
				backup, err := svc.GetUserKeyBackup(r.Context(), userID)
				if err != nil {
					writeE2EServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "e2e.key_backup.get.success"),
					"backup":  backup,
				})
				return
			case http.MethodPut:
				var req upsertUserKeyBackupRequest
				if err := decodeJSON(r, &req); err != nil {
					writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "error.request.invalid_json", nil, i18n, defaultLocale)
					return
				}
				backup, err := svc.UpsertUserKeyBackup(r.Context(), e2esvc.UpsertUserKeyBackupInput{
					UserID:     userID,
					Alg:        req.Alg,
					KDF:        req.KDF,
					Salt:       req.Salt,
					Params:     req.Params,
					Ciphertext: req.Ciphertext,
				})
				if err != nil {
					writeE2EServiceError(w, r, err, i18n, defaultLocale)
					return
				}
				locale := requestLocale(r, defaultLocale)
				writeJSON(w, http.StatusOK, map[string]any{
					"message": i18n.Translate(locale, "e2e.key_backup.upsert.success"),
					"backup":  backup,
				})
				return
			default:
				writeMethodNotAllowed(w, r, i18n, defaultLocale)
				return
			}
		}

		writeAPIError(w, r, http.StatusNotFound, "not_found", "error.request.not_found", nil, i18n, defaultLocale)
	}
}

func e2eDeviceIDFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/e2e/devices/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 1 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

func e2eUserIDFromDevicesPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/e2e/users/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "devices" {
		return "", false
	}
	return parts[0], true
}

func e2eBundleClaimFromPath(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/e2e/users/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 5 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] != "devices" || parts[2] == "" || parts[3] != "bundle:claim" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func e2eUserKeyBackupFromPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	const prefix = "/api/private/v1/e2e/users/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] == "" || parts[1] != "key-backup" {
		return "", false
	}
	return parts[0], true
}

func writeE2EServiceError(w http.ResponseWriter, r *http.Request, err error, i18n Translator, defaultLocale string) {
	var svcErr *e2esvc.Error
	if errors.As(err, &svcErr) {
		status := http.StatusInternalServerError
		switch svcErr.Code {
		case e2esvc.CodeInvalidArgument:
			status = http.StatusBadRequest
		case e2esvc.CodeForbidden:
			status = http.StatusForbidden
		case e2esvc.CodeNotFound:
			status = http.StatusNotFound
		}
		writeAPIError(w, r, status, svcErr.Code, svcErr.MessageKey, svcErr.Details, i18n, defaultLocale)
		return
	}
	writeAPIError(w, r, http.StatusInternalServerError, "internal", "error.internal", nil, i18n, defaultLocale)
}
