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
	"time"

	vkrepo "combox-backend/internal/repository/valkey"
	"combox-backend/internal/service/chat"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type wsRealtime interface {
	Client() *redis.Client
}

type wsDeps struct {
	ChatService   ChatService
	SearchService SearchService
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
		connID := newPresenceConnID()
		presenceConnsKey := "presence:conns:" + userID
		_ = valkey.Client().SAdd(ctx, presenceConnsKey, connID).Err()
		_ = valkey.Client().Expire(ctx, presenceConnsKey, 90*time.Second).Err()
		now := time.Now().UTC()
		_ = presenceRepo.SetOnline(ctx, userID, now, 90*time.Second)
		_ = eventPublisher.PublishPresence(ctx, vkrepo.PresenceEvent{
			UserID:    userID,
			Online:    true,
			LastSeen:  now,
			UpdatedAt: now,
		})
		defer func() {
			_ = conn.Close()
			_ = valkey.Client().SRem(ctx, presenceConnsKey, connID).Err()
			if cnt, err := valkey.Client().SCard(ctx, presenceConnsKey).Result(); err == nil && cnt == 0 {
				offlineAt := time.Now().UTC()
				_ = presenceRepo.SetOffline(ctx, userID, offlineAt, 30*24*time.Hour)
				_ = eventPublisher.PublishPresence(ctx, vkrepo.PresenceEvent{
					UserID:    userID,
					Online:    false,
					LastSeen:  offlineAt,
					UpdatedAt: offlineAt,
				})
			}
		}()

		var subMu sync.Mutex
		var writeMu sync.Mutex
		presenceSubs := map[string]struct{}{}
		lastPresenceTouch := time.Now().UTC().Add(-10 * time.Second)
		touchPresence := func(force bool) {
			nowTouch := time.Now().UTC()
			if !force && nowTouch.Sub(lastPresenceTouch) < 3*time.Second {
				return
			}
			lastPresenceTouch = nowTouch
			_ = presenceRepo.SetOnline(ctx, userID, nowTouch, 90*time.Second)
			_ = valkey.Client().Expire(ctx, presenceConnsKey, 90*time.Second).Err()
			_ = eventPublisher.PublishPresence(ctx, vkrepo.PresenceEvent{
				UserID:    userID,
				Online:    true,
				LastSeen:  nowTouch,
				UpdatedAt: nowTouch,
			})
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
					touchPresence(true)
					continue
				}
				touchPresence(false)
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
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
				writeMu.Unlock()
				pingAt := time.Now().UTC()
				_ = presenceRepo.SetOnline(ctx, userID, pingAt, 90*time.Second)
				_ = valkey.Client().Expire(ctx, presenceConnsKey, 90*time.Second).Err()
				_ = eventPublisher.PublishPresence(ctx, vkrepo.PresenceEvent{
					UserID:    userID,
					Online:    true,
					LastSeen:  pingAt,
					UpdatedAt: pingAt,
				})
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				payload := strings.TrimSpace(msg.Payload)
				if payload == "" {
					continue
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
		reply(map[string]any{"items": page.Items, "next_cursor": page.NextCursor})
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

func verifyAccessToken(token, secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", errors.New("missing access secret")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return "", errors.New("invalid token format")
	}
	unsigned := parts[0] + "." + parts[1]
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(unsigned))
	expected := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", errors.New("invalid signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid payload")
	}
	var payload struct {
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return "", errors.New("invalid payload")
	}
	if strings.TrimSpace(payload.Sub) == "" {
		return "", errors.New("missing sub")
	}
	if payload.Exp > 0 && time.Now().UTC().Unix() > payload.Exp {
		return "", errors.New("token expired")
	}
	return strings.TrimSpace(payload.Sub), nil
}

func newPresenceConnID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("conn-%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}
