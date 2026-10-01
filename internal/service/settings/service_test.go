package settings

import (
	"context"
	"errors"
	"testing"
)

type memUserSettingsRepo struct {
	rows map[string]string
	// failWith makes UpsertMany/GetAll return this error.
	failWith error
	// lastPatch records what UpsertMany received.
	lastPatch map[string]string
}

func newMemUserSettingsRepo() *memUserSettingsRepo {
	return &memUserSettingsRepo{rows: map[string]string{}}
}

func (m *memUserSettingsRepo) GetAll(_ context.Context, userID string) (map[string]string, error) {
	if m.failWith != nil {
		return nil, m.failWith
	}
	out := make(map[string]string, len(m.rows))
	for key, value := range m.rows {
		_ = userID
		out[key] = value
	}
	return out, nil
}

func (m *memUserSettingsRepo) UpsertMany(_ context.Context, userID string, values map[string]string) error {
	if m.failWith != nil {
		return m.failWith
	}
	_ = userID
	m.lastPatch = map[string]string{}
	for key, value := range values {
		m.rows[key] = value
		m.lastPatch[key] = value
	}
	return nil
}

func TestDefaultsCoverExactlyTheWhitelist(t *testing.T) {
	want := map[string]string{
		"notifications_enabled":     "true",
		"notification_previews":     "true",
		"sounds_enabled":            "true",
		"badge_enabled":             "true",
		"voice_autoplay":            "false",
		"media_autoplay":            "true",
		"data_saver":                "false",
		"auto_download_photos":      "true",
		"auto_download_videos":      "false",
		"auto_download_files":       "false",
		"notifications_private":     "true",
		"notifications_groups":      "true",
		"notifications_channels":    "true",
		"notifications_reactions":   "true",
		"notification_preview_name": "true",
		"notification_preview_text": "true",
		"events_contact_joined":     "true",
		"events_pinned":             "true",
		"calls_accept":              "true",
		"badge_include_muted":       "false",
		"badge_folders_count":       "false",
		"badge_count_messages":      "false",
		"delete_account_ttl":        "6_months",
	}
	if len(Keys()) != len(want) {
		t.Fatalf("whitelist size = %d, want %d", len(Keys()), len(want))
	}
	for key, wantValue := range want {
		if !IsKey(key) {
			t.Fatalf("IsKey(%q) = false, want true", key)
		}
		got, ok := Default(key)
		if !ok || got != wantValue {
			t.Fatalf("Default(%q) = %q,%v want %q,true", key, got, ok, wantValue)
		}
	}
	if IsKey("totally_made_up") {
		t.Fatalf("IsKey accepted a key outside the whitelist")
	}
	if _, ok := Default("totally_made_up"); ok {
		t.Fatalf("Default returned a value for a key outside the whitelist")
	}
}

func TestGetUserSettingsReturnsDefaultsWhenNothingStored(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := svc.GetUserSettings(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != len(Keys()) {
		t.Fatalf("got %d keys, want %d", len(got), len(Keys()))
	}
	for key, want := range Defaults() {
		if got[key] != want {
			t.Fatalf("got[%q] = %q, want %q", key, got[key], want)
		}
	}
}

func TestGetUserSettingsMergesStoredValuesAndIgnoresJunk(t *testing.T) {
	repo := newMemUserSettingsRepo()
	repo.rows = map[string]string{
		"sounds_enabled": "false",
		"data_saver":     "true",
		"legacy_key":     "true",
		"voice_autoplay": "sometimes",
	}
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := svc.GetUserSettings(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["sounds_enabled"] != "false" || got["data_saver"] != "true" {
		t.Fatalf("stored values were not merged: %v", got)
	}
	if _, ok := got["legacy_key"]; ok {
		t.Fatalf("a key outside the whitelist leaked into the response")
	}
	if got["voice_autoplay"] != "false" {
		t.Fatalf("an invalid stored value should fall back to the default, got %q", got["voice_autoplay"])
	}
	if len(got) != len(Keys()) {
		t.Fatalf("got %d keys, want %d", len(got), len(Keys()))
	}
}

func TestUpdateUserSettingsWritesAndReturnsMerged(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{
		"sounds_enabled": "false",
		"badge_enabled":  "false",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if repo.lastPatch["sounds_enabled"] != "false" || repo.lastPatch["badge_enabled"] != "false" {
		t.Fatalf("repo patch = %v", repo.lastPatch)
	}
	if len(repo.lastPatch) != 2 {
		t.Fatalf("repo patch has %d keys, want 2 (only the patch is written)", len(repo.lastPatch))
	}
	if got["sounds_enabled"] != "false" || got["badge_enabled"] != "false" {
		t.Fatalf("merged response = %v", got)
	}
	if got["media_autoplay"] != "true" {
		t.Fatalf("untouched keys must keep their default, got %q", got["media_autoplay"])
	}
}

func TestUpdateUserSettingsRejectsUnknownKey(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{
		"totally_made_up": "true",
	})
	if err == nil {
		t.Fatalf("expected an error for an unknown key")
	}
	// Privacy knobs (last_seen, profile_photos, ...) live in privacy_settings,
	// not here: they must stay rejected so the two stores never diverge.
	for _, key := range []string{"privacy_lastseen", "privacy_birthday", "last_seen"} {
		if _, err := svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{key: "true"}); err == nil {
			t.Fatalf("expected an error for privacy-duplicate key %q", key)
		}
	}
	if err == nil {
		t.Fatalf("expected an error for an unknown key")
	}
	var svcErr *Error
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if svcErr.Code != CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", svcErr.Code, CodeInvalidArgument)
	}
	if svcErr.Details["key"] != "totally_made_up" {
		t.Fatalf("details = %v, want the offending key", svcErr.Details)
	}
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("error should unwrap to ErrUnknownKey")
	}
	if len(repo.rows) != 0 {
		t.Fatalf("a rejected patch must not touch the store: %v", repo.rows)
	}
}

func TestUpdateUserSettingsRejectsNonBooleanValue(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, value := range []string{"", "yes", "TRUE", "1", "0"} {
		_, err := svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{
			"sounds_enabled": value,
		})
		if err == nil {
			t.Fatalf("value %q should be rejected", value)
		}
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != CodeInvalidArgument {
			t.Fatalf("value %q: want invalid_argument, got %v", value, err)
		}
	}
	if len(repo.rows) != 0 {
		t.Fatalf("rejected patches must not touch the store: %v", repo.rows)
	}
}

func TestUpdateUserSettingsAcceptsNewBooleanKeys(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{
		"notifications_private":     "false",
		"notifications_groups":      "false",
		"notifications_channels":    "false",
		"notifications_reactions":   "false",
		"notification_preview_name": "false",
		"notification_preview_text": "false",
		"events_contact_joined":     "false",
		"events_pinned":             "false",
		"calls_accept":              "false",
		"badge_include_muted":       "true",
		"badge_folders_count":       "true",
		"badge_count_messages":      "true",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	for key, want := range map[string]string{
		"notifications_private":     "false",
		"notifications_groups":      "false",
		"notifications_channels":    "false",
		"notifications_reactions":   "false",
		"notification_preview_name": "false",
		"notification_preview_text": "false",
		"events_contact_joined":     "false",
		"events_pinned":             "false",
		"calls_accept":              "false",
		"badge_include_muted":       "true",
		"badge_folders_count":       "true",
		"badge_count_messages":      "true",
	} {
		if got[key] != want {
			t.Fatalf("got[%q] = %q, want %q", key, got[key], want)
		}
	}
}

func TestUpdateUserSettingsAcceptsDeleteAccountTTL(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, value := range []string{"1_month", "3_months", "6_months", "12_months"} {
		got, err := svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{
			"delete_account_ttl": value,
		})
		if err != nil {
			t.Fatalf("value %q should be accepted: %v", value, err)
		}
		if got["delete_account_ttl"] != value {
			t.Fatalf("got[delete_account_ttl] = %q, want %q", got["delete_account_ttl"], value)
		}
	}
}

func TestUpdateUserSettingsRejectsBadDeleteAccountTTL(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, value := range []string{"", "true", "false", "month", "3", "6", "18_months", "never", "TRUE"} {
		_, err := svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{
			"delete_account_ttl": value,
		})
		if err == nil {
			t.Fatalf("value %q should be rejected", value)
		}
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != CodeInvalidArgument {
			t.Fatalf("value %q: want invalid_argument, got %v", value, err)
		}
		if !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("value %q: error should unwrap to ErrInvalidValue", value)
		}
	}
	if len(repo.rows) != 0 {
		t.Fatalf("rejected patches must not touch the store: %v", repo.rows)
	}
}

func TestGetUserSettingsFallsBackOnBadStoredTTL(t *testing.T) {
	repo := newMemUserSettingsRepo()
	repo.rows = map[string]string{"delete_account_ttl": "someday"}
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := svc.GetUserSettings(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["delete_account_ttl"] != "6_months" {
		t.Fatalf("an invalid stored TTL should fall back to the default, got %q", got["delete_account_ttl"])
	}
}

func TestIsBooleanKey(t *testing.T) {
	if !IsBooleanKey("sounds_enabled") || !IsBooleanKey("badge_count_messages") {
		t.Fatalf("boolean keys must report true")
	}
	if IsBooleanKey("delete_account_ttl") {
		t.Fatalf("delete_account_ttl is an enum key, not boolean")
	}
	if IsBooleanKey("totally_made_up") {
		t.Fatalf("unknown keys must report false")
	}
}

func TestUpdateUserSettingsToleratesEmptyPatch(t *testing.T) {
	repo := newMemUserSettingsRepo()
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := svc.UpdateUserSettings(context.Background(), "user-1", map[string]string{})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(got) != len(Keys()) {
		t.Fatalf("got %d keys, want %d", len(got), len(Keys()))
	}
	if repo.lastPatch != nil {
		t.Fatalf("an empty patch must not write anything: %v", repo.lastPatch)
	}
}

func TestUserSettingsRejectsEmptyUserID(t *testing.T) {
	svc, err := New(newMemUserSettingsRepo())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := svc.GetUserSettings(context.Background(), "   "); err == nil {
		t.Fatalf("expected an error for an empty user id")
	}
	if _, err := svc.UpdateUserSettings(context.Background(), "", map[string]string{}); err == nil {
		t.Fatalf("expected an error for an empty user id")
	}
}

func TestUserSettingsRepositoryErrorsAreInternal(t *testing.T) {
	repo := newMemUserSettingsRepo()
	repo.failWith = errors.New("boom")
	svc, err := New(repo)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, call := range []func() error{
		func() error { _, err := svc.GetUserSettings(context.Background(), "u"); return err },
		func() error {
			_, err := svc.UpdateUserSettings(context.Background(), "u", map[string]string{"data_saver": "true"})
			return err
		},
	} {
		err := call()
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != CodeInternal {
			t.Fatalf("want internal error, got %v", err)
		}
	}
}

func TestNewRequiresRepository(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatalf("expected an error when the repository is missing")
	}
}
