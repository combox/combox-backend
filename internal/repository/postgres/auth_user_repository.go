package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	authsvc "combox-backend/internal/service/auth"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type AuthUserRepository struct {
	client *Client
}

func NewAuthUserRepository(client *Client) *AuthUserRepository {
	return &AuthUserRepository{client: client}
}

const userProfileSelect = `id::text, email, username, password_hash, is_legacy_unverified, legacy_username, COALESCE(first_name, ''), last_name, birth_date::text, avatar_data_url, avatar_gradient, COALESCE(bio, ''), phone_number, COALESCE(name_color, ''), COALESCE(playlist_title, ''), COALESCE(playlist_is_public, TRUE), COALESCE(saved_tracks, '[]'::jsonb), session_idle_ttl_seconds`

func scanUserRow(row pgx.Row) (authsvc.User, error) {
	var user authsvc.User
	var tracksRaw []byte
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.Username,
		&user.PasswordHash,
		&user.IsLegacyUnverified,
		&user.LegacyUsername,
		&user.FirstName,
		&user.LastName,
		&user.BirthDate,
		&user.AvatarDataURL,
		&user.AvatarGradient,
		&user.Bio,
		&user.PhoneNumber,
		&user.NameColor,
		&user.PlaylistTitle,
		&user.PlaylistIsPublic,
		&tracksRaw,
		&user.SessionIdleTTLSeconds,
	)
	if err != nil {
		return authsvc.User{}, err
	}
	if len(tracksRaw) > 0 {
		_ = json.Unmarshal(tracksRaw, &user.SavedTracks)
	}
	if user.SavedTracks == nil {
		user.SavedTracks = []authsvc.SavedTrack{}
	}
	return user, nil
}

func (r *AuthUserRepository) Create(ctx context.Context, input authsvc.CreateUserInput) (authsvc.User, error) {
	const query = `
		INSERT INTO users (email, username, password_hash, first_name, last_name, birth_date, avatar_data_url, avatar_gradient)
		VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8)
		RETURNING ` + userProfileSelect + `
	`

	var err error
	var user authsvc.User
	var tracksRaw []byte
	err = r.client.pool.QueryRow(
		ctx,
		query,
		input.Email,
		input.Username,
		input.PasswordHash,
		input.FirstName,
		input.LastName,
		input.BirthDate,
		input.AvatarDataURL,
		input.AvatarGradient,
	).Scan(
		&user.ID,
		&user.Email,
		&user.Username,
		&user.PasswordHash,
		&user.IsLegacyUnverified,
		&user.LegacyUsername,
		&user.FirstName,
		&user.LastName,
		&user.BirthDate,
		&user.AvatarDataURL,
		&user.AvatarGradient,
		&user.Bio,
		&user.PhoneNumber,
		&user.NameColor,
		&user.PlaylistTitle,
		&user.PlaylistIsPublic,
		&tracksRaw,
		&user.SessionIdleTTLSeconds,
	)
	if len(tracksRaw) > 0 {
		_ = json.Unmarshal(tracksRaw, &user.SavedTracks)
	}
	if user.SavedTracks == nil {
		user.SavedTracks = []authsvc.SavedTrack{}
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "email") {
				return authsvc.User{}, authsvc.ErrEmailTaken
			}
			if strings.Contains(pgErr.ConstraintName, "username") {
				return authsvc.User{}, authsvc.ErrUsernameTaken
			}
			return authsvc.User{}, authsvc.ErrEmailTaken
		}
		return authsvc.User{}, err
	}
	return user, nil
}

func (r *AuthUserRepository) FindByID(ctx context.Context, userID string) (authsvc.User, error) {
	const query = `
		SELECT ` + userProfileSelect + `
		FROM users
		WHERE id = $1::uuid
		LIMIT 1
	`

	user, err := scanUserRow(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authsvc.User{}, authsvc.ErrUserNotFound
		}
		return authsvc.User{}, err
	}
	return user, nil
}

func (r *AuthUserRepository) FindByLogin(ctx context.Context, login string) (authsvc.User, error) {
	const query = `
		SELECT ` + userProfileSelect + `
		FROM users
		WHERE email = $1 OR username = $1
		LIMIT 1
	`

	user, err := scanUserRow(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(strings.ToLower(login))))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authsvc.User{}, authsvc.ErrUserNotFound
		}
		return authsvc.User{}, err
	}
	return user, nil
}

func (r *AuthUserRepository) UpdateSessionIdleTTL(ctx context.Context, userID string, sessionIdleTTLSeconds *int64) error {
	const query = `
		UPDATE users
		SET session_idle_ttl_seconds = $2, updated_at = NOW()
		WHERE id = $1::uuid
	`

	argsVal := any(nil)
	if sessionIdleTTLSeconds != nil {
		argsVal = *sessionIdleTTLSeconds
	}

	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(userID), argsVal)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return authsvc.ErrUserNotFound
	}
	return nil
}

func (r *AuthUserRepository) UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error {
	const query = `
		UPDATE users
		SET password_hash = $2, updated_at = NOW()
		WHERE id = $1::uuid
	`
	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(passwordHash))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return authsvc.ErrUserNotFound
	}
	return nil
}

func (r *AuthUserRepository) UpdateProfile(ctx context.Context, input authsvc.UpdateProfileInput) (authsvc.User, error) {
	setClauses := make([]string, 0, 6)
	args := make([]any, 0, 8)
	arg := 1

	if input.Username.Set {
		setClauses = append(setClauses, fmt.Sprintf("username = $%d", arg))
		args = append(args, input.Username.Value)
		arg++
	}
	if input.FirstName.Set {
		setClauses = append(setClauses, fmt.Sprintf("first_name = $%d", arg))
		args = append(args, input.FirstName.Value)
		arg++
	}
	if input.LastName.Set {
		setClauses = append(setClauses, fmt.Sprintf("last_name = $%d", arg))
		args = append(args, input.LastName.Value)
		arg++
	}
	if input.BirthDate.Set {
		setClauses = append(setClauses, fmt.Sprintf("birth_date = $%d::date", arg))
		args = append(args, input.BirthDate.Value)
		arg++
	}
	if input.AvatarDataURL.Set {
		setClauses = append(setClauses, fmt.Sprintf("avatar_data_url = $%d", arg))
		args = append(args, input.AvatarDataURL.Value)
		arg++
	}
	if input.AvatarGradient.Set {
		setClauses = append(setClauses, fmt.Sprintf("avatar_gradient = $%d", arg))
		args = append(args, input.AvatarGradient.Value)
		arg++
	}
	if input.Bio.Set {
		setClauses = append(setClauses, fmt.Sprintf("bio = $%d::text", arg))
		args = append(args, input.Bio.Value)
		arg++
	}
	if input.PhoneNumber.Set {
		setClauses = append(setClauses, fmt.Sprintf("phone_number = $%d::text", arg))
		args = append(args, input.PhoneNumber.Value)
		arg++
	}
	if input.NameColor.Set {
		setClauses = append(setClauses, fmt.Sprintf("name_color = $%d::text", arg))
		args = append(args, input.NameColor.Value)
		arg++
	}
	if input.SavedTracks.Set {
		setClauses = append(setClauses, fmt.Sprintf("saved_tracks = $%d::jsonb", arg))
		raw, _ := json.Marshal(input.SavedTracks.Value)
		args = append(args, string(raw))
		arg++
	}
	if input.PlaylistTitle.Set {
		setClauses = append(setClauses, fmt.Sprintf("playlist_title = $%d::text", arg))
		args = append(args, input.PlaylistTitle.Value)
		arg++
	}
	if input.PlaylistIsPublic.Set {
		setClauses = append(setClauses, fmt.Sprintf("playlist_is_public = $%d::boolean", arg))
		args = append(args, input.PlaylistIsPublic.Value)
		arg++
	}

	if len(setClauses) == 0 {
		return authsvc.User{}, authsvc.ErrUserNotFound
	}

	query := fmt.Sprintf(`
		UPDATE users
		SET %s, updated_at = NOW()
		WHERE id = $%d::uuid
		RETURNING `+userProfileSelect+`
	`, strings.Join(setClauses, ", "), arg)
	args = append(args, strings.TrimSpace(input.UserID))

	user, err := scanUserRow(r.client.pool.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authsvc.User{}, authsvc.ErrUserNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "username") {
				return authsvc.User{}, authsvc.ErrUsernameTaken
			}
			return authsvc.User{}, authsvc.ErrUsernameTaken
		}
		return authsvc.User{}, err
	}
	return user, nil
}

func (r *AuthUserRepository) UpdateEmail(ctx context.Context, userID, email string) (authsvc.User, error) {
	const query = `
		UPDATE users
		SET email = $2, updated_at = NOW()
		WHERE id = $1::uuid
		RETURNING ` + userProfileSelect + `
	`

	user, err := scanUserRow(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(strings.ToLower(email))))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authsvc.User{}, authsvc.ErrUserNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if strings.Contains(pgErr.ConstraintName, "email") {
				return authsvc.User{}, authsvc.ErrEmailTaken
			}
			return authsvc.User{}, authsvc.ErrEmailTaken
		}
		return authsvc.User{}, err
	}
	return user, nil
}

// BindLegacyEmail stores the real email of a migrated user and clears
// is_legacy_unverified. legacy_username is deliberately left untouched for
// audit. Callers must have verified the OTP before invoking this.
func (r *AuthUserRepository) BindLegacyEmail(ctx context.Context, userID, email string) (authsvc.User, error) {
	const query = `
		UPDATE users
		SET email = $2, is_legacy_unverified = FALSE, updated_at = NOW()
		WHERE id = $1::uuid
		RETURNING ` + userProfileSelect + `
	`

	user, err := scanUserRow(r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(strings.ToLower(email))))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authsvc.User{}, authsvc.ErrUserNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return authsvc.User{}, authsvc.ErrEmailTaken
		}
		return authsvc.User{}, err
	}
	return user, nil
}
