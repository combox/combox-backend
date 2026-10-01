package postgres

import (
	"context"
	"errors"
	"strings"

	"combox-backend/internal/service/chat"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// folderQuerier is the subset of pgxpool.Pool / pgx.Tx the folder store needs,
// so every helper works both outside and inside a transaction.
type folderQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const chatFolderSelect = `
	SELECT f.id::text,
	       f.name,
	       f.icon,
	       f.position,
	       f.created_at,
	       COALESCE(
	           array_agg(fc.chat_id::text ORDER BY fc.position, fc.chat_id)
	           FILTER (WHERE fc.chat_id IS NOT NULL),
	           '{}'
	       )
	FROM chat_folders f
	LEFT JOIN chat_folder_chats fc ON fc.folder_id = f.id
`

// ChatFolderRepository persists the Telegram style dialog filters
// (migration 000041_chat_folders).
type ChatFolderRepository struct {
	client *Client
}

func NewChatFolderRepository(client *Client) *ChatFolderRepository {
	return &ChatFolderRepository{client: client}
}

// ListChatFolders returns every folder of one user ordered by
// (position, created_at, id); each folder carries its chat ids ordered by
// chat_folder_chats.position.
func (r *ChatFolderRepository) ListChatFolders(ctx context.Context, userID string) ([]chat.ChatFolder, error) {
	query := chatFolderSelect + `
		WHERE f.user_id = $1::uuid
		GROUP BY f.id
		ORDER BY f.position ASC, f.created_at ASC, f.id ASC
	`
	rows, err := r.client.pool.Query(ctx, query, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]chat.ChatFolder, 0)
	for rows.Next() {
		item, err := scanChatFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetChatFolder loads one folder owned by userID. A folder of somebody else is
// indistinguishable from a missing one, so callers can answer 404 for both.
func (r *ChatFolderRepository) GetChatFolder(ctx context.Context, userID, folderID string) (chat.ChatFolder, error) {
	return getChatFolder(ctx, r.client.pool, userID, folderID)
}

// CreateChatFolder inserts a folder at position max(position)+1 and attaches
// its chats in the given order. The position guard makes the "at most
// maxFolders folders" rule race free:
//   - the count guard fails        -> chat.ErrChatFolderLimit
//   - UNIQUE (user_id, name) fails -> chat.ErrChatFolderNameTaken
func (r *ChatFolderRepository) CreateChatFolder(ctx context.Context, input chat.CreateChatFolderInput, maxFolders int) (chat.ChatFolder, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.ChatFolder{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insert = `
		INSERT INTO chat_folders (user_id, name, icon, position)
		SELECT $1::uuid, $2, $3,
		       COALESCE((SELECT MAX(position) + 1 FROM chat_folders WHERE user_id = $1::uuid), 0)
		WHERE (SELECT count(*) FROM chat_folders WHERE user_id = $1::uuid) < $4::int
		RETURNING id::text
	`
	var folderID string
	err = tx.QueryRow(
		ctx,
		insert,
		strings.TrimSpace(input.UserID),
		input.Name,
		input.Icon,
		maxFolders,
	).Scan(&folderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.ChatFolder{}, chat.ErrChatFolderLimit
		}
		if isUniqueViolation(err) {
			return chat.ChatFolder{}, chat.ErrChatFolderNameTaken
		}
		return chat.ChatFolder{}, err
	}

	if len(input.ChatIDs) > 0 {
		if err := insertFolderChats(ctx, tx, folderID, input.ChatIDs); err != nil {
			return chat.ChatFolder{}, err
		}
	}

	item, err := getChatFolder(ctx, tx, input.UserID, folderID)
	if err != nil {
		return chat.ChatFolder{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return chat.ChatFolder{}, err
	}
	return item, nil
}

// UpdateChatFolderMeta renames a folder and/or replaces its icon. A nil
// pointer keeps the stored value, so callers only pass the fields they want to
// change. Renaming onto an existing name violates UNIQUE (user_id, name) and
// surfaces as chat.ErrChatFolderNameTaken.
func (r *ChatFolderRepository) UpdateChatFolderMeta(ctx context.Context, userID, folderID string, name, icon *string) (chat.ChatFolder, error) {
	const query = `
		UPDATE chat_folders
		SET name = COALESCE($3, name),
		    icon = COALESCE($4, icon)
		WHERE user_id = $1::uuid AND id = $2::uuid
		RETURNING id::text
	`
	var updated string
	err := r.client.pool.QueryRow(
		ctx,
		query,
		strings.TrimSpace(userID),
		strings.TrimSpace(folderID),
		name,
		icon,
	).Scan(&updated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.ChatFolder{}, chat.ErrChatFolderNotFound
		}
		if isUniqueViolation(err) {
			return chat.ChatFolder{}, chat.ErrChatFolderNameTaken
		}
		return chat.ChatFolder{}, err
	}
	return getChatFolder(ctx, r.client.pool, userID, folderID)
}

// MoveChatFolder puts the folder at the requested position and rewrites the
// positions of the other folders of the same user, so the whole sequence stays
// a dense, stable permutation.
func (r *ChatFolderRepository) MoveChatFolder(ctx context.Context, userID, folderID string, position int) (chat.ChatFolder, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.ChatFolder{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockQuery = `
		SELECT id::text
		FROM chat_folders
		WHERE user_id = $1::uuid
		ORDER BY position ASC, created_at ASC, id ASC
		FOR UPDATE
	`
	rows, err := tx.Query(ctx, lockQuery, strings.TrimSpace(userID))
	if err != nil {
		return chat.ChatFolder{}, err
	}
	ordered := make([]string, 0, 8)
	targetIndex := -1
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return chat.ChatFolder{}, err
		}
		if id == strings.TrimSpace(folderID) {
			targetIndex = len(ordered)
		}
		ordered = append(ordered, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return chat.ChatFolder{}, err
	}
	if targetIndex < 0 {
		return chat.ChatFolder{}, chat.ErrChatFolderNotFound
	}

	// Remove the folder, clamp the requested index and re-insert it there.
	rest := make([]string, 0, len(ordered)-1)
	rest = append(rest, ordered[:targetIndex]...)
	rest = append(rest, ordered[targetIndex+1:]...)
	target := position
	if target < 0 {
		target = 0
	}
	if target > len(rest) {
		target = len(rest)
	}
	updated := make([]string, 0, len(ordered))
	updated = append(updated, rest[:target]...)
	updated = append(updated, strings.TrimSpace(folderID))
	updated = append(updated, rest[target:]...)

	ids := make([]string, len(updated))
	positions := make([]int, len(updated))
	for i, id := range updated {
		ids[i] = id
		positions[i] = i
	}
	const reorder = `
		UPDATE chat_folders AS f
		SET position = x.position
		FROM unnest($2::text[], $3::int[]) AS x(id, position)
		WHERE f.id = x.id::uuid AND f.user_id = $1::uuid
	`
	if _, err := tx.Exec(ctx, reorder, strings.TrimSpace(userID), ids, positions); err != nil {
		return chat.ChatFolder{}, err
	}

	item, err := getChatFolder(ctx, tx, userID, folderID)
	if err != nil {
		return chat.ChatFolder{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return chat.ChatFolder{}, err
	}
	return item, nil
}

// ReplaceChatFolderChats swaps the whole chat set of a folder for chatIDs,
// keeping the input order. The folder must belong to userID.
func (r *ChatFolderRepository) ReplaceChatFolderChats(ctx context.Context, userID, folderID string, chatIDs []string) (chat.ChatFolder, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return chat.ChatFolder{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const ownerQuery = `SELECT id::text FROM chat_folders WHERE user_id = $1::uuid AND id = $2::uuid FOR UPDATE`
	var owned string
	if err := tx.QueryRow(ctx, ownerQuery, strings.TrimSpace(userID), strings.TrimSpace(folderID)).Scan(&owned); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.ChatFolder{}, chat.ErrChatFolderNotFound
		}
		return chat.ChatFolder{}, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM chat_folder_chats WHERE folder_id = $1::uuid`, strings.TrimSpace(folderID)); err != nil {
		return chat.ChatFolder{}, err
	}
	if len(chatIDs) > 0 {
		if err := insertFolderChats(ctx, tx, folderID, chatIDs); err != nil {
			return chat.ChatFolder{}, err
		}
	}

	item, err := getChatFolder(ctx, tx, userID, folderID)
	if err != nil {
		return chat.ChatFolder{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return chat.ChatFolder{}, err
	}
	return item, nil
}

// DeleteChatFolder removes a folder of the given user (the membership rows go
// away through ON DELETE CASCADE). A foreign or missing folder reports
// chat.ErrChatFolderNotFound.
func (r *ChatFolderRepository) DeleteChatFolder(ctx context.Context, userID, folderID string) error {
	const query = `DELETE FROM chat_folders WHERE user_id = $1::uuid AND id = $2::uuid`
	tag, err := r.client.pool.Exec(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(folderID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return chat.ErrChatFolderNotFound
	}
	return nil
}

func insertFolderChats(ctx context.Context, q folderQuerier, folderID string, chatIDs []string) error {
	const query = `
		INSERT INTO chat_folder_chats (folder_id, chat_id, position)
		SELECT $1::uuid, x.chat_id::uuid, x.position
		FROM unnest($2::text[], $3::int[]) AS x(chat_id, position)
	`
	ids := make([]string, len(chatIDs))
	positions := make([]int, len(chatIDs))
	for i, id := range chatIDs {
		ids[i] = id
		positions[i] = i
	}
	_, err := q.Exec(ctx, query, strings.TrimSpace(folderID), ids, positions)
	return err
}

func getChatFolder(ctx context.Context, q folderQuerier, userID, folderID string) (chat.ChatFolder, error) {
	query := chatFolderSelect + `
		WHERE f.user_id = $1::uuid AND f.id = $2::uuid
		GROUP BY f.id
	`
	item, err := scanChatFolder(q.QueryRow(ctx, query, strings.TrimSpace(userID), strings.TrimSpace(folderID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chat.ChatFolder{}, chat.ErrChatFolderNotFound
		}
		return chat.ChatFolder{}, err
	}
	return item, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanChatFolder(row rowScanner) (chat.ChatFolder, error) {
	var item chat.ChatFolder
	if err := row.Scan(&item.ID, &item.Name, &item.Icon, &item.Position, &item.CreatedAt, &item.ChatIDs); err != nil {
		return chat.ChatFolder{}, err
	}
	if item.ChatIDs == nil {
		item.ChatIDs = []string{}
	}
	return item, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
