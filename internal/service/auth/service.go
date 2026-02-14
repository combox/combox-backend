package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	CodeInvalidArgument   = "invalid_argument"
	CodeInvalidCredential = "invalid_credentials"
	CodeUnauthorized      = "unauthorized"
	CodeConflict          = "conflict"
	CodeInternal          = "internal"
)

type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

type User struct {
	ID           string
	Email        string
	Username     string
	PasswordHash string
}

type Session struct {
	ID               string
	UserID           string
	RefreshTokenHash string
	ExpiresAt        time.Time
}

type CreateUserInput struct {
	Email        string
	Username     string
	PasswordHash string
}

type CreateSessionInput struct {
	ID               string
	UserID           string
	RefreshTokenHash string
	UserAgent        string
	IPAddress        string
	ExpiresAt        time.Time
}

type UserRepository interface {
	Create(ctx context.Context, input CreateUserInput) (User, error)
	FindByLogin(ctx context.Context, login string) (User, error)
}

type SessionRepository interface {
	Create(ctx context.Context, input CreateSessionInput) (Session, error)
	FindByID(ctx context.Context, sessionID string) (Session, error)
	UpdateRefresh(ctx context.Context, sessionID, refreshTokenHash string, expiresAt time.Time) error
	DeleteByID(ctx context.Context, sessionID string) error
}

var (
	ErrUserNotFound    = errors.New("user not found")
	ErrSessionNotFound = errors.New("session not found")
	ErrEmailTaken      = errors.New("email taken")
	ErrUsernameTaken   = errors.New("username taken")
)

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresInSec int64  `json:"expires_in_sec"`
}

type RegisterInput struct {
	Email     string
	Username  string
	Password  string
	UserAgent string
	IPAddress string
}

type LoginInput struct {
	Login     string
	Password  string
	UserAgent string
	IPAddress string
}

type RefreshInput struct {
	RefreshToken string
	UserAgent    string
	IPAddress    string
}

type LogoutInput struct {
	RefreshToken string
}

type Service struct {
	users         UserRepository
	sessions      SessionRepository
	accessSecret  string
	refreshSecret string
	accessTTL     time.Duration
	refreshTTL    time.Duration
	nowFn         func() time.Time
}

type Config struct {
	Users         UserRepository
	Sessions      SessionRepository
	AccessSecret  string
	RefreshSecret string
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
}

func New(cfg Config) (*Service, error) {
	if cfg.Users == nil {
		return nil, errors.New("users repository is required")
	}
	if cfg.Sessions == nil {
		return nil, errors.New("sessions repository is required")
	}
	if strings.TrimSpace(cfg.AccessSecret) == "" {
		return nil, errors.New("access secret is required")
	}
	if strings.TrimSpace(cfg.RefreshSecret) == "" {
		return nil, errors.New("refresh secret is required")
	}
	if cfg.AccessTTL <= 0 {
		return nil, errors.New("access ttl must be positive")
	}
	if cfg.RefreshTTL <= 0 {
		return nil, errors.New("refresh ttl must be positive")
	}

	return &Service{
		users:         cfg.Users,
		sessions:      cfg.Sessions,
		accessSecret:  cfg.AccessSecret,
		refreshSecret: cfg.RefreshSecret,
		accessTTL:     cfg.AccessTTL,
		refreshTTL:    cfg.RefreshTTL,
		nowFn:         time.Now,
	}, nil
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (User, Tokens, error) {
	email := strings.TrimSpace(strings.ToLower(input.Email))
	username := strings.TrimSpace(input.Username)
	password := strings.TrimSpace(input.Password)

	if email == "" || username == "" || password == "" {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}

	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	user, err := s.users.Create(ctx, CreateUserInput{
		Email:        email,
		Username:     username,
		PasswordHash: string(passwordHashBytes),
	})
	if err != nil {
		if errors.Is(err, ErrEmailTaken) || errors.Is(err, ErrUsernameTaken) {
			return User{}, Tokens{}, &Error{
				Code:       CodeConflict,
				MessageKey: "error.auth.already_exists",
				Cause:      err,
			}
		}
		return User{}, Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	tokens, err := s.issueSessionTokens(ctx, user.ID, input.UserAgent, input.IPAddress)
	if err != nil {
		return User{}, Tokens{}, err
	}

	return user, tokens, nil
}

func (s *Service) Login(ctx context.Context, input LoginInput) (User, Tokens, error) {
	login := strings.TrimSpace(input.Login)
	if login == "" || strings.TrimSpace(input.Password) == "" {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}

	user, err := s.users.FindByLogin(ctx, login)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, Tokens{}, &Error{
				Code:       CodeInvalidCredential,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		return User{}, Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidCredential,
			MessageKey: "error.auth.invalid_credentials",
			Cause:      err,
		}
	}

	tokens, err := s.issueSessionTokens(ctx, user.ID, input.UserAgent, input.IPAddress)
	if err != nil {
		return User{}, Tokens{}, err
	}

	return user, tokens, nil
}

func (s *Service) Refresh(ctx context.Context, input RefreshInput) (Tokens, error) {
	sessionID, refreshSecretPart, err := parseRefreshToken(input.RefreshToken)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeUnauthorized,
			MessageKey: "error.auth.invalid_refresh_token",
			Cause:      err,
		}
	}

	session, err := s.sessions.FindByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return Tokens{}, &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_refresh_token",
				Cause:      err,
			}
		}
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	if session.ExpiresAt.Before(s.nowFn().UTC()) {
		return Tokens{}, &Error{
			Code:       CodeUnauthorized,
			MessageKey: "error.auth.refresh_expired",
		}
	}

	expectedHash := hashRefreshValue(refreshSecretPart, s.refreshSecret)
	if !hmac.Equal([]byte(expectedHash), []byte(session.RefreshTokenHash)) {
		return Tokens{}, &Error{
			Code:       CodeUnauthorized,
			MessageKey: "error.auth.invalid_refresh_token",
		}
	}

	nextRefreshPart, err := newRandomToken(32)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	accessToken, expiresInSec, err := s.newAccessToken(session.UserID)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	nextRefreshToken := session.ID + "." + nextRefreshPart
	nextExpiresAt := s.nowFn().UTC().Add(s.refreshTTL)
	if err := s.sessions.UpdateRefresh(ctx, session.ID, hashRefreshValue(nextRefreshPart, s.refreshSecret), nextExpiresAt); err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	return Tokens{
		AccessToken:  accessToken,
		RefreshToken: nextRefreshToken,
		ExpiresInSec: expiresInSec,
	}, nil
}

func (s *Service) Logout(ctx context.Context, input LogoutInput) error {
	sessionID, _, err := parseRefreshToken(input.RefreshToken)
	if err != nil {
		return &Error{
			Code:       CodeUnauthorized,
			MessageKey: "error.auth.invalid_refresh_token",
			Cause:      err,
		}
	}

	if err := s.sessions.DeleteByID(ctx, sessionID); err != nil && !errors.Is(err, ErrSessionNotFound) {
		return &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	return nil
}

func (s *Service) issueSessionTokens(ctx context.Context, userID, userAgent, ipAddress string) (Tokens, error) {
	sessionID, err := newRandomToken(16)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	refreshPart, err := newRandomToken(32)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	accessToken, expiresInSec, err := s.newAccessToken(userID)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	refreshToken := sessionID + "." + refreshPart
	expiresAt := s.nowFn().UTC().Add(s.refreshTTL)
	_, err = s.sessions.Create(ctx, CreateSessionInput{
		ID:               sessionID,
		UserID:           userID,
		RefreshTokenHash: hashRefreshValue(refreshPart, s.refreshSecret),
		UserAgent:        userAgent,
		IPAddress:        ipAddress,
		ExpiresAt:        expiresAt,
	})
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	return Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresInSec: expiresInSec,
	}, nil
}

func (s *Service) newAccessToken(userID string) (string, int64, error) {
	headerJSON := `{"alg":"HS256","typ":"JWT"}`
	exp := s.nowFn().UTC().Add(s.accessTTL).Unix()
	payloadBytes, err := json.Marshal(map[string]any{
		"sub": userID,
		"exp": exp,
	})
	if err != nil {
		return "", 0, err
	}

	header := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	unsigned := header + "." + payload
	h := hmac.New(sha256.New, []byte(s.accessSecret))
	_, _ = h.Write([]byte(unsigned))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return unsigned + "." + signature, int64(s.accessTTL / time.Second), nil
}

func hashRefreshValue(tokenPart, secret string) string {
	sum := sha256.Sum256([]byte(secret + ":" + tokenPart))
	return hex.EncodeToString(sum[:])
}

func parseRefreshToken(refreshToken string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(refreshToken), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("invalid refresh token format")
	}
	return parts[0], parts[1], nil
}

func newRandomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
