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

	"combox-backend/internal/avatarcrop"
	profilephotosvc "combox-backend/internal/service/profilephoto"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	CodeInvalidArgument   = "invalid_argument"
	CodeInvalidCredential = "invalid_credentials"
	CodeUnauthorized      = "unauthorized"
	CodeConflict          = "conflict"
	// CodeNotFound reports a missing resource that is correctly formed (e.g.
	// revoking somebody else's session); it maps to HTTP 404.
	CodeNotFound = "not_found"
	CodeInternal = "internal"
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

type SavedTrack struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Duration int    `json:"duration"`
	FileSize string `json:"fileSize"`
	FileURL  string `json:"fileUrl"`
	AddedAt  string `json:"addedAt"`
	// AttachmentID is the owned copy id from POST /media/attachments/{id}/pin.
	// The playlist client sends it on every save (including removals of other
	// tracks), so it must be a known field: decodeJSON uses
	// DisallowUnknownFields and an unknown field turns the whole PATCH
	// /profile into 400, which broke track removal ("could not update the
	// playlist"). It is persisted in saved_tracks JSONB like the rest.
	AttachmentID string `json:"attachmentId,omitempty"`
}

type User struct {
	ID           string
	Email        string
	Username     string
	PasswordHash string
	// IsLegacyUnverified is the SINGLE criterion marking a migrated boxchat
	// user (migration 000044_legacy_auth, set by the ETL). TRUE means the
	// password is a werkzeug scrypt hash and login issues a migr-limited
	// token until the email is bound; FALSE is a regular combox user.
	IsLegacyUnverified bool
	// LegacyUsername is the original boxchat username, kept for audit. The
	// bind flow never clears it.
	LegacyUsername        *string
	FirstName             string
	LastName              *string
	BirthDate             *string
	AvatarDataURL         *string
	AvatarGradient        *string
	Bio                   *string
	PhoneNumber           *string
	NameColor             *string
	PlaylistTitle         string
	PlaylistIsPublic      bool
	SavedTracks           []SavedTrack
	SessionIdleTTLSeconds *int64
}

type Session struct {
	ID               string
	UserID           string
	RefreshTokenHash string
	ExpiresAt        time.Time
	// UserAgent and IPAddress are what the "Active sessions" list shows;
	// they are recorded when the session is created and never change.
	UserAgent string
	IPAddress string
	CreatedAt time.Time
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
	// BindLegacyEmail sets the real email of a migrated user and clears
	// is_legacy_unverified while KEEPING legacy_username for audit.
	BindLegacyEmail(ctx context.Context, userID, email string) (User, error)
}

type SessionRepository interface {
	Create(ctx context.Context, input CreateSessionInput) (Session, error)
	FindByID(ctx context.Context, sessionID string) (Session, error)
	UpdateRefresh(ctx context.Context, sessionID, refreshTokenHash string, expiresAt time.Time) error
	UpdateExpiryByUser(ctx context.Context, userID string, expiresAt time.Time) error
	DeleteByID(ctx context.Context, sessionID string) error
	// ListByUserID returns every live session of a user, newest first.
	ListByUserID(ctx context.Context, userID string) ([]Session, error)
	// DeleteByUserIDAndID revokes one session of a user; it reports
	// ErrSessionNotFound when the row is missing or belongs to somebody else.
	DeleteByUserIDAndID(ctx context.Context, userID, sessionID string) error
	// DeleteOthersByUserID revokes every session of a user except keepSessionID
	// (which may be empty to revoke them all) and returns the number of rows.
	DeleteOthersByUserID(ctx context.Context, userID, keepSessionID string) (int64, error)
}

// ProfileUpdatedEvent mirrors the public directory (search) user shape plus
// the profile owner id, so clients can refresh cached names and avatars.
type ProfileUpdatedEvent struct {
	UserID          string
	RecipientUserID string
	Email           string
	Username        string
	FirstName       string
	LastName        *string
	BirthDate       *string
	AvatarDataURL   *string
	AvatarGradient  *string
}

type ProfileEventPublisher interface {
	PublishProfileUpdate(ctx context.Context, ev ProfileUpdatedEvent) error
}

// ProfileAudienceResolver resolves extra recipients for profile updates: the
// users that share at least one chat with the profile owner.
type ProfileAudienceResolver interface {
	ListSharedChatMemberIDs(ctx context.Context, userID string) ([]string, error)
}

// ProfilePhotoRecorder archives every avatar object that gets written for an
// owner, so the fullscreen gallery can replay the whole photo history.
type ProfilePhotoRecorder interface {
	Record(ctx context.Context, ownerKind, ownerID, objectKey string) error
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

type OptionalBool struct {
	Set   bool
	Value bool
}

type OptionalTracks struct {
	Set   bool
	Value []SavedTrack
}

type UpdateProfileInput struct {
	UserID           string
	Username         OptionalString
	FirstName        OptionalString
	LastName         OptionalString
	BirthDate        OptionalString
	AvatarDataURL    OptionalString
	AvatarGradient   OptionalString
	Bio              OptionalString
	PhoneNumber      OptionalString
	NameColor        OptionalString
	PlaylistTitle    OptionalString
	PlaylistIsPublic OptionalBool
	SavedTracks      OptionalTracks
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
	publisher     ProfileEventPublisher
	audience      ProfileAudienceResolver
	photoHistory  ProfilePhotoRecorder
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
	maxAvatarDataURLLen = 8 * 1024 * 1024
	maxNameLen          = 64
	maxBioLen           = 70
	maxPhoneLen         = 32
	maxNameColorLen     = 24
	maxPlaylistTitleLen = 64
	maxSavedTracks      = 500
	// SessionIdleTTLForeverSeconds is the stored sentinel for "Forever / no
	// auto-logout" in users.session_idle_ttl_seconds. NULL keeps the
	// historical meaning "use the server default refreshTTL" (existing rows,
	// never touched by the user); 0 means the user explicitly picked
	// "Forever" (the SDK sends JSON null for it, the handler maps it to 0).
	SessionIdleTTLForeverSeconds = int64(0)
	// maxSessionIdleTTLSeconds caps a custom TTL at 100 years so
	// time.Duration(seconds)*time.Second can never overflow int64 ns
	// (~292 years max). The UI only offers up to 30 days; anything larger
	// is either a mistake or an overflow attempt.
	maxSessionIdleTTLSeconds = int64(100 * 365 * 24 * 3600)
	// foreverSessionTTL is the effective session lifetime for "Forever".
	// sessions.expires_at is NOT NULL, so "no auto-logout" is a far-future
	// expiry rather than NULL.
	foreverSessionTTL = 100 * 365 * 24 * time.Hour
)

// resolveIdleTTL maps the nullable users.session_idle_ttl_seconds onto an
// effective session lifetime: nil -> server default, 0 -> forever
// (far-future), n>0 -> n seconds.
func resolveIdleTTL(userTTL *int64, def time.Duration) time.Duration {
	if userTTL == nil {
		return def
	}
	if *userTTL == SessionIdleTTLForeverSeconds {
		return foreverSessionTTL
	}
	return time.Duration(*userTTL) * time.Second
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

// SetProfileEventPublisher wires realtime profile.update publishing.
func (s *Service) SetProfileEventPublisher(publisher ProfileEventPublisher) {
	s.publisher = publisher
}

// SetProfileAudienceResolver wires the extra recipients of profile.update events.
func (s *Service) SetProfileAudienceResolver(resolver ProfileAudienceResolver) {
	s.audience = resolver
}

// SetProfilePhotoRecorder wires the avatar history archive.
func (s *Service) SetProfilePhotoRecorder(recorder ProfilePhotoRecorder) {
	s.photoHistory = recorder
}

// recordProfilePhoto archives a freshly uploaded avatar object. The avatar
// itself is already stored at this point, so a history failure is swallowed
// instead of failing the profile update that produced it. Clearing an avatar
// or leaving it untouched never produces an object key, hence no record.
func (s *Service) recordProfilePhoto(ctx context.Context, ownerID, objectKey string) {
	if s.photoHistory == nil {
		return
	}
	ownerID = strings.TrimSpace(ownerID)
	objectKey = strings.TrimSpace(objectKey)
	if ownerID == "" || objectKey == "" {
		return
	}
	_ = s.photoHistory.Record(ctx, profilephotosvc.OwnerUser, ownerID, objectKey)
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
	var uploadedAvatarKey string
	if avatarDataURL != nil && s.avatars != nil {
		objectKey, err := s.uploadAvatarDataURL(ctx, *avatarDataURL)
		if err != nil {
			return User{}, Tokens{}, &Error{
				Code:       CodeInternal,
				MessageKey: "error.internal",
				Cause:      err,
			}
		}
		uploadedAvatarKey = objectKey
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
	s.recordProfilePhoto(ctx, user.ID, uploadedAvatarKey)

	idleTTL := resolveIdleTTL(user.SessionIdleTTLSeconds, s.refreshTTL)
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

	// Legacy (migrated boxchat) users: werkzeug scrypt password, limited
	// migr=true session. A wrong password reads as the ordinary 401, with no
	// hint that the account is legacy. Regular users fall through to bcrypt.
	if user.IsLegacyUnverified {
		if err := VerifyWerkzeugScrypt(user.PasswordHash, input.Password); err != nil {
			return User{}, Tokens{}, &Error{
				Code:       CodeInvalidCredential,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		s.resolveAvatarURL(ctx, &user)

		idleTTL := resolveIdleTTL(user.SessionIdleTTLSeconds, s.refreshTTL)
		tokens, err := s.issueSessionTokensWithMigration(ctx, user.ID, input.UserAgent, input.IPAddress, idleTTL, true)
		if err != nil {
			return User{}, Tokens{}, err
		}

		return user, tokens, nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidCredential,
			MessageKey: "error.auth.invalid_credentials",
			Cause:      err,
		}
	}
	s.resolveAvatarURL(ctx, &user)

	idleTTL := resolveIdleTTL(user.SessionIdleTTLSeconds, s.refreshTTL)
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

	idleTTL := resolveIdleTTL(user.SessionIdleTTLSeconds, s.refreshTTL)

	nextRefreshPart, err := newRandomToken(32)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	accessToken, expiresInSec, err := s.newAccessTokenWithMigration(session.UserID, session.ID, user.IsLegacyUnverified)
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

// IsLegacyUnverified reports whether login identifies a migrated boxchat user
// (users.is_legacy_unverified, migration 000044). Unknown logins and lookup
// failures read as (false, nil|err) so the login handler falls back to the
// regular flow without leaking account existence.
func (s *Service) IsLegacyUnverified(ctx context.Context, login string) (bool, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return false, nil
	}
	user, err := s.users.FindByLogin(ctx, login)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return false, nil
		}
		return false, err
	}
	return user.IsLegacyUnverified, nil
}

// CompleteLegacyBind finishes the boxchat migration for a user whose OTP was
// already verified by the caller: it re-checks email uniqueness (lowercased),
// stores the real email, clears is_legacy_unverified while KEEPING
// legacy_username for audit, and issues a FULL session (same code as login).
func (s *Service) CompleteLegacyBind(ctx context.Context, userID, email, userAgent, ipAddress string) (User, Tokens, error) {
	userID = strings.TrimSpace(userID)
	email = strings.TrimSpace(strings.ToLower(email))
	if userID == "" || email == "" {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}

	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, Tokens{}, &Error{
				Code:       CodeUnauthorized,
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
	if !user.IsLegacyUnverified {
		return User{}, Tokens{}, &Error{
			Code:       CodeInvalidArgument,
			MessageKey: "error.auth.invalid_input",
		}
	}

	if existing, err := s.users.FindByLogin(ctx, email); err == nil {
		if strings.TrimSpace(existing.ID) != strings.TrimSpace(userID) {
			return User{}, Tokens{}, &Error{
				Code:       CodeConflict,
				MessageKey: "error.auth.already_exists",
				Cause:      ErrEmailTaken,
			}
		}
	} else if !errors.Is(err, ErrUserNotFound) {
		return User{}, Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	user, err = s.users.BindLegacyEmail(ctx, userID, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, Tokens{}, &Error{
				Code:       CodeUnauthorized,
				MessageKey: "error.auth.invalid_credentials",
				Cause:      err,
			}
		}
		if errors.Is(err, ErrEmailTaken) {
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

	idleTTL := resolveIdleTTL(user.SessionIdleTTLSeconds, s.refreshTTL)
	tokens, err := s.issueSessionTokens(ctx, user.ID, userAgent, ipAddress, idleTTL)
	if err != nil {
		return User{}, Tokens{}, err
	}

	return user, tokens, nil
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

	hasUpdates := input.Username.Set || input.FirstName.Set || input.LastName.Set || input.BirthDate.Set || input.AvatarDataURL.Set || input.AvatarGradient.Set || input.Bio.Set || input.PhoneNumber.Set || input.NameColor.Set || input.PlaylistTitle.Set || input.PlaylistIsPublic.Set || input.SavedTracks.Set
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
		if v == "" || len([]rune(v)) > maxNameLen {
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

	var uploadedAvatarKey string
	if input.AvatarDataURL.Set && input.AvatarDataURL.Value != nil {
		v := strings.TrimSpace(*input.AvatarDataURL.Value)
		if v == "" {
			input.AvatarDataURL.Value = nil
		} else if len(v) > maxAvatarDataURLLen {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		} else if s.avatars != nil {
			objectKey, err := s.uploadAvatarDataURL(ctx, v)
			if err != nil {
				return User{}, &Error{
					Code:       CodeInternal,
					MessageKey: "error.internal",
					Cause:      err,
				}
			}
			uploadedAvatarKey = objectKey
			ref := avatarRefPrefix + objectKey
			input.AvatarDataURL.Value = &ref
		} else {
			input.AvatarDataURL.Value = &v
		}
	}

	if input.Bio.Set {
		if input.Bio.Value == nil {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		v := strings.TrimSpace(*input.Bio.Value)
		if len([]rune(v)) > maxBioLen {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.profile.invalid_bio",
			}
		}
		input.Bio.Value = &v
	}

	if input.PhoneNumber.Set {
		if input.PhoneNumber.Value == nil {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		v := strings.TrimSpace(*input.PhoneNumber.Value)
		if len([]rune(v)) > maxPhoneLen {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.profile.invalid_phone",
			}
		}
		input.PhoneNumber.Value = &v
	}

	if input.NameColor.Set {
		if input.NameColor.Value == nil {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		v := strings.TrimSpace(*input.NameColor.Value)
		if len([]rune(v)) > maxNameColorLen {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.profile.invalid_name_color",
			}
		}
		input.NameColor.Value = &v
	}

	if input.PlaylistTitle.Set {
		if input.PlaylistTitle.Value == nil {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		v := strings.TrimSpace(*input.PlaylistTitle.Value)
		if len([]rune(v)) > maxPlaylistTitleLen {
			return User{}, &Error{
				Code:       CodeInvalidArgument,
				MessageKey: "error.auth.invalid_input",
			}
		}
		input.PlaylistTitle.Value = &v
	}

	if input.SavedTracks.Set {
		if len(input.SavedTracks.Value) > maxSavedTracks {
			input.SavedTracks.Value = input.SavedTracks.Value[:maxSavedTracks]
		}
		clean := make([]SavedTrack, 0, len(input.SavedTracks.Value))
		for _, tr := range input.SavedTracks.Value {
			tr.ID = strings.TrimSpace(tr.ID)
			tr.Title = strings.TrimSpace(tr.Title)
			tr.Artist = strings.TrimSpace(tr.Artist)
			tr.FileSize = strings.TrimSpace(tr.FileSize)
			tr.FileURL = strings.TrimSpace(tr.FileURL)
			tr.AttachmentID = strings.TrimSpace(tr.AttachmentID)
			if tr.ID == "" || tr.Title == "" {
				continue
			}
			if tr.AddedAt == "" {
				tr.AddedAt = time.Now().UTC().Format(time.RFC3339)
			}
			clean = append(clean, tr)
		}
		input.SavedTracks.Value = clean
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
	s.recordProfilePhoto(ctx, userID, uploadedAvatarKey)
	s.publishProfileUpdate(ctx, user, input)
	return user, nil
}

// publishProfileUpdate fans a refreshed public user shape out to the owner's
// own sockets and to members of chats the owner belongs to. Publish failures
// never fail the profile update itself.
func (s *Service) publishProfileUpdate(ctx context.Context, user User, input UpdateProfileInput) {
	if s.publisher == nil {
		return
	}
	if !input.Username.Set && !input.FirstName.Set && !input.LastName.Set && !input.BirthDate.Set && !input.AvatarDataURL.Set && !input.AvatarGradient.Set {
		return
	}
	recipients := make([]string, 0, 8)
	seen := map[string]struct{}{}
	ownerID := strings.TrimSpace(user.ID)
	if ownerID != "" {
		recipients = append(recipients, ownerID)
		seen[ownerID] = struct{}{}
	}
	if s.audience != nil {
		shared, err := s.audience.ListSharedChatMemberIDs(ctx, ownerID)
		if err == nil {
			for _, memberID := range shared {
				memberID = strings.TrimSpace(memberID)
				if memberID == "" {
					continue
				}
				if _, exists := seen[memberID]; exists {
					continue
				}
				seen[memberID] = struct{}{}
				recipients = append(recipients, memberID)
			}
		}
	}
	ev := ProfileUpdatedEvent{
		UserID:         ownerID,
		Email:          user.Email,
		Username:       user.Username,
		FirstName:      user.FirstName,
		LastName:       user.LastName,
		BirthDate:      user.BirthDate,
		AvatarDataURL:  user.AvatarDataURL,
		AvatarGradient: user.AvatarGradient,
	}
	for _, recipientID := range recipients {
		ev.RecipientUserID = recipientID
		_ = s.publisher.PublishProfileUpdate(ctx, ev)
	}
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
	// 0 = "Forever / no auto-logout" (far-future expiry, see
	// foreverSessionTTL); nil is left to the repository/handler layer.
	// Negative values and values that would overflow time.Duration are
	// rejected instead of silently wrapping.
	if sessionIdleTTLSeconds != nil && (*sessionIdleTTLSeconds < SessionIdleTTLForeverSeconds || *sessionIdleTTLSeconds > maxSessionIdleTTLSeconds) {
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

	idleTTL := resolveIdleTTL(sessionIdleTTLSeconds, s.refreshTTL)
	if err := s.sessions.UpdateExpiryByUser(ctx, userID, s.nowFn().UTC().Add(idleTTL)); err != nil {
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
	return s.issueSessionTokensWithMigration(ctx, userID, userAgent, ipAddress, idleTTL, false)
}

// issueSessionTokensWithMigration mints a session; migr=true marks the
// access token with the "migr" claim, limiting the bearer to the legacy
// email-binding endpoints until the email is bound (see the auth middleware
// allowlist). The refresh token itself carries no mark: Refresh re-derives
// migr from the user's is_legacy_unverified flag.
func (s *Service) issueSessionTokensWithMigration(ctx context.Context, userID, userAgent, ipAddress string, idleTTL time.Duration, migr bool) (Tokens, error) {
	sessionID := uuid.NewString()

	refreshPart, err := newRandomToken(32)
	if err != nil {
		return Tokens{}, &Error{
			Code:       CodeInternal,
			MessageKey: "error.internal",
			Cause:      err,
		}
	}

	accessToken, expiresInSec, err := s.newAccessTokenWithMigration(userID, sessionID, migr)
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

// newAccessToken signs the bearer token. Besides sub/exp it carries the
// session id as "sid", so the auth middleware can tell the caller which of the
// sessions in the list it is talking from without trusting a client header.
func (s *Service) newAccessToken(userID, sessionID string) (string, int64, error) {
	return s.newAccessTokenWithMigration(userID, sessionID, false)
}

// newAccessTokenWithMigration is newAccessToken plus the legacy "migr" claim:
// migr=true marks a limited boxchat-migration session. The claim is omitted
// when false, so pre-migration tokens keep verifying unchanged.
func (s *Service) newAccessTokenWithMigration(userID, sessionID string, migr bool) (string, int64, error) {
	headerJSON := `{"alg":"HS256","typ":"JWT"}`
	exp := s.nowFn().UTC().Add(s.accessTTL).Unix()
	claims := map[string]any{
		"sub": userID,
		"exp": exp,
	}
	if strings.TrimSpace(sessionID) != "" {
		claims["sid"] = strings.TrimSpace(sessionID)
	}
	if migr {
		claims["migr"] = true
	}
	payloadBytes, err := json.Marshal(claims)
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
	// New uploads are stored center-square (JPEG/PNG are cropped when they
	// decode, GIF and the rest pass through): the circle UI never stretches
	// a rectangle, and the archived history keeps whatever was stored.
	contentType, payload = avatarcrop.Square(contentType, payload)
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
