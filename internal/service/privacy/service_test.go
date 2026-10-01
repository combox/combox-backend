package privacy

import (
	"context"
	"errors"
	"sync"
	"testing"
)

const (
	testOwner   = "11111111-1111-1111-1111-111111111111"
	testViewer  = "22222222-2222-2222-2222-222222222222"
	testOther   = "33333333-3333-3333-3333-333333333333"
	testContact = "44444444-4444-4444-4444-444444444444"
)

type upsertCall struct {
	userID string
	param  string
	rule   string
	allow  []string
	deny   []string
}

type fakeRepo struct {
	mu         sync.Mutex
	rows       map[string]map[string]Row
	contacts   map[string]bool
	getAllErr  error
	upsertErr  error
	contactErr error
	getAllCall int
	contactQ   int
	upserts    []upsertCall
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[string]map[string]Row{}, contacts: map[string]bool{}}
}

func (f *fakeRepo) set(userID, param, rule string, allow, deny []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[userID] == nil {
		f.rows[userID] = map[string]Row{}
	}
	f.rows[userID][param] = Row{Param: param, Rule: rule, AllowIDs: allow, DenyIDs: deny}
}

func (f *fakeRepo) GetAll(_ context.Context, userID string) ([]Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getAllCall++
	if f.getAllErr != nil {
		return nil, f.getAllErr
	}
	out := make([]Row, 0, len(f.rows[userID]))
	for _, row := range f.rows[userID] {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeRepo) Upsert(_ context.Context, userID, param, rule string, allowIDs, denyIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts = append(f.upserts, upsertCall{userID: userID, param: param, rule: rule, allow: allowIDs, deny: denyIDs})
	if f.rows[userID] == nil {
		f.rows[userID] = map[string]Row{}
	}
	f.rows[userID][param] = Row{Param: param, Rule: rule, AllowIDs: allowIDs, DenyIDs: denyIDs}
	return nil
}

func (f *fakeRepo) AreDirectContacts(_ context.Context, userAID, userBID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contactQ++
	if f.contactErr != nil {
		return false, f.contactErr
	}
	return f.contacts[userAID+"|"+userBID], nil
}

func newTestService(t *testing.T, repo *fakeRepo) *Service {
	t.Helper()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestDefaultsCoverEveryParamWithTheTelegramShape(t *testing.T) {
	if len(Params) != 12 {
		t.Fatalf("len(Params) = %d, want 12", len(Params))
	}
	wantDefaults := map[string]string{
		"phone_number":       RuleContacts,
		"forwarded_messages": RuleContacts,
		"last_seen":          RuleEverybody,
		"profile_photos":     RuleEverybody,
		"calls":              RuleEverybody,
		"voice_messages":     RuleEverybody,
		"messages":           RuleEverybody,
		"birthday":           RuleEverybody,
		"gifts":              RuleEverybody,
		"bio":                RuleEverybody,
		"saved_music":        RuleEverybody,
		"invites":            RuleEverybody,
	}
	for param, want := range wantDefaults {
		if !IsParam(param) {
			t.Fatalf("IsParam(%q) = false", param)
		}
		if got := DefaultRule(param); got != want {
			t.Fatalf("DefaultRule(%q) = %q, want %q", param, got, want)
		}
	}
	if IsParam("nonsense") {
		t.Fatalf("IsParam(nonsense) = true")
	}
	if IsRule("sometimes") {
		t.Fatalf("IsRule(sometimes) = true")
	}
}

func TestGetReturnsDefaultsForUntouchedParams(t *testing.T) {
	svc := newTestService(t, newFakeRepo())

	res, err := svc.Get(context.Background(), testOwner)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(res.Settings) != len(Params) {
		t.Fatalf("len(settings) = %d, want %d", len(res.Settings), len(Params))
	}
	for i, setting := range res.Settings {
		if setting.Param != Params[i] {
			t.Fatalf("settings[%d].param = %q, want %q", i, setting.Param, Params[i])
		}
		if setting.Rule != DefaultRule(setting.Param) {
			t.Fatalf("%s rule = %q, want %q", setting.Param, setting.Rule, DefaultRule(setting.Param))
		}
		if setting.AllowIDs == nil || len(setting.AllowIDs) != 0 {
			t.Fatalf("%s allow_ids = %#v, want empty non-nil", setting.Param, setting.AllowIDs)
		}
		if setting.DenyIDs == nil || len(setting.DenyIDs) != 0 {
			t.Fatalf("%s deny_ids = %#v, want empty non-nil", setting.Param, setting.DenyIDs)
		}
		if setting.AllowCount != 0 || setting.DenyCount != 0 {
			t.Fatalf("%s counts = %d/%d, want 0/0", setting.Param, setting.AllowCount, setting.DenyCount)
		}
	}
	if len(res.Defaults) != len(Params) {
		t.Fatalf("len(defaults) = %d, want %d", len(res.Defaults), len(Params))
	}
	if res.Defaults["phone_number"] != RuleContacts || res.Defaults["forwarded_messages"] != RuleContacts {
		t.Fatalf("defaults = %#v, want phone_number/forwarded_messages = contacts", res.Defaults)
	}
}

func TestGetReturnsStoredRowsAndDoesNotMaterialiseDefaults(t *testing.T) {
	repo := newFakeRepo()
	repo.set(testOwner, ParamLastSeen, RuleNobody, []string{testViewer}, nil)

	svc := newTestService(t, repo)
	res, err := svc.Get(context.Background(), testOwner)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var lastSeen *Setting
	for i := range res.Settings {
		if res.Settings[i].Param == ParamLastSeen {
			lastSeen = &res.Settings[i]
		}
	}
	if lastSeen == nil {
		t.Fatalf("last_seen missing from %#v", res.Settings)
	}
	if lastSeen.Rule != RuleNobody || lastSeen.AllowCount != 1 || lastSeen.AllowIDs[0] != testViewer {
		t.Fatalf("last_seen = %#v", *lastSeen)
	}
	if len(repo.upserts) != 0 {
		t.Fatalf("Get wrote %d rows, want 0", len(repo.upserts))
	}
}

func TestSetValidatesAndNormalises(t *testing.T) {
	svc := newTestService(t, newFakeRepo())

	cases := []struct {
		name    string
		param   string
		rule    string
		ids     []string
		wantErr error
	}{
		{name: "unknown param", param: "timezone", rule: RuleEverybody, wantErr: ErrUnknownParam},
		{name: "unknown rule", param: ParamLastSeen, rule: "sometimes", wantErr: ErrInvalidRule},
		{name: "bad allow id", param: ParamLastSeen, rule: RuleNobody, ids: []string{"not-a-uuid"}, wantErr: ErrInvalidID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Set(context.Background(), testOwner, tc.param, tc.rule, tc.ids, nil)
			var svcErr *Error
			if !errors.As(err, &svcErr) {
				t.Fatalf("err = %v, want *Error", err)
			}
			if svcErr.Code != CodeInvalidArgument {
				t.Fatalf("code = %q, want %q", svcErr.Code, CodeInvalidArgument)
			}
			if svcErr.MessageKey != "error.request.invalid_input" {
				t.Fatalf("message key = %q", svcErr.MessageKey)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tc.wantErr)
			}
		})
	}

	if _, err := svc.Set(context.Background(), "viewer-1", ParamLastSeen, RuleNobody, nil, nil); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid user id err = %v, want ErrInvalidID", err)
	}
}

func TestSetPersistsNormalisedIDsAndCounts(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(t, repo)

	setting, err := svc.Set(context.Background(), testOwner, ParamPhoneNumber, RuleNobody,
		[]string{" " + testViewer + " ", testViewer, testOther}, []string{"", testContact})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if setting.Rule != RuleNobody || setting.AllowCount != 2 || setting.DenyCount != 1 {
		t.Fatalf("setting = %#v", setting)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("upserts = %#v", repo.upserts)
	}
	call := repo.upserts[0]
	if call.userID != testOwner || call.param != ParamPhoneNumber || call.rule != RuleNobody {
		t.Fatalf("call = %#v", call)
	}
	if len(call.allow) != 2 || call.allow[0] != testViewer || call.allow[1] != testOther {
		t.Fatalf("allow = %#v", call.allow)
	}
	if len(call.deny) != 1 || call.deny[0] != testContact {
		t.Fatalf("deny = %#v", call.deny)
	}
}

func TestSetMapsRepositoryFailure(t *testing.T) {
	repo := newFakeRepo()
	repo.upsertErr = errors.New("db down")
	svc := newTestService(t, repo)

	_, err := svc.Set(context.Background(), testOwner, ParamLastSeen, RuleEverybody, nil, nil)
	var svcErr *Error
	if !errors.As(err, &svcErr) || svcErr.Code != CodeInternal {
		t.Fatalf("err = %v, want internal Error", err)
	}
}

func TestEvaluateRuleMatrix(t *testing.T) {
	cases := []struct {
		name    string
		rule    string
		allow   []string
		deny    []string
		contact bool
		want    bool
	}{
		{name: "everybody", rule: RuleEverybody, want: true},
		{name: "everybody but deny", rule: RuleEverybody, deny: []string{testViewer}, want: false},
		{name: "contacts with direct chat", rule: RuleContacts, contact: true, want: true},
		{name: "contacts without direct chat", rule: RuleContacts, contact: false, want: false},
		{name: "contacts allowlisted", rule: RuleContacts, contact: false, allow: []string{testViewer}, want: true},
		{name: "contacts denylisted", rule: RuleContacts, contact: true, deny: []string{testViewer}, want: false},
		{name: "nobody", rule: RuleNobody, want: false},
		{name: "nobody allowlisted", rule: RuleNobody, allow: []string{testViewer}, want: true},
		{name: "nobody denylisted beats allowlist", rule: RuleNobody, allow: []string{testViewer}, deny: []string{testViewer}, want: false},
		{name: "unknown stored rule fails closed", rule: "sometimes", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			repo.set(testOwner, ParamLastSeen, tc.rule, tc.allow, tc.deny)
			repo.contacts[testViewer+"|"+testOwner] = tc.contact
			svc := newTestService(t, repo)

			got, err := svc.Evaluate(context.Background(), testViewer, testOwner, ParamLastSeen)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Evaluate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluateShortCircuits(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(t, repo)

	if ok, err := svc.Evaluate(context.Background(), testOwner, testOwner, ParamLastSeen); err != nil || !ok {
		t.Fatalf("owner evaluate = %v, %v; want true, nil", ok, err)
	}
	if repo.getAllCall != 0 {
		t.Fatalf("owner evaluation read rows %d times", repo.getAllCall)
	}
	if ok, err := svc.Evaluate(context.Background(), "", testOwner, ParamLastSeen); err != nil || ok {
		t.Fatalf("empty viewer = %v, %v; want false, nil", ok, err)
	}
	if ok, err := svc.Evaluate(context.Background(), testViewer, testOwner, "timezone"); err == nil || ok {
		t.Fatalf("unknown param = %v, %v; want false, error", ok, err)
	}
	if repo.getAllCall != 0 {
		t.Fatalf("short-circuited evaluations read rows %d times", repo.getAllCall)
	}
}

func TestEvaluateMemoisesRowsAndDecisions(t *testing.T) {
	repo := newFakeRepo()
	repo.set(testOwner, ParamLastSeen, RuleContacts, nil, nil)
	repo.set(testOwner, ParamPhoneNumber, RuleNobody, nil, nil)
	repo.contacts[testViewer+"|"+testOwner] = true
	svc := newTestService(t, repo)

	ctx := WithMemo(context.Background())
	for i := 0; i < 5; i++ {
		if ok, err := svc.Evaluate(ctx, testViewer, testOwner, ParamLastSeen); err != nil || !ok {
			t.Fatalf("Evaluate #%d = %v, %v", i, ok, err)
		}
		if ok, err := svc.Evaluate(ctx, testViewer, testOwner, ParamPhoneNumber); err != nil || ok {
			t.Fatalf("Evaluate phone #%d = %v, %v", i, ok, err)
		}
	}
	if repo.getAllCall != 1 {
		t.Fatalf("GetAll calls = %d, want 1", repo.getAllCall)
	}
	if repo.contactQ != 1 {
		t.Fatalf("AreDirectContacts calls = %d, want 1", repo.contactQ)
	}
	if _, ok := memoFrom(ctx).decisions[testViewer+"|"+testOwner+"|"+ParamLastSeen]; !ok {
		t.Fatalf("decision not memoised: %#v", memoFrom(ctx).decisions)
	}
}

func TestEvaluateWithoutMemoStillWorksAndDoesNotShareReads(t *testing.T) {
	repo := newFakeRepo()
	repo.set(testOwner, ParamLastSeen, RuleEverybody, nil, nil)
	svc := newTestService(t, repo)

	for i := 0; i < 2; i++ {
		if ok, err := svc.Evaluate(context.Background(), testViewer, testOwner, ParamLastSeen); err != nil || !ok {
			t.Fatalf("Evaluate #%d = %v, %v", i, ok, err)
		}
	}
	if repo.getAllCall != 2 {
		t.Fatalf("GetAll calls = %d, want 2", repo.getAllCall)
	}
}

func TestEvaluateSurfacesRepositoryFailures(t *testing.T) {
	repo := newFakeRepo()
	repo.getAllErr = errors.New("db down")
	svc := newTestService(t, repo)

	if _, err := svc.Evaluate(context.Background(), testViewer, testOwner, ParamLastSeen); err == nil {
		t.Fatalf("Evaluate err = nil, want error")
	} else {
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != CodeInternal {
			t.Fatalf("err = %v, want internal Error", err)
		}
	}

	repo2 := newFakeRepo()
	repo2.set(testOwner, ParamLastSeen, RuleContacts, nil, nil)
	repo2.contactErr = errors.New("db down")
	svc2 := newTestService(t, repo2)
	if _, err := svc2.Evaluate(context.Background(), testViewer, testOwner, ParamLastSeen); err == nil {
		t.Fatalf("Evaluate contact err = nil, want error")
	}
}

func TestNewRequiresRepository(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatalf("New(nil) err = nil, want error")
	}
}
