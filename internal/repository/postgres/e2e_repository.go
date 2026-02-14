package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"combox-backend/internal/service/e2e"

	"github.com/jackc/pgx/v5"
)

type E2ERepository struct {
	client *Client
}

func NewE2ERepository(client *Client) *E2ERepository {
	return &E2ERepository{client: client}
}

func (r *E2ERepository) UpsertDeviceKeys(ctx context.Context, in e2e.UpsertDeviceKeysInput) (e2e.Device, error) {
	tx, err := r.client.pool.Begin(ctx)
	if err != nil {
		return e2e.Device{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertDevice = `
		INSERT INTO e2e_devices (device_id, user_id, identity_key, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, NOW())
		ON CONFLICT (device_id) DO UPDATE
		SET user_id = EXCLUDED.user_id,
		    identity_key = EXCLUDED.identity_key,
		    updated_at = NOW()
		RETURNING device_id::text, user_id::text, identity_key, updated_at
	`

	var out e2e.Device
	if err := tx.QueryRow(ctx, upsertDevice, in.DeviceID, in.UserID, strings.TrimSpace(in.IdentityKey)).
		Scan(&out.DeviceID, &out.UserID, &out.IdentityKey, &out.UpdatedAt); err != nil {
		return e2e.Device{}, fmt.Errorf("upsert device: %w", err)
	}

	const upsertSignedPrekey = `
		INSERT INTO e2e_signed_prekeys (device_id, key_id, public_key, signature)
		VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (device_id, key_id) DO UPDATE
		SET public_key = EXCLUDED.public_key,
		    signature = EXCLUDED.signature,
		    created_at = NOW()
	`

	if _, err := tx.Exec(ctx, upsertSignedPrekey, in.DeviceID, in.SignedPreKey.KeyID, strings.TrimSpace(in.SignedPreKey.PublicKey), strings.TrimSpace(in.SignedPreKey.Signature)); err != nil {
		return e2e.Device{}, fmt.Errorf("upsert signed prekey: %w", err)
	}

	const insertPrekey = `
		INSERT INTO e2e_one_time_prekeys (device_id, key_id, public_key)
		VALUES ($1::uuid, $2, $3)
		ON CONFLICT (device_id, key_id) DO NOTHING
	`
	for _, pk := range in.OneTimePreKeys {
		if pk.KeyID <= 0 {
			continue
		}
		if strings.TrimSpace(pk.PublicKey) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, insertPrekey, in.DeviceID, pk.KeyID, strings.TrimSpace(pk.PublicKey)); err != nil {
			return e2e.Device{}, fmt.Errorf("insert one-time prekey: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return e2e.Device{}, fmt.Errorf("commit tx: %w", err)
	}
	return out, nil
}

func (r *E2ERepository) ListUserDevices(ctx context.Context, userID string) ([]e2e.DeviceSummary, error) {
	const query = `
		SELECT device_id::text, identity_key
		FROM e2e_devices
		WHERE user_id = $1::uuid
		ORDER BY updated_at DESC
	`

	rows, err := r.client.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]e2e.DeviceSummary, 0)
	for rows.Next() {
		var item e2e.DeviceSummary
		if err := rows.Scan(&item.DeviceID, &item.IdentityKey); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *E2ERepository) ClaimPreKeyBundle(ctx context.Context, userID, deviceID string) (e2e.PreKeyBundle, error) {
	tx, err := r.client.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return e2e.PreKeyBundle{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const selectDevice = `
		SELECT user_id::text, device_id::text, identity_key
		FROM e2e_devices
		WHERE user_id = $1::uuid AND device_id = $2::uuid
		LIMIT 1
	`
	var bundle e2e.PreKeyBundle
	if err := tx.QueryRow(ctx, selectDevice, userID, deviceID).Scan(&bundle.UserID, &bundle.DeviceID, &bundle.IdentityKey); err != nil {
		if err == pgx.ErrNoRows {
			return e2e.PreKeyBundle{}, nil
		}
		return e2e.PreKeyBundle{}, fmt.Errorf("select device: %w", err)
	}

	const selectSigned = `
		SELECT key_id, public_key, signature
		FROM e2e_signed_prekeys
		WHERE device_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT 1
	`
	if err := tx.QueryRow(ctx, selectSigned, deviceID).Scan(&bundle.SignedPreKey.KeyID, &bundle.SignedPreKey.PublicKey, &bundle.SignedPreKey.Signature); err != nil {
		if err == pgx.ErrNoRows {
			return e2e.PreKeyBundle{}, fmt.Errorf("missing signed prekey")
		}
		return e2e.PreKeyBundle{}, fmt.Errorf("select signed prekey: %w", err)
	}

	// Consume one unconsumed prekey (if any). This is safe for concurrency.
	const selectOneTime = `
		SELECT key_id, public_key
		FROM e2e_one_time_prekeys
		WHERE device_id = $1::uuid AND consumed_at IS NULL
		ORDER BY created_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`
	var ot e2e.OneTimePreKey
	if err := tx.QueryRow(ctx, selectOneTime, deviceID).Scan(&ot.KeyID, &ot.PublicKey); err == nil {
		now := time.Now().UTC()
		const markConsumed = `
			UPDATE e2e_one_time_prekeys
			SET consumed_at = $3
			WHERE device_id = $1::uuid AND key_id = $2
		`
		if _, err := tx.Exec(ctx, markConsumed, deviceID, ot.KeyID, now); err != nil {
			return e2e.PreKeyBundle{}, fmt.Errorf("consume one-time prekey: %w", err)
		}
		bundle.OneTimePreKey = &ot
	} else if err != pgx.ErrNoRows {
		return e2e.PreKeyBundle{}, fmt.Errorf("select one-time prekey: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return e2e.PreKeyBundle{}, fmt.Errorf("commit tx: %w", err)
	}
	return bundle, nil
}

func (r *E2ERepository) UpsertUserKeyBackup(ctx context.Context, in e2e.UpsertUserKeyBackupInput) (e2e.UserKeyBackup, error) {
	const query = `
		INSERT INTO e2e_user_key_backups (user_id, alg, kdf, salt, params, ciphertext, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6, NOW())
		ON CONFLICT (user_id) DO UPDATE
		SET alg = EXCLUDED.alg,
		    kdf = EXCLUDED.kdf,
		    salt = EXCLUDED.salt,
		    params = EXCLUDED.params,
		    ciphertext = EXCLUDED.ciphertext,
		    updated_at = NOW()
		RETURNING user_id::text, alg, kdf, salt, params::text, ciphertext, updated_at
	`

	var out e2e.UserKeyBackup
	var paramsText string
	if err := r.client.pool.QueryRow(
		ctx,
		query,
		strings.TrimSpace(in.UserID),
		strings.TrimSpace(in.Alg),
		strings.TrimSpace(in.KDF),
		strings.TrimSpace(in.Salt),
		string(in.Params),
		strings.TrimSpace(in.Ciphertext),
	).Scan(&out.UserID, &out.Alg, &out.KDF, &out.Salt, &paramsText, &out.Ciphertext, &out.UpdatedAt); err != nil {
		return e2e.UserKeyBackup{}, err
	}
	out.Params = []byte(paramsText)
	return out, nil
}

func (r *E2ERepository) GetUserKeyBackup(ctx context.Context, userID string) (e2e.UserKeyBackup, bool, error) {
	const query = `
		SELECT user_id::text, alg, kdf, salt, params::text, ciphertext, updated_at
		FROM e2e_user_key_backups
		WHERE user_id = $1::uuid
		LIMIT 1
	`

	var out e2e.UserKeyBackup
	var paramsText string
	err := r.client.pool.QueryRow(ctx, query, strings.TrimSpace(userID)).Scan(
		&out.UserID,
		&out.Alg,
		&out.KDF,
		&out.Salt,
		&paramsText,
		&out.Ciphertext,
		&out.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return e2e.UserKeyBackup{}, false, nil
		}
		return e2e.UserKeyBackup{}, false, err
	}
	out.Params = []byte(paramsText)
	return out, true, nil
}
