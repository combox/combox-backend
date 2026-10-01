// Package profilephoto keeps the archive of every avatar object that was
// ever written for a user or a chat and serves it back as a presigned
// Telegram-style photo gallery.
package profilephoto

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	// OwnerUser marks the photo history of a single account.
	OwnerUser = "user"
	// OwnerChat marks the photo history of a group / channel / standalone channel.
	OwnerChat = "chat"

	// DefaultListLimit caps one gallery page.
	DefaultListLimit = 100

	defaultURLTTL = 24 * time.Hour * 7
)

var (
	// ErrInvalidArgument is returned for an unknown owner kind or empty ids.
	ErrInvalidArgument = errors.New("invalid profile photo argument")
	// ErrNotFound is returned when the owner (or the gallery) does not exist.
	ErrNotFound = errors.New("profile photo history not found")
	// ErrForbidden is returned when the viewer may not see this gallery.
	ErrForbidden = errors.New("profile photo history is not visible to the viewer")
)

// Record is one archived avatar object.
type Record struct {
	ID        string
	OwnerKind string
	OwnerID   string
	ObjectKey string
	CreatedAt time.Time
}

// Photo is a single gallery row already resolved to a temporary URL.
// Migrated marks rows the boxchat ETL backfilled for legacy avatars: their
// CreatedAt is the migration moment (legacy stores no avatar timestamp), not
// the original install moment, so the viewer must label them instead of
// stating a false install date.
type Photo struct {
	ID        string
	URL       string
	CreatedAt time.Time
	Migrated  bool
}

// Store is the persistence of the archive.
type Store interface {
	Add(ctx context.Context, ownerKind, ownerID, objectKey string) error
	List(ctx context.Context, ownerKind, ownerID string, limit int) ([]Record, error)
}

// Presigner turns an object key into a temporary download URL.
type Presigner interface {
	PresignGetObject(ctx context.Context, objectKey string, expires time.Duration) (string, error)
}

// UserAccess is the privacy gate of a user's photo history: whoever may view
// the profile may view the history, unless the owner opted out of it.
type UserAccess interface {
	CanViewUserPhotos(ctx context.Context, viewerID, ownerID string) error
}

// ChatAccess is the privacy gate of a chat's photo history: members only
// (public channels fall back to "whoever may open the channel").
type ChatAccess interface {
	CanViewChatPhotos(ctx context.Context, viewerID, chatID string) error
}

// Config wires the storage, the presigner and the privacy gates.
type Config struct {
	Store      Store
	Avatars    Presigner
	TTL        time.Duration
	Limit      int
	UserAccess UserAccess
	ChatAccess ChatAccess
}

// Service records new avatar objects and lists the history back to a viewer.
type Service struct {
	store      Store
	avatars    Presigner
	ttl        time.Duration
	limit      int
	userAccess UserAccess
	chatAccess ChatAccess
}

// New builds the service. A missing privacy gate fails closed: List then
// reports ErrForbidden instead of leaking the archive.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("profile photo store is required")
	}
	if cfg.Avatars == nil {
		return nil, errors.New("avatar presigner is required")
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultURLTTL
	}
	if cfg.Limit <= 0 {
		cfg.Limit = DefaultListLimit
	}
	return &Service{
		store:      cfg.Store,
		avatars:    cfg.Avatars,
		ttl:        cfg.TTL,
		limit:      cfg.Limit,
		userAccess: cfg.UserAccess,
		chatAccess: cfg.ChatAccess,
	}, nil
}

// isMigratedKey reports whether the archived object came from the boxchat
// legacy migration (boxchat-migrate uploads under boxchat-legacy/...).
func isMigratedKey(objectKey string) bool {
	return strings.HasPrefix(strings.TrimSpace(objectKey), "boxchat-legacy/")
}

// Record archives a freshly written avatar object. Clearing an avatar or
// re-saving an unchanged reference never reaches this method, so nothing is
// recorded for those updates.
func (s *Service) Record(ctx context.Context, ownerKind, ownerID, objectKey string) error {
	ownerKind = strings.TrimSpace(ownerKind)
	ownerID = strings.TrimSpace(ownerID)
	objectKey = strings.TrimSpace(objectKey)
	if ownerKind != OwnerUser && ownerKind != OwnerChat {
		return ErrInvalidArgument
	}
	if ownerID == "" || objectKey == "" {
		return ErrInvalidArgument
	}
	return s.store.Add(ctx, ownerKind, ownerID, objectKey)
}

// List returns the owner's photo history, newest first, with every URL
// presigned for the current viewer. Rows whose object can no longer be
// presigned are skipped instead of failing the whole gallery.
func (s *Service) List(ctx context.Context, viewerID, ownerKind, ownerID string) ([]Photo, error) {
	viewerID = strings.TrimSpace(viewerID)
	ownerKind = strings.TrimSpace(ownerKind)
	ownerID = strings.TrimSpace(ownerID)
	if viewerID == "" || ownerID == "" {
		return nil, ErrNotFound
	}

	switch ownerKind {
	case OwnerUser:
		if s.userAccess == nil {
			return nil, ErrForbidden
		}
		if err := s.userAccess.CanViewUserPhotos(ctx, viewerID, ownerID); err != nil {
			return nil, err
		}
	case OwnerChat:
		if s.chatAccess == nil {
			return nil, ErrForbidden
		}
		if err := s.chatAccess.CanViewChatPhotos(ctx, viewerID, ownerID); err != nil {
			return nil, err
		}
	default:
		return nil, ErrNotFound
	}

	records, err := s.store.List(ctx, ownerKind, ownerID, s.limit)
	if err != nil {
		return nil, err
	}

	photos := make([]Photo, 0, len(records))
	for _, rec := range records {
		objectKey := strings.TrimSpace(rec.ObjectKey)
		if objectKey == "" {
			continue
		}
		presigned, err := s.avatars.PresignGetObject(ctx, objectKey, s.ttl)
		if err != nil || strings.TrimSpace(presigned) == "" {
			continue
		}
		photos = append(photos, Photo{ID: rec.ID, URL: presigned, CreatedAt: rec.CreatedAt, Migrated: isMigratedKey(objectKey)})
	}
	return photos, nil
}
