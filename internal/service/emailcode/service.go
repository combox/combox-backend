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
	return s.sendCode(ctx, email, locale, true)
}

func (s *Service) SendCodeEmailOnly(ctx context.Context, email, locale string) error {
	return s.sendCode(ctx, email, locale, false)
}

func (s *Service) sendCode(ctx context.Context, email, locale string, notify bool) error {
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
	if notify && s.notifier != nil {
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
	subject := s.translatef(locale, "mail.auth.email_code.subject", "ComBox: verification code")
	text := s.translatef(
		locale,
		"mail.auth.email_code.text",
		"Enter this temporary verification code to continue:\n\n%s\n\nValid until %s UTC.\nIf this wasn't you, ignore this email.",
		code,
		expiry,
	)
	headline := s.translatef(locale, "mail.auth.email_code.headline", "Enter this temporary verification code to continue:")
	validUntil := s.translatef(locale, "mail.auth.email_code.valid_until", "Valid until %s UTC.", expiry)
	ignore := s.translatef(locale, "mail.auth.email_code.ignore", "Please ignore this email if this wasn't you trying to sign in to ComBox.")
	emailLine := s.translatef(locale, "mail.auth.email_code.email_line", "Email: %s", email)
	signoff := s.translatef(locale, "mail.auth.email_code.signoff", "Best,<br/>The ComBox team")

	// Keep HTML style consistent across locales (tweb-like/OpenAI-like),
	// translate only small strings to avoid embedding full HTML in i18n JSON files.
	html := fmt.Sprintf(
		`<!doctype html>
<html>
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>ComBox verification code</title>
    <style type="text/css">
      /* Email client reset */
      img { border: 0; height: auto; outline: 0; text-decoration: none; }
      table { border-collapse: collapse !important; }
      body { margin: 0; padding: 0; width: 100%% !important; height: 100%% !important; }
      #bodyCell { padding: 20px; }
      #bodyTable { width: 560px; }
      @media only screen and (max-width: 480px) {
        #bodyCell, #bodyTable, body { width: 100%% !important; }
        #bodyTable { max-width: 560px !important; }
      }
    </style>
  </head>
  <body style="background-color:#ffffff;">
    <center>
      <table id="bodyTable" align="center" border="0" cellpadding="0" cellspacing="0" width="100%%"
        style="width:560px;margin:0;padding:0;background-color:#ffffff;">
        <tr>
          <td id="bodyCell" align="center" valign="top" style="padding:0 16px;">
            <table border="0" cellpadding="0" cellspacing="0" width="100%%" style="width:100%%;">
              <tr>
                <td style="padding:56px 0 22px 0;text-align:left;font-family:Roboto,Helvetica,Arial,sans-serif;">
                  <div style="font-size:28px;font-weight:800;color:#0b0f17;letter-spacing:0.2px;">ComBox</div>
                </td>
              </tr>
              <tr>
                <td style="text-align:left;font-family:Roboto,Helvetica,Arial,sans-serif;color:#334155;">
                  <p style="font-size:16px;line-height:24px;margin:0;color:#202123;">
                    %s
                  </p>
                  <p style="font-family:Menlo,Monaco,Consolas,Lucida Console,monospace;font-size:24px;line-height:28px;background-color:#F3F3F3;color:#5D5D5D;border-radius:16px;padding:28px 24px;margin:24px 0;">
                    %s
                  </p>
                  <p style="font-size:16px;line-height:24px;margin:0;color:#353740;">
                    %s
                  </p>
                  <p style="font-size:16px;line-height:24px;margin:16px 0 0 0;color:#353740;">
                    %s
                  </p>
                  <p style="font-size:12px;line-height:16px;margin:16px 0 0 0;color:#8F8F8F;">
                    %s
                  </p>
                </td>
              </tr>
              <tr>
                <td style="padding:32px 0 0 0;">
                  <div style="height:1px;background:#e5e7eb;width:100%%;"></div>
                </td>
              </tr>
              <tr>
                <td style="padding:18px 0 44px 0;text-align:left;font-family:Roboto,Helvetica,Arial,sans-serif;color:#94a3b8;font-size:12px;line-height:16px;">
                  %s
                </td>
              </tr>
            </table>
          </td>
        </tr>
      </table>
    </center>
  </body>
</html>`,
		headline,
		code,
		validUntil,
		ignore,
		emailLine,
		signoff,
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
