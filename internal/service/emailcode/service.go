package emailcode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type Sender interface {
	Send(ctx context.Context, to, subject, html, text string) error
}

type Notifier interface {
	NotifyLoginCode(ctx context.Context, email, code string, expiresAt time.Time, locale string) (bool, error)
}

type Translator interface {
	Translate(requestLocale, key string) string
}

type CodeEntry struct {
	Hash      string
	Salt      string
	ExpiresAt time.Time
	Attempts  int
}

type Service struct {
	sender      Sender
	notifier    Notifier
	codeTTL     time.Duration
	maxAttempts int
	nowFn       func() time.Time
	i18n        Translator
	verifiedTTL time.Duration

	mu       sync.Mutex
	codes    map[string]CodeEntry
	verified map[string]time.Time
	loginKey map[string]CodeEntry
}

var ErrInvalidEmail = errors.New("invalid email")

type Config struct {
	Sender      Sender
	Notifier    Notifier
	I18n        Translator
	CodeTTL     time.Duration
	VerifiedTTL time.Duration
	MaxAttempts int
}

func New(cfg Config) (*Service, error) {
	if cfg.Sender == nil {
		return nil, fmt.Errorf("sender is required")
	}
	if cfg.CodeTTL <= 0 {
		return nil, fmt.Errorf("code ttl must be positive")
	}
	if cfg.MaxAttempts <= 0 {
		return nil, fmt.Errorf("max attempts must be positive")
	}
	if cfg.VerifiedTTL <= 0 {
		cfg.VerifiedTTL = 10 * time.Minute
	}

	return &Service{
		sender:      cfg.Sender,
		notifier:    cfg.Notifier,
		i18n:        cfg.I18n,
		codeTTL:     cfg.CodeTTL,
		verifiedTTL: cfg.VerifiedTTL,
		maxAttempts: cfg.MaxAttempts,
		nowFn:       time.Now,
		codes:       make(map[string]CodeEntry),
		verified:    make(map[string]time.Time),
		loginKey:    make(map[string]CodeEntry),
	}, nil
}

func (s *Service) SendCode(ctx context.Context, email, locale string) error {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return err
	}

	code, err := randomDigits(6)
	if err != nil {
		return err
	}
	salt, err := randomHex(8)
	if err != nil {
		return err
	}

	now := s.nowFn().UTC()
	expiresAt := now.Add(s.codeTTL)

	s.mu.Lock()
	s.pruneExpiredVerifiedLocked(now)
	s.codes[normalized] = CodeEntry{
		Hash:      codeHash(salt, code),
		Salt:      salt,
		ExpiresAt: expiresAt,
		Attempts:  0,
	}
	delete(s.verified, normalized)
	delete(s.loginKey, normalized)
	s.mu.Unlock()

	subject, htmlBody, textBody := s.messageForLocale(locale, code, expiresAt, normalized)
	emailErr := safeEmailSend(func() error {
		return s.sender.Send(ctx, normalized, subject, htmlBody, textBody)
	})

	deliveredByBot := false
	var botErr error
	if s.notifier != nil {
		deliveredByBot, botErr = safeBotNotify(func() (bool, error) {
			return s.notifier.NotifyLoginCode(ctx, normalized, code, expiresAt, locale)
		})
	}

	if emailErr == nil || deliveredByBot {
		return nil
	}
	if botErr != nil {
		return fmt.Errorf("email send failed: %w; bot delivery failed: %v", emailErr, botErr)
	}
	return emailErr
}

func (s *Service) VerifyCode(_ context.Context, email, code string) (bool, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return false, err
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false, nil
	}

	now := s.nowFn().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredVerifiedLocked(now)

	entry, ok := s.codes[normalized]
	if !ok {
		return false, nil
	}
	if now.After(entry.ExpiresAt) {
		delete(s.codes, normalized)
		return false, nil
	}
	if entry.Attempts >= s.maxAttempts {
		delete(s.codes, normalized)
		return false, nil
	}

	if codeHash(entry.Salt, code) != entry.Hash {
		entry.Attempts++
		if entry.Attempts >= s.maxAttempts {
			delete(s.codes, normalized)
		} else {
			s.codes[normalized] = entry
		}
		return false, nil
	}

	delete(s.codes, normalized)
	s.verified[normalized] = now.Add(s.verifiedTTL)
	return true, nil
}

func (s *Service) ConsumeVerified(_ context.Context, email string) (bool, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return false, err
	}

	now := s.nowFn().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredVerifiedLocked(now)

	expiresAt, ok := s.verified[normalized]
	if !ok {
		return false, nil
	}
	if now.After(expiresAt) {
		delete(s.verified, normalized)
		return false, nil
	}

	delete(s.verified, normalized)
	return true, nil
}

func (s *Service) IssueLoginKey(_ context.Context, email string) (string, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return "", err
	}

	now := s.nowFn().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredVerifiedLocked(now)
	s.pruneExpiredLoginKeysLocked(now)

	expiresAt, ok := s.verified[normalized]
	if !ok || now.After(expiresAt) {
		delete(s.verified, normalized)
		return "", nil
	}

	key, err := randomHex(16)
	if err != nil {
		return "", err
	}
	salt, err := randomHex(8)
	if err != nil {
		return "", err
	}
	s.loginKey[normalized] = CodeEntry{
		Hash:      codeHash(salt, key),
		Salt:      salt,
		ExpiresAt: now.Add(s.verifiedTTL),
		Attempts:  0,
	}
	delete(s.verified, normalized)
	return key, nil
}

func (s *Service) ValidateLoginKey(_ context.Context, email, key string) (bool, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return false, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false, nil
	}

	now := s.nowFn().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLoginKeysLocked(now)

	entry, ok := s.loginKey[normalized]
	if !ok {
		return false, nil
	}
	if now.After(entry.ExpiresAt) {
		delete(s.loginKey, normalized)
		return false, nil
	}
	return codeHash(entry.Salt, key) == entry.Hash, nil
}

func (s *Service) ConsumeLoginKey(_ context.Context, email, key string) (bool, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return false, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false, nil
	}

	now := s.nowFn().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLoginKeysLocked(now)

	entry, ok := s.loginKey[normalized]
	if !ok {
		return false, nil
	}
	if now.After(entry.ExpiresAt) {
		delete(s.loginKey, normalized)
		return false, nil
	}
	if codeHash(entry.Salt, key) != entry.Hash {
		return false, nil
	}

	delete(s.loginKey, normalized)
	return true, nil
}

func (s *Service) pruneExpiredVerifiedLocked(now time.Time) {
	for email, expiresAt := range s.verified {
		if now.After(expiresAt) {
			delete(s.verified, email)
		}
	}
}

func (s *Service) pruneExpiredLoginKeysLocked(now time.Time) {
	for email, entry := range s.loginKey {
		if now.After(entry.ExpiresAt) {
			delete(s.loginKey, email)
		}
	}
}

func normalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return "", ErrInvalidEmail
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func codeHash(salt, code string) string {
	sum := sha256.Sum256([]byte(salt + ":" + code))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomDigits(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	chars := make([]byte, n)
	for i := 0; i < n; i++ {
		chars[i] = byte('0' + (buf[i] % 10))
	}
	return string(chars), nil
}

func (s *Service) messageForLocale(locale, code string, expiresAt time.Time, email string) (string, string, string) {
	expiry := expiresAt.Format("15:04")
	subject := s.translatef(locale, "mail.auth.email_code.subject", "Combox: verification code")
	text := s.translatef(
		locale,
		"mail.auth.email_code.text",
		"Your verification code: %s\nEmail: %s\nValid until %s UTC.\n\nIf this wasn't you, ignore this email.",
		code,
		email,
		expiry,
	)
	html := s.translatef(
		locale,
		"mail.auth.email_code.html",
		`<div style="background:#0b1220;border:1px solid #24344f;border-radius:16px;padding:20px;color:#e8edff;font-family:Arial,sans-serif"><p style="margin:0 0 10px 0;font-size:14px;opacity:.85">Combox</p><p style="margin:0 0 8px 0;font-size:14px;opacity:.85">Verification code</p><p style="margin:0 0 14px 0;font-size:34px;letter-spacing:6px;font-weight:700">%s</p><p style="margin:0 0 8px 0;font-size:14px">Email: <b>%s</b></p><p style="margin:0 0 8px 0;font-size:14px">Valid until %s UTC.</p><p style="margin:12px 0 0 0;font-size:12px;opacity:.8">If this wasn't you, ignore this email.</p></div>`,
		code,
		email,
		expiry,
	)
	return subject, html, text
}

func (s *Service) translatef(locale, key, fallback string, args ...any) string {
	pattern := fallback
	if s.i18n != nil {
		value := strings.TrimSpace(s.i18n.Translate(locale, key))
		if value != "" && value != key {
			pattern = value
		}
	}
	return fmt.Sprintf(pattern, args...)
}

func safeEmailSend(fn func() error) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("email sender panic: %v\n%s", rec, debug.Stack())
		}
	}()
	return fn()
}

func safeBotNotify(fn func() (bool, error)) (delivered bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			delivered = false
			err = fmt.Errorf("bot notifier panic: %v\n%s", rec, debug.Stack())
		}
	}()
	return fn()
}
