package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Sender struct {
	apiKey     string
	from       string
	baseURL    string
	httpClient *http.Client
}

type Config struct {
	APIKey  string
	From    string
	BaseURL string
}

func New(cfg Config) (*Sender, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("resend api key is required")
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("resend from is required")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.resend.com"
	}

	return &Sender{
		apiKey:  cfg.APIKey,
		from:    cfg.From,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

func (s *Sender) Send(ctx context.Context, to, subject, html, text string) error {
	payload := map[string]any{
		"from":    s.from,
		"to":      []string{to},
		"subject": subject,
		"html":    html,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("resend returned status %d", resp.StatusCode)
	}
	return nil
}
