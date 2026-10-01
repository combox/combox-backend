# boxchat -> combox migration (boxchat-migrate)

CLI: `cmd/boxchat-migrate` (package main). New files only; existing repo
files are untouched. Reuses `internal/config`, `internal/repository/minio`
conventions and the existing `go.mod` deps (`pgx/v5`, `minio-go/v7`,
`uuid`) — no new modules, no pip packages.

## Target mapping (verified against `migrations/*.sql`)

| legacy (boxchat, SQLite) | combox (Postgres + MinIO) | notes |
|---|---|---|
| `user` (39, key `lower(username)`; **no email column**) | `users` (UUID PK, `email NOT NULL UNIQUE`) | match existing by `lower(username)`, fallback `lower(email)`; else create with placeholder email `boxchat-legacy-<id>-<user>@legacy.invalid` + `legacy_username`, `is_legacy_unverified=true`. Intra-legacy `lower()` dups (canon: 2× `wopt2`) map to the SAME combox user as their primary (`merged_users.log`, `users_dup_merged`) so their memberships survive. Passwords are werkzeug-scrypt (162ch) — not verifiable by bcrypt backend → marker hash `!legacy!...`, user must reset password. Bio truncated to 70 chars (auth cap). |
| `room` dm (39, `dm_<a>_<b>`) | `chats` `chat_kind='direct'`, `is_direct=true` | creator = first member else owner/fallback. No owner in legacy DM. |
| `room` server (10) | `chats` `chat_kind='group'`, `chat_type='standard'` | `is_public`/`description` carried; `avatar_url` re-uploaded → `avatar_data_url='s3key:<key>'`. |
| `room` broadcast (6) | `chats` `chat_kind='standalone_channel'` | `public_channel` was renamed to this in `000026`. Comments stay as plain messages (no thread fan-out). |
| `channel` root per room (`main` or lowest id) | parent chat itself | message `channel_id=root` → parent `chat_id`. Mirrors `000023` General→group-root pattern. |
| `channel` non-root (17 = 72−55) | `chats` `chat_kind='channel'`, `parent_chat_id`, `channel_type='text'`, `topic_number>=2` | icon_emoji/description carried; icon_image re-uploaded. |
| `room.invite_token` UNIQUE | `chat_invite_links(token='boxchat-<tok>', is_primary)` | |
| `member` (151, owner34/admin50/member67) | `chat_members(chat_id,user_id,role)` + copies into topic sub-chats | roles pass through (`owner`/`admin`/`member`, else `member`); broadcast rooms map plain members to `subscriber` (`members_normalized_subscriber`); **every parent-room membership is copied into each topic sub-chat** (`topic_members_upserted`) — reading a normal chat requires membership, without the copy all topics (107 msgs in canon) are invisible. `muted_until` all NULL in canon → nothing to do (`public_channel_mutes` stays empty). |
| `role` (106) / `member_role` (237) / `writer_role_ids_json` | **no target table** | best-effort only (admin-ish role names already reflected via `member.role`); definitions skipped → `skipped.log`. |
| `message` (910, ids 1..945 with gaps) | `messages` (UUID, `idempotency_key='boxchat-legacy-<id>'`, `reply_to_message_id` wired in pass 2, `edited_at` kept) | orphans (`reply_to_id` → missing, 2) inserted with `reply_to=NULL`. |
| `message.file_url` → `/uploads/<sub>/<uuid>_<orig>` | `attachments` + `message_attachments` **+ `[[att:<id>\|<file>\|<mime>\|<kind>]]` token appended to `messages.content`** | the token is REQUIRED: the frontend discovers attachments only via `parseMessageContent` and the backend access check (`CanUserAccessAttachment`) joins through `content LIKE '%[[att:<id>\|%'` — rows without the token render as `(empty)` and deny `getAttachment`. Empty-text media messages get `text + "\n" + token`. Re-runs backfill missing tokens from `legacy_id_map(kind='attachment')` without re-upload. Missing/empty/placeholder refs → text-only message + `failed_files.log`; refs with empty text additionally get a `[вложение не перенесено: <name>]` placeholder (`placeholders_written`) so they don't render as `(empty)`. |
| avatars (`user`/`room`), `channel.icon_image_url` | MinIO + `avatar_data_url='s3key:<key>'` (channel icons land on the sub-chat avatar) | backend resolves `s3key:` via presigned GET (see `internal/service/auth`, `chat`, `search`). External URLs (`https://via.placeholder…`) left as-is. Idempotent: re-runs skip rows whose avatar already has the `s3key:boxchat-legacy/` prefix (no orphan re-uploads). |
| `message_reaction` (252 rows → 198 distinct pairs, emoji only, **no timestamp columns**) | `message_reactions(message_id,user_id,emoji,created_at,updated_at)` | target PK keeps **one** emoji per (message,user): 54 dup rows collapse to first → `skipped.log`. Timestamps = parent message time (R3, see below); re-runs backfill pre-fix rows idempotently (`reactions_backfilled`). |
| `read_message` (256 watermarks) | **no target** | combox has `message_statuses` (per-message) + `chat_user_states` (archived/pinned) but no channel watermark → skipped. |
| `friendship` (10) / `friend_request` (16) | **no target** | no contacts/friends table in combox (verified by grep) → skipped. |
| `user_music` (10) | `users.saved_tracks` JSONB (`{id,title,artist,duration,fileSize,fileUrl,addedAt}`, cap 500) | file re-uploaded, `fileUrl='s3key:<key>'`. No separate playlist table. |
| `room_ban` (2, both on broadcast rooms) | `public_channel_bans` for broadcast; server/DM bans have no table | → skipped with log. |
| `sticker`(0)/`sticker_pack`(0)/`auth_throttle`(9)/`schema_migrations` | never migrated | infra/empty. |

MinIO layout (existing bucket, e.g. `chat-media` from `MINIO_BUCKET`):
backend convention is `u/<userID>/<attachmentID>/<filename>` for new
uploads and `chat-avatars/<uuid><ext>` for chat avatars; legacy imports use
a separate prefix so they never collide: `boxchat-legacy/<sub>/<uuid>_<orig>`
(`sub` ∈ avatars, room_avatars, channel_icons, files, music, videos).
Avatar columns store `s3key:<objectKey>` refs (backend presigns on read).

## Order of operations

1. Apply migration `000044` (`users.legacy_username`, `users.is_legacy_unverified`).
2. Dry-run (default, read-only): `go run ./cmd/boxchat-migrate --sqlite <canon> --uploads <uploads> --out-dir ./migrate-logs`.
3. Apply: same + `--apply --pg-dsn ... --minio-endpoint ... --minio-bucket ... --minio-user ... --minio-pass ...`.
4. Re-running `--apply` is safe: `legacy_id_map(kind,legacy_id,new_id)` skips done rows; `chat_members`/`message_reactions`/invites use `ON CONFLICT DO NOTHING`.
   A re-run over a DB migrated by the pre-fix version backfills what's missing
   without re-uploading: `[[att:]]` tokens (from `legacy_id_map`
   `kind='attachment'`), avatars (skipped when `s3key:boxchat-legacy/` already
   set), topic memberships, dup-user maps, broken-empty placeholders,
   **reaction timestamps** (`message_reactions.created_at/updated_at` converged
   to the parent message time for legacy pairs only, R3).

## Timestamps (R3 — bug: migrated data stamped with migration time)

Target date columns (verified via `information_schema`, prod SELECT-only):

| table | date columns |
|---|---|
| `messages` | `created_at NOT NULL DEFAULT now()`, `updated_at NOT NULL DEFAULT now()`, `edited_at NULL` — ETL already writes honest values (`created_at=updated_at=legacy timestamp`, `edited_at` kept). |
| `message_reactions` | `created_at NOT NULL DEFAULT now()`, `updated_at NOT NULL DEFAULT now()` — old ETL omitted both → every migrated reaction stamped with migration time (e.g. Feb-14 message with a "Today" reaction). |
| `chats` | `created_at NOT NULL DEFAULT now()`, `updated_at NOT NULL DEFAULT now()` — **no** `last_message_at` / `last_message_preview` columns exist. Both are derived at read time (`ListChatsByUser`: `LATERAL latest.created_at AS last_message_at`, preview from `latest.content`), so they are already honest wherever messages exist. Chat-list sort is `ORDER BY chats.created_at DESC`. |

Source date columns (verified via `PRAGMA table_info`, python3+sqlite3 read-only):

| legacy table | date columns |
|---|---|
| `message` | `timestamp DATETIME`, `edited_at DATETIME` (all 910 rows carry `timestamp`). |
| `message_reaction` | **none** — `(id, message_id, user_id, emoji, reaction_type)`. Honest reaction dates do not exist in the source. |
| `room` | **none** — no timestamps at all. Chat creation time is unknowable. |

Decisions (no client hacks, ETL-only):

- **Reactions: `created_at = updated_at = parent message `created_at`.**
  Rationale: the only honest anchor available; a reaction cannot predate its
  message, while `now()` is provably false. Fallback chain per reaction:
  exact instant written for the parent message this run → re-derived legacy
  timestamp (deterministic; covers rows migrated by earlier runs).
  Backfill safety: the `UPDATE` targets exact legacy `(message_id,user_id)`
  pairs, which can only originate from this migration (post-migration
  same-pair inserts are PK no-ops) — genuine new user reactions always carry
  non-legacy pairs and are untouched. Converged rows no-op on repeat runs.
- **Chats: `created_at` left at DB default (not invented).**
  Any creation time would be fabrication (`room` has no timestamps); stamping
  message-derived times into it would pretend knowledge we don't have.
  `updated_at` intentionally untouched: nothing in the read path uses it
  (list query neither selects nor sorts by it; it is only bumped on manual
  chat-settings edits), so rewriting it would be churn without effect.
- **Empty chats (canon: 21 rooms with zero migratable messages): no anchor.**
  Derived `last_message_at` stays `NULL`; if the client falls back to
  `chats.created_at` there, no honest ETL value can change that — documented,
  not faked.
- **Chat-list ordering** (`ORDER BY chats.created_at` groups all migrated
  chats under one migration timestamp) can only be fixed in the backend list
  query (out of scope for this tool) — flagged for the owner, not worked
  around here.

## SQLite without a Go driver

`go.mod` has no SQLite driver and editing it is out of scope, so legacy
reads go through a `python3` stdlib bridge (`sqlite3`, `mode=ro` URI,
`SELECT`-only guard, JSON over stdout). No pip deps, no temp files.
Apply-mode Postgres uses `pgx/v5` (already in `go.mod`), MinIO uses
`minio-go/v7` (+ `internal/repository/minio` bucket-ensure convention).

## Logs

`--out-dir` (default `.`): `merged_users.log` (planned `lower(username)`
mapping), `failed_files.log` (missing/empty/placeholder refs),
`skipped.log` (entity → reason). Stdout prints the per-entity report.
