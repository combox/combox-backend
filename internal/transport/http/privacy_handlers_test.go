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

	authsvc "combox-backend/internal/service/auth"
	privacysvc "combox-backend/internal/service/privacy"
)

type stubPrivacyService struct {
	getResult privacysvc.Result
	getErr    error
	setResult privacysvc.Setting
	setErr    error
	allowed   bool
	evalErr   error

	getUserID string
	setUserID string
	setParam  string
	setRule   string
	setAllow  []string
	setDeny   []string
	evalCalls int
}

func (s *stubPrivacyService) Get(_ context.Context, userID string) (privacysvc.Result, error) {
	s.getUserID = userID
	if s.getErr != nil {
		return privacysvc.Result{}, s.getErr
	}
	return s.getResult, nil
}

func (s *stubPrivacyService) Set(_ context.Context, userID, param, rule string, allowIDs, denyIDs []string) (privacysvc.Setting, error) {
	s.setUserID = userID
	s.setParam = param
	s.setRule = rule
	s.setAllow = allowIDs
	s.setDeny = denyIDs
	if s.setErr != nil {
		return privacysvc.Setting{}, s.setErr
	}
	return s.setResult, nil
}

func (s *stubPrivacyService) Evaluate(_ context.Context, viewerID, targetID, param string) (bool, error) {
	s.evalCalls++
	if s.evalErr != nil {
		return false, s.evalErr
	}
	return s.allowed, nil
}

type stubPrivacyRepo struct{}

func (stubPrivacyRepo) GetAll(context.Context, string) ([]privacysvc.Row, error) {
	return nil, nil
}

func (stubPrivacyRepo) Upsert(context.Context, string, string, string, []string, []string) error {
	return nil
}

func (stubPrivacyRepo) AreDirectContacts(context.Context, string, string) (bool, error) {
	return false, nil
}

func newRealPrivacyService(t *testing.T) *privacysvc.Service {
	t.Helper()
	svc, err := privacysvc.New(stubPrivacyRepo{})
	if err != nil {
		t.Fatalf("privacy.New: %v", err)
	}
	return svc
}

type privacyProfileAuthStub struct {
	stubAuthService
	user authsvc.User
}

func (s privacyProfileAuthStub) GetProfile(context.Context, string) (authsvc.User, error) {
	return s.user, nil
}

func newPrivacyRouter(t *testing.T, privacy PrivacyService) (stdhttp.Handler, string) {
	t.Helper()
	const secret = "privacy-test-secret"
	router := NewRouter(RouterDeps{
		Logger:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Postgres:      stubPinger{},
		Valkey:        stubPinger{},
		ReadyTimeout:  time.Second,
		I18n:          testTranslator(),
		DefaultLocale: "en",
		AccessSecret:  secret,
		Privacy:       privacy,
	})
	return router, secret
}

const privacyViewerID = "11111111-1111-1111-1111-111111111111"

func doPrivacyRequest(t *testing.T, router stdhttp.Handler, secret, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	token := makeAccessToken(t, privacyViewerID, secret, time.Now().UTC().Add(10*time.Minute).Unix())
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func decodePrivacyPayload(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	return payload
}

func TestPrivacySettingsGetEndpoint(t *testing.T) {
	privacy := &stubPrivacyService{getResult: privacysvc.Result{
		Settings: []privacysvc.Setting{{
			Param:      "last_seen",
			Rule:       "nobody",
			AllowIDs:   []string{},
			DenyIDs:    []string{},
			AllowCount: 0,
		}},
		Defaults: map[string]string{"last_seen": "everybody"},
	}}
	router, secret := newPrivacyRouter(t, privacy)

	rr := doPrivacyRequest(t, router, secret, stdhttp.MethodGet, "/api/private/v1/profile/privacy", "")
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if privacy.getUserID != privacyViewerID {
		t.Fatalf("Get(user) = %q, want %q", privacy.getUserID, privacyViewerID)
	}
	payload := decodePrivacyPayload(t, rr)
	if payload["message"] != "ok" {
		t.Fatalf("message = %#v", payload["message"])
	}
	settings, ok := payload["settings"].([]any)
	if !ok || len(settings) != 1 {
		t.Fatalf("settings = %#v", payload["settings"])
	}
	first, ok := settings[0].(map[string]any)
	if !ok || first["param"] != "last_seen" || first["rule"] != "nobody" {
		t.Fatalf("settings[0] = %#v", settings[0])
	}
	if _, ok := first["allow_ids"]; !ok {
		t.Fatalf("settings[0] missing allow_ids: %#v", first)
	}
	if _, ok := first["deny_count"]; !ok {
		t.Fatalf("settings[0] missing deny_count: %#v", first)
	}
	defaults, ok := payload["defaults"].(map[string]any)
	if !ok || defaults["last_seen"] != "everybody" {
		t.Fatalf("defaults = %#v", payload["defaults"])
	}
}

func TestPrivacySettingsGetRequiresAuth(t *testing.T) {
	router, _ := newPrivacyRouter(t, &stubPrivacyService{})
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/profile/privacy", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	payload := decodePrivacyPayload(t, rr)
	if payload["code"] != "unauthorized" {
		t.Fatalf("code = %#v", payload["code"])
	}
}

func TestPrivacySettingPutEndpoint(t *testing.T) {
	privacy := &stubPrivacyService{setResult: privacysvc.Setting{
		Param:      "last_seen",
		Rule:       "nobody",
		AllowIDs:   []string{"22222222-2222-2222-2222-222222222222"},
		DenyIDs:    []string{},
		AllowCount: 1,
		DenyCount:  0,
	}}
	router, secret := newPrivacyRouter(t, privacy)

	rr := doPrivacyRequest(t, router, secret, stdhttp.MethodPut, "/api/private/v1/profile/privacy/last_seen",
		`{"rule":"nobody","allow_ids":["22222222-2222-2222-2222-222222222222"],"deny_ids":[]}`)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if privacy.setUserID != privacyViewerID || privacy.setParam != "last_seen" || privacy.setRule != "nobody" {
		t.Fatalf("Set call = %q/%q/%q", privacy.setUserID, privacy.setParam, privacy.setRule)
	}
	if len(privacy.setAllow) != 1 || privacy.setAllow[0] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("allow ids = %#v", privacy.setAllow)
	}
	payload := decodePrivacyPayload(t, rr)
	if payload["message"] != "ok" || payload["param"] != "last_seen" || payload["rule"] != "nobody" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload["allow_count"] != float64(1) || payload["deny_count"] != float64(0) {
		t.Fatalf("counts = %#v / %#v", payload["allow_count"], payload["deny_count"])
	}
	if _, ok := payload["allow_ids"].([]any); !ok {
		t.Fatalf("allow_ids = %#v", payload["allow_ids"])
	}
}

func TestPrivacySettingPutRejectsInvalidRule(t *testing.T) {
	privacy := &stubPrivacyService{setErr: &privacysvc.Error{
		Code:       privacysvc.CodeInvalidArgument,
		MessageKey: "error.request.invalid_input",
		Details:    map[string]string{"rule": "sometimes"},
		Cause:      privacysvc.ErrInvalidRule,
	}}
	router, secret := newPrivacyRouter(t, privacy)

	rr := doPrivacyRequest(t, router, secret, stdhttp.MethodPut, "/api/private/v1/profile/privacy/last_seen",
		`{"rule":"sometimes"}`)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	payload := decodePrivacyPayload(t, rr)
	if payload["code"] != "error.validation" {
		t.Fatalf("code = %#v, want error.validation", payload["code"])
	}
	if payload["message"] != "error.request.invalid_input" && payload["message"] != "invalid input" {
		t.Fatalf("message = %#v", payload["message"])
	}
}

func TestPrivacySettingPutRejectsUnknownParamAndBadJSON(t *testing.T) {
	// Real service underneath: unknown params and unknown rules are rejected
	// by the privacy service itself, the handler only maps the error code.
	router, secret := newPrivacyRouter(t, newRealPrivacyService(t))

	rr := doPrivacyRequest(t, router, secret, stdhttp.MethodPut, "/api/private/v1/profile/privacy/timezone",
		`{"rule":"everybody"}`)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("unknown param status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if payload := decodePrivacyPayload(t, rr); payload["code"] != "error.validation" {
		t.Fatalf("unknown param code = %#v", payload["code"])
	}

	rr = doPrivacyRequest(t, router, secret, stdhttp.MethodPut, "/api/private/v1/profile/privacy/last_seen",
		`{"rule":"sometimes"}`)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("unknown rule status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if payload := decodePrivacyPayload(t, rr); payload["code"] != "error.validation" {
		t.Fatalf("unknown rule code = %#v", payload["code"])
	}

	rr = doPrivacyRequest(t, router, secret, stdhttp.MethodPut, "/api/private/v1/profile/privacy/last_seen",
		`{"rule":"nobody","allow_ids":["not-a-uuid"]}`)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("bad id status = %d, body = %s", rr.Code, rr.Body.String())
	}

	rr = doPrivacyRequest(t, router, secret, stdhttp.MethodPut, "/api/private/v1/profile/privacy/last_seen",
		`{not json`)
	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("bad json status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if payload := decodePrivacyPayload(t, rr); payload["code"] != "invalid_json" {
		t.Fatalf("bad json code = %#v", payload["code"])
	}
}

func TestPrivacySettingsGetEndpointWithRealService(t *testing.T) {
	router, secret := newPrivacyRouter(t, newRealPrivacyService(t))

	rr := doPrivacyRequest(t, router, secret, stdhttp.MethodGet, "/api/private/v1/profile/privacy", "")
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	payload := decodePrivacyPayload(t, rr)
	settings, ok := payload["settings"].([]any)
	if !ok || len(settings) != 12 {
		t.Fatalf("settings = %#v, want 12 entries", payload["settings"])
	}
	rules := map[string]string{}
	for _, raw := range settings {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("setting = %#v", raw)
		}
		param, _ := entry["param"].(string)
		rule, _ := entry["rule"].(string)
		rules[param] = rule
		if _, ok := entry["allow_ids"]; !ok {
			t.Fatalf("%s missing allow_ids: %#v", param, entry)
		}
		if _, ok := entry["deny_count"]; !ok {
			t.Fatalf("%s missing deny_count: %#v", param, entry)
		}
	}
	if rules["phone_number"] != "contacts" || rules["forwarded_messages"] != "contacts" {
		t.Fatalf("default rules = %#v", rules)
	}
	if rules["last_seen"] != "everybody" {
		t.Fatalf("last_seen default = %q", rules["last_seen"])
	}
	defaults, ok := payload["defaults"].(map[string]any)
	if !ok || len(defaults) != 12 {
		t.Fatalf("defaults = %#v", payload["defaults"])
	}
}

func TestPrivacySettingHandlerRejectsMissingParamSegment(t *testing.T) {
	privacy := &stubPrivacyService{}
	handler := newPrivacySettingHandler(privacy, testTranslator(), "en")

	req := httptest.NewRequest(stdhttp.MethodPut, "/api/private/v1/profile/privacy/", strings.NewReader(`{"rule":"everybody"}`))
	req.Header.Set("X-User-ID", privacyViewerID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if privacy.setParam != "" {
		t.Fatalf("service was called with param %q", privacy.setParam)
	}
	if payload := decodePrivacyPayload(t, rr); payload["code"] != "error.validation" {
		t.Fatalf("code = %#v", payload["code"])
	}
}

func TestPrivacySettingEndpointMethodNotAllowed(t *testing.T) {
	router, secret := newPrivacyRouter(t, &stubPrivacyService{})

	rr := doPrivacyRequest(t, router, secret, stdhttp.MethodGet, "/api/private/v1/profile/privacy/last_seen", "")
	if rr.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestUserProfilePrivacyMasksPhoneAndBio(t *testing.T) {
	phone := "+15551234567"
	bio := "hello there"
	authStub := privacyProfileAuthStub{user: authsvc.User{
		ID:          "target-1",
		Username:    "target",
		PhoneNumber: &phone,
		Bio:         &bio,
	}}

	privacy := &stubPrivacyService{allowed: false}
	handler := newUserByIDHandler(authStub, privacy, testTranslator(), "en")
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/users/target-1", nil)
	req.Header.Set("X-User-ID", privacyViewerID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if privacy.evalCalls != 2 {
		t.Fatalf("Evaluate calls = %d, want 2 (phone + bio)", privacy.evalCalls)
	}
	payload := decodePrivacyPayload(t, rr)
	user, ok := payload["user"].(map[string]any)
	if !ok {
		t.Fatalf("user = %#v", payload["user"])
	}
	if user["phone_number"] != "" || user["bio"] != "" {
		t.Fatalf("masked fields = phone:%#v bio:%#v", user["phone_number"], user["bio"])
	}

	allowed := &stubPrivacyService{allowed: true}
	handler = newUserByIDHandler(authStub, allowed, testTranslator(), "en")
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/users/target-1", nil)
	req.Header.Set("X-User-ID", privacyViewerID)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	payload = decodePrivacyPayload(t, rr)
	user = payload["user"].(map[string]any)
	if user["phone_number"] != phone || user["bio"] != bio {
		t.Fatalf("visible fields = phone:%#v bio:%#v", user["phone_number"], user["bio"])
	}

	// The owner always sees their own profile, no privacy lookup needed.
	selfPrivacy := &stubPrivacyService{allowed: false}
	handler = newUserByIDHandler(authStub, selfPrivacy, testTranslator(), "en")
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/users/target-1", nil)
	req.Header.Set("X-User-ID", "target-1")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if selfPrivacy.evalCalls != 0 {
		t.Fatalf("owner profile made %d privacy calls, want 0", selfPrivacy.evalCalls)
	}
	payload = decodePrivacyPayload(t, rr)
	user = payload["user"].(map[string]any)
	if user["phone_number"] != phone || user["bio"] != bio {
		t.Fatalf("owner fields = phone:%#v bio:%#v", user["phone_number"], user["bio"])
	}
}

func TestUserProfilePrivacyFailsClosedOnEvaluationError(t *testing.T) {
	phone := "+15551234567"
	authStub := privacyProfileAuthStub{user: authsvc.User{
		ID:          "target-1",
		Username:    "target",
		PhoneNumber: &phone,
	}}

	privacy := &stubPrivacyService{evalErr: context.DeadlineExceeded}
	handler := newUserByIDHandler(authStub, privacy, testTranslator(), "en")
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/private/v1/users/target-1", nil)
	req.Header.Set("X-User-ID", privacyViewerID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	payload := decodePrivacyPayload(t, rr)
	user := payload["user"].(map[string]any)
	if user["phone_number"] != "" {
		t.Fatalf("phone_number = %#v, want empty", user["phone_number"])
	}
}

func TestFilterPresencePayload(t *testing.T) {
	const presencePayload = `{"type":"presence.update","user_id":"owner-1","online":true,"last_seen":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"}`

	t.Run("non presence frames pass through", func(t *testing.T) {
		privacy := &stubPrivacyService{allowed: false}
		payload := `{"type":"message.created","id":"m1"}`
		if got := filterPresencePayload(context.Background(), privacy, "viewer-1", payload); got != payload {
			t.Fatalf("got %q, want unchanged", got)
		}
		if privacy.evalCalls != 0 {
			t.Fatalf("Evaluate calls = %d, want 0", privacy.evalCalls)
		}
	})

	t.Run("allowed presence is byte identical", func(t *testing.T) {
		privacy := &stubPrivacyService{allowed: true}
		if got := filterPresencePayload(context.Background(), privacy, "viewer-1", presencePayload); got != presencePayload {
			t.Fatalf("got %q, want unchanged", got)
		}
	})

	t.Run("own presence passes through", func(t *testing.T) {
		privacy := &stubPrivacyService{allowed: false}
		payload := `{"type":"presence.update","user_id":"viewer-1","online":true,"last_seen":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"}`
		if got := filterPresencePayload(context.Background(), privacy, "viewer-1", payload); got != payload {
			t.Fatalf("got %q, want unchanged", got)
		}
		if privacy.evalCalls != 0 {
			t.Fatalf("Evaluate calls = %d, want 0", privacy.evalCalls)
		}
	})

	t.Run("denied presence is masked", func(t *testing.T) {
		privacy := &stubPrivacyService{allowed: false}
		got := filterPresencePayload(context.Background(), privacy, "viewer-1", presencePayload)
		var frame map[string]any
		if err := json.Unmarshal([]byte(got), &frame); err != nil {
			t.Fatalf("decode %q: %v", got, err)
		}
		if frame["user_id"] != "owner-1" || frame["type"] != "presence.update" {
			t.Fatalf("frame identity = %#v", frame)
		}
		if frame["online"] != false {
			t.Fatalf("online = %#v, want false", frame["online"])
		}
		if frame["last_seen_visible"] != false {
			t.Fatalf("last_seen_visible = %#v, want false", frame["last_seen_visible"])
		}
		if _, ok := frame["last_seen"]; ok {
			t.Fatalf("last_seen leaked: %#v", frame)
		}
		if _, ok := frame["updated_at"]; ok {
			t.Fatalf("updated_at leaked: %#v", frame)
		}
	})

	t.Run("evaluation error fails closed", func(t *testing.T) {
		privacy := &stubPrivacyService{evalErr: context.DeadlineExceeded}
		got := filterPresencePayload(context.Background(), privacy, "viewer-1", presencePayload)
		var frame map[string]any
		if err := json.Unmarshal([]byte(got), &frame); err != nil {
			t.Fatalf("decode %q: %v", got, err)
		}
		if _, ok := frame["last_seen"]; ok {
			t.Fatalf("last_seen leaked on error: %#v", frame)
		}
		if frame["online"] != false || frame["last_seen_visible"] != false {
			t.Fatalf("frame not masked: %#v", frame)
		}
	})
}
