package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider is a real translation engine behind a uniform contract. Adding a
// new engine means implementing these four methods — no handler changes.
type Provider interface {
	// Name is the engine id for logs ("mymemory", "libretranslate").
	Name() string
	// SupportsAutoDetect reports whether Translate accepts an empty source
	// and resolves it server-side.
	SupportsAutoDetect() bool
	// Translate returns the translated text and the resolved source language.
	// source may be "" only when SupportsAutoDetect is true.
	Translate(ctx context.Context, text, source, target string) (translated, detectedSource string, err error)
	// Languages lists the target languages offered to clients.
	Languages(ctx context.Context) ([]Language, error)
}

const myMemoryBaseURL = "https://api.mymemory.translated.net"

// myMemoryProvider is the default engine: free MyMemory anonymous endpoint,
// no API key required. Verified reachable 2026-09-30 (GET /get returns 200).
// Limitation: MyMemory has no detect endpoint and rejects langpair values
// like "auto|x" or "|x" with 403, so an explicit source language is required.
type myMemoryProvider struct {
	client *http.Client
	email  string
}

func NewMyMemoryProvider(email string, client *http.Client) Provider {
	return &myMemoryProvider{client: httpClientOrDefault(client), email: strings.TrimSpace(email)}
}

func (p *myMemoryProvider) Name() string { return "mymemory" }

func (p *myMemoryProvider) SupportsAutoDetect() bool { return false }

func (p *myMemoryProvider) Translate(ctx context.Context, text, source, target string) (string, string, error) {
	if strings.TrimSpace(text) == "" {
		return "", "", &Error{Code: CodeInvalidArgument, Message: "text is required"}
	}
	if strings.TrimSpace(source) == "" {
		return "", "", &Error{
			Code:    CodeDetectNotSupported,
			Message: "source language is required with the MyMemory engine; set TRANSLATE_ENGINE_URL to a LibreTranslate-compatible endpoint for auto-detect",
		}
	}
	params := url.Values{}
	params.Set("q", text)
	params.Set("langpair", source+"|"+target)
	if p.email != "" {
		params.Set("de", p.email)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, myMemoryBaseURL+"/get?"+params.Encode(), nil)
	if err != nil {
		return "", "", &Error{Code: CodeInternal, Message: err.Error()}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", &Error{Code: CodeUpstream, Message: "translation engine unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", &Error{Code: CodeUpstream, Message: "translation engine read failed: " + err.Error()}
	}
	translated, err := parseMyMemoryPayload(body, source, target)
	if err != nil {
		return "", "", err
	}
	return translated, source, nil
}

func (p *myMemoryProvider) Languages(_ context.Context) ([]Language, error) {
	return copyLanguages(staticLanguages), nil
}

// myMemoryPayload mirrors the /get response envelope. responseStatus arrives
// as a number (200, 403, 429) in practice; flexStatus tolerates a string form.
type myMemoryPayload struct {
	ResponseData struct {
		TranslatedText string `json:"translatedText"`
	} `json:"responseData"`
	ResponseDetails string     `json:"responseDetails"`
	ResponseStatus  flexStatus `json:"responseStatus"`
	QuotaFinished   bool       `json:"quotaFinished"`
}

type flexStatus int

func (s *flexStatus) UnmarshalJSON(data []byte) error {
	var num int
	if err := json.Unmarshal(data, &num); err == nil {
		*s = flexStatus(num)
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(str), "%d", &parsed); err != nil {
		return err
	}
	*s = flexStatus(parsed)
	return nil
}

// parseMyMemoryPayload maps the upstream envelope onto service errors:
//   - 200 with non-empty translatedText → success (JSON unescaping of \uXXXX
//     is handled by encoding/json).
//   - "INVALID ... LANGUAGE ..." details → unsupported_language (400).
//   - "QUERY LENGTH LIMIT EXCEEDED" → invalid_argument (400).
//   - quotaFinished or 429 → upstream_failed (502), the client may retry.
//   - anything else → upstream_failed (502).
func parseMyMemoryPayload(body []byte, source, target string) (string, error) {
	var payload myMemoryPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", &Error{Code: CodeUpstream, Message: "translation engine returned invalid JSON"}
	}
	details := strings.ToUpper(strings.TrimSpace(payload.ResponseDetails))
	translated := strings.TrimSpace(payload.ResponseData.TranslatedText)
	if int(payload.ResponseStatus) == http.StatusOK && translated != "" &&
		!strings.HasPrefix(strings.ToUpper(translated), "INVALID ") &&
		!strings.HasPrefix(strings.ToUpper(translated), "QUERY LENGTH") {
		return payload.ResponseData.TranslatedText, nil
	}
	switch {
	case strings.Contains(details, "QUERY LENGTH"):
		return "", &Error{Code: CodeInvalidArgument, Message: "text exceeds the translation engine quota per request"}
	case strings.Contains(details, "INVALID") && strings.Contains(details, "LANGUAGE"):
		return "", &Error{Code: CodeUnsupportedLanguage, Message: fmt.Sprintf("unsupported language pair: %s -> %s", source, target)}
	case strings.Contains(details, "INVALID EMAIL"):
		return "", &Error{Code: CodeUpstream, Message: "translation engine rejected the sender address"}
	case payload.QuotaFinished || int(payload.ResponseStatus) == http.StatusTooManyRequests:
		return "", &Error{Code: CodeUpstream, Message: "translation engine quota exhausted, retry later"}
	case strings.Contains(translated, "MYMEMORY WARNING"):
		return "", &Error{Code: CodeUpstream, Message: "translation engine returned a warning instead of text"}
	default:
		if int(payload.ResponseStatus) != 0 {
			return "", &Error{Code: CodeUpstream, Message: fmt.Sprintf("translation engine status %d", int(payload.ResponseStatus))}
		}
		return "", &Error{Code: CodeUpstream, Message: "translation engine returned an empty result"}
	}
}

// libreTranslateProvider talks to any LibreTranslate-compatible HTTP API
// (self-hosted LibreTranslate, compatible gateways). Base URL comes from
// TRANSLATE_ENGINE_URL; optional TRANSLATE_API_KEY is sent as api_key.
type libreTranslateProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewLibreTranslateProvider(baseURL, apiKey string, client *http.Client) Provider {
	return &libreTranslateProvider{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		client:  httpClientOrDefault(client),
	}
}

func (p *libreTranslateProvider) Name() string { return "libretranslate" }

func (p *libreTranslateProvider) SupportsAutoDetect() bool { return true }

type libreTranslateRequest struct {
	Q            string `json:"q"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	Format       string `json:"format"`
	APIKey       string `json:"api_key,omitempty"`
	Alternatives int    `json:"alternatives,omitempty"`
}

type libreTranslateResponse struct {
	TranslatedText   string `json:"translatedText"`
	DetectedLanguage any    `json:"detectedLanguage"`
}

type libreDetectHit struct {
	Language   string  `json:"language"`
	Confidence float64 `json:"confidence"`
}

func (p *libreTranslateProvider) Translate(ctx context.Context, text, source, target string) (string, string, error) {
	if strings.TrimSpace(text) == "" {
		return "", "", &Error{Code: CodeInvalidArgument, Message: "text is required"}
	}
	resolved := strings.TrimSpace(source)
	if resolved == "" {
		// Auto-detect through the engine API (/detect, top-1 hit) when the
		// caller did not pass a source language.
		detected, err := p.Detect(ctx, text)
		if err != nil {
			return "", "", err
		}
		resolved = detected
	}
	payload := libreTranslateRequest{Q: text, Source: resolved, Target: target, Format: "text", APIKey: p.apiKey}
	var out libreTranslateResponse
	if err := p.postJSON(ctx, "/translate", payload, &out); err != nil {
		return "", "", err
	}
	translated := strings.TrimSpace(out.TranslatedText)
	if translated == "" {
		return "", "", &Error{Code: CodeUpstream, Message: "translation engine returned an empty result"}
	}
	if detected := libreDetectedLanguage(out.DetectedLanguage); detected != "" {
		resolved = detected
	}
	return out.TranslatedText, resolved, nil
}

// Detect resolves the source language via POST /detect and returns the
// single best (top-1 by confidence) hit.
func (p *libreTranslateProvider) Detect(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", &Error{Code: CodeInvalidArgument, Message: "text is required"}
	}
	body := map[string]any{"q": text}
	if p.apiKey != "" {
		body["api_key"] = p.apiKey
	}
	var hits []libreDetectHit
	if err := p.postJSON(ctx, "/detect", body, &hits); err != nil {
		return "", err
	}
	if len(hits) == 0 || strings.TrimSpace(hits[0].Language) == "" {
		return "", &Error{Code: CodeUpstream, Message: "translation engine could not detect the language"}
	}
	return NormalizeLang(hits[0].Language), nil
}

func (p *libreTranslateProvider) Languages(ctx context.Context) ([]Language, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/languages", nil)
	if err != nil {
		return copyLanguages(staticLanguages), nil
	}
	resp, err := p.client.Do(req)
	if err != nil {
		// Engine down must not break the language picker: serve the
		// verified static list instead.
		return copyLanguages(staticLanguages), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return copyLanguages(staticLanguages), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return copyLanguages(staticLanguages), nil
	}
	var items []Language
	if err := json.Unmarshal(body, &items); err != nil {
		return copyLanguages(staticLanguages), nil
	}
	out := make([]Language, 0, len(items))
	for _, item := range items {
		code := NormalizeLang(item.Code)
		name := strings.TrimSpace(item.Name)
		if code == "" || name == "" || !IsSupported(code) {
			continue
		}
		out = append(out, Language{Code: code, Name: name})
	}
	if len(out) == 0 {
		return copyLanguages(staticLanguages), nil
	}
	return out, nil
}

func (p *libreTranslateProvider) postJSON(ctx context.Context, path string, payload, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return &Error{Code: CodeInternal, Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, strings.NewReader(string(raw)))
	if err != nil {
		return &Error{Code: CodeInternal, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return &Error{Code: CodeUpstream, Message: "translation engine unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &Error{Code: CodeUpstream, Message: "translation engine read failed: " + err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
			return &Error{Code: CodeUnsupportedLanguage, Message: fmt.Sprintf("translation engine rejected the request (status %d)", resp.StatusCode)}
		}
		return &Error{Code: CodeUpstream, Message: fmt.Sprintf("translation engine status %d", resp.StatusCode)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &Error{Code: CodeUpstream, Message: "translation engine returned invalid JSON"}
	}
	return nil
}

// libreDetectedLanguage accepts both LibreTranslate shapes:
// {"language":"en","confidence":..} and the plain string "en".
func libreDetectedLanguage(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if lang, ok := typed["language"].(string); ok {
			return NormalizeLang(lang)
		}
	case string:
		return NormalizeLang(typed)
	}
	return ""
}

func httpClientOrDefault(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 8 * time.Second}
}
