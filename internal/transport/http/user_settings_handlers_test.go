package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	settingssvc "combox-backend/internal/service/settings"
)

type userSettingsStub struct {
	current   map[string]string
	gotPatch  map[string]string
	gotUserID string
	getErr    error
	updateErr error
}

func (s *userSettingsStub) GetUserSettings(_ context.Context, userID string) (map[string]string, error) {
	s.gotUserID = userID
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.current == nil {
		return map[string]string{}, nil
	}
	return s.current, nil
}

func (s *userSettingsStub) UpdateUserSettings(_ context.Context, userID string, patch map[string]string) (map[string]string, error) {
	s.gotUserID = userID
	s.gotPatch = patch
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	out := map[string]string{"sounds_enabled": "true", "data_saver": "true"}
	for key, value := range patch {
		out[key] = value
	}
	return out, nil
}

func newUserSettingsRouter(svc UserSettingsService) stdhttp.Handler {
	return NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  "test-secret",
		UserSettings:  svc,
	})
}

func userSettingsRequest(t *testing.T, router stdhttp.Handler, method, body string) (int, map[string]any) {
	t.Helper()
	var req *stdhttp.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/private/v1/profile/user-settings", nil)
	} else {
		req = httptest.NewRequest(method, "/api/private/v1/profile/user-settings", strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+makeAccessToken(t, "u-test", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix()))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	payload := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &payload)
	return rr.Code, payload
}

func TestGetUserSettingsRoute(t *testing.T) {
	svc := &userSettingsStub{current: map[string]string{"data_saver": "false"}}
	status, payload := userSettingsRequest(t, newUserSettingsRouter(svc), stdhttp.MethodGet, "")
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	if svc.gotUserID != "u-test" {
		t.Fatalf("user id = %q", svc.gotUserID)
	}
	settings, ok := payload["settings"].(map[string]any)
	if !ok || settings["data_saver"] != "false" {
		t.Fatalf("settings = %v", payload["settings"])
	}
}

func TestUpdateUserSettingsCoercesBooleansAndStrings(t *testing.T) {
	svc := &userSettingsStub{}
	body := `{"settings":{"data_saver":true,"sounds_enabled":"false","badge_enabled":false}}`
	status, payload := userSettingsRequest(t, newUserSettingsRouter(svc), stdhttp.MethodPut, body)
	if status != stdhttp.StatusOK {
		t.Fatalf("status = %d, body=%v", status, payload)
	}
	want := map[string]string{
		"data_saver":     "true",
		"sounds_enabled": "false",
		"badge_enabled":  "false",
	}
	if len(svc.gotPatch) != len(want) {
		t.Fatalf("patch = %v", svc.gotPatch)
	}
	for key, value := range want {
		if svc.gotPatch[key] != value {
			t.Fatalf("patch[%q] = %q, want %q", key, svc.gotPatch[key], value)
		}
	}
	settings := payload["settings"].(map[string]any)
	if settings["data_saver"] != "true" {
		t.Fatalf("settings = %v", settings)
	}
}

func TestUpdateUserSettingsRejectsMissingSettingsField(t *testing.T) {
	svc := &userSettingsStub{}
	status, payload := userSettingsRequest(t, newUserSettingsRouter(svc), stdhttp.MethodPut, `{}`)
	if status != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", status, payload)
	}
	if payload["code"] != "invalid_argument" {
		t.Fatalf("code = %v", payload["code"])
	}
	if svc.gotPatch != nil {
		t.Fatalf("the store must not be touched: %v", svc.gotPatch)
	}
}

func TestUpdateUserSettingsRejectsBadPayload(t *testing.T) {
	cases := []string{
		`{"settings":{"data_saver":"yes"}}`,
		`{"settings":{"totally_made_up":true}}`,
		`{"settings":{"data_saver":5}}`,
	}
	for _, body := range cases {
		svc := &userSettingsStub{updateErr: &settingssvc.Error{
			Code:       settingssvc.CodeInvalidArgument,
			MessageKey: "error.user_settings.invalid_value",
		}}
		status, payload := userSettingsRequest(t, newUserSettingsRouter(svc), stdhttp.MethodPut, body)
		if status != stdhttp.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400; payload=%v", body, status, payload)
		}
		if payload["code"] != "invalid_argument" {
			t.Fatalf("body %s: code = %v", body, payload["code"])
		}
	}
}

func TestUpdateUserSettingsPassesNonBooleanValuesToTheService(t *testing.T) {
	// A number cannot be silently accepted: it is stringified so the service
	// rejects it with the canonical invalid_value error, keeping one source of
	// truth for validation.
	svc := &userSettingsStub{updateErr: &settingssvc.Error{
		Code:       settingssvc.CodeInvalidArgument,
		MessageKey: "error.user_settings.invalid_value",
		Details:    map[string]string{"key": "data_saver", "value": "5"},
	}}
	status, payload := userSettingsRequest(t, newUserSettingsRouter(svc), stdhttp.MethodPut,
		`{"settings":{"data_saver":5}}`)
	if status != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400; payload=%v", status, payload)
	}
	if svc.gotPatch["data_saver"] == "true" || svc.gotPatch["data_saver"] == "false" {
		t.Fatalf("a number was coerced into a boolean: %v", svc.gotPatch)
	}
	details, _ := payload["details"].(map[string]any)
	if details["key"] != "data_saver" || details["value"] != "5" {
		t.Fatalf("details = %v", payload["details"])
	}
}

func TestUserSettingsInternalErrorMapsTo500(t *testing.T) {
	svc := &userSettingsStub{getErr: &settingssvc.Error{
		Code: settingssvc.CodeInternal, MessageKey: "error.internal",
	}}
	status, payload := userSettingsRequest(t, newUserSettingsRouter(svc), stdhttp.MethodGet, "")
	if status != stdhttp.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; payload=%v", status, payload)
	}
	if payload["code"] != "internal" {
		t.Fatalf("code = %v", payload["code"])
	}
}

func TestUserSettingsRouteRejectsWrongMethodAndAnonymous(t *testing.T) {
	router := newUserSettingsRouter(&userSettingsStub{})

	status, _ := userSettingsRequest(t, router, stdhttp.MethodDelete, "")
	if status != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", status)
	}

	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/profile/user-settings", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestUserSettingsRouteDoesNotDisturbProfileSettings(t *testing.T) {
	// /profile/settings lives on a different pattern and must still answer
	// without the new service being wired into it.
	router := newUserSettingsRouter(&userSettingsStub{})
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/profile/settings", nil)
	req.Header.Set("Authorization", "Bearer "+makeAccessToken(t, "u-test", "test-secret", time.Now().UTC().Add(10*time.Minute).Unix()))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != stdhttp.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no profile repo is wired", rr.Code)
	}
}
