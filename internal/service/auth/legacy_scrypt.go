package auth

import (
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// VerifyWerkzeugScrypt checks a password against a werkzeug scrypt hash of the
// form "scrypt:N:r:p$<salt-hex>$<hash-hex>" (boxchat legacy passwords, e.g.
// "scrypt:32768:8:1$<salt>$<hash>"). The derived key length is inferred from
// the stored hash length, so any werkzeug dklen verifies. Comparison is
// constant-time. A wrong password and a malformed hash both return an error;
// callers map every error to the generic 401 invalid_credentials response,
// so no oracle is exposed.
//
// Hashes that do not carry the "scrypt:" prefix (bcrypt, ...) are rejected:
// a user flagged is_legacy_unverified is werkzeug-only by contract.
func VerifyWerkzeugScrypt(storedHash, password string) error {
	storedHash = strings.TrimSpace(storedHash)
	if !strings.HasPrefix(storedHash, "scrypt:") {
		return errors.New("not a werkzeug scrypt hash")
	}
	rest := strings.TrimPrefix(storedHash, "scrypt:")
	parts := strings.Split(rest, "$")
	if len(parts) != 3 {
		return fmt.Errorf("malformed werkzeug scrypt hash: expected 3 $-segments, got %d", len(parts))
	}
	params := strings.Split(parts[0], ":")
	if len(params) != 3 {
		return fmt.Errorf("malformed werkzeug scrypt params: %q", parts[0])
	}
	n, err := strconv.Atoi(strings.TrimSpace(params[0]))
	if err != nil || n <= 1 || n&(n-1) != 0 {
		return fmt.Errorf("invalid scrypt N: %q", params[0])
	}
	r, err := strconv.Atoi(strings.TrimSpace(params[1]))
	if err != nil || r <= 0 {
		return fmt.Errorf("invalid scrypt r: %q", params[1])
	}
	p, err := strconv.Atoi(strings.TrimSpace(params[2]))
	if err != nil || p <= 0 {
		return fmt.Errorf("invalid scrypt p: %q", params[2])
	}

	salt, err := hex.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil || len(salt) == 0 {
		return errors.New("invalid scrypt salt encoding")
	}
	want, err := hex.DecodeString(strings.TrimSpace(parts[2]))
	if err != nil || len(want) == 0 {
		return errors.New("invalid scrypt hash encoding")
	}

	got, err := scrypt.Key([]byte(password), salt, n, r, p, len(want))
	if err != nil {
		return fmt.Errorf("scrypt derive: %w", err)
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return errors.New("password mismatch")
	}
	return nil
}
