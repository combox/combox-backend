package postgres

import (
	"context"
	"errors"
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
		INSERT INTO users (email, username, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id::text, email, username, password_hash
	`

	var user authsvc.User
	err := r.client.pool.QueryRow(ctx, query, input.Email, input.Username, input.PasswordHash).
		Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash)
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

func (r *AuthUserRepository) FindByLogin(ctx context.Context, login string) (authsvc.User, error) {
	const query = `
		SELECT id::text, email, username, password_hash
		FROM users
		WHERE email = $1 OR username = $1
		LIMIT 1
	`

	var user authsvc.User
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(strings.ToLower(login))).
		Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authsvc.User{}, authsvc.ErrUserNotFound
		}
		return authsvc.User{}, err
	}
	return user, nil
}
