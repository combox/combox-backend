package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
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

func (e *Error) Unwrap() error { return e.Cause }

type SignedPreKey struct {
	KeyID     int    `json:"key_id"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

type OneTimePreKey struct {
	KeyID     int    `json:"key_id"`
	PublicKey string `json:"public_key"`
}

type Device struct {
	DeviceID    string    `json:"device_id"`
	UserID      string    `json:"user_id"`
	IdentityKey string    `json:"identity_key"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DeviceSummary struct {
	DeviceID    string `json:"device_id"`
	IdentityKey string `json:"identity_key"`
}

type PreKeyBundle struct {
	UserID        string         `json:"user_id"`
	DeviceID      string         `json:"device_id"`
	IdentityKey   string         `json:"identity_key"`
	SignedPreKey  SignedPreKey   `json:"signed_prekey"`
	OneTimePreKey *OneTimePreKey `json:"one_time_prekey,omitempty"`
}

type UpsertDeviceKeysInput struct {
	UserID         string
	DeviceID       string
	IdentityKey    string
	SignedPreKey   SignedPreKey
	OneTimePreKeys []OneTimePreKey
}

type UserKeyBackup struct {
	UserID     string          `json:"user_id"`
	Alg        string          `json:"alg"`
	KDF        string          `json:"kdf"`
	Salt       string          `json:"salt"`
	Params     json.RawMessage `json:"params"`
	Ciphertext string          `json:"ciphertext"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type UpsertUserKeyBackupInput struct {
	UserID     string
	Alg        string
	KDF        string
	Salt       string
	Params     json.RawMessage
	Ciphertext string
}

type Repository interface {
	UpsertDeviceKeys(ctx context.Context, in UpsertDeviceKeysInput) (Device, error)
	ListUserDevices(ctx context.Context, userID string) ([]DeviceSummary, error)
	ClaimPreKeyBundle(ctx context.Context, userID, deviceID string) (PreKeyBundle, error)
	UpsertUserKeyBackup(ctx context.Context, in UpsertUserKeyBackupInput) (UserKeyBackup, error)
	GetUserKeyBackup(ctx context.Context, userID string) (UserKeyBackup, bool, error)
}

type Service struct {
	repo Repository
}

func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("e2e repository is required")
	}
	return &Service{repo: repo}, nil
}

func (s *Service) UpsertDeviceKeys(ctx context.Context, in UpsertDeviceKeysInput) (Device, error) {
	userID := strings.TrimSpace(in.UserID)
	deviceID := strings.TrimSpace(in.DeviceID)
	identityKey := strings.TrimSpace(in.IdentityKey)

	if userID == "" || deviceID == "" || identityKey == "" {
		return Device{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_input"}
	}
	if _, err := uuid.Parse(userID); err != nil {
		return Device{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_user_id", Cause: err}
	}
	if _, err := uuid.Parse(deviceID); err != nil {
		return Device{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_device_id", Cause: err}
	}
	if strings.TrimSpace(in.SignedPreKey.PublicKey) == "" || strings.TrimSpace(in.SignedPreKey.Signature) == "" {
		return Device{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_signed_prekey"}
	}
	if in.SignedPreKey.KeyID <= 0 {
		return Device{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_signed_prekey"}
	}

	created, err := s.repo.UpsertDeviceKeys(ctx, in)
	if err != nil {
		return Device{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	return created, nil
}

func (s *Service) ListUserDevices(ctx context.Context, userID string) ([]DeviceSummary, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_user_id"}
	}
	if _, err := uuid.Parse(userID); err != nil {
		return nil, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_user_id", Cause: err}
	}

	items, err := s.repo.ListUserDevices(ctx, userID)
	if err != nil {
		return nil, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	return items, nil
}

func (s *Service) ClaimPreKeyBundle(ctx context.Context, userID, deviceID string) (PreKeyBundle, error) {
	userID = strings.TrimSpace(userID)
	deviceID = strings.TrimSpace(deviceID)
	if userID == "" || deviceID == "" {
		return PreKeyBundle{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_input"}
	}
	if _, err := uuid.Parse(userID); err != nil {
		return PreKeyBundle{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_user_id", Cause: err}
	}
	if _, err := uuid.Parse(deviceID); err != nil {
		return PreKeyBundle{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_device_id", Cause: err}
	}

	bundle, err := s.repo.ClaimPreKeyBundle(ctx, userID, deviceID)
	if err != nil {
		return PreKeyBundle{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if bundle.UserID == "" {
		return PreKeyBundle{}, &Error{Code: CodeNotFound, MessageKey: "error.e2e.device_not_found"}
	}
	return bundle, nil
}

func (s *Service) UpsertUserKeyBackup(ctx context.Context, in UpsertUserKeyBackupInput) (UserKeyBackup, error) {
	userID := strings.TrimSpace(in.UserID)
	alg := strings.TrimSpace(in.Alg)
	kdf := strings.TrimSpace(in.KDF)
	salt := strings.TrimSpace(in.Salt)
	ciphertext := strings.TrimSpace(in.Ciphertext)

	if userID == "" || alg == "" || kdf == "" || salt == "" || ciphertext == "" || len(in.Params) == 0 {
		return UserKeyBackup{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_input"}
	}
	if _, err := uuid.Parse(userID); err != nil {
		return UserKeyBackup{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_user_id", Cause: err}
	}
	if !json.Valid(in.Params) {
		return UserKeyBackup{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_input"}
	}

	out, err := s.repo.UpsertUserKeyBackup(ctx, UpsertUserKeyBackupInput{
		UserID:     userID,
		Alg:        alg,
		KDF:        kdf,
		Salt:       salt,
		Params:     in.Params,
		Ciphertext: ciphertext,
	})
	if err != nil {
		return UserKeyBackup{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	return out, nil
}

func (s *Service) GetUserKeyBackup(ctx context.Context, userID string) (UserKeyBackup, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UserKeyBackup{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_user_id"}
	}
	if _, err := uuid.Parse(userID); err != nil {
		return UserKeyBackup{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.e2e.invalid_user_id", Cause: err}
	}

	out, ok, err := s.repo.GetUserKeyBackup(ctx, userID)
	if err != nil {
		return UserKeyBackup{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	if !ok {
		return UserKeyBackup{}, &Error{Code: CodeNotFound, MessageKey: "error.e2e.key_backup.not_found"}
	}
	return out, nil
}
