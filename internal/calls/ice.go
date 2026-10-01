package calls

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"time"
)

// TurnCredential mints a time-limited coturn REST credential.
//
//	username = "<expiryUnix>:<userID>"
//	password = base64(HMAC-SHA1(sharedSecret, username))
//
// coturn must run with `use-auth-secret` and the same static auth secret.
func TurnCredential(sharedSecret, userID string, ttl time.Duration, now time.Time) (username, password string) {
	expiry := now.UTC().Add(ttl).Unix()
	username = fmt.Sprintf("%d:%s", expiry, userID)
	mac := hmac.New(sha1.New, []byte(sharedSecret))
	_, _ = mac.Write([]byte(username))
	password = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return username, password
}

// BuildICEServers returns the RTCIceServer list handed to browsers.
func BuildICEServers(cfg Config, userID string, now time.Time) []ICEServer {
	servers := make([]ICEServer, 0, len(cfg.STUNURLs)+len(cfg.TURNURLs))
	if len(cfg.STUNURLs) > 0 {
		servers = append(servers, ICEServer{URLs: append([]string(nil), cfg.STUNURLs...)})
	}
	if len(cfg.TURNURLs) > 0 && cfg.TURNSharedSecret != "" {
		username, password := TurnCredential(cfg.TURNSharedSecret, userID, cfg.TURNCredentialTTL, now)
		servers = append(servers, ICEServer{
			URLs:       append([]string(nil), cfg.TURNURLs...),
			Username:   username,
			Credential: password,
		})
	}
	return servers
}
