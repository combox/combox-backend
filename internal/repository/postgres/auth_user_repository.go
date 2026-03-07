package postgres

import (
	"context"
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

func (r *AuthUserRepository) Create(ctx context.Context, input authsvc.CreateUserInput) (authsvc.User, error) {
	const query = `
		INSERT INTO users (email, username, password_hash, first_name, last_name, birth_date, avatar_data_url, avatar_gradient)
		VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8)
		RETURNING id::text, email, username, password_hash, COALESCE(first_name, ''), last_name, birth_date::text, avatar_data_url, avatar_gradient, session_idle_ttl_seconds
	`

	var user authsvc.User
	err := r.client.pool.QueryRow(
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
		&user.FirstName,
		&user.LastName,
		&user.BirthDate,
		&user.AvatarDataURL,
		&user.AvatarGradient,
		&user.SessionIdleTTLSeconds,
	)
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
		SELECT id::text, email, username, password_hash, COALESCE(first_name, ''), last_name, birth_date::text, avatar_data_url, avatar_gradient, session_idle_ttl_seconds
		FROM users
		WHERE id = $1::uuid
		LIMIT 1
	`

	var user authsvc.User
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userID)).
		Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.FirstName, &user.LastName, &user.BirthDate, &user.AvatarDataURL, &user.AvatarGradient, &user.SessionIdleTTLSeconds)
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
		SELECT id::text, email, username, password_hash, COALESCE(first_name, ''), last_name, birth_date::text, avatar_data_url, avatar_gradient, session_idle_ttl_seconds
		FROM users
		WHERE email = $1 OR username = $1
		LIMIT 1
	`

	var user authsvc.User
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(strings.ToLower(login))).
		Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.FirstName, &user.LastName, &user.BirthDate, &user.AvatarDataURL, &user.AvatarGradient, &user.SessionIdleTTLSeconds)
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

	if len(setClauses) == 0 {
		return authsvc.User{}, authsvc.ErrUserNotFound
	}

	query := fmt.Sprintf(`
		UPDATE users
		SET %s, updated_at = NOW()
		WHERE id = $%d::uuid
		RETURNING id::text, email, username, password_hash, COALESCE(first_name, ''), last_name, birth_date::text, avatar_data_url, avatar_gradient, session_idle_ttl_seconds
	`, strings.Join(setClauses, ", "), arg)
	args = append(args, strings.TrimSpace(input.UserID))

	var user authsvc.User
	err := r.client.pool.QueryRow(ctx, query, args...).Scan(
		&user.ID,
		&user.Email,
		&user.Username,
		&user.PasswordHash,
		&user.FirstName,
		&user.LastName,
		&user.BirthDate,
		&user.AvatarDataURL,
		&user.AvatarGradient,
		&user.SessionIdleTTLSeconds,
	)
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
		RETURNING id::text, email, username, password_hash, COALESCE(first_name, ''), last_name, birth_date::text, avatar_data_url, avatar_gradient, session_idle_ttl_seconds
	`

	var user authsvc.User
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(strings.ToLower(email))).Scan(
		&user.ID,
		&user.Email,
		&user.Username,
		&user.PasswordHash,
		&user.FirstName,
		&user.LastName,
		&user.BirthDate,
		&user.AvatarDataURL,
		&user.AvatarGradient,
		&user.SessionIdleTTLSeconds,
	)
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
