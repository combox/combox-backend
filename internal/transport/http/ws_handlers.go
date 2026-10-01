package http

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	vkrepo "combox-backend/internal/repository/valkey"
	"combox-backend/internal/service/chat"
	privacysvc "combox-backend/internal/service/privacy"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type wsRealtime interface {
	Client() *redis.Client
}

type wsDeps struct {
	ChatService   ChatService
	SearchService SearchService
	ProfileRepo   *vkrepo.ProfileSettingsRepository
	// Privacy gates live presence frames (last_seen rule of the origin owner);
	// nil keeps the legacy pass-through behaviour.
	Privacy PrivacyService
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func newWSHandler(valkey wsRealtime, deps wsDeps, accessSecret string, i18n Translator, defaultLocale string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.URL.Query().Get("access_token"))
		if token == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.missing_user_context", nil, i18n, defaultLocale)
			return
		}
		userID, err := verifyAccessToken(token, accessSecret)
		if err != nil {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "error.auth.invalid_credentials", nil, i18n, defaultLocale)
			return
		}
		if valkey == nil || valkey.Client() == nil {
			writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "error.internal", nil, i18n, defaultLocale)
			return
		}

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		channels := []string{"user:" + userID}
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
		if deviceID != "" {
			channels = append(channels, "device:"+deviceID)
		}

		ctx := r.Context()
		pubsub := valkey.Client().Subscribe(ctx, channels...)
		defer func() { _ = pubsub.Close() }()
		msgCh := pubsub.Channel(redis.WithChannelSize(256))

		presenceRepo := vkrepo.NewPresenceRepositoryFromRedis(valkey.Client())
		eventPublisher := vkrepo.NewEventPublisherFromRedis(valkey.Client())
		// Presence frames carry the owner's "show last seen" setting so a
		// subscriber never renders a timestamp the owner has hidden.
		lastSeenVisible := true
		if deps.ProfileRepo != nil {
			if settings, settingsErr := deps.ProfileRepo.Get(ctx, userID); settingsErr == nil {
				lastSeenVisible = settings.ShowLastSeen
			}
		}
		publishPresence := func(online bool, at time.Time) {
			_ = eventPublisher.PublishPresence(ctx, vkrepo.PresenceEvent{
				UserID:          userID,
				Online:          online,
				LastSeen:        at,
				UpdatedAt:       at,
				LastSeenVisible: &lastSeenVisible,
			})
		}
		connID := newPresenceConnID()
		presenceConnsKey := "presence:conns:" + userID
		// Connections that currently report an active (visible) tab. Online is
		// derived from this set instead of the socket being open, so a hidden
		// or collapsed tab stops presenting itself as online after its TTL.
		presenceActiveKey := "presence:active:" + userID
		// Per-connection visibility flag, authoritative source is the latest
		// presence.ping from this socket (default true at connect). The
		// server ping ticker and non-presence frames must never flip an
		// inactive connection back to active: they only refresh the conns TTL
		// (socket is alive) while the tab stays reported offline.
		var connActive atomic.Bool
		connActive.Store(true)
		_ = valkey.Client().SAdd(ctx, presenceConnsKey, connID).Err()
		_ = valkey.Client().Expire(ctx, presenceConnsKey, 90*time.Second).Err()
		_ = valkey.Client().SAdd(ctx, presenceActiveKey, connID).Err()
		_ = valkey.Client().Expire(ctx, presenceActiveKey, 90*time.Second).Err()
		now := time.Now().UTC()
		_ = presenceRepo.SetOnline(ctx, userID, now, 90*time.Second)
		publishPresence(true, now)
		offlinePublished := false
		defer func() {
			_ = conn.Close()
			_ = valkey.Client().SRem(ctx, presenceConnsKey, connID).Err()
			_ = valkey.Client().SRem(ctx, presenceActiveKey, connID).Err()
			if cnt, err := valkey.Client().SCard(ctx, presenceActiveKey).Result(); err == nil && cnt == 0 {
				offlineAt := time.Now().UTC()
				_ = presenceRepo.SetOffline(ctx, userID, offlineAt, 30*24*time.Hour)
				publishPresence(false, offlineAt)
			}
		}()

		var subMu sync.Mutex
		var writeMu sync.Mutex
		presenceSubs := map[string]struct{}{}
		chatSubs := map[string]struct{}{}
		lastTypingAt := map[string]time.Time{}
		lastChatJoinAt := time.Time{}
		lastPresenceTouch := time.Now().UTC().Add(-10 * time.Second)
		// active==false means the owning tab is hidden/minimised: drop this
		// connection from the active set and only report offline once no other
		// connection still claims to be active.
		touchPresence := func(force bool, active bool) {
			nowTouch := time.Now().UTC()
			if !force && nowTouch.Sub(lastPresenceTouch) < 3*time.Second {
				return
			}
			lastPresenceTouch = nowTouch
			_ = valkey.Client().Expire(ctx, presenceConnsKey, 90*time.Second).Err()
			if !active {
				_ = valkey.Client().SRem(ctx, presenceActiveKey, connID).Err()
				if cnt, err := valkey.Client().SCard(ctx, presenceActiveKey).Result(); err == nil && cnt > 0 {
					return
				}
				if offlinePublished {
					return
				}
				offlinePublished = true
				_ = presenceRepo.SetOffline(ctx, userID, nowTouch, 30*24*time.Hour)
				publishPresence(false, nowTouch)
				return
			}
			offlinePublished = false
			_ = valkey.Client().SAdd(ctx, presenceActiveKey, connID).Err()
			_ = valkey.Client().Expire(ctx, presenceActiveKey, 90*time.Second).Err()
			_ = presenceRepo.SetOnline(ctx, userID, nowTouch, 90*time.Second)
			publishPresence(true, nowTouch)
		}

		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				_, body, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var raw map[string]any
				if err := json.Unmarshal(body, &raw); err != nil {
					continue
				}
				msgType, _ := raw["type"].(string)
				msgType = strings.TrimSpace(msgType)
				if msgType == "" {
					continue
				}
				if msgType == "presence.ping" {
					active := true
					if rawActive, ok := raw["active"].(bool); ok {
						active = rawActive
					}
					connActive.Store(active)
					touchPresence(true, active)
					continue
				}
				if connActive.Load() {
					touchPresence(false, true)
				} else {
					// Hidden tab: the socket is alive so refresh only the conns
					// TTL. Never re-add to the active set or re-publish online;
					// only a presence.ping with active=true may do that.
					_ = valkey.Client().Expire(ctx, presenceConnsKey, 90*time.Second).Err()
				}
				// Chat scoped frames: membership is checked once when joining a
				// chat channel, typing heartbeats are then broadcast to it.
				if msgType == "chat.subscribe" || msgType == "chat.unsubscribe" || msgType == "typing" {
					handleChatFrame(ctx, raw, msgType, userID, deps, pubsub, valkey.Client(), &subMu, chatSubs, lastTypingAt, &lastChatJoinAt)
					continue
				}
				// Requests with id/response pattern
				if strings.HasPrefix(msgType, "request.") {
					handleRequest(ctx, conn, &writeMu, raw, msgType, userID, deps, i18n, defaultLocale)
					continue
				}
				// Legacy presence subscribe/unsubscribe
				var msg struct {
					Type    string   `json:"type"`
					UserIDs []string `json:"user_ids"`
				}
				// Re-marshal raw into msg struct for legacy handling
				b, _ := json.Marshal(raw)
				if err := json.Unmarshal(b, &msg); err != nil {
					continue
				}
				if strings.TrimSpace(msg.Type) == "presence.subscribe" {
					ch := make([]string, 0, len(msg.UserIDs))
					subMu.Lock()
					for _, id := range msg.UserIDs {
						id = strings.TrimSpace(id)
						if id == "" {
							continue
						}
						channel := "presence:" + id
						if _, exists := presenceSubs[channel]; exists {
							continue
						}
						presenceSubs[channel] = struct{}{}
						ch = append(ch, channel)
					}
					subMu.Unlock()
					if len(ch) > 0 {
						_ = pubsub.Subscribe(ctx, ch...)
					}
				}
				if strings.TrimSpace(msg.Type) == "presence.unsubscribe" {
					ch := make([]string, 0, len(msg.UserIDs))
					subMu.Lock()
					for _, id := range msg.UserIDs {
						id = strings.TrimSpace(id)
						if id == "" {
							continue
						}
						channel := "presence:" + id
						if _, exists := presenceSubs[channel]; !exists {
							continue
						}
						delete(presenceSubs, channel)
						ch = append(ch, channel)
					}
					subMu.Unlock()
					if len(ch) > 0 {
						_ = pubsub.Unsubscribe(ctx, ch...)
					}
				}
			}
		}()

		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-readDone:
				return
			case <-ping.C:
				writeMu.Lock()
				pingErr := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
				writeMu.Unlock()
				if pingErr != nil {
					// The peer is unreachable (frozen tab, dropped network).
					// Stop refreshing presence so an active-but-dead session
					// cannot stay online until the read deadline fires.
					return
				}
				pingAt := time.Now().UTC()
				// Socket keepalive: the connection is alive regardless of tab
				// visibility, so its TTL is always refreshed.
				_ = valkey.Client().Expire(ctx, presenceConnsKey, 90*time.Second).Err()
				if !connActive.Load() {
					// Hidden tab: do not re-add to the active set and do not
					// re-publish online; the tab stays offline until it sends
					// presence.ping with active=true again.
					continue
				}
				_ = presenceRepo.SetOnline(ctx, userID, pingAt, 90*time.Second)
				_ = valkey.Client().Expire(ctx, presenceActiveKey, 90*time.Second).Err()
				publishPresence(true, pingAt)
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				payload := strings.TrimSpace(msg.Payload)
				if payload == "" {
					continue
				}
				if deps.Privacy != nil {
					payload = filterPresencePayload(ctx, deps.Privacy, userID, payload)
				}
				writeMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, []byte(payload))
				writeMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}
}

func handleRequest(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, raw map[string]any, msgType, userID string, deps wsDeps, i18n Translator, defaultLocale string) {
	reqID, _ := raw["id"].(string)
	if reqID == "" {
		reqID = "unknown"
	}
	reply := func(payload any) {
		out := map[string]any{"id": reqID, "payload": payload}
		if b, err := json.Marshal(out); err == nil {
			if writeMu != nil {
				writeMu.Lock()
				defer writeMu.Unlock()
			}
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}
	}
	switch msgType {
	case "request.chats":
		if deps.ChatService == nil {
			reply(map[string]any{"error": "service_unavailable"})
			return
		}
		chats, err := deps.ChatService.ListChats(ctx, userID)
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"chats": chats})
	case "request.messages":
		if deps.ChatService == nil {
			reply(map[string]any{"error": "service_unavailable"})
			return
		}
		chatID, _ := raw["chat_id"].(string)
		cursor, _ := raw["cursor"].(string)
		deviceID, _ := raw["device_id"].(string)
		limitVal, _ := raw["limit"].(float64)
		limit := int(limitVal)
		if limit <= 0 {
			limit = 50
		}
		if chatID == "" {
			reply(map[string]any{"error": "missing chat_id"})
			return
		}
		page, err := deps.ChatService.ListMessages(ctx, chat.ListMessagesInput{
			UserID:   userID,
			ChatID:   chatID,
			Cursor:   cursor,
			Limit:    limit,
			DeviceID: deviceID,
		})
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"items": page.Items, "statuses": page.Statuses, "next_cursor": page.NextCursor})
	case "request.mark_read":
		if deps.ChatService == nil {
			reply(map[string]any{"error": "service_unavailable"})
			return
		}
		messageID, _ := raw["message_id"].(string)
		if messageID == "" {
			reply(map[string]any{"error": "missing message_id"})
			return
		}
		status, err := deps.ChatService.MarkMessageReadByID(ctx, userID, messageID)
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"status": status})
	case "request.send":
		if deps.ChatService == nil {
			reply(map[string]any{"error": "service_unavailable"})
			return
		}
		chatID, _ := raw["chat_id"].(string)
		content, _ := raw["content"].(string)
		attachmentIDsAny, _ := raw["attachment_ids"].([]any)
		var attachmentIDs []string
		for _, v := range attachmentIDsAny {
			if s, ok := v.(string); ok {
				attachmentIDs = append(attachmentIDs, s)
			}
		}
		replyToMessageID, _ := raw["reply_to_message_id"].(string)
		if chatID == "" || content == "" {
			reply(map[string]any{"error": "missing chat_id or content"})
			return
		}
		msg, err := deps.ChatService.CreateMessage(ctx, chat.CreateMessageInput{
			UserID:           userID,
			ChatID:           chatID,
			Content:          content,
			AttachmentIDs:    attachmentIDs,
			ReplyToMessageID: replyToMessageID,
		})
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"message": msg})
	case "request.react":
		if deps.ChatService == nil {
			reply(map[string]any{"error": "service_unavailable"})
			return
		}
		messageID, _ := raw["message_id"].(string)
		emoji, _ := raw["emoji"].(string)
		if messageID == "" || emoji == "" {
			reply(map[string]any{"error": "missing message_id or emoji"})
			return
		}
		reactions, action, err := deps.ChatService.ToggleMessageReactionByID(ctx, userID, messageID, emoji)
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"reactions": reactions, "action": action})
	case "request.delete":
		if deps.ChatService == nil {
			reply(map[string]any{"error": "service_unavailable"})
			return
		}
		messageID, _ := raw["message_id"].(string)
		if messageID == "" {
			reply(map[string]any{"error": "missing message_id"})
			return
		}
		err := deps.ChatService.DeleteMessageByID(ctx, userID, messageID)
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"deleted": true})
	case "request.search":
		if deps.SearchService == nil {
			reply(map[string]any{"error": "service_unavailable"})
			return
		}
		q, _ := raw["q"].(string)
		scope, _ := raw["scope"].(string)
		limitVal, _ := raw["limit"].(float64)
		limit := int(limitVal)
		if limit <= 0 {
			limit = 20
		}
		if q == "" {
			reply(map[string]any{"error": "missing q"})
			return
		}
		results, err := deps.SearchService.Search(ctx, q, scope, limit)
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"users": results.Users, "chats": results.Chats})
	default:
		reply(map[string]any{"error": "unknown_request"})
	}
}

// verifyAccessToken checks the signature, expiry and sub claim and returns the
// user id. Migr-limited legacy tokens are rejected: realtime is unavailable
// until the email is bound (the HTTP migr allowlist has no WS entry).
func verifyAccessToken(token, secret string) (string, error) {
	userID, _, migr, err := verifyAccessTokenWithSession(token, secret)
	if err != nil {
		return "", err
	}
	if migr {
		return "", errors.New("migration email binding required")
	}
	return userID, nil
}

// verifyAccessTokenWithSession additionally returns the session id embedded as
// "sid" and the legacy "migr" mark (boxchat migration, 000044). Tokens minted
// before those claims existed simply return empty/false, which handlers treat
// as "unknown"/"full".
func verifyAccessTokenWithSession(token, secret string) (string, string, bool, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", "", false, errors.New("missing access secret")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return "", "", false, errors.New("invalid token format")
	}
	unsigned := parts[0] + "." + parts[1]
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(unsigned))
	expected := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", "", false, errors.New("invalid signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", false, errors.New("invalid payload")
	}
	var payload struct {
		Sub  string `json:"sub"`
		Sid  string `json:"sid"`
		Migr bool   `json:"migr"`
		Exp  int64  `json:"exp"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return "", "", false, errors.New("invalid payload")
	}
	if strings.TrimSpace(payload.Sub) == "" {
		return "", "", false, errors.New("missing sub")
	}
	if payload.Exp > 0 && time.Now().UTC().Unix() > payload.Exp {
		return "", "", false, errors.New("token expired")
	}
	return strings.TrimSpace(payload.Sub), strings.TrimSpace(payload.Sid), payload.Migr, nil
}

func newPresenceConnID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("conn-%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}

const (
	maxChatSubscriptions = 64
	chatJoinMinGap       = 250 * time.Millisecond
	typingPublishMinGap  = 1200 * time.Millisecond
)

// handleChatFrame routes the chat scoped frames of a realtime connection:
//   - "chat.subscribe"   join the chat broadcast channel (membership checked here)
//   - "chat.unsubscribe" leave it again
//   - "typing"           forward a typing heartbeat to everyone in that chat
//
// Membership is verified once, when joining, so the per keystroke frame stays a
// cheap Redis publish instead of a database round trip.
func handleChatFrame(
	ctx context.Context,
	raw map[string]any,
	msgType string,
	userID string,
	deps wsDeps,
	pubsub *redis.PubSub,
	rdb *redis.Client,
	subMu *sync.Mutex,
	chatSubs map[string]struct{},
	lastTypingAt map[string]time.Time,
	lastChatJoinAt *time.Time,
) {
	if rdb == nil {
		return
	}
	chatID, _ := raw["chat_id"].(string)
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return
	}
	channel := "chat:" + chatID

	switch msgType {
	case "chat.subscribe":
		if deps.ChatService == nil {
			return
		}
		subMu.Lock()
		_, already := chatSubs[channel]
		full := !already && len(chatSubs) >= maxChatSubscriptions
		if !already && lastChatJoinAt != nil && time.Since(*lastChatJoinAt) < chatJoinMinGap {
			subMu.Unlock()
			return
		}
		subMu.Unlock()
		if already || full {
			return
		}
		// ListMembers only answers for members of the chat.
		if _, err := deps.ChatService.ListMembers(ctx, userID, chatID, false); err != nil {
			return
		}
		if err := pubsub.Subscribe(ctx, channel); err != nil {
			return
		}
		subMu.Lock()
		chatSubs[channel] = struct{}{}
		if lastChatJoinAt != nil {
			*lastChatJoinAt = time.Now().UTC()
		}
		subMu.Unlock()
	case "chat.unsubscribe":
		subMu.Lock()
		_, subscribed := chatSubs[channel]
		if subscribed {
			delete(chatSubs, channel)
		}
		subMu.Unlock()
		if subscribed {
			_ = pubsub.Unsubscribe(ctx, channel)
		}
	case "typing":
		subMu.Lock()
		_, subscribed := chatSubs[channel]
		if !subscribed {
			subMu.Unlock()
			return
		}
		if last, ok := lastTypingAt[chatID]; ok && time.Since(last) < typingPublishMinGap {
			subMu.Unlock()
			return
		}
		lastTypingAt[chatID] = time.Now().UTC()
		subMu.Unlock()

		frame := map[string]any{
			"type":    "typing",
			"chat_id": chatID,
			"user_id": userID,
			"at":      time.Now().UTC().Format(time.RFC3339Nano),
		}
		payload, err := json.Marshal(frame)
		if err != nil {
			return
		}
		_ = rdb.Publish(ctx, channel, payload).Err()
	}
}

// filterPresencePayload hides an origin owner's live presence from a viewer
// that the owner's last_seen privacy rule does not allow. Every other frame
// (chat, typing, reactions, profile updates, ...) passes through untouched,
// and an allowed presence frame keeps its original payload byte for byte.
func filterPresencePayload(ctx context.Context, privacy PrivacyService, viewerID, payload string) string {
	if !strings.Contains(payload, vkrepo.EventTypePresence) {
		return payload
	}
	var ev vkrepo.PresenceEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return payload
	}
	if ev.Type != vkrepo.EventTypePresence {
		return payload
	}
	ownerID := strings.TrimSpace(ev.UserID)
	if ownerID == "" || ownerID == viewerID {
		return payload
	}
	allowed, err := privacy.Evaluate(privacysvc.WithMemo(ctx), viewerID, ownerID, privacysvc.ParamLastSeen)
	if err == nil && allowed {
		return payload
	}
	// Fail closed: the presence lookup failed or the owner hides last seen.
	// The frame keeps type/user_id but loses last_seen, updated_at (which is
	// the same instant for an offline frame) and reports online: false with
	// last_seen_visible: false, matching the REST presence shape.
	frame := map[string]any{
		"type":              ev.Type,
		"user_id":           ev.UserID,
		"online":            false,
		"last_seen_visible": false,
	}
	out, err := json.Marshal(frame)
	if err != nil {
		return payload
	}
	return string(out)
}
