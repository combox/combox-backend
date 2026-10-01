package auth

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// Vectors generated with python3 hashlib.scrypt (cross-implementation check
// against x/crypto/scrypt). Salts are raw ASCII like real werkzeug gen_salt
// output (NOT hex) — see prod hashes, e.g. scrypt:32768:8:1$HKyVS63GwMcRAbg$…
//
//	python3 -c "import hashlib; ..."
const (
	werkzeugVectorHash     = "scrypt:1024:8:1$Ab3dEf7hIjKlMnOp$27a57e2c0ed5dde019fe59cf7d8bbc285a0a3d92e1b1d06dbec7c5d8fb9c1241"
	werkzeugVectorPassword = "migr-Test-pass-42"

	werkzeugVector64Hash     = "scrypt:1024:8:1$Qr9sTuVwXyZ01234$0a4faf5f583ecfda34791f40350bf6cd0f32f1efb865e1e988dbcea726bacb21"
	werkzeugVector64Password = "another!Pass99"
)

func TestVerifyWerkzeugScryptVectors(t *testing.T) {
	if err := VerifyWerkzeugScrypt(werkzeugVectorHash, werkzeugVectorPassword); err != nil {
		t.Fatalf("valid vector rejected: %v", err)
	}
	if err := VerifyWerkzeugScrypt(werkzeugVector64Hash, werkzeugVector64Password); err != nil {
		t.Fatalf("valid 64-byte vector rejected: %v", err)
	}
}

func TestVerifyWerkzeugScryptWrongPassword(t *testing.T) {
	if err := VerifyWerkzeugScrypt(werkzeugVectorHash, "wrong-password"); err == nil {
		t.Fatalf("wrong password accepted")
	}
	if err := VerifyWerkzeugScrypt(werkzeugVectorHash, ""); err == nil {
		t.Fatalf("empty password accepted")
	}
}

func TestVerifyWerkzeugScryptRejectsNonWerkzeug(t *testing.T) {
	// bcrypt hashes (regular combox users) must never verify here.
	bcryptLike := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	if err := VerifyWerkzeugScrypt(bcryptLike, "whatever"); err == nil {
		t.Fatalf("bcrypt hash accepted as werkzeug")
	}
	for _, malformed := range []string{
		"",
		"scrypt",
		"scrypt:32768:8:1",
		"scrypt:32768:8:1$onlysalt",
		"scrypt:0:8:1$aa$bb",
		"scrypt:1000:8:1$aa$bb",    // N not a power of two
		"scrypt:32768:0:1$aa$bb",   // r zero
		"scrypt:32768:8:x$aa$bb",   // p not a number
		"scrypt:32768:8:1$zzzz$bb", // salt not hex
		"scrypt:32768:8:1$aa$zzzz", // hash not hex
		"scrypt:32768:8:1$$bb",     // empty salt
		"scrypt:32768:8:1$aa$",     // empty hash
		"pbkdf2:sha256:1000$aa$bb", // another scheme entirely
		" scrypt:32768:8:1$aa$bb ", // well-formed shape, unknown values
	} {
		if err := VerifyWerkzeugScrypt(malformed, "whatever"); err == nil {
			t.Fatalf("malformed hash accepted: %q", malformed)
		}
	}
}

func newLegacyTestService(users *memUserRepo, sessions *memSessionRepo) *Service {
	svc, err := New(Config{
		Users:         users,
		Sessions:      sessions,
		AccessSecret:  "access-secret",
		RefreshSecret: "refresh-secret",
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    24 * time.Hour,
	})
	if err != nil {
		panic(err)
	}
	return svc
}

func seedLegacyUser(users *memUserRepo) User {
	legacy := "boxchat_oldname"
	user := User{
		ID:                 "legacy-1",
		Email:              "migrated_placeholder@invalid.local",
		Username:           "boxchatuser",
		PasswordHash:       werkzeugVectorHash,
		IsLegacyUnverified: true,
		LegacyUsername:     &legacy,
		FirstName:          "Box",
	}
	if users.usersByID == nil {
		users.usersByID = map[string]User{}
	}
	if users.usersByLogin == nil {
		users.usersByLogin = map[string]User{}
	}
	users.usersByID[user.ID] = user
	users.usersByLogin[user.Email] = user
	users.usersByLogin[user.Username] = user
	return user
}

func accessTokenHasMigr(t *testing.T, token string) bool {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a compact token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return strings.Contains(string(raw), `"migr":true`)
}

func TestLoginLegacyIssuesMigrToken(t *testing.T) {
	users := &memUserRepo{}
	sessions := &memSessionRepo{sessions: map[string]Session{}}
	svc := newLegacyTestService(users, sessions)
	seedLegacyUser(users)

	user, tokens, err := svc.Login(context.Background(), LoginInput{
		Login:    "boxchatuser",
		Password: werkzeugVectorPassword,
	})
	if err != nil {
		t.Fatalf("legacy login: %v", err)
	}
	if !user.IsLegacyUnverified {
		t.Fatalf("expected legacy flag on user")
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("expected tokens")
	}
	if !accessTokenHasMigr(t, tokens.AccessToken) {
		t.Fatalf("legacy access token lacks migr claim")
	}
}

func TestLoginLegacyWrongPasswordIsPlain401(t *testing.T) {
	users := &memUserRepo{}
	sessions := &memSessionRepo{sessions: map[string]Session{}}
	svc := newLegacyTestService(users, sessions)
	seedLegacyUser(users)

	_, _, err := svc.Login(context.Background(), LoginInput{
		Login:    "boxchatuser",
		Password: "wrong-password",
	})
	if err == nil {
		t.Fatalf("expected login error")
	}
	authErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected auth error type, got %T", err)
	}
	// No hint that the account is legacy: same code as a regular bad password.
	if authErr.Code != CodeInvalidCredential {
		t.Fatalf("unexpected error code: %s", authErr.Code)
	}
	if authErr.MessageKey != "error.auth.invalid_credentials" {
		t.Fatalf("unexpected message key: %s", authErr.MessageKey)
	}
}

func TestRefreshKeepsMigrForLegacy(t *testing.T) {
	users := &memUserRepo{}
	sessions := &memSessionRepo{sessions: map[string]Session{}}
	svc := newLegacyTestService(users, sessions)
	seedLegacyUser(users)

	_, tokens, err := svc.Login(context.Background(), LoginInput{
		Login:    "boxchatuser",
		Password: werkzeugVectorPassword,
	})
	if err != nil {
		t.Fatalf("legacy login: %v", err)
	}
	refreshed, err := svc.Refresh(context.Background(), RefreshInput{RefreshToken: tokens.RefreshToken})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !accessTokenHasMigr(t, refreshed.AccessToken) {
		t.Fatalf("refreshed access token lost migr claim")
	}
}

func TestCompleteLegacyBind(t *testing.T) {
	users := &memUserRepo{}
	sessions := &memSessionRepo{sessions: map[string]Session{}}
	svc := newLegacyTestService(users, sessions)
	seedLegacyUser(users)

	legacy, err := svc.IsLegacyUnverified(context.Background(), "boxchatuser")
	if err != nil || !legacy {
		t.Fatalf("expected legacy=true, got %v, %v", legacy, err)
	}
	if legacy, _ := svc.IsLegacyUnverified(context.Background(), "nobody"); legacy {
		t.Fatalf("unknown login must not read as legacy")
	}

	user, tokens, err := svc.CompleteLegacyBind(context.Background(), "legacy-1", "Real@Example.com", "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if user.Email != "real@example.com" {
		t.Fatalf("email not lowercased/stored: %q", user.Email)
	}
	if user.IsLegacyUnverified {
		t.Fatalf("legacy flag not cleared")
	}
	if user.LegacyUsername == nil || *user.LegacyUsername != "boxchat_oldname" {
		t.Fatalf("legacy_username must be kept for audit, got %+v", user.LegacyUsername)
	}
	if accessTokenHasMigr(t, tokens.AccessToken) {
		t.Fatalf("post-bind token must be full (no migr claim)")
	}

	// Second bind on the same (now regular) user is rejected.
	if _, _, err := svc.CompleteLegacyBind(context.Background(), "legacy-1", "other@example.com", "", ""); err == nil {
		t.Fatalf("expected error on re-bind")
	}
}
