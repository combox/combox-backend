package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memUserRepo struct {
	usersByID    map[string]User
	usersByLogin map[string]User
}

func (m *memUserRepo) Create(_ context.Context, input CreateUserInput) (User, error) {
	if _, ok := m.usersByLogin[input.Email]; ok {
		return User{}, ErrEmailTaken
	}
	if _, ok := m.usersByLogin[input.Username]; ok {
		return User{}, ErrUsernameTaken
	}
	user := User{
		ID:           "user-1",
		Email:        input.Email,
		Username:     input.Username,
		PasswordHash: input.PasswordHash,
	}
	if m.usersByID == nil {
		m.usersByID = map[string]User{}
	}
	m.usersByID[user.ID] = user
	m.usersByLogin[input.Email] = user
	m.usersByLogin[input.Username] = user
	return user, nil
}

func (m *memUserRepo) FindByID(_ context.Context, userID string) (User, error) {
	if m.usersByID == nil {
		return User{}, ErrUserNotFound
	}
	user, ok := m.usersByID[userID]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (m *memUserRepo) FindByLogin(_ context.Context, login string) (User, error) {
	user, ok := m.usersByLogin[login]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (m *memUserRepo) UpdateSessionIdleTTL(_ context.Context, userID string, sessionIdleTTLSeconds *int64) error {
	if m.usersByID == nil {
		return ErrUserNotFound
	}
	user, ok := m.usersByID[userID]
	if !ok {
		return ErrUserNotFound
	}
	user.SessionIdleTTLSeconds = sessionIdleTTLSeconds
	m.usersByID[userID] = user
	m.usersByLogin[user.Email] = user
	m.usersByLogin[user.Username] = user
	return nil
}

type memSessionRepo struct {
	sessions map[string]Session
}

func (m *memSessionRepo) Create(_ context.Context, input CreateSessionInput) (Session, error) {
	session := Session{
		ID:               input.ID,
		UserID:           input.UserID,
		RefreshTokenHash: input.RefreshTokenHash,
		ExpiresAt:        input.ExpiresAt,
	}
	m.sessions[input.ID] = session
	return session, nil
}

func (m *memSessionRepo) FindByID(_ context.Context, sessionID string) (Session, error) {
	session, ok := m.sessions[sessionID]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return session, nil
}

func (m *memSessionRepo) UpdateRefresh(_ context.Context, sessionID, refreshTokenHash string, expiresAt time.Time) error {
	session, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	session.RefreshTokenHash = refreshTokenHash
	session.ExpiresAt = expiresAt
	m.sessions[sessionID] = session
	return nil
}

func (m *memSessionRepo) DeleteByID(_ context.Context, sessionID string) error {
	if _, ok := m.sessions[sessionID]; !ok {
		return ErrSessionNotFound
	}
	delete(m.sessions, sessionID)
	return nil
}

func TestRegisterLoginRefreshLogoutFlow(t *testing.T) {
	users := &memUserRepo{usersByLogin: map[string]User{}, usersByID: map[string]User{}}
	sessions := &memSessionRepo{sessions: map[string]Session{}}

	svc, err := New(Config{
		Users:         users,
		Sessions:      sessions,
		AccessSecret:  "access-secret",
		RefreshSecret: "refresh-secret",
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx := context.Background()
	user, registerTokens, err := svc.Register(ctx, RegisterInput{
		Email:    "user@example.com",
		Username: "user",
		Password: "StrongPassword123!",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if user.ID == "" || registerTokens.AccessToken == "" || registerTokens.RefreshToken == "" {
		t.Fatalf("expected user and tokens to be generated")
	}

	_, loginTokens, err := svc.Login(ctx, LoginInput{
		Login:    "user@example.com",
		Password: "StrongPassword123!",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if loginTokens.RefreshToken == registerTokens.RefreshToken {
		t.Fatalf("expected new session token on login")
	}

	refreshed, err := svc.Refresh(ctx, RefreshInput{RefreshToken: loginTokens.RefreshToken})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.RefreshToken == loginTokens.RefreshToken {
		t.Fatalf("expected rotated refresh token")
	}

	if err := svc.Logout(ctx, LogoutInput{RefreshToken: refreshed.RefreshToken}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	_, err = svc.Refresh(ctx, RefreshInput{RefreshToken: refreshed.RefreshToken})
	if err == nil {
		t.Fatalf("expected refresh error after logout")
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	users := &memUserRepo{usersByLogin: map[string]User{}, usersByID: map[string]User{}}
	sessions := &memSessionRepo{sessions: map[string]Session{}}
	svc, err := New(Config{
		Users:         users,
		Sessions:      sessions,
		AccessSecret:  "access-secret",
		RefreshSecret: "refresh-secret",
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	_, _, err = svc.Login(context.Background(), LoginInput{
		Login:    "missing",
		Password: "pwd",
	})
	if err == nil {
		t.Fatalf("expected login error")
	}

	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("expected auth error type")
	}
	if authErr.Code != CodeInvalidCredential {
		t.Fatalf("unexpected error code: %s", authErr.Code)
	}
}

func TestRefreshExtendsSessionUsingUserIdleTTL(t *testing.T) {
	users := &memUserRepo{usersByLogin: map[string]User{}, usersByID: map[string]User{}}
	sessions := &memSessionRepo{sessions: map[string]Session{}}

	svc, err := New(Config{
		Users:         users,
		Sessions:      sessions,
		AccessSecret:  "access-secret",
		RefreshSecret: "refresh-secret",
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return base }

	ctx := context.Background()
	_, _, err = svc.Register(ctx, RegisterInput{
		Email:    "user@example.com",
		Username: "user",
		Password: "StrongPassword123!",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	thirtyDays := int64((30 * 24 * time.Hour) / time.Second)
	if err := users.UpdateSessionIdleTTL(ctx, "user-1", &thirtyDays); err != nil {
		t.Fatalf("update session idle ttl: %v", err)
	}
	_, tokens, err := svc.Login(ctx, LoginInput{
		Login:    "user@example.com",
		Password: "StrongPassword123!",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Simulate activity later; refresh should extend expires_at by the user-defined TTL.
	base2 := base.Add(10 * 24 * time.Hour)
	svc.nowFn = func() time.Time { return base2 }

	refreshed, err := svc.Refresh(ctx, RefreshInput{RefreshToken: tokens.RefreshToken})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	sessionID, _, err := parseRefreshToken(refreshed.RefreshToken)
	if err != nil {
		t.Fatalf("parse refresh token: %v", err)
	}
	session := sessions.sessions[sessionID]

	expected := base2.Add(30 * 24 * time.Hour)
	if !session.ExpiresAt.Equal(expected) {
		t.Fatalf("expected expires_at to be extended: got=%s expected=%s", session.ExpiresAt, expected)
	}
}
