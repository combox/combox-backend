package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	CodeInvalidArgument = "invalid_argument"
	CodeInternal        = "internal"
)

// Sentinel validation errors, unwrappable through Error.Cause.
var (
	// ErrInvalidUser is returned for an empty user id.
	ErrInvalidUser = errors.New("invalid user id")
	// ErrUnknownKey is returned for a key outside the whitelist.
	ErrUnknownKey = errors.New("unknown user setting key")
	// ErrInvalidValue is returned for a value outside the key's domain
	// ("true"/"false" for boolean keys, the DeleteAccountTTL set for
	// delete_account_ttl).
	ErrInvalidValue = errors.New("invalid user setting value")
)

// Error is the typed error of the settings service. Handlers map
// CodeInvalidArgument onto HTTP 400 and everything else onto HTTP 500.
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

func invalidArg(key string, cause error, details map[string]string) *Error {
	return &Error{Code: CodeInvalidArgument, MessageKey: key, Details: details, Cause: cause}
}

func internalErr(cause error) *Error {
	return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: cause}
}

// Boolean keys: value is exactly "true" or "false".
const (
	KeyNotificationsEnabled = "notifications_enabled"
	KeyNotificationPreviews = "notification_previews"
	KeySoundsEnabled        = "sounds_enabled"
	KeyBadgeEnabled         = "badge_enabled"
	KeyVoiceAutoplay        = "voice_autoplay"
	KeyMediaAutoplay        = "media_autoplay"
	KeyDataSaver            = "data_saver"
	KeyAutoDownloadPhotos   = "auto_download_photos"
	KeyAutoDownloadVideos   = "auto_download_videos"
	KeyAutoDownloadFiles    = "auto_download_files"
	// Telegram-style per-type notification toggles. The *_enabled trio above
	// stays the global on/off switch; these refine it per chat kind.
	KeyNotificationsPrivate   = "notifications_private"
	KeyNotificationsGroups    = "notifications_groups"
	KeyNotificationsChannels  = "notifications_channels"
	KeyNotificationsReactions = "notifications_reactions"
	// Preview split: notification_previews stays the master switch, these two
	// refine it (sender name line vs message text).
	KeyNotificationPreviewName = "notification_preview_name"
	KeyNotificationPreviewText = "notification_preview_text"
	// Service events (the "Events" section): contact joined, pinned message.
	KeyEventsContactJoined = "events_contact_joined"
	KeyEventsPinned        = "events_pinned"
	// CallsAccept is "accept voice/video calls" (reject-play-tone otherwise).
	KeyCallsAccept = "calls_accept"
	// Badge refinements: badge_enabled stays the master switch.
	// badge_count_messages=false counts chats (Telegram default), true counts
	// every unread message.
	KeyBadgeIncludeMuted  = "badge_include_muted"
	KeyBadgeFoldersCount  = "badge_folders_count"
	KeyBadgeCountMessages = "badge_count_messages"
)

// DeleteAccountTTL is the only non-boolean key: months of inactivity after
// which the account is flagged for deletion. STORAGE ONLY: no background job
// consumes it yet, nothing deletes anything automatically.
const KeyDeleteAccountTTL = "delete_account_ttl"

// Delete-account TTL domain. Values read as "<n>_month(s)".
const (
	DeleteAccountTTL1Month   = "1_month"
	DeleteAccountTTL3Months  = "3_months"
	DeleteAccountTTL6Months  = "6_months"
	DeleteAccountTTL12Months = "12_months"
)

// settingsKeys is the whitelist in a stable order. Nothing outside this list
// is ever read or written.
var settingsKeys = []string{
	"notifications_enabled",
	"notification_previews",
	"sounds_enabled",
	"badge_enabled",
	"voice_autoplay",
	"media_autoplay",
	"data_saver",
	"auto_download_photos",
	"auto_download_videos",
	"auto_download_files",
	"notifications_private",
	"notifications_groups",
	"notifications_channels",
	"notifications_reactions",
	"notification_preview_name",
	"notification_preview_text",
	"events_contact_joined",
	"events_pinned",
	"calls_accept",
	"badge_include_muted",
	"badge_folders_count",
	"badge_count_messages",
	"delete_account_ttl",
}

// defaults maps every whitelisted key onto the value a user who never touched
// it gets. Both maps are read-only at runtime.
var defaults = map[string]string{
	KeyNotificationsEnabled:    "true",
	KeyNotificationPreviews:    "true",
	KeySoundsEnabled:           "true",
	KeyBadgeEnabled:            "true",
	KeyVoiceAutoplay:           "false",
	KeyMediaAutoplay:           "true",
	KeyDataSaver:               "false",
	KeyAutoDownloadPhotos:      "true",
	KeyAutoDownloadVideos:      "false",
	KeyAutoDownloadFiles:       "false",
	KeyNotificationsPrivate:    "true",
	KeyNotificationsGroups:     "true",
	KeyNotificationsChannels:   "true",
	KeyNotificationsReactions:  "true",
	KeyNotificationPreviewName: "true",
	KeyNotificationPreviewText: "true",
	KeyEventsContactJoined:     "true",
	KeyEventsPinned:            "true",
	KeyCallsAccept:             "true",
	KeyBadgeIncludeMuted:       "false",
	KeyBadgeFoldersCount:       "false",
	KeyBadgeCountMessages:      "false",
	KeyDeleteAccountTTL:        "6_months",
}

// isValidValue reports whether value is inside the domain of key.
func isValidValue(key, value string) bool {
	if key == KeyDeleteAccountTTL {
		switch value {
		case DeleteAccountTTL1Month,
			DeleteAccountTTL3Months,
			DeleteAccountTTL6Months,
			DeleteAccountTTL12Months:
			return true
		default:
			return false
		}
	}
	return value == "true" || value == "false"
}

// IsBooleanKey reports whether key stores "true"/"false"
// (every key except delete_account_ttl).
func IsBooleanKey(key string) bool {
	key = strings.TrimSpace(key)
	if _, ok := defaults[key]; !ok {
		return false
	}
	return key != KeyDeleteAccountTTL
}

// Keys returns a copy of the whitelist, in a stable order.
func Keys() []string {
	out := make([]string, len(settingsKeys))
	copy(out, settingsKeys)
	return out
}

// IsKey reports whether key is part of the whitelist.
func IsKey(key string) bool {
	_, ok := defaults[strings.TrimSpace(key)]
	return ok
}

// Default returns the documented default of a whitelisted key.
func Default(key string) (string, bool) {
	value, ok := defaults[strings.TrimSpace(key)]
	return value, ok
}

// Defaults returns a copy of the whole default map.
func Defaults() map[string]string {
	out := make(map[string]string, len(defaults))
	for key, value := range defaults {
		out[key] = value
	}
	return out
}

// Repository is the persistence of the user_settings table. A missing row
// means "the user never touched this key".
type Repository interface {
	// GetAll returns the stored rows of one user.
	GetAll(ctx context.Context, userID string) (map[string]string, error)
	// UpsertMany writes every pair atomically.
	UpsertMany(ctx context.Context, userID string, values map[string]string) error
}

// Service serves the global app settings: the "Notifications and Sounds" and
// "Data and Storage" toggles plus the delete-account inactivity TTL
// (storage only, see KeyDeleteAccountTTL).
type Service struct {
	repo Repository
}

// New builds the service.
func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("user settings repository is required")
	}
	return &Service{repo: repo}, nil
}

// GetUserSettings returns every whitelisted key: the documented defaults,
// overlaid with whatever the user has stored.
func (s *Service) GetUserSettings(ctx context.Context, userID string) (map[string]string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, invalidArg("error.user_settings.invalid_input", ErrInvalidUser, map[string]string{"user_id": userID})
	}
	stored, err := s.repo.GetAll(ctx, userID)
	if err != nil {
		return nil, internalErr(err)
	}
	return mergeStored(stored), nil
}

// UpdateUserSettings applies a partial patch. Every key must be whitelisted
// and every value must be inside the key's domain ("true"/"false" for boolean
// keys, the DeleteAccountTTL set for delete_account_ttl); otherwise the whole
// patch is rejected with a typed invalid_argument error. The write is atomic.
func (s *Service) UpdateUserSettings(ctx context.Context, userID string, patch map[string]string) (map[string]string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, invalidArg("error.user_settings.invalid_input", ErrInvalidUser, map[string]string{"user_id": userID})
	}

	normalized := make(map[string]string, len(patch))
	for key, value := range patch {
		cleanKey := strings.TrimSpace(key)
		if !IsKey(cleanKey) {
			return nil, invalidArg("error.user_settings.unknown_key", ErrUnknownKey, map[string]string{"key": key})
		}
		cleanValue := strings.TrimSpace(value)
		if !isValidValue(cleanKey, cleanValue) {
			return nil, invalidArg("error.user_settings.invalid_value", ErrInvalidValue, map[string]string{"key": cleanKey, "value": value})
		}
		normalized[cleanKey] = cleanValue
	}

	if len(normalized) > 0 {
		if err := s.repo.UpsertMany(ctx, userID, normalized); err != nil {
			return nil, internalErr(err)
		}
	}

	stored, err := s.repo.GetAll(ctx, userID)
	if err != nil {
		return nil, internalErr(err)
	}
	merged := mergeStored(stored)
	for key, value := range normalized {
		merged[key] = value
	}
	return merged, nil
}

// mergeStored lays the stored rows over the full default map and drops every
// stored key that is no longer whitelisted (or holds a value outside the
// key's domain, e.g. written before the key turned into an enum).
func mergeStored(stored map[string]string) map[string]string {
	out := make(map[string]string, len(settingsKeys))
	for _, key := range settingsKeys {
		out[key] = defaults[key]
	}
	for key, value := range stored {
		if _, ok := defaults[key]; !ok {
			continue
		}
		if !isValidValue(key, value) {
			continue
		}
		out[key] = value
	}
	return out
}
