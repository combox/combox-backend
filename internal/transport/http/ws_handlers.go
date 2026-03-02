package http

import (
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

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type wsRealtime interface {
	Client() *redis.Client
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func newWSHandler(valkey wsRealtime, accessSecret string, i18n Translator, defaultLocale string) http.HandlerFunc {
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
		presenceSubs := map[string]struct{}{}

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
				var msg struct {
					Type    string   `json:"type"`
					UserIDs []string `json:"user_ids"`
				}
				if err := json.Unmarshal(body, &msg); err != nil {
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
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
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
				if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
					return
				}
			}
		}
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
