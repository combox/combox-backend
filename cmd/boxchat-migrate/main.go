// Command boxchat-migrate migrates legacy boxchat (SQLite + uploads/ dir)
// into combox (Postgres + MinIO S3).
//
// MIGRATION ETL: legacy boxchat -> combox. Read-only by default.
//
// WHAT IT DOES
//  1. Reads legacy SQLite (via python3 stdlib sqlite3 bridge, read-only
//     mode=ro URI, SELECT-only guard) and walks the legacy uploads/ dir.
//  2. Plans the mapping legacy INTEGER ids -> new UUIDs (in-memory maps +
//     persistent Postgres table legacy_id_map for restart idempotency).
//  3. In --apply mode writes to Postgres (batches, transactions) and
//     uploads media to the MinIO bucket under boxchat-legacy/<sub>/...,
//     then rewrites all links (message file_url, avatars) to the new keys.
//  4. Writes local logs (merged_users.log, failed_files.log, skipped.log)
//     next to --out-dir and prints a stdout summary report.
//
// MODES
//
//	--dry-run is the DEFAULT (flag --apply=false). Dry-run opens NOTHING
//	except the legacy SQLite file (read-only) and the uploads dir
//	(read-only). It never connects to Postgres or MinIO and never writes
//	to any database or bucket. Use it for inspection:
//	  go run ./cmd/boxchat-migrate --sqlite <canon.db> --uploads <uploads/>
//	Apply mode (writes!) requires the explicit flag plus connection config:
//	  go run ./cmd/boxchat-migrate --apply \
//	    --sqlite <canon.db> --uploads <uploads/> \
//	    --pg-dsn 'postgres://...?sslmode=disable' \
//	    --minio-endpoint minio:9000 --minio-bucket chat-media \
//	    --minio-user USER --minio-pass PASS
//	Env fallback is supported: POSTGRES_DSN, MINIO_API_INTERNAL,
//	MINIO_BUCKET, MINIO_ROOT_USER, MINIO_ROOT_PASSWORD, MINIO_SECURE.
//
// PREREQUISITE
//
//	Migration 000044 (users.legacy_username, users.is_legacy_unverified)
//	must be applied BEFORE --apply. The tool probes information_schema for
//	these columns and aborts apply with a clear error if they are missing.
//	INSERTs of legacy users always go through the legacy-columns path; the
//	email column in combox is NOT NULL UNIQUE, so new users get a synthetic
//	placeholder email (see placeholderEmail) + is_legacy_unverified=true.
//	Legacy werkzeug-scrypt password hashes are NOT portable to the bcrypt
//	backend, so migrated users get an unusable marker hash and must use
//	password reset.
//
// IDEMPOTENCY / RESTARTS
//
//	Apply mode creates (if missing):
//	  legacy_id_map(kind TEXT, legacy_id BIGINT, new_id UUID,
//	                PRIMARY KEY(kind, legacy_id))
//	Every created row registers here. A repeated --apply run reads the map
//	first and skips kinds/ids already present. Message inserts use a stable
//	idempotency_key ('boxchat-legacy-<id>'); chat_members use
//	ON CONFLICT DO NOTHING / DO UPDATE.
//	Attachment visibility depends on [[att:...]] tokens embedded in
//	messages.content (frontend parseMessageContent + backend
//	CanUserAccessAttachment both key off the token), so applyAttachments
//	appends the token to the message content right after the upload+link
//	(and backfills it on re-runs when the map row exists but the token is
//	missing). Avatar steps are idempotent via the 's3key:boxchat-legacy/'
//	prefix check on avatar_data_url (no re-upload when already set).
//	Avatar history (profile_photos, R9b) is backfilled idempotently by
//	(owner_kind, owner_id, object_key) existence check: every
//	boxchat-legacy avatar gets exactly one history row, created_at =
//	migration time (legacy has no avatar timestamp; the viewer labels such
//	rows 'from boxchat' instead of stating a false install date).
//	Topic sub-chats inherit parent-room memberships (copied with the same
//	roles, ON CONFLICT DO NOTHING). Intra-legacy lower(username) dups map
//	to the same combox user as their primary (kind='user' map row), so
//	their memberships/messages/reactions are preserved too. Re-runs also
//	converge message_reactions.created_at/updated_at to the parent message
//	time (R3 backfill: only legacy pairs, idempotent).
//
// BATCHING
//
//	--batch N (default 200): rows per Postgres transaction for users,
//	chats, messages, reactions and attachments.
//
// EXIT CODES: 0 ok, 1 usage/config error, 2 legacy read error, 3 apply error.
//
// See MIGRATE_BOXCHAT.md next to this file for the full table mapping.
// TIMESTAMP POLICY (R3).
//
// Legacy message_reaction has NO timestamp columns (PRAGMA table_info:
// id, message_id, user_id, emoji, reaction_type) — honest reaction dates do
// not exist in the source. Target message_reactions.created_at/updated_at
// are NOT NULL DEFAULT now(), so omitting them stamps migration time (bug:
// Feb messages with "Today" reactions). The only honest anchor is the
// parent message timestamp (a reaction cannot predate its message; now() is
// false), so ETL writes created_at=updated_at=parent message created_at on
// insert and converges pre-fix rows to the same value on re-runs.
// Legacy room likewise has no timestamps, so chats.created_at is left at the
// DB default (not invented); target has no last_message_at/preview columns
// (derived at read from messages) and chat-list sort is ORDER BY
// chats.created_at, so chats.updated_at is intentionally untouched. See
// MIGRATE_BOXCHAT.md ("Timestamps (R3)") for the full rationale.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"combox-backend/internal/config"
	miniorepo "combox-backend/internal/repository/minio"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ---------------------------------------------------------------------------
// CLI flags & config
// ---------------------------------------------------------------------------

type cliConfig struct {
	sqlite        string
	uploads       string
	outDir        string
	apply         bool
	batch         int
	pgDSN         string
	minioEndpoint string
	minioBucket   string
	minioUser     string
	minioPass     string
	minioSecure   bool
	minioRegion   string
}

func parseFlags() cliConfig {
	var c cliConfig
	flag.StringVar(&c.sqlite, "sqlite", "/home/d7tun6/files/mounts/TS480SSD/services/boxchat/thecomboxmsgr.db", "path to canonical legacy SQLite file (read-only)")
	flag.StringVar(&c.uploads, "uploads", "/home/d7tun6/files/mounts/TS480SSD/services/boxchat/uploads", "path to legacy uploads dir (read-only walk)")
	flag.StringVar(&c.outDir, "out-dir", ".", "directory for merged_users.log / failed_files.log / skipped.log")
	flag.BoolVar(&c.apply, "apply", false, "write to Postgres + MinIO (default false = dry-run, no writes)")
	flag.IntVar(&c.batch, "batch", 200, "rows per transaction in apply mode")
	flag.StringVar(&c.pgDSN, "pg-dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN (apply mode only)")
	flag.StringVar(&c.minioEndpoint, "minio-endpoint", os.Getenv("MINIO_API_INTERNAL"), "MinIO endpoint host:port (apply mode only)")
	flag.StringVar(&c.minioBucket, "minio-bucket", os.Getenv("MINIO_BUCKET"), "MinIO bucket (apply mode only)")
	flag.StringVar(&c.minioUser, "minio-user", os.Getenv("MINIO_ROOT_USER"), "MinIO user (apply mode only)")
	flag.StringVar(&c.minioPass, "minio-pass", os.Getenv("MINIO_ROOT_PASSWORD"), "MinIO password (apply mode only)")
	flag.BoolVar(&c.minioSecure, "minio-secure", strings.EqualFold(os.Getenv("MINIO_SECURE"), "true"), "use TLS for MinIO")
	flag.StringVar(&c.minioRegion, "minio-region", envOr("MINIO_REGION", "us-east-1"), "MinIO region")
	flag.Parse()
	if c.batch < 1 {
		c.batch = 1
	}
	if c.batch > 1000 {
		c.batch = 1000
	}
	return c
}

func envOr(k, fb string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return fb
}

// ---------------------------------------------------------------------------
// Legacy SQLite bridge (python3 stdlib, read-only).
// ---------------------------------------------------------------------------
//
// Rationale: combox-backend/go.mod has no SQLite driver (pgx + minio-go +
// uuid only), and adding one would require editing go.mod (forbidden).
// python3 with stdlib sqlite3 is always available (verified: sqlite 3.53.3).
// The bridge opens the DB with mode=ro URI and only allows SELECT/WITH.

const pyBridge = `
import json, sqlite3, sys
db_path, sql = sys.argv[1], sys.argv[2]
s = sql.strip().lower()
assert s.startswith("select") or s.startswith("with"), "only SELECT allowed"
con = sqlite3.connect("file:" + db_path + "?mode=ro", uri=True)
con.row_factory = sqlite3.Row
cur = con.cursor()
cur.execute(sql)
cols = [d[0] for d in cur.description] if cur.description else []
out = []
for r in cur.fetchall():
    out.append({c: r[c] for c in cols})
con.close()
sys.stdout.write(json.dumps(out, ensure_ascii=False))
`

func fetchAll(sqlitePath, sql string) ([]map[string]any, error) {
	t := strings.TrimSpace(strings.ToLower(sql))
	if !strings.HasPrefix(t, "select") && !strings.HasPrefix(t, "with") {
		return nil, fmt.Errorf("refusing non-SELECT query")
	}
	if _, err := os.Stat(sqlitePath); err != nil {
		return nil, fmt.Errorf("sqlite file: %w", err)
	}
	cmd := exec.Command("python3", "-c", pyBridge, sqlitePath, sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("python3 bridge: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	var rows []map[string]any
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode bridge JSON: %w", err)
	}
	return rows, nil
}

func asInt(v any) int64 {
	switch t := v.(type) {
	case nil:
		return 0
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	default:
		return 0
	}
}

func asStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func asBool(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		l := strings.ToLower(strings.TrimSpace(t))
		return l == "1" || l == "true" || l == "t"
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Legacy entity structs
// ---------------------------------------------------------------------------

type legacyUser struct {
	id       int64
	username string
	password string
	bio      string
	avatar   string
	birth    string
	presence string
	lastSeen string
	super    bool
	banned   bool
	banWhy   string
}

type legacyRoom struct {
	id       int64
	name     string
	typ      string // dm|server|broadcast
	isPublic bool
	ownerID  int64
	hasOwner bool
	avatar   string
	invite   string
	desc     string
	banner   string
}

type legacyChannel struct {
	id      int64
	name    string
	roomID  int64
	desc    string
	emoji   string
	iconImg string
	writers string
}

type legacyMember struct {
	id     int64
	userID int64
	roomID int64
	role   string
	muted  string
}

type legacyMessage struct {
	id        int64
	content   string
	ts        string
	edited    string
	userID    int64
	hasUser   bool
	channelID int64
	mtype     string
	fileURL   string
	fileName  string
	fileSize  int64
	replyTo   int64
	hasReply  bool
}

type legacyReaction struct {
	id    int64
	msgID int64
	user  int64
	emoji string
}

type legacyRead struct {
	id     int64
	user   int64
	chanID int64
	lastID int64
}

type legacyMusic struct {
	id     int64
	user   int64
	title  string
	artist string
	file   string
	cover  string
}

type legacyBan struct {
	id     int64
	room   int64
	user   int64
	byID   int64
	hasBy  bool
	reason string
}

// ---------------------------------------------------------------------------
// Loading legacy (read-only)
// ---------------------------------------------------------------------------

type legacyDB struct {
	users      []legacyUser
	rooms      []legacyRoom
	channels   []legacyChannel
	members    []legacyMember
	messages   []legacyMessage
	reactions  []legacyReaction
	reads      []legacyRead
	music      []legacyMusic
	bans       []legacyBan
	friendN    int
	friendReqN map[string]int
	roleN      int
	mroleN     int
	usersByID  map[int64]legacyUser
	roomsByID  map[int64]legacyRoom
	chansByID  map[int64]legacyChannel
	msgsByID   map[int64]legacyMessage
}

func loadLegacy(sqlite string) (*legacyDB, error) {
	db := &legacyDB{
		usersByID: map[int64]legacyUser{},
		roomsByID: map[int64]legacyRoom{},
		chansByID: map[int64]legacyChannel{},
		msgsByID:  map[int64]legacyMessage{},
	}
	var err error
	if db.users, err = loadUsers(sqlite); err != nil {
		return nil, err
	}
	if db.rooms, err = loadRooms(sqlite); err != nil {
		return nil, err
	}
	if db.channels, err = loadChannels(sqlite); err != nil {
		return nil, err
	}
	if db.members, err = loadMembers(sqlite); err != nil {
		return nil, err
	}
	if db.messages, err = loadMessages(sqlite); err != nil {
		return nil, err
	}
	if db.reactions, err = loadReactions(sqlite); err != nil {
		return nil, err
	}
	if db.reads, err = loadReads(sqlite); err != nil {
		return nil, err
	}
	if db.music, err = loadMusic(sqlite); err != nil {
		return nil, err
	}
	if db.bans, err = loadBans(sqlite); err != nil {
		return nil, err
	}
	if db.friendN, db.friendReqN, err = loadFriends(sqlite); err != nil {
		return nil, err
	}
	if db.roleN, db.mroleN, err = loadRoleCounts(sqlite); err != nil {
		return nil, err
	}
	for _, u := range db.users {
		db.usersByID[u.id] = u
	}
	for _, r := range db.rooms {
		db.roomsByID[r.id] = r
	}
	for _, c := range db.channels {
		db.chansByID[c.id] = c
	}
	for _, m := range db.messages {
		db.msgsByID[m.id] = m
	}
	return db, nil
}

func loadUsers(sqlite string) ([]legacyUser, error) {
	rows, err := fetchAll(sqlite, `SELECT id,username,password,bio,avatar_url,birth_date,presence_status,last_seen,is_superuser,is_banned,ban_reason FROM user ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyUser{
			id: asInt(r["id"]), username: asStr(r["username"]), password: asStr(r["password"]),
			bio: asStr(r["bio"]), avatar: asStr(r["avatar_url"]), birth: asStr(r["birth_date"]),
			presence: asStr(r["presence_status"]), lastSeen: asStr(r["last_seen"]),
			super: asBool(r["is_superuser"]), banned: asBool(r["is_banned"]), banWhy: asStr(r["ban_reason"]),
		})
	}
	return out, nil
}

func loadRooms(sqlite string) ([]legacyRoom, error) {
	rows, err := fetchAll(sqlite, `SELECT id,name,type,is_public,owner_id,avatar_url,invite_token,linked_chat_id,description,banner_url FROM room ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyRoom, 0, len(rows))
	for _, r := range rows {
		owner := r["owner_id"]
		oid, has := int64(0), owner != nil
		if has {
			oid = asInt(owner)
		}
		out = append(out, legacyRoom{
			id: asInt(r["id"]), name: asStr(r["name"]), typ: asStr(r["type"]),
			isPublic: asBool(r["is_public"]), ownerID: oid, hasOwner: has,
			avatar: asStr(r["avatar_url"]), invite: asStr(r["invite_token"]),
			desc: asStr(r["description"]), banner: asStr(r["banner_url"]),
		})
	}
	return out, nil
}

func loadChannels(sqlite string) ([]legacyChannel, error) {
	rows, err := fetchAll(sqlite, `SELECT id,name,room_id,description,icon_emoji,icon_image_url,writer_role_ids_json FROM channel ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyChannel, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyChannel{
			id: asInt(r["id"]), name: asStr(r["name"]), roomID: asInt(r["room_id"]),
			desc: asStr(r["description"]), emoji: asStr(r["icon_emoji"]),
			iconImg: asStr(r["icon_image_url"]), writers: asStr(r["writer_role_ids_json"]),
		})
	}
	return out, nil
}

func loadMembers(sqlite string) ([]legacyMember, error) {
	rows, err := fetchAll(sqlite, `SELECT id,user_id,room_id,role,muted_until FROM member ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyMember, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyMember{
			id: asInt(r["id"]), userID: asInt(r["user_id"]), roomID: asInt(r["room_id"]),
			role: asStr(r["role"]), muted: asStr(r["muted_until"]),
		})
	}
	return out, nil
}

func loadMessages(sqlite string) ([]legacyMessage, error) {
	rows, err := fetchAll(sqlite, `SELECT id,content,timestamp,edited_at,user_id,channel_id,message_type,file_url,file_name,file_size,reply_to_id FROM message ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyMessage, 0, len(rows))
	for _, r := range rows {
		uid, hasU := int64(0), r["user_id"] != nil
		if hasU {
			uid = asInt(r["user_id"])
		}
		rt, hasR := int64(0), r["reply_to_id"] != nil
		if hasR {
			rt = asInt(r["reply_to_id"])
		}
		out = append(out, legacyMessage{
			id: asInt(r["id"]), content: asStr(r["content"]), ts: asStr(r["timestamp"]),
			edited: asStr(r["edited_at"]), userID: uid, hasUser: hasU,
			channelID: asInt(r["channel_id"]), mtype: asStr(r["message_type"]),
			fileURL: asStr(r["file_url"]), fileName: asStr(r["file_name"]),
			fileSize: asInt(r["file_size"]), replyTo: rt, hasReply: hasR,
		})
	}
	return out, nil
}

func loadReactions(sqlite string) ([]legacyReaction, error) {
	rows, err := fetchAll(sqlite, `SELECT id,message_id,user_id,emoji FROM message_reaction ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyReaction, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyReaction{id: asInt(r["id"]), msgID: asInt(r["message_id"]), user: asInt(r["user_id"]), emoji: asStr(r["emoji"])})
	}
	return out, nil
}

func loadReads(sqlite string) ([]legacyRead, error) {
	rows, err := fetchAll(sqlite, `SELECT id,user_id,channel_id,last_read_message_id FROM read_message ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyRead{id: asInt(r["id"]), user: asInt(r["user_id"]), chanID: asInt(r["channel_id"]), lastID: asInt(r["last_read_message_id"])})
	}
	return out, nil
}

func loadMusic(sqlite string) ([]legacyMusic, error) {
	rows, err := fetchAll(sqlite, `SELECT id,user_id,title,artist,file_url,cover_url FROM user_music ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyMusic, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyMusic{
			id: asInt(r["id"]), user: asInt(r["user_id"]), title: asStr(r["title"]),
			artist: asStr(r["artist"]), file: asStr(r["file_url"]), cover: asStr(r["cover_url"]),
		})
	}
	return out, nil
}

func loadBans(sqlite string) ([]legacyBan, error) {
	rows, err := fetchAll(sqlite, `SELECT id,room_id,user_id,banned_by_id,reason FROM room_ban ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]legacyBan, 0, len(rows))
	for _, r := range rows {
		by, has := int64(0), r["banned_by_id"] != nil
		if has {
			by = asInt(r["banned_by_id"])
		}
		out = append(out, legacyBan{id: asInt(r["id"]), room: asInt(r["room_id"]), user: asInt(r["user_id"]), byID: by, hasBy: has, reason: asStr(r["reason"])})
	}
	return out, nil
}

func loadFriends(sqlite string) (int, map[string]int, error) {
	n := 0
	rows, err := fetchAll(sqlite, `SELECT COUNT(*) AS c FROM friendship`)
	if err != nil {
		return 0, nil, err
	}
	if len(rows) > 0 {
		n = int(asInt(rows[0]["c"]))
	}
	m := map[string]int{}
	rows2, err := fetchAll(sqlite, `SELECT status,COUNT(*) AS c FROM friend_request GROUP BY status`)
	if err != nil {
		return n, m, err
	}
	for _, r := range rows2 {
		m[asStr(r["status"])] = int(asInt(r["c"]))
	}
	return n, m, nil
}

func loadRoleCounts(sqlite string) (int, int, error) {
	a, err := fetchAll(sqlite, `SELECT COUNT(*) AS c FROM role`)
	if err != nil {
		return 0, 0, err
	}
	b, err := fetchAll(sqlite, `SELECT COUNT(*) AS c FROM member_role`)
	if err != nil {
		return 0, 0, err
	}
	return int(asInt(a[0]["c"])), int(asInt(b[0]["c"])), nil
}

// ---------------------------------------------------------------------------
// Mapping plan (shared dry-run + apply)
// ---------------------------------------------------------------------------

type plan struct {
	// users
	userNew      []legacyUser    // to create (dedup key unique in dry-run)
	userDupInner int             // lower(username) collisions inside legacy
	userDupMap   map[int64]int64 // dup legacy user id -> primary legacy user id (same lower(username), first wins)

	// chats
	chatKindOf  map[int64]string // roomID -> target chat_kind
	rootChanOf  map[int64]int64  // roomID -> root channelID (goes to parent chat)
	topicChans  []legacyChannel  // non-root channels -> sub-chats (chat_kind='channel')
	nDirect     int
	nGroup      int
	nStandalone int
	// topicMemberJobs: planned (sub-chat, user) membership copies from parent rooms.
	topicMemberJobs int

	// messages
	msgOK          int
	msgNoUser      int
	msgNoChannel   int
	msgReplyOK     int
	msgReplyOrphan int

	// attachments / media
	attachOK     []legacyMessage // message has usable file_url + file exists
	attachBroken []brokenFile
	brokenEmpty  []legacyMessage // attach-broken messages with empty content -> placeholder text
	avatarRefs   []avatarRef
	channelIcons []avatarRef
	musicOK      int
	musicBroken  []brokenFile

	// reactions
	reactOK   int
	reactSkip int // (message,user) dups beyond first

	// skipped (no target)
	skippedReads   int
	skippedFriends int
	skippedFreq    int
	skippedRoles   int
	skippedMRoles  int
}

type brokenFile struct {
	where string // e.g. "message:84"
	url   string
	why   string
}

type avatarRef struct {
	where string // e.g. "user:2"
	url   string
}

func isLocalUpload(u string) bool {
	return strings.HasPrefix(u, "/uploads/")
}

func localPath(uploadsBase, url string) string {
	rel := strings.TrimPrefix(url, "/")
	return filepath.Join(uploadsBase, strings.TrimPrefix(rel, "uploads/"))
}

func buildPlan(db *legacyDB, uploadsBase string) *plan {
	p := &plan{
		chatKindOf: map[int64]string{},
		rootChanOf: map[int64]int64{},
		userDupMap: map[int64]int64{},
	}
	// --- users: intra-legacy dedup on lower(username), first id wins ---
	seen := map[string]int64{}
	for _, u := range db.users {
		k := strings.ToLower(strings.TrimSpace(u.username))
		if prev, ok := seen[k]; ok {
			p.userDupInner++
			p.userDupMap[u.id] = prev
			continue
		}
		seen[k] = u.id
		p.userNew = append(p.userNew, u)
	}
	// --- rooms -> chat kinds ---
	for _, r := range db.rooms {
		switch strings.ToLower(strings.TrimSpace(r.typ)) {
		case "dm":
			p.chatKindOf[r.id] = "direct"
			p.nDirect++
		case "broadcast":
			p.chatKindOf[r.id] = "standalone_channel"
			p.nStandalone++
		default: // "server" and anything unknown -> group
			p.chatKindOf[r.id] = "group"
			p.nGroup++
		}
	}
	// --- channels: root = 'main' (case-insensitive) else lowest id ---
	byRoom := map[int64][]legacyChannel{}
	for _, c := range db.channels {
		byRoom[c.roomID] = append(byRoom[c.roomID], c)
	}
	for roomID, list := range byRoom {
		sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
		root := list[0].id
		for _, c := range list {
			if strings.EqualFold(strings.TrimSpace(c.name), "main") {
				root = c.id
				break
			}
		}
		p.rootChanOf[roomID] = root
		for _, c := range list {
			if c.id != root {
				p.topicChans = append(p.topicChans, c)
			}
		}
	}
	// --- topic memberships: sub-chats inherit every parent-room member ---
	// (reading a normal chat requires membership; without this copy all
	// topic sub-chats stay invisible with their history).
	membersByRoom := map[int64]int{}
	for _, m := range db.members {
		membersByRoom[m.roomID]++
	}
	for _, c := range p.topicChans {
		p.topicMemberJobs += membersByRoom[c.roomID]
	}
	// --- messages ---
	msgIDs := map[int64]bool{}
	for _, m := range db.messages {
		msgIDs[m.id] = true
	}
	for _, m := range db.messages {
		if _, ok := db.chansByID[m.channelID]; !ok {
			p.msgNoChannel++
			continue
		}
		if !m.hasUser {
			p.msgNoUser++
			continue
		}
		if _, ok := db.usersByID[m.userID]; !ok {
			p.msgNoUser++
			continue
		}
		p.msgOK++
		if m.hasReply {
			if msgIDs[m.replyTo] {
				p.msgReplyOK++
			} else {
				p.msgReplyOrphan++
			}
		}
		// attachments
		fu := strings.TrimSpace(m.fileURL)
		if fu == "" || fu == "google.com/search" {
			if m.mtype != "text" {
				p.attachBroken = append(p.attachBroken, brokenFile{
					where: fmt.Sprintf("message:%d", m.id), url: m.fileURL,
					why: "empty-or-placeholder file_url with non-text message_type=" + m.mtype,
				})
			}
			continue
		}
		if !isLocalUpload(fu) {
			p.attachBroken = append(p.attachBroken, brokenFile{where: fmt.Sprintf("message:%d", m.id), url: fu, why: "non-local URL (external/placeholder), kept as text only"})
			continue
		}
		fp := localPath(uploadsBase, fu)
		st, err := os.Stat(fp)
		if err != nil || st.IsDir() || st.Size() == 0 {
			why := "file missing on disk"
			if err == nil && st.Size() == 0 {
				why = "file empty on disk"
			}
			p.attachBroken = append(p.attachBroken, brokenFile{where: fmt.Sprintf("message:%d", m.id), url: fu, why: why})
			continue
		}
		p.attachOK = append(p.attachOK, m)
	}
	// text messages with NULL file_url are fine (no attachment expected).
	// --- broken refs on messages with empty content: remember for placeholder ---
	for _, b := range p.attachBroken {
		var mid int64
		if _, err := fmt.Sscanf(b.where, "message:%d", &mid); err != nil {
			continue
		}
		lm, ok := db.msgsByID[mid]
		if !ok {
			continue
		}
		if strings.TrimSpace(lm.content) == "" {
			p.brokenEmpty = append(p.brokenEmpty, lm)
		}
	}
	// --- avatars / icons ---
	for _, u := range db.users {
		a := strings.TrimSpace(u.avatar)
		if isLocalUpload(a) {
			fp := localPath(uploadsBase, a)
			if st, err := os.Stat(fp); err != nil || st.IsDir() {
				p.attachBroken = append(p.attachBroken, brokenFile{where: fmt.Sprintf("user-avatar:%d", u.id), url: a, why: "avatar file missing on disk"})
			} else {
				p.avatarRefs = append(p.avatarRefs, avatarRef{where: fmt.Sprintf("user:%d", u.id), url: a})
			}
		}
	}
	for _, r := range db.rooms {
		for _, av := range []struct {
			where string
			url   string
		}{
			{fmt.Sprintf("room:%d", r.id), r.avatar},
		} {
			a := strings.TrimSpace(av.url)
			if isLocalUpload(a) {
				fp := localPath(uploadsBase, a)
				if st, err := os.Stat(fp); err != nil || st.IsDir() {
					p.attachBroken = append(p.attachBroken, brokenFile{where: "room-avatar:" + fmt.Sprint(r.id), url: a, why: "room avatar missing on disk"})
				} else {
					p.avatarRefs = append(p.avatarRefs, avatarRef{where: av.where, url: a})
				}
			}
		}
	}
	for _, c := range db.channels {
		a := strings.TrimSpace(c.iconImg)
		if isLocalUpload(a) {
			fp := localPath(uploadsBase, a)
			if st, err := os.Stat(fp); err != nil || st.IsDir() {
				p.attachBroken = append(p.attachBroken, brokenFile{where: fmt.Sprintf("channel-icon:%d", c.id), url: a, why: "channel icon missing on disk"})
			} else {
				p.channelIcons = append(p.channelIcons, avatarRef{where: fmt.Sprintf("channel:%d", c.id), url: a})
			}
		}
	}
	// --- music -> saved_tracks ---
	for _, mu := range db.music {
		f := strings.TrimSpace(mu.file)
		if !isLocalUpload(f) {
			p.musicBroken = append(p.musicBroken, brokenFile{where: fmt.Sprintf("user_music:%d", mu.id), url: f, why: "non-local file_url"})
			continue
		}
		if st, err := os.Stat(localPath(uploadsBase, f)); err != nil || st.IsDir() {
			p.musicBroken = append(p.musicBroken, brokenFile{where: fmt.Sprintf("user_music:%d", mu.id), url: f, why: "music file missing on disk"})
			continue
		}
		p.musicOK++
	}
	// --- reactions: target PK (message_id,user_id) keeps one emoji ---
	seenRU := map[string]bool{}
	for _, r := range db.reactions {
		k := fmt.Sprintf("%d\x00%d", r.msgID, r.user)
		if seenRU[k] {
			p.reactSkip++
			continue
		}
		seenRU[k] = true
		p.reactOK++
	}
	p.skippedReads = len(db.reads)
	p.skippedFriends = db.friendN
	p.skippedFreq = 0
	for _, n := range db.friendReqN {
		p.skippedFreq += n
	}
	p.skippedRoles = db.roleN
	p.skippedMRoles = db.mroleN
	return p
}

// ---------------------------------------------------------------------------
// Media helpers (shared)
// ---------------------------------------------------------------------------

func s3KeyFor(url string) string {
	u := strings.TrimPrefix(strings.TrimSpace(url), "/uploads/")
	u = strings.TrimPrefix(u, "uploads/")
	parts := strings.SplitN(u, "/", 2)
	sub, rest := "files", u
	if len(parts) == 2 && parts[0] != "" {
		sub, rest = parts[0], parts[1]
	}
	switch sub {
	case "avatars", "room_avatars", "channel_icons", "files", "music", "videos":
	default:
		sub = "files"
		rest = u
	}
	rest = strings.ReplaceAll(rest, "\\", "_")
	return "boxchat-legacy/" + sub + "/" + uuid.NewString() + "_" + filepath.Base(rest)
}

func mimeByExt(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".mid", ".midi":
		return "audio/midi"
	case ".txt":
		return "text/plain"
	case ".js":
		return "text/javascript"
	case ".py":
		return "text/x-python"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

// ---------------------------------------------------------------------------
// Attachment tokens ([[att:id|file|mime|kind]]) — REQUIRED for visibility.
//
// The frontend discovers message attachments ONLY by parsing [[att:...]]
// tokens out of messages.content (combox-api parseMessageContent +
// hydrateAttachmentURLs), and the backend access check
// (CanUserAccessAttachment) joins attachments to messages through the same
// token (m.content LIKE '%[[att:' || id || '|%'). Rows in
// message_attachments alone render NOTHING: without the token the message
// shows "(empty)" (or bare text) and getAttachment denies access. Every
// migrated attachment MUST therefore append its token to the message.
// ---------------------------------------------------------------------------

// jsEscape mimics JS encodeURIComponent (unreserved marks A-Za-z0-9 -_.!~*'()
// plus parentheses stay raw, everything else is %XX over UTF-8 bytes), so the
// frontend decodeURIComponent round-trips filenames byte-exact.
func jsEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '!' || c == '~' || c == '*' || c == '\'' || c == '(' || c == ')' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func encodeAttToken(attachmentID, filename, mime, kind string) string {
	return "[[att:" + attachmentID + "|" + jsEscape(filename) + "|" + jsEscape(mime) + "|" + jsEscape(kind) + "]]"
}

func hasAttToken(content, attachmentID string) bool {
	return strings.Contains(content, "[[att:"+attachmentID+"|")
}

func appendAttToken(content, token string) string {
	if strings.TrimSpace(content) == "" {
		return token
	}
	return strings.TrimRight(content, "\n") + "\n" + token
}

func attachKind(mtype, mime string) string {
	switch strings.ToLower(strings.TrimSpace(mtype)) {
	case "image":
		return "image"
	case "video":
		return "video"
	case "music":
		return "audio"
	case "file":
		if strings.HasPrefix(mime, "image/") {
			return "image"
		}
		if strings.HasPrefix(mime, "video/") {
			return "video"
		}
		if strings.HasPrefix(mime, "audio/") {
			return "audio"
		}
		return "file"
	default:
		switch {
		case strings.HasPrefix(mime, "image/"):
			return "image"
		case strings.HasPrefix(mime, "video/"):
			return "video"
		case strings.HasPrefix(mime, "audio/"):
			return "audio"
		default:
			return "file"
		}
	}
}

// placeholderEmail: combox users.email is NOT NULL UNIQUE, legacy has no
// email at all, so synthesized placeholders must be unique + recognizable.
func placeholderEmail(username string, legacyID int64) string {
	u := strings.ToLower(strings.TrimSpace(username))
	var b strings.Builder
	for _, r := range u {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	s := b.String()
	if s == "" {
		s = "user"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return fmt.Sprintf("boxchat-legacy-%d-%s@legacy.invalid", legacyID, s)
}

// ---------------------------------------------------------------------------
// Local logs (allowed in both modes: local files, not DB/minio)
// ---------------------------------------------------------------------------

func writeLogs(outDir string, db *legacyDB, p *plan) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// merged_users.log: planned lower(username) mapping.
	var mb strings.Builder
	mb.WriteString("# boxchat-migrate merged_users log (planned lower(username) mapping)\n")
	mb.WriteString("# NOTE: matches against EXISTING combox users happen only in --apply mode\n")
	mb.WriteString("# (by lower(username) and, as a fallback, lower(email)). Dry-run lists legacy users.\n")
	for _, u := range p.userNew {
		fmt.Fprintf(&mb, "legacy_id=%d lower=%s username=%s action=create_or_map email=%s is_legacy_unverified=true legacy_username=%s\n",
			u.id, strings.ToLower(strings.TrimSpace(u.username)), u.username, placeholderEmail(u.username, u.id), u.username)
	}
	for dupID, primaryID := range p.userDupMap {
		fmt.Fprintf(&mb, "legacy_id=%d action=merge_into_primary primary_legacy_id=%d (same lower(username); shares combox user, keeps memberships)\n",
			dupID, primaryID)
	}
	if err := os.WriteFile(filepath.Join(outDir, "merged_users.log"), []byte(mb.String()), 0o644); err != nil {
		return err
	}
	// failed_files.log
	var fb strings.Builder
	fb.WriteString("# boxchat-migrate failed_files log (missing/empty/unusable file refs)\n")
	for _, b := range p.attachBroken {
		fmt.Fprintf(&fb, "%s url=%s why=%s\n", b.where, b.url, b.why)
	}
	for _, b := range p.musicBroken {
		fmt.Fprintf(&fb, "%s url=%s why=%s\n", b.where, b.url, b.why)
	}
	if err := os.WriteFile(filepath.Join(outDir, "failed_files.log"), []byte(fb.String()), 0o644); err != nil {
		return err
	}
	// skipped.log
	var sb strings.Builder
	sb.WriteString("# boxchat-migrate skipped log (entity -> reason)\n")
	fmt.Fprintf(&sb, "read_message:%d -> NO TARGET TABLE (no per-channel watermark in combox; message_statuses is per-message, chat_user_states is archived/pinned) -- skipped\n", p.skippedReads)
	fmt.Fprintf(&sb, "friendship:%d -> NO TARGET TABLE (no contacts/friends table in combox) -- skipped\n", p.skippedFriends)
	fmt.Fprintf(&sb, "friend_request:%d -> NO TARGET TABLE (no contacts/friends table in combox) -- skipped\n", p.skippedFreq)
	fmt.Fprintf(&sb, "role:%d -> NO TARGET RBAC TABLE (chat_members.role is a plain text owner/admin/member) -- best-effort promotion only, definitions skipped\n", p.skippedRoles)
	fmt.Fprintf(&sb, "member_role:%d -> NO TARGET TABLE (folded into chat_members.role best-effort) -- skipped\n", p.skippedMRoles)
	fmt.Fprintf(&sb, "message_reactions_dup:%d -> TARGET PK (message_id,user_id) keeps ONE emoji per user -- extras skipped\n", p.reactSkip)
	fmt.Fprintf(&sb, "message_reply_orphan:%d -> reply_to_id points to missing message -- inserted with reply_to=NULL\n", p.msgReplyOrphan)
	fmt.Fprintf(&sb, "auth_throttle:9 schema_migrations:9 sticker:0 sticker_pack:0 -> infra/empty tables, never migrated\n")
	_ = db
	if err := os.WriteFile(filepath.Join(outDir, "skipped.log"), []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Uploads inventory (read-only walk)
// ---------------------------------------------------------------------------

type uploadsInventory struct {
	filesBySub map[string]int
	totalFiles int
	totalBytes int64
}

func walkUploads(base string) (*uploadsInventory, error) {
	inv := &uploadsInventory{filesBySub: map[string]int{}}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort: unreadable entry counted as absent
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		sub := rel
		if i := strings.IndexByte(rel, string(os.PathSeparator)[0]); i >= 0 {
			sub = rel[:i]
		}
		inv.filesBySub[sub]++
		inv.totalFiles++
		if st, err := d.Info(); err == nil {
			inv.totalBytes += st.Size()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// ---------------------------------------------------------------------------
// Apply mode (writes!). Only runs with --apply.
// ---------------------------------------------------------------------------

func runApply(ctx context.Context, cfg cliConfig, db *legacyDB, p *plan) error {
	if strings.TrimSpace(cfg.pgDSN) == "" {
		return fmt.Errorf("apply needs --pg-dsn (or POSTGRES_DSN env)")
	}
	pool, err := pgxpool.New(ctx, strings.TrimSpace(cfg.pgDSN))
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	// Prerequisite: migration 000044 columns.
	var hasLegacyUsername, hasUnverified bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='legacy_username')`).Scan(&hasLegacyUsername); err != nil {
		return fmt.Errorf("probe users.legacy_username: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='is_legacy_unverified')`).Scan(&hasUnverified); err != nil {
		return fmt.Errorf("probe users.is_legacy_unverified: %w", err)
	}
	if !hasLegacyUsername || !hasUnverified {
		return fmt.Errorf("STOP: migration 000044 not applied (users.legacy_username=%v users.is_legacy_unverified=%v); run it first, then re-run --apply", hasLegacyUsername, hasUnverified)
	}
	// Idempotency table.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS legacy_id_map (
		kind TEXT NOT NULL, legacy_id BIGINT NOT NULL, new_id UUID NOT NULL,
		PRIMARY KEY (kind, legacy_id))`); err != nil {
		return fmt.Errorf("create legacy_id_map: %w", err)
	}
	// MinIO client (only in apply).
	mcfg, err := config.Load()
	if err != nil {
		// Fall back to flags only (config.Load requires secrets); build manually.
		mcfg = config.Config{MinIO: config.MinIOConfig{
			APIInternal: cfg.minioEndpoint, Bucket: cfg.minioBucket,
			RootUser: cfg.minioUser, RootPassword: cfg.minioPass,
			Secure: cfg.minioSecure, Region: cfg.minioRegion, SSEMode: "s3",
		}}
	} else {
		if strings.TrimSpace(cfg.minioEndpoint) != "" {
			mcfg.MinIO.APIInternal = cfg.minioEndpoint
		}
		if strings.TrimSpace(cfg.minioBucket) != "" {
			mcfg.MinIO.Bucket = cfg.minioBucket
		}
		if strings.TrimSpace(cfg.minioUser) != "" {
			mcfg.MinIO.RootUser = cfg.minioUser
		}
		if strings.TrimSpace(cfg.minioPass) != "" {
			mcfg.MinIO.RootPassword = cfg.minioPass
		}
		mcfg.MinIO.Secure = cfg.minioSecure
		if strings.TrimSpace(cfg.minioRegion) != "" {
			mcfg.MinIO.Region = cfg.minioRegion
		}
	}
	if strings.TrimSpace(mcfg.MinIO.APIInternal) == "" || strings.TrimSpace(mcfg.MinIO.Bucket) == "" {
		return fmt.Errorf("apply needs MinIO endpoint+bucket (--minio-endpoint/--minio-bucket or env)")
	}
	mc, err := miniorepo.New(mcfg.MinIO)
	if err != nil {
		return fmt.Errorf("minio client: %w", err)
	}
	if err := mc.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("minio ensure bucket: %w", err)
	}
	rawS3, err := newRawS3(mcfg.MinIO)
	if err != nil {
		return fmt.Errorf("minio raw client: %w", err)
	}

	// Load idempotency map + existing users (dedup base).
	idmap, err := loadIDMap(ctx, pool)
	if err != nil {
		return err
	}
	existingByLower, existingMailByLower, err := loadExistingUsers(ctx, pool)
	if err != nil {
		return err
	}

	counts := map[string]int{}
	// 1) users
	userNewID := map[int64]string{} // legacy user id -> combox uuid string
	for k, v := range idmap["user"] {
		userNewID[k] = v
	}
	if err := applyUsers(ctx, pool, db, p, cfg.batch, existingByLower, existingMailByLower, idmap, userNewID, counts); err != nil {
		return err
	}
	// 2) chats (rooms + topic sub-chats)
	chatID := map[string]string{} // "room:<id>" / "channel:<id>" -> uuid
	for k, v := range idmap["room"] {
		chatID[fmt.Sprintf("room:%d", k)] = v
	}
	for k, v := range idmap["channel"] {
		chatID[fmt.Sprintf("channel:%d", k)] = v
	}
	if err := applyChats(ctx, pool, db, p, cfg.batch, userNewID, idmap, chatID, counts); err != nil {
		return err
	}
	// 3) members
	if err := applyMembers(ctx, pool, db, p, cfg.batch, userNewID, chatID, counts); err != nil {
		return err
	}
	// 3b) topic sub-chats inherit parent-room memberships (else invisible)
	if err := applyTopicMembers(ctx, pool, db, p, cfg.batch, userNewID, chatID, counts); err != nil {
		return err
	}
	// 4) messages (two passes)
	msgID := map[int64]string{}
	for k, v := range idmap["message"] {
		msgID[k] = v
	}
	// R3: resolved messages.created_at per legacy message id (exact stored
	// instants; reactions inherit them as their honest timestamp anchor).
	msgTs := map[int64]time.Time{}
	chanToChat := map[int64]string{}
	for _, c := range db.channels {
		if c.id == p.rootChanOf[c.roomID] {
			chanToChat[c.id] = chatID[fmt.Sprintf("room:%d", c.roomID)]
		} else {
			chanToChat[c.id] = chatID[fmt.Sprintf("channel:%d", c.id)]
		}
	}
	if err := applyMessages(ctx, pool, db, p, cfg.batch, userNewID, chanToChat, idmap, msgID, msgTs, counts); err != nil {
		return err
	}
	// 5) attachments + minio uploads (+ [[att:]] tokens into content)
	if err := applyAttachments(ctx, pool, rawS3, mcfg.MinIO.Bucket, db, p, cfg, cfg.batch, userNewID, msgID, counts); err != nil {
		return err
	}
	// 5b) placeholders for unrecoverable empty media messages
	if err := applyBrokenPlaceholders(ctx, pool, p, cfg.batch, msgID, counts); err != nil {
		return err
	}
	// 6) avatars (users + rooms) -> minio + s3key: refs
	if err := applyAvatars(ctx, pool, rawS3, mcfg.MinIO.Bucket, cfg, db, p, userNewID, chatID, counts); err != nil {
		return err
	}
	// 6b) avatar history backfill (R9b): every boxchat-legacy avatar needs a
	// profile_photos row, or the fullscreen gallery has nothing to page
	// through and shows no date at all. Idempotent (see func comment).
	if err := applyAvatarHistory(ctx, pool, counts); err != nil {
		return err
	}
	// 7) reactions (honest timestamps = parent message time, R3)
	if err := applyReactions(ctx, pool, db, p, cfg.batch, userNewID, msgID, msgTs, counts); err != nil {
		return err
	}
	// 8) playlist (user_music -> users.saved_tracks) + bans
	if err := applyMusicAndBans(ctx, pool, rawS3, mcfg.MinIO.Bucket, cfg, db, p, userNewID, chatID, counts); err != nil {
		return err
	}

	fmt.Println("\nAPPLY SUMMARY (written):")
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-28s %d\n", k, counts[k])
	}
	fmt.Println("Idempotency: re-running --apply skips rows present in legacy_id_map.")
	return nil
}

func loadIDMap(ctx context.Context, pool *pgxpool.Pool) (map[string]map[int64]string, error) {
	out := map[string]map[int64]string{}
	rows, err := pool.Query(ctx, `SELECT kind, legacy_id, new_id::text FROM legacy_id_map`)
	if err != nil {
		return nil, fmt.Errorf("read legacy_id_map: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, nid string
		var lid int64
		if err := rows.Scan(&kind, &lid, &nid); err != nil {
			return nil, err
		}
		if out[kind] == nil {
			out[kind] = map[int64]string{}
		}
		out[kind][lid] = nid
	}
	return out, rows.Err()
}

func loadExistingUsers(ctx context.Context, pool *pgxpool.Pool) (map[string]string, map[string]string, error) {
	byName := map[string]string{}
	byMail := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT id::text, username, email FROM users`)
	if err != nil {
		return nil, nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, un, em string
		if err := rows.Scan(&id, &un, &em); err != nil {
			return nil, nil, err
		}
		byName[strings.ToLower(un)] = id
		byMail[strings.ToLower(em)] = id
	}
	return byName, byMail, rows.Err()
}

func batched[T any](items []T, n int, fn func([]T) error) error {
	for i := 0; i < len(items); i += n {
		j := i + n
		if j > len(items) {
			j = len(items)
		}
		if err := fn(items[i:j]); err != nil {
			return err
		}
	}
	return nil
}

func applyUsers(ctx context.Context, pool *pgxpool.Pool, db *legacyDB, p *plan, batch int,
	existingByLower, existingMailByLower map[string]string,
	idmap map[string]map[int64]string, userNewID map[int64]string, counts map[string]int) error {
	if idmap["user"] == nil {
		idmap["user"] = map[int64]string{}
	}
	type job struct {
		u       legacyUser
		mergeTo string // existing combox id, "" = create
	}
	var jobs []job
	for _, u := range p.userNew {
		if nid, ok := idmap["user"][u.id]; ok {
			userNewID[u.id] = nid
			counts["users_skipped_idmap"]++
			continue
		}
		lk := strings.ToLower(strings.TrimSpace(u.username))
		if eid, ok := existingByLower[lk]; ok {
			jobs = append(jobs, job{u, eid})
			continue
		}
		if eid, ok := existingMailByLower[lk]; ok {
			jobs = append(jobs, job{u, eid})
			continue
		}
		jobs = append(jobs, job{u, ""})
	}
	appendLog := func(line string) {
		f, _ := os.OpenFile("merged_users.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if f != nil {
			defer f.Close()
			fmt.Fprintln(f, line)
		}
	}
	_ = appendLog
	if err := batched(jobs, batch, func(js []job) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, j := range js {
			if j.mergeTo != "" {
				if _, err := tx.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('user',$1,$2) ON CONFLICT DO NOTHING`,
					j.u.id, j.mergeTo); err != nil {
					return err
				}
				idmap["user"][j.u.id] = j.mergeTo
				userNewID[j.u.id] = j.mergeTo
				counts["users_merged"]++
				continue
			}
			nid := uuid.NewString()
			email := placeholderEmail(j.u.username, j.u.id)
			bio := strings.TrimSpace(j.u.bio)
			if len([]rune(bio)) > 70 {
				bio = string([]rune(bio)[:70]) // combox auth caps bio at 70
			}
			if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,username,password_hash,bio,legacy_username,is_legacy_unverified)
				VALUES($1,$2,$3,$4,$5,$6,TRUE)
				ON CONFLICT (username) DO NOTHING`, nid, email, j.u.username, "!legacy!"+j.u.password, bio, j.u.username); err != nil {
				return fmt.Errorf("insert user %q: %w", j.u.username, err)
			}
			// Resolve the id we actually got (ON CONFLICT DO NOTHING may have skipped).
			var got string
			if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE username=$1`, j.u.username).Scan(&got); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('user',$1,$2) ON CONFLICT DO NOTHING`, j.u.id, got); err != nil {
				return err
			}
			idmap["user"][j.u.id] = got
			userNewID[j.u.id] = got
			counts["users_created"]++
		}
		return tx.Commit(ctx)
	}); err != nil {
		return err
	}
	// Intra-legacy lower(username) dups: point them at the SAME combox user
	// as their primary, so their memberships/messages/reactions migrate
	// instead of being skipped for lack of a map entry.
	type dupJob struct {
		dupID     int64
		primaryID int64
	}
	var djobs []dupJob
	for dupID, primaryID := range p.userDupMap {
		if _, ok := idmap["user"][dupID]; ok {
			userNewID[dupID] = idmap["user"][dupID]
			counts["users_dup_skipped_idmap"]++
			continue
		}
		djobs = append(djobs, dupJob{dupID, primaryID})
	}
	sort.Slice(djobs, func(i, j int) bool { return djobs[i].dupID < djobs[j].dupID })
	for _, dj := range djobs {
		target, ok := userNewID[dj.primaryID]
		if !ok || strings.TrimSpace(target) == "" {
			counts["users_dup_skipped_no_primary"]++
			continue
		}
		if _, err := pool.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('user',$1,$2) ON CONFLICT DO NOTHING`, dj.dupID, target); err != nil {
			return fmt.Errorf("map dup user %d: %w", dj.dupID, err)
		}
		idmap["user"][dj.dupID] = target
		userNewID[dj.dupID] = target
		counts["users_dup_merged"]++
	}
	return nil
}

func applyChats(ctx context.Context, pool *pgxpool.Pool, db *legacyDB, p *plan, batch int,
	userNewID map[int64]string, idmap map[string]map[int64]string, chatID map[string]string, counts map[string]int) error {
	if idmap["room"] == nil {
		idmap["room"] = map[int64]string{}
	}
	if idmap["channel"] == nil {
		idmap["channel"] = map[int64]string{}
	}
	fallbackCreator := ""
	for _, uid := range userNewID {
		fallbackCreator = uid
		break
	}
	if fallbackCreator == "" {
		return fmt.Errorf("no users mapped; cannot set chats.created_by")
	}
	creatorOf := func(r legacyRoom) string {
		if r.hasOwner {
			if id, ok := userNewID[r.ownerID]; ok {
				return id
			}
		}
		// DM rooms have no owner: use first member, else fallback.
		for _, m := range db.members {
			if m.roomID == r.id {
				if id, ok := userNewID[m.userID]; ok {
					return id
				}
			}
		}
		return fallbackCreator
	}
	type roomJob struct{ r legacyRoom }
	var rjobs []roomJob
	for _, r := range db.rooms {
		if _, ok := idmap["room"][r.id]; ok {
			continue
		}
		rjobs = append(rjobs, roomJob{r})
	}
	err := batched(rjobs, batch, func(js []roomJob) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, j := range js {
			kind := p.chatKindOf[j.r.id]
			isDirect := kind == "direct"
			title := strings.TrimSpace(j.r.name)
			if title == "" {
				title = fmt.Sprintf("boxchat-%d", j.r.id)
			}
			nid := uuid.NewString()
			if _, err := tx.Exec(ctx, `INSERT INTO chats(id,title,is_direct,created_by,chat_type,chat_kind,is_public,description)
				VALUES($1,$2,$3,$4,'standard',$5,$6,$7)`,
				nid, title, isDirect, creatorOf(j.r), kind, j.r.isPublic, strings.TrimSpace(j.r.desc)); err != nil {
				return fmt.Errorf("insert chat room %d: %w", j.r.id, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('room',$1,$2) ON CONFLICT DO NOTHING`, j.r.id, nid); err != nil {
				return err
			}
			idmap["room"][j.r.id] = nid
			chatID[fmt.Sprintf("room:%d", j.r.id)] = nid
			counts["chats_created"]++
			if tok := strings.TrimSpace(j.r.invite); tok != "" {
				if _, err := tx.Exec(ctx, `INSERT INTO chat_invite_links(chat_id,created_by,token,is_primary) VALUES($1,$2,$3,TRUE) ON CONFLICT DO NOTHING`,
					nid, creatorOf(j.r), "boxchat-"+tok); err != nil {
					return fmt.Errorf("invite link room %d: %w", j.r.id, err)
				}
				counts["invite_links_created"]++
			}
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		return err
	}
	// Topic sub-chats for non-root channels.
	return batched(p.topicChans, batch, func(js []legacyChannel) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, c := range js {
			if _, ok := idmap["channel"][c.id]; ok {
				counts["topics_skipped_idmap"]++
				continue
			}
			parent, ok := idmap["room"][c.roomID]
			if !ok {
				counts["topics_skipped_no_parent"]++
				continue
			}
			nid := uuid.NewString()
			title := strings.TrimSpace(c.name)
			if title == "" {
				title = fmt.Sprintf("boxchat-channel-%d", c.id)
			}
			// Next free topic_number for this parent.
			var maxN *int
			if err := tx.QueryRow(ctx, `SELECT MAX(topic_number) FROM chats WHERE parent_chat_id=$1 AND chat_kind='channel'`, parent).Scan(&maxN); err != nil {
				return err
			}
			next := 2
			if maxN != nil {
				next = *maxN + 1
			}
			var creator string
			_ = pool.QueryRow(ctx, `SELECT created_by::text FROM chats WHERE id=$1`, parent).Scan(&creator)
			if creator == "" {
				creator = fallbackCreator
			}
			if _, err := tx.Exec(ctx, `INSERT INTO chats(id,title,is_direct,created_by,chat_type,chat_kind,parent_chat_id,channel_type,topic_number,description,icon_emoji)
				VALUES($1,$2,FALSE,$3,'standard','channel',$4,'text',$5,$6,$7)`,
				nid, title, creator, parent, next, strings.TrimSpace(c.desc), strings.TrimSpace(c.emoji)); err != nil {
				return fmt.Errorf("insert topic channel %d: %w", c.id, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('channel',$1,$2) ON CONFLICT DO NOTHING`, c.id, nid); err != nil {
				return err
			}
			idmap["channel"][c.id] = nid
			chatID[fmt.Sprintf("channel:%d", c.id)] = nid
			counts["topics_created"]++
		}
		return tx.Commit(ctx)
	})
}

func normRole(r string) string {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "owner":
		return "owner"
	case "admin":
		return "admin"
	default:
		return "member"
	}
}

// normChannelRole maps a legacy member role onto a standalone_channel role.
// Broadcast rooms have no 'member' role (only owner/admin/subscriber/banned):
// plain members are channel subscribers. Used for rooms whose planned kind
// is standalone_channel; every other kind keeps normRole.
func normChannelRole(r string) string {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "owner":
		return "owner"
	case "admin":
		return "admin"
	case "banned":
		return "banned"
	default:
		return "subscriber"
	}
}

func applyMembers(ctx context.Context, pool *pgxpool.Pool, db *legacyDB, p *plan, batch int,
	userNewID map[int64]string, chatID map[string]string, counts map[string]int) error {
	_ = p
	return batched(db.members, batch, func(js []legacyMember) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, m := range js {
			uid, ok1 := userNewID[m.userID]
			cid, ok2 := chatID[fmt.Sprintf("room:%d", m.roomID)]
			if !ok1 || !ok2 {
				counts["members_skipped_no_map"]++
				continue
			}
			role := normRole(m.role)
			if p.chatKindOf[m.roomID] == "standalone_channel" {
				// L6: broadcast members are channel subscribers, not
				// 'member' (invalid for standalone channels and invisible
				// to the client's is_subscribed check).
				if next := normChannelRole(m.role); next != role {
					role = next
					counts["members_normalized_subscriber"]++
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO chat_members(chat_id,user_id,role) VALUES($1,$2,$3)
				ON CONFLICT (chat_id,user_id) DO UPDATE SET role=EXCLUDED.role`, cid, uid, role); err != nil {
				return fmt.Errorf("member %d: %w", m.id, err)
			}
			// Subscription journal: re-apply converges (DO UPDATE above
			// also backfills roles of rows migrated before the L6 fix),
			// so entries are recorded but never used to skip members.
			if _, err := tx.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('subscription',$1,$2) ON CONFLICT DO NOTHING`, m.id, cid); err != nil {
				return fmt.Errorf("member idmap %d: %w", m.id, err)
			}
			counts["members_upserted"]++
		}
		return tx.Commit(ctx)
	})
}

// applyTopicMembers copies every parent-room membership into the topic
// sub-chat with the same role (ON CONFLICT DO NOTHING so hand-edited roles
// win). Reading a normal chat requires membership; without this step every
// topic sub-chat (and its history + media) is invisible to all users.
func applyTopicMembers(ctx context.Context, pool *pgxpool.Pool, db *legacyDB, p *plan, batch int,
	userNewID map[int64]string, chatID map[string]string, counts map[string]int) error {
	type topicJob struct {
		subChatID string
		members   []legacyMember
	}
	membersByRoom := map[int64][]legacyMember{}
	for _, m := range db.members {
		membersByRoom[m.roomID] = append(membersByRoom[m.roomID], m)
	}
	var jobs []topicJob
	for _, c := range p.topicChans {
		sub, ok := chatID[fmt.Sprintf("channel:%d", c.id)]
		if !ok || strings.TrimSpace(sub) == "" {
			counts["topic_members_skipped_no_subchat"]++
			continue
		}
		jobs = append(jobs, topicJob{sub, membersByRoom[c.roomID]})
	}
	return batched(jobs, batch, func(js []topicJob) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, j := range js {
			for _, m := range j.members {
				uid, ok := userNewID[m.userID]
				if !ok || strings.TrimSpace(uid) == "" {
					counts["topic_members_skipped_no_user"]++
					continue
				}
				if _, err := tx.Exec(ctx, `INSERT INTO chat_members(chat_id,user_id,role) VALUES($1,$2,$3)
					ON CONFLICT (chat_id,user_id) DO NOTHING`, j.subChatID, uid, normRole(m.role)); err != nil {
					return fmt.Errorf("topic member room-member %d: %w", m.id, err)
				}
				counts["topic_members_upserted"]++
			}
		}
		return tx.Commit(ctx)
	})
}

func parseLegacyTime(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return nil
}

func applyMessages(ctx context.Context, pool *pgxpool.Pool, db *legacyDB, p *plan, batch int,
	userNewID map[int64]string, chanToChat map[int64]string,
	idmap map[string]map[int64]string, msgID map[int64]string, msgTs map[int64]time.Time, counts map[string]int) error {
	if idmap["message"] == nil {
		idmap["message"] = map[int64]string{}
	}
	// Pass 1: insert without reply_to.
	err := batched(db.messages, batch, func(js []legacyMessage) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, m := range js {
			if _, ok := idmap["message"][m.id]; ok {
				counts["messages_skipped_idmap"]++
				continue
			}
			uid, ok1 := userNewID[m.userID]
			cid, ok2 := chanToChat[m.channelID]
			if !ok1 || !ok2 {
				counts["messages_skipped_no_map"]++
				continue
			}
			nid := uuid.NewString()
			content := m.content // text preserved verbatim
			ts := parseLegacyTime(m.ts)
			ed := parseLegacyTime(m.edited)
			var created, updated any
			created, updated = ts, ts
			if created == nil {
				created = time.Now().UTC()
				updated = created
			}
			// R3: remember the exact value stored, so reactions inherit the
			// same honest timestamp (identical instant, not a recomputed now()).
			if t, ok := created.(time.Time); ok {
				msgTs[m.id] = t
			}
			if _, err := tx.Exec(ctx, `INSERT INTO messages(id,chat_id,user_id,content,idempotency_key,created_at,updated_at,edited_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`,
				nid, cid, uid, content, fmt.Sprintf("boxchat-legacy-%d", m.id), created, updated, ed); err != nil {
				return fmt.Errorf("message %d: %w", m.id, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('message',$1,$2) ON CONFLICT DO NOTHING`, m.id, nid); err != nil {
				return err
			}
			idmap["message"][m.id] = nid
			msgID[m.id] = nid
			counts["messages_created"]++
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		return err
	}
	// Pass 2: wire reply_to through the map (orphans stay NULL).
	return batched(db.messages, batch, func(js []legacyMessage) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, m := range js {
			if !m.hasReply {
				continue
			}
			dst, ok1 := msgID[m.id]
			src, ok2 := msgID[m.replyTo]
			if !ok1 || !ok2 {
				counts["replies_skipped_orphan"]++
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE messages SET reply_to_message_id=$2 WHERE id=$1`, dst, src); err != nil {
				return err
			}
			counts["replies_wired"]++
		}
		return tx.Commit(ctx)
	})
}

// rawS3 is a thin PutObject wrapper (single-part, fine for legacy sizes).
type rawS3 struct {
	c      *minio.Client
	bucket string
}

func newRawS3(cfg config.MinIOConfig) (*rawS3, error) {
	c, err := minio.New(strings.TrimSpace(cfg.APIInternal), &minio.Options{
		Creds:  credentials.NewStaticV4(strings.TrimSpace(cfg.RootUser), strings.TrimSpace(cfg.RootPassword), ""),
		Secure: cfg.Secure,
		Region: strings.TrimSpace(cfg.Region),
	})
	if err != nil {
		return nil, err
	}
	return &rawS3{c: c, bucket: strings.TrimSpace(cfg.Bucket)}, nil
}

func (r *rawS3) put(ctx context.Context, key, ctype, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	_, err = r.c.PutObject(ctx, r.bucket, key, f, st.Size(), minio.PutObjectOptions{ContentType: ctype})
	return err
}

func applyAttachments(ctx context.Context, pool *pgxpool.Pool, s3 *rawS3, bucket string,
	db *legacyDB, p *plan, cfg cliConfig, batch int,
	userNewID map[int64]string, msgID map[int64]string, counts map[string]int) error {
	_ = db
	if idmapCheck(ctx, pool, "attachment") {
		// loaded lazily below; keep idempotent via object_key unique index too.
	}
	// ensureAttToken appends the [[att:...]] token to the message content
	// when missing. Without the token the frontend renders "(empty)" and
	// the backend access check denies the attachment, so this step is what
	// actually makes migrated media visible.
	ensureAttToken := func(tx pgx.Tx, mid, aid, fname, mime, kind string) error {
		var content string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(content,'') FROM messages WHERE id=$1`, mid).Scan(&content); err != nil {
			return fmt.Errorf("read message content %s: %w", mid, err)
		}
		if hasAttToken(content, aid) {
			counts["attachment_tokens_already"]++
			return nil
		}
		token := encodeAttToken(aid, fname, mime, kind)
		if _, err := tx.Exec(ctx, `UPDATE messages SET content=$2 WHERE id=$1`, mid, appendAttToken(content, token)); err != nil {
			return fmt.Errorf("write token msg %s: %w", mid, err)
		}
		counts["attachment_tokens_written"]++
		return nil
	}
	return batched(p.attachOK, batch, func(js []legacyMessage) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, m := range js {
			mid, ok := msgID[m.id]
			if !ok {
				counts["attachments_skipped_no_msg"]++
				continue
			}
			fname := strings.TrimSpace(m.fileName)
			if fname == "" {
				fname = filepath.Base(strings.TrimSpace(m.fileURL))
			}
			mime := mimeByExt(fname)
			kind := attachKind(m.mtype, mime)
			var aid string
			if err := tx.QueryRow(ctx, `SELECT new_id::text FROM legacy_id_map WHERE kind='attachment' AND legacy_id=$1`, m.id).Scan(&aid); err == nil && strings.TrimSpace(aid) != "" {
				// Already migrated (e.g. the prod run wrote rows but no
				// tokens): make sure the link + token exist, no re-upload.
				if _, err := tx.Exec(ctx, `INSERT INTO message_attachments(message_id,attachment_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, mid, aid); err != nil {
					return err
				}
				// Canonical filename/mime/kind may differ from an older
				// run; the token carries them, so refresh from legacy.
				if err := ensureAttToken(tx, mid, aid, fname, mime, kind); err != nil {
					return err
				}
				counts["attachments_skipped_idmap"]++
				continue
			}
			uid := userNewID[m.userID]
			key := s3KeyFor(m.fileURL)
			local := localPath(cfg.uploads, m.fileURL)
			if err := s3.put(ctx, key, mime, local); err != nil {
				return fmt.Errorf("upload message %d: %w", m.id, err)
			}
			aid = uuid.NewString()
			var size *int64
			if m.fileSize > 0 {
				v := m.fileSize
				size = &v
			}
			if _, err := tx.Exec(ctx, `INSERT INTO attachments(id,user_id,filename,mime_type,kind,variant,size_bytes,bucket,object_key,upload_type,processing_status)
				VALUES($1,$2,$3,$4,$5,'original',$6,$7,$8,'single','uploaded')`,
				aid, uid, fname, mime, kind, size, bucket, key); err != nil {
				return fmt.Errorf("attachment row msg %d: %w", m.id, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO message_attachments(message_id,attachment_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, mid, aid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO legacy_id_map(kind,legacy_id,new_id) VALUES('attachment',$1,$2) ON CONFLICT DO NOTHING`, m.id, aid); err != nil {
				return err
			}
			if err := ensureAttToken(tx, mid, aid, fname, mime, kind); err != nil {
				return err
			}
			counts["attachments_created"]++
		}
		return tx.Commit(ctx)
	})
}

// applyBrokenPlaceholders fills messages that lost their file (missing on
// disk / placeholder URL) AND have empty text with an honest marker so they
// render as text instead of "(empty)". Idempotent: only touches legacy
// messages that are still empty and have no attachment link and no token.
func applyBrokenPlaceholders(ctx context.Context, pool *pgxpool.Pool, p *plan, batch int,
	msgID map[int64]string, counts map[string]int) error {
	return batched(p.brokenEmpty, batch, func(js []legacyMessage) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, m := range js {
			mid, ok := msgID[m.id]
			if !ok {
				counts["placeholders_skipped_no_msg"]++
				continue
			}
			var content string
			if err := tx.QueryRow(ctx, `SELECT COALESCE(content,'') FROM messages WHERE id=$1`, mid).Scan(&content); err != nil {
				return fmt.Errorf("read message %s: %w", mid, err)
			}
			if strings.Contains(content, "[[att:") {
				counts["placeholders_skipped_has_token"]++
				continue
			}
			var linked bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM message_attachments WHERE message_id=$1)`, mid).Scan(&linked); err != nil {
				return err
			}
			if linked {
				counts["placeholders_skipped_linked"]++
				continue
			}
			if strings.TrimSpace(content) != "" && !strings.HasPrefix(strings.TrimSpace(content), "[вложение не перенесено:") {
				counts["placeholders_skipped_nonempty"]++
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(content), "[вложение не перенесено:") {
				counts["placeholders_already"]++
				continue
			}
			name := strings.TrimSpace(m.fileName)
			if name == "" {
				name = filepath.Base(strings.TrimSpace(m.fileURL))
			}
			if name == "" || name == "." || name == "/" {
				name = "файл"
			}
			marker := "[вложение не перенесено: " + name + "]"
			if _, err := tx.Exec(ctx, `UPDATE messages SET content=$2 WHERE id=$1`, mid, marker); err != nil {
				return fmt.Errorf("write placeholder %s: %w", mid, err)
			}
			counts["placeholders_written"]++
		}
		return tx.Commit(ctx)
	})
}

func idmapCheck(ctx context.Context, pool *pgxpool.Pool, kind string) bool {
	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM legacy_id_map WHERE kind=$1`, kind).Scan(&n)
	return n > 0
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func applyAvatars(ctx context.Context, pool *pgxpool.Pool, s3 *rawS3, bucket string, cfg cliConfig,
	db *legacyDB, p *plan, userNewID map[int64]string, chatID map[string]string, counts map[string]int) error {
	_ = bucket
	_ = db
	// splitWhere parses "user:2" / "room:7" / "channel:9" refs. NOTE: this
	// used fmt.Sscanf with "%[^:]:%d", which Go's fmt does NOT support
	// ("bad verb '%['"), so every avatar was silently skipped.
	splitWhere := func(where string) (string, int64, bool) {
		parts := strings.SplitN(where, ":", 2)
		if len(parts) != 2 {
			return "", 0, false
		}
		var lid int64
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &lid); err != nil {
			return "", 0, false
		}
		return strings.TrimSpace(parts[0]), lid, true
	}
	// alreadyLegacy reports whether the avatar column already points at a
	// migrated object: re-runs then skip the re-upload (idempotent, no
	// orphan objects).
	alreadyLegacy := func(cur string) bool {
		return strings.HasPrefix(strings.TrimSpace(cur), "s3key:boxchat-legacy/")
	}
	// user + room avatars -> minio + avatar_data_url='s3key:<key>'
	for _, a := range p.avatarRefs {
		kind, lid, ok := splitWhere(a.where)
		if !ok {
			counts["avatars_skipped_bad_ref"]++
			continue
		}
		var targetID, col, tbl string
		switch kind {
		case "user":
			uid, ok := userNewID[lid]
			if !ok {
				counts["avatars_skipped_no_map"]++
				continue
			}
			targetID, col, tbl = uid, "avatar_data_url", "users"
		case "room":
			cid, ok := chatID[fmt.Sprintf("room:%d", lid)]
			if !ok {
				counts["avatars_skipped_no_map"]++
				continue
			}
			targetID, col, tbl = cid, "avatar_data_url", "chats"
		default:
			counts["avatars_skipped_bad_ref"]++
			continue
		}
		var cur string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(`+col+`,'') FROM `+tbl+` WHERE id=$1`, targetID).Scan(&cur); err != nil {
			return fmt.Errorf("read avatar %s %s: %w", tbl, targetID, err)
		}
		if alreadyLegacy(cur) {
			counts["avatars_skipped_already_set"]++
			continue
		}
		key := s3KeyFor(a.url)
		local := localPath(cfg.uploads, a.url)
		if err := s3.put(ctx, key, mimeByExt(local), local); err != nil {
			counts["avatars_failed"]++
			continue
		}
		ref := "s3key:" + key
		if _, err := pool.Exec(ctx, `UPDATE `+tbl+` SET `+col+`=$2 WHERE id=$1`, targetID, ref); err != nil {
			return err
		}
		counts["user_avatars_set"] += boolToInt(tbl == "users")
		counts["chat_avatars_set"] += boolToInt(tbl == "chats")
	}
	for _, a := range p.channelIcons {
		// Channel icons land on the sub-chat avatar (best-effort).
		_, lid, ok := splitWhere(a.where)
		if !ok {
			counts["avatars_skipped_bad_ref"]++
			continue
		}
		sub, ok := chatID[fmt.Sprintf("channel:%d", lid)]
		if !ok || strings.TrimSpace(sub) == "" {
			counts["avatars_skipped_no_map"]++
			continue
		}
		var cur string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(avatar_data_url,'') FROM chats WHERE id=$1`, sub).Scan(&cur); err != nil {
			return fmt.Errorf("read sub-chat avatar %s: %w", sub, err)
		}
		if alreadyLegacy(cur) {
			counts["avatars_skipped_already_set"]++
			continue
		}
		key := s3KeyFor(a.url)
		if err := s3.put(ctx, key, mimeByExt(a.url), localPath(cfg.uploads, a.url)); err != nil {
			counts["avatars_failed"]++
			continue
		}
		if _, err := pool.Exec(ctx, `UPDATE chats SET avatar_data_url=$2 WHERE id=$1`, sub, "s3key:"+key); err != nil {
			return err
		}
		counts["channel_icons_linked"]++
	}
	return nil
}

// applyAvatarHistory backfills profile_photos rows for migrated avatars
// (R9b). applyAvatars only sets users/chats.avatar_data_url; without a
// history row the gallery has nothing to page through and the viewer shows
// no date at all.
//
// HONESTY: legacy boxchat stores NO avatar timestamp, so there is no true
// install moment to migrate. profile_photos.created_at/taken_at are NOT NULL
// DEFAULT now() (no migrated flag column), therefore backfilled rows carry
// the migration time. The viewer must NOT present that as the install date:
// the photo API derives migrated=true from the boxchat-legacy/ object-key
// prefix and the viewer renders such rows as "<date> · from boxchat" /
// "из boxchat".
//
// IDEMPOTENCY: rows are keyed by (owner_kind, owner_id, object_key) via a
// pre-insert EXISTS check (the table has no unique constraint on those, only
// a UUID PK). Re-runs insert only what is still missing — including avatars
// migrated by an older run that never wrote history (the current prod state:
// 20 user + 11 chat avatars set, 1 history row). The scan reads the CURRENT
// users/chats tables (LIKE 's3key:boxchat-legacy/%'), so it covers both
// freshly uploaded avatars from this run and pre-existing ones.
func applyAvatarHistory(ctx context.Context, pool *pgxpool.Pool, counts map[string]int) error {
	const legacyPrefix = "s3key:boxchat-legacy/"
	type job struct {
		kind      string // 'user' | 'chat'
		ownerID   string
		objectKey string
	}
	var jobs []job
	// Users with a migrated avatar.
	rows, err := pool.Query(ctx, `SELECT id::text, COALESCE(avatar_data_url,'') FROM users WHERE avatar_data_url LIKE $1`, legacyPrefix+"%")
	if err != nil {
		return fmt.Errorf("scan legacy user avatars: %w", err)
	}
	for rows.Next() {
		var id, ref string
		if err := rows.Scan(&id, &ref); err != nil {
			rows.Close()
			return err
		}
		if key := strings.TrimPrefix(strings.TrimSpace(ref), "s3key:"); key != "" {
			jobs = append(jobs, job{"user", id, key})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan legacy user avatars: %w", err)
	}
	// Chats (groups, standalone channels, topic sub-chats) with a migrated avatar.
	rows, err = pool.Query(ctx, `SELECT id::text, COALESCE(avatar_data_url,'') FROM chats WHERE avatar_data_url LIKE $1`, legacyPrefix+"%")
	if err != nil {
		return fmt.Errorf("scan legacy chat avatars: %w", err)
	}
	for rows.Next() {
		var id, ref string
		if err := rows.Scan(&id, &ref); err != nil {
			rows.Close()
			return err
		}
		if key := strings.TrimPrefix(strings.TrimSpace(ref), "s3key:"); key != "" {
			jobs = append(jobs, job{"chat", id, key})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan legacy chat avatars: %w", err)
	}
	for _, j := range jobs {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM profile_photos WHERE owner_kind=$1 AND owner_id=$2::uuid AND object_key=$3)`,
			j.kind, j.ownerID, j.objectKey).Scan(&exists); err != nil {
			return fmt.Errorf("check avatar history %s %s: %w", j.kind, j.ownerID, err)
		}
		if exists {
			counts["avatar_history_already"]++
			continue
		}
		// created_at/taken_at default to now() = migration moment (see
		// honesty note above); the viewer labels these rows explicitly.
		if _, err := pool.Exec(ctx, `INSERT INTO profile_photos(id, owner_kind, owner_id, object_key) VALUES($1::uuid, $2, $3::uuid, $4)`,
			uuid.NewString(), j.kind, j.ownerID, j.objectKey); err != nil {
			return fmt.Errorf("insert avatar history %s %s: %w", j.kind, j.ownerID, err)
		}
		counts["avatar_history_created"]++
	}
	return nil
}

// parentMessageTime resolves the honest timestamp anchor for a legacy
// reaction: the resolved created_at of its parent message (same instant as
// stored in messages.created_at). msgTs holds exact values written this run;
// otherwise the value is re-derived deterministically from the legacy
// timestamp (all legacy messages carry one). There is no third source:
// legacy message_reaction has no timestamp columns.
func parentMessageTime(db *legacyDB, msgTs map[int64]time.Time, legacyMsgID int64) (time.Time, bool) {
	if t, ok := msgTs[legacyMsgID]; ok {
		return t, true
	}
	if lm, ok := db.msgsByID[legacyMsgID]; ok {
		if pt := parseLegacyTime(lm.ts); pt != nil {
			if t, ok := pt.(time.Time); ok {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func applyReactions(ctx context.Context, pool *pgxpool.Pool, db *legacyDB, p *plan, batch int,
	userNewID map[int64]string, msgID map[int64]string, msgTs map[int64]time.Time, counts map[string]int) error {
	_ = p
	seen := map[string]bool{}
	var first []legacyReaction
	for _, r := range db.reactions {
		k := fmt.Sprintf("%d\x00%d", r.msgID, r.user)
		if seen[k] {
			counts["reactions_skipped_dup"]++
			continue
		}
		seen[k] = true
		first = append(first, r)
	}
	return batched(first, batch, func(js []legacyReaction) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, r := range js {
			mid, ok1 := msgID[r.msgID]
			uid, ok2 := userNewID[r.user]
			if !ok1 || !ok2 {
				counts["reactions_skipped_no_map"]++
				continue
			}
			if strings.TrimSpace(r.emoji) == "" {
				counts["reactions_skipped_empty"]++
				continue
			}
			// R3: honest timestamp = parent message time (NOT NULL columns
			// would otherwise default to migration-time now()).
			ts, ok := parentMessageTime(db, msgTs, r.msgID)
			if !ok {
				counts["reactions_skipped_no_time"]++
				continue
			}
			tag, err := tx.Exec(ctx, `INSERT INTO message_reactions(message_id,user_id,emoji,created_at,updated_at) VALUES($1,$2,$3,$4,$4)
				ON CONFLICT (message_id,user_id) DO NOTHING`, mid, uid, r.emoji, ts)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				counts["reactions_created"]++
				continue
			}
			// Row pre-exists (pre-R3 migration stamped now(), or re-run):
			// converge timestamps to the parent message time. Safe: a row
			// with a legacy (message,user) pair can only come from this
			// migration (post-migration same-pair inserts are PK no-ops),
			// so this never clobbers genuine new user activity, which
			// always carries a non-legacy pair. Idempotent (re-runs no-op
			// once timestamps match).
			btag, err := tx.Exec(ctx, `UPDATE message_reactions SET created_at=$3, updated_at=$3
				WHERE message_id=$1 AND user_id=$2 AND (created_at<>$3 OR updated_at<>$3)`, mid, uid, ts)
			if err != nil {
				return err
			}
			if btag.RowsAffected() > 0 {
				counts["reactions_backfilled"]++
			} else {
				counts["reactions_ts_already"]++
			}
		}
		return tx.Commit(ctx)
	})
}

func applyMusicAndBans(ctx context.Context, pool *pgxpool.Pool, s3 *rawS3, bucket string, cfg cliConfig,
	db *legacyDB, p *plan, userNewID map[int64]string, chatID map[string]string, counts map[string]int) error {
	_ = bucket
	// user_music -> users.saved_tracks JSONB append (cap 500 like the service).
	type track struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Artist   string `json:"artist"`
		Duration int    `json:"duration"`
		FileSize string `json:"fileSize"`
		FileURL  string `json:"fileUrl"`
		AddedAt  string `json:"addedAt"`
	}
	byUser := map[int64][]legacyMusic{}
	for _, mu := range db.music {
		byUser[mu.user] = append(byUser[mu.user], mu)
	}
	for uidLegacy, list := range byUser {
		uid, ok := userNewID[uidLegacy]
		if !ok {
			counts["tracks_skipped_no_user"] += len(list)
			continue
		}
		var raw json.RawMessage
		if err := pool.QueryRow(ctx, `SELECT COALESCE(saved_tracks,'[]'::jsonb) FROM users WHERE id=$1`, uid).Scan(&raw); err != nil {
			return err
		}
		var cur []track
		_ = json.Unmarshal(raw, &cur)
		have := map[string]bool{}
		for _, t := range cur {
			have[t.ID] = true
		}
		for _, mu := range list {
			tid := "boxchat-" + fmt.Sprint(mu.id)
			if have[tid] {
				// Already migrated (re-run): skip, else tracks duplicate
				// on every --apply and orphan MinIO objects pile up.
				counts["tracks_skipped_idmap"]++
				continue
			}
			f := strings.TrimSpace(mu.file)
			if !isLocalUpload(f) {
				counts["tracks_skipped_broken"]++
				continue
			}
			if st, err := os.Stat(localPath(cfg.uploads, f)); err != nil || st.IsDir() {
				counts["tracks_skipped_broken"]++
				continue
			}
			key := s3KeyFor(f)
			if err := s3.put(ctx, key, mimeByExt(f), localPath(cfg.uploads, f)); err != nil {
				counts["tracks_failed_upload"]++
				continue
			}
			if len(cur) >= 500 {
				counts["tracks_skipped_cap"]++
				continue
			}
			cur = append(cur, track{
				ID: tid, Title: mu.title, Artist: mu.artist,
				FileURL: "s3key:" + key, AddedAt: time.Now().UTC().Format(time.RFC3339),
			})
			have[tid] = true
			counts["tracks_added"]++
		}
		enc, _ := json.Marshal(cur)
		if _, err := pool.Exec(ctx, `UPDATE users SET saved_tracks=$2::jsonb WHERE id=$1`, uid, string(enc)); err != nil {
			return err
		}
	}
	// room_ban: broadcast rooms -> public_channel_bans; server/dm -> chat event note (skipped).
	for _, b := range db.bans {
		room, ok := db.roomsByID[b.room]
		if !ok {
			counts["bans_skipped_no_room"]++
			continue
		}
		uid, ok1 := userNewID[b.user]
		cid, ok2 := chatID[fmt.Sprintf("room:%d", b.room)]
		if !ok1 || !ok2 {
			counts["bans_skipped_no_map"]++
			continue
		}
		by := uid
		if b.hasBy {
			if v, ok := userNewID[b.byID]; ok {
				by = v
			}
		}
		if strings.EqualFold(strings.TrimSpace(room.typ), "broadcast") {
			if _, err := pool.Exec(ctx, `INSERT INTO public_channel_bans(channel_chat_id,user_id,created_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, cid, uid, by); err != nil {
				return err
			}
			counts["bans_created_broadcast"]++
		} else {
			counts["bans_skipped_no_target"]++
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	cfg := parseFlags()
	mode := "DRY-RUN (no writes)"
	if cfg.apply {
		mode = "APPLY (writes!)"
	}
	fmt.Printf("boxchat-migrate %s\n  sqlite=%s\n  uploads=%s\n  out-dir=%s batch=%d\n", mode, cfg.sqlite, cfg.uploads, cfg.outDir, cfg.batch)

	db, err := loadLegacy(cfg.sqlite)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR reading legacy DB: %v\n", err)
		os.Exit(2)
	}
	inv, err := walkUploads(cfg.uploads)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR walking uploads: %v\n", err)
		os.Exit(2)
	}
	p := buildPlan(db, cfg.uploads)
	if err := writeLogs(cfg.outDir, db, p); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR writing logs: %v\n", err)
		os.Exit(2)
	}

	printReport(db, p, inv)

	if !cfg.apply {
		fmt.Println("\nDry-run complete: NOTHING was written to Postgres/MinIO.")
		fmt.Println("Local logs written to --out-dir: merged_users.log failed_files.log skipped.log")
		fmt.Println("To apply (after migration 000044): re-run with --apply + --pg-dsn/--minio-* flags.")
		return
	}
	ctx := context.Background()
	if err := runApply(ctx, cfg, db, p); err != nil {
		fmt.Fprintf(os.Stderr, "APPLY ERROR: %v\n", err)
		os.Exit(3)
	}
}

func printReport(db *legacyDB, p *plan, inv *uploadsInventory) {
	fmt.Println("\n================ LEGACY COUNTS (sqlite, read-only) ================")
	fmt.Printf("  users=%d rooms=%d channels=%d members=%d messages=%d reactions=%d read_watermarks=%d\n",
		len(db.users), len(db.rooms), len(db.channels), len(db.members), len(db.messages), len(db.reactions), len(db.reads))
	fmt.Printf("  friendships=%d friend_requests=%v roles=%d member_roles=%d user_music=%d room_bans=%d\n",
		db.friendN, db.friendReqN, db.roleN, db.mroleN, len(db.music), len(db.bans))
	fmt.Println("\n================ PLAN (target mapping) ================")
	fmt.Printf("  users: create_or_map=%d (intra-legacy lower() dups=%d merged into primary, see merged_users.log; combox email NOT NULL -> placeholder + is_legacy_unverified)\n", len(p.userNew), p.userDupInner)
	fmt.Printf("  chats: direct=%d group=%d standalone_channel=%d (rooms->chats) + topic_subchats(channel)=%d => total chat rows=%d\n",
		p.nDirect, p.nGroup, p.nStandalone, len(p.topicChans), len(db.rooms)+len(p.topicChans))
	fmt.Printf("  members: chat_members upserts=%d (roles owner/admin/member pass through; broadcast member->subscriber, see members_normalized_subscriber in APPLY SUMMARY)\n", len(db.members))
	fmt.Printf("  topic_members: sub-chats inherit parent memberships, copies=%d (else topics invisible: reading requires membership)\n", p.topicMemberJobs)
	fmt.Printf("  messages: ok=%d no_user=%d no_channel=%d replies_ok=%d replies_orphan(NULL)=%d\n",
		p.msgOK, p.msgNoUser, p.msgNoChannel, p.msgReplyOK, p.msgReplyOrphan)
	fmt.Printf("  attachments: ok=%d (+[[att:]] token appended to message content each) broken=%d (kept as text-only, see failed_files.log)\n", len(p.attachOK), len(p.attachBroken))
	fmt.Printf("  broken_empty: %d messages with empty text and no file -> placeholder '[вложение не перенесено: ...]'\n", len(p.brokenEmpty))
	fmt.Printf("  avatars local refs: user/room=%d channel_icons=%d (linked as s3key:, skip when already set)\n", len(p.avatarRefs), len(p.channelIcons))
	fmt.Printf("  avatar history (R9b): %d user/room refs + %d channel icons -> profile_photos rows (apply-only backfill, idempotent by owner+object_key)\n",
		len(p.avatarRefs), len(p.channelIcons))
	fmt.Printf("    created_at = migration time (legacy has no avatar timestamp; NOT NULL, no migrated column) -> viewer labels them 'from boxchat' / 'из boxchat', never a false install date.\n")
	fmt.Printf("  reactions: ok=%d skipped_dup(PK message_id,user_id)=%d\n", p.reactOK, p.reactSkip)
	fmt.Println("\n================ TIMESTAMPS R3 (legacy-only audit, no PG) ================")
	fmt.Printf("  reactions: %d distinct (message,user) pairs inherit parent message timestamp as created_at=updated_at\n", p.reactOK)
	fmt.Printf("    (legacy message_reaction has NO timestamp columns; target defaults now() = migration time = bug).\n")
	fmt.Printf("    Re-run backfill target: same %d legacy pairs (UPDATE converges rows whose ts differs; idempotent).\n", p.reactOK)
	// Chats audit: rooms with >=1 migratable message get an honest derived
	// last_message_at at read time; rooms with none stay NULL (UI falls back
	// to chats.created_at = migration time; no honest anchor exists: legacy
	// room has no timestamps). Mirrors buildPlan message gating.
	roomsWithMsg := map[int64]bool{}
	for _, m := range db.messages {
		ch, ok := db.chansByID[m.channelID]
		if !ok || !m.hasUser {
			continue
		}
		if _, ok := db.usersByID[m.userID]; !ok {
			continue
		}
		roomsWithMsg[ch.roomID] = true
	}
	nEmpty := 0
	for _, r := range db.rooms {
		if !roomsWithMsg[r.id] {
			nEmpty++
		}
	}
	fmt.Printf("  chats: rooms=%d with_messages=%d (derived last_message_at honest) empty=%d (last_message_at NULL, no anchor)\n",
		len(db.rooms), len(db.rooms)-nEmpty, nEmpty)
	fmt.Printf("    chats.created_at left at DB default (legacy room has no timestamps; not invented).\n")
	fmt.Printf("    chats.updated_at untouched (not used by chat-list sort ORDER BY chats.created_at).\n")
	fmt.Printf("  user_music -> saved_tracks: ok=%d broken=%d\n", p.musicOK, len(p.musicBroken))
	fmt.Printf("  skipped (no target): reads=%d friendships=%d friend_requests=%d roles=%d member_roles=%d\n",
		p.skippedReads, p.skippedFriends, p.skippedFreq, p.skippedRoles, p.skippedMRoles)
	fmt.Println("\n================ UPLOADS (read-only walk) ================")
	subs := make([]string, 0, len(inv.filesBySub))
	for s := range inv.filesBySub {
		subs = append(subs, s)
	}
	sort.Strings(subs)
	for _, s := range subs {
		fmt.Printf("  %-16s %d files\n", s, inv.filesBySub[s])
	}
	fmt.Printf("  TOTAL %d files, %d bytes\n", inv.totalFiles, inv.totalBytes)
	if len(p.attachBroken) > 0 {
		fmt.Println("\n  BROKEN REFS (first 15, full list in failed_files.log):")
		for i, b := range p.attachBroken {
			if i >= 15 {
				break
			}
			fmt.Printf("    %s url=%s why=%s\n", b.where, b.url, b.why)
		}
	}
}
