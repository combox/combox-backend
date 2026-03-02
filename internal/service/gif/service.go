package gif

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	PreviewURL string `json:"preview_url"`
	URL        string `json:"url"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type SearchResult struct {
	Items   []Item `json:"items"`
	NextPos string `json:"next_pos,omitempty"`
}

type Service struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func cleanURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func New(apiKey string) *Service {
	return &Service{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: "https://api.giphy.com",
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

func (s *Service) Enabled() bool {
	return s != nil && strings.TrimSpace(s.apiKey) != ""
}

func (s *Service) SearchGIFs(ctx context.Context, q, pos string, limit int) (SearchResult, error) {
	return s.search(ctx, q, pos, limit)
}

type tenorResponse struct {
	Data []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Images struct {
			FixedWidth struct {
				URL    string `json:"url"`
				Width  string `json:"width"`
				Height string `json:"height"`
			} `json:"fixed_width"`
			FixedWidthStill struct {
				URL string `json:"url"`
			} `json:"fixed_width_still"`
			Original struct {
				URL string `json:"url"`
			} `json:"original"`
		} `json:"images"`
	} `json:"data"`
	Pagination struct {
		Offset int `json:"offset"`
		Count  int `json:"count"`
		Total  int `json:"total_count"`
	} `json:"pagination"`
}

func (s *Service) search(ctx context.Context, q, pos string, limit int) (SearchResult, error) {
	if !s.Enabled() {
		return SearchResult{}, nil
	}

	if limit <= 0 {
		limit = 24
	}
	if limit > 50 {
		limit = 50
	}

	offset := 0
	if strings.TrimSpace(pos) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(pos)); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	params := url.Values{}
	params.Set("api_key", s.apiKey)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	params.Set("rating", "pg-13")
	params.Set("lang", "en")

	path := "/v1/gifs/trending"
	if strings.TrimSpace(q) != "" {
		path = "/v1/gifs/search"
		params.Set("q", strings.TrimSpace(q))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return SearchResult{}, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return SearchResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SearchResult{}, fmt.Errorf("giphy status: %d", resp.StatusCode)
	}

	var payload tenorResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return SearchResult{}, err
	}

	items := make([]Item, 0, len(payload.Data))
	for _, r := range payload.Data {
		full := strings.TrimSpace(r.Images.Original.URL)
		if full == "" {
			full = strings.TrimSpace(r.Images.FixedWidth.URL)
		}
		if full == "" {
			continue
		}
		preview := strings.TrimSpace(r.Images.FixedWidthStill.URL)
		if preview == "" {
			preview = strings.TrimSpace(r.Images.FixedWidth.URL)
		}
		w, _ := strconv.Atoi(strings.TrimSpace(r.Images.FixedWidth.Width))
		h, _ := strconv.Atoi(strings.TrimSpace(r.Images.FixedWidth.Height))

		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = "GIF"
		}
		id := strings.TrimSpace(r.ID)
		shareURL := cleanURL(full)
		if id != "" {
			shareURL = "https://giphy.com/gifs/" + id
		}

		items = append(items, Item{
			ID:         id,
			Title:      title,
			PreviewURL: preview,
			URL:        shareURL,
			Width:      w,
			Height:     h,
		})
	}

	nextPos := ""
	nextOffset := payload.Pagination.Offset + payload.Pagination.Count
	if nextOffset < payload.Pagination.Total && payload.Pagination.Count > 0 {
		nextPos = strconv.Itoa(nextOffset)
	}

	return SearchResult{
		Items:   items,
		NextPos: nextPos,
	}, nil
}
