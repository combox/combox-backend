package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	CodeInvalidArgument   = "invalid_argument"
	CodeInvalidCredential = "invalid_credentials"
	CodeUnauthorized      = "unauthorized"
	CodeConflict          = "conflict"
	CodeInternal          = "internal"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_]{4,32}$`)

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
	ID                    string
	Email                 string
	Username              string
	PasswordHash          string
	FirstName             string
	LastName              *string
	BirthDate             *string
	AvatarDataURL         *string
	AvatarGradient        *string
	SessionIdleTTLSeconds *int64
}

type Session struct {
	ID               string
	UserID           string
	RefreshTokenHash string
	ExpiresAt        time.Time
}

type CreateUserInput struct {
	Email          string
	Username       string
	PasswordHash   string
	FirstName      string
	LastName       *string
	BirthDate      *string
	AvatarDataURL  *string
	AvatarGradient *string
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
	FindByID(ctx context.Context, userID string) (User, error)
	FindByLogin(ctx context.Context, login string) (User, error)
	UpdateSessionIdleTTL(ctx context.Context, userID string, sessionIdleTTLSeconds *int64) error
	UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error
	UpdateProfile(ctx context.Context, input UpdateProfileInput) (User, error)
	UpdateEmail(ctx context.Context, userID, email string) (User, error)
}

type SessionRepository interface {
	Create(ctx context.Context, input CreateSessionInput) (Session, error)
	FindByID(ctx context.Context, sessionID string) (Session, error)
	UpdateRefresh(ctx context.Context, sessionID, refreshTokenHash string, expiresAt time.Time) error
	DeleteByID(ctx context.Context, sessionID string) error
}

type AvatarStore interface {
	PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, size int64) error
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
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
	Email          string
	Username       string
	Password       string
	FirstName      string
	LastName       *string
	BirthDate      *string
	AvatarDataURL  *string
	AvatarGradient *string
	UserAgent      string
	IPAddress      string
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

type OptionalString struct {
	Set   bool
	Value *string
}

type UpdateProfileInput struct {
	UserID         string
	Username       OptionalString
	FirstName      OptionalString
	LastName       OptionalString
	BirthDate      OptionalString
	AvatarDataURL  OptionalString
	AvatarGradient OptionalString
}

type Service struct {
	users         UserRepository
	sessions      SessionRepository
	avatars       AvatarStore
	accessSecret  string
	refreshSecret string
	accessTTL     time.Duration
	refreshTTL    time.Duration
	avatarURLTTL  time.Duration
	nowFn         func() time.Time
}

type Config struct {
	Users         UserRepository
	Sessions      SessionRepository
	Avatars       AvatarStore
	AccessSecret  string
	RefreshSecret string
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	AvatarURLTTL  time.Duration
}

const (
	defaultAvatarURLTTL = 24 * time.Hour * 7
	avatarRefPrefix     = "s3key:"
)

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
	if cfg.AvatarURLTTL <= 0 {
		cfg.AvatarURLTTL = defaultAvatarURLTTL
	}

	return &Service{
		users:         cfg.Users,
		sessions:      cfg.Sessions,
		avatars:       cfg.Avatars,
		accessSecret:  cfg.AccessSecret,
		refreshSecret: cfg.RefreshSecret,
		accessTTL:     cfg.AccessTTL,
		refreshTTL:    cfg.RefreshTTL,
		avatarURLTTL:  cfg.AvatarURLTTL,
		nowFn:         time.Now,
	}, nil
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (User, Tokens, error) {
	email := strings.TrimSpace(strings.ToLower(input.Email))
	username := strings.TrimSpace(strings.ToLower(input.Username))
	password := strings.TrimSpace(input.Password)
	firstName := strings.TrimSpace(input.FirstName)
	var lastName *string
	if input.LastName != nil {
		v := strings.TrimSpace(*input.LastName)
		if v != "" {
			lastName = &v
		}
	}
	var birthDate *string
	if input.BirthDate != nil {
		v := strings.TrimSpace(*input.BirthDate)
		if v != "" {
			birthDate = &v
		}
	}
	var avatarDataURL *string
	if input.AvatarDataURL != nil {
		v := strings.TrimSpace(*input.AvatarDataURL)
		if v != "" {
			avatarDataURL = &v
		}
	}
	var avatarGradient *string
	if input.AvatarGradient != nil {
		v := strings.TrimSpace(*input.AvatarGradient)
		if v != "" {
			avatarGradient = &v
		}
	}

	if email == "" || username == "" || password == "" || firstName == "" {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	if !usernameRe.MatchString(username) {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	if avatarDataURL != nil && s.avatars != nil {
		objectKey, err := s.uploadAvatarDataURL(ctx, *avatarDataURL)
		if err != nil {
			return User{}, Tokens{}, &Error{
				Code:       CodeInternal,
				MessageKey: "error.internal",
				Cause:      err,
			}
		}
		ref := avatarRefPrefix + objectKey
		avatarDataURL = &ref
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
		Email:          email,
		Username:       username,
		PasswordHash:   string(passwordHashBytes),
		FirstName:      firstName,
		LastName:       lastName,
		BirthDate:      birthDate,
		AvatarDataURL:  avatarDataURL,
		AvatarGradient: avatarGradient,
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
	s.resolveAvatarURL(ctx, &user)

	idleTTL := s.refreshTTL
	if user.SessionIdleTTLSeconds != nil {
		idleTTL = time.Duration(*user.SessionIdleTTLSeconds) * time.Second
	}
	tokens, err := s.issueSessionTokens(ctx, user.ID, input.UserAgent, input.IPAddress, idleTTL)
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
	s.resolveAvatarURL(ctx, &user)

	idleTTL := s.refreshTTL
	if user.SessionIdleTTLSeconds != nil {
		idleTTL = time.Duration(*user.SessionIdleTTLSeconds) * time.Second
	}
	tokens, err := s.issueSessionTokens(ctx, user.ID, input.UserAgent, input.IPAddress, idleTTL)
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

	user, err := s.users.FindByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
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

	idleTTL := s.refreshTTL
	if user.SessionIdleTTLSeconds != nil {
		idleTTL = time.Duration(*user.SessionIdleTTLSeconds) * time.Second
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
	nextExpiresAt := s.nowFn().UTC().Add(idleTTL)
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

func (s *Service) EmailExists(ctx context.Context, email string) (bool, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return false, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}

	_, err := s.users.FindByLogin(ctx, email)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrUserNotFound) {
		return false, nil
	}
	return false, &Error{
		Code:       CodeInternal,
		MessageKey: "error.internal",
		Cause:      err,
	}
}

func (s *Service) GetProfile(ctx context.Context, userID string) (User, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return User{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		return User{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	s.resolveAvatarURL(ctx, &user)
	return user, nil
}

func (s *Service) UpdateProfile(ctx context.Context, input UpdateProfileInput) (User, error) {
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		return User{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	input.UserID = userID

	hasUpdates := input.Username.Set || input.FirstName.Set || input.LastName.Set || input.BirthDate.Set || input.AvatarDataURL.Set || input.AvatarGradient.Set
	if !hasUpdates {
		return User{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}

	if input.Username.Set {
		if input.Username.Value == nil {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		v := strings.TrimSpace(strings.ToLower(*input.Username.Value))
		if v == "" || !usernameRe.MatchString(v) {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		input.Username.Value = &v
	}

	if input.FirstName.Set {
		if input.FirstName.Value == nil {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		v := strings.TrimSpace(*input.FirstName.Value)
		if v == "" {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		input.FirstName.Value = &v
	}

	if input.LastName.Set && input.LastName.Value != nil {
		v := strings.TrimSpace(*input.LastName.Value)
		if v == "" {
			input.LastName.Value = nil
		} else {
			input.LastName.Value = &v
		}
	}

	if input.BirthDate.Set && input.BirthDate.Value != nil {
		v := strings.TrimSpace(*input.BirthDate.Value)
		if v == "" {
			input.BirthDate.Value = nil
		} else {
			input.BirthDate.Value = &v
		}
	}

	if input.AvatarGradient.Set && input.AvatarGradient.Value != nil {
		v := strings.TrimSpace(*input.AvatarGradient.Value)
		if v == "" {
			input.AvatarGradient.Value = nil
		} else {
			input.AvatarGradient.Value = &v
		}
	}

	if input.AvatarDataURL.Set && input.AvatarDataURL.Value != nil {
		v := strings.TrimSpace(*input.AvatarDataURL.Value)
		if v == "" {
			input.AvatarDataURL.Value = nil
		} else if s.avatars != nil {
			objectKey, err := s.uploadAvatarDataURL(ctx, v)
			if err != nil {
				return User{}, &Error{
					Code:       CodeInternal,
					MessageKey: "error.internal",
					Cause:      err,
				}
			}
			ref := avatarRefPrefix + objectKey
			input.AvatarDataURL.Value = &ref
		} else {
			input.AvatarDataURL.Value = &v
		}
	}

	user, err := s.users.UpdateProfile(ctx, input)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		if errors.Is(err, ErrUsernameTaken) {
			return User{}, &Error{
				Code:       CodeConflict,
				MessageKey: "error.auth.already_exists",
				Cause:      err,
			}
		}
		return User{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	s.resolveAvatarURL(ctx, &user)
	return user, nil
}

func (s *Service) UpdateEmail(ctx context.Context, userID, email string) (User, error) {
	userID = strings.TrimSpace(userID)
	email = strings.TrimSpace(strings.ToLower(email))
	if userID == "" || email == "" {
		return User{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return User{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}

	user, err := s.users.UpdateEmail(ctx, userID, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		if errors.Is(err, ErrEmailTaken) {
			return User{}, &Error{
				Code:       CodeConflict,
				MessageKey: "error.auth.already_exists",
				Cause:      err,
			}
		}
		return User{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	s.resolveAvatarURL(ctx, &user)
	return user, nil
}

func (s *Service) UpdateSessionIdleTTL(ctx context.Context, userID string, sessionIdleTTLSeconds *int64) (User, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return User{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	if sessionIdleTTLSeconds != nil && *sessionIdleTTLSeconds <= 0 {
		return User{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	if err := s.users.UpdateSessionIdleTTL(ctx, userID, sessionIdleTTLSeconds); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		return User{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		return User{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	s.resolveAvatarURL(ctx, &user)
	return user, nil
}

func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	userID = strings.TrimSpace(userID)
	currentPassword = strings.TrimSpace(currentPassword)
	newPassword = strings.TrimSpace(newPassword)
	if userID == "" || currentPassword == "" || newPassword == "" || len(newPassword) < 8 {
		return &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		return &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		return &Error{
			Code:       CodeInvalidCredential,
			MessageKey: "error.auth.invalid_credentials",
			Cause:      err,
		}
	}
	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	if err := s.users.UpdatePasswordHash(ctx, userID, string(passwordHashBytes)); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		return &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}
	return nil
}

func (s *Service) issueSessionTokens(ctx context.Context, userID, userAgent, ipAddress string, idleTTL time.Duration) (Tokens, error) {
	sessionID := uuid.NewString()

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
	expiresAt := s.nowFn().UTC().Add(idleTTL)
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

func (s *Service) resolveAvatarURL(ctx context.Context, user *User) {
	if s.avatars == nil || user == nil || user.AvatarDataURL == nil {
		return
	}
	ref := strings.TrimSpace(*user.AvatarDataURL)
	if !strings.HasPrefix(ref, avatarRefPrefix) {
		return
	}
	objectKey := strings.TrimPrefix(ref, avatarRefPrefix)
	if strings.TrimSpace(objectKey) == "" {
		return
	}
	presigned, err := s.avatars.PresignGetObject(ctx, objectKey, s.avatarURLTTL)
	if err != nil || strings.TrimSpace(presigned) == "" {
		return
	}
	user.AvatarDataURL = &presigned
}

func (s *Service) uploadAvatarDataURL(ctx context.Context, raw string) (string, error) {
	contentType, payload, err := decodeDataURL(raw)
	if err != nil {
		return "", err
	}
	ext := extensionByContentType(contentType)
	objectKey := fmt.Sprintf("avatars/%s%s", uuid.NewString(), ext)
	if err := s.avatars.PutObject(ctx, objectKey, contentType, bytes.NewReader(payload), int64(len(payload))); err != nil {
		return "", err
	}
	return objectKey, nil
}

func decodeDataURL(raw string) (string, []byte, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return "", nil, errors.New("avatar must be data url")
	}
	commaIdx := strings.Index(value, ",")
	if commaIdx <= 5 {
		return "", nil, errors.New("invalid data url")
	}
	meta := value[5:commaIdx]
	dataPart := value[commaIdx+1:]
	parts := strings.Split(meta, ";")
	contentType := strings.TrimSpace(parts[0])
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	isBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}
	if isBase64 {
		payload, err := base64.StdEncoding.DecodeString(dataPart)
		if err != nil {
			return "", nil, err
		}
		return contentType, payload, nil
	}
	decoded, err := url.QueryUnescape(dataPart)
	if err != nil {
		return "", nil, err
	}
	return contentType, []byte(decoded), nil
}

func extensionByContentType(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/svg+xml":
		return ".svg"
	default:
		return ".bin"
	}
}
