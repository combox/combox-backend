package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	importURLTimeout  = 30 * time.Second
	maxImportURLBytes = int64(64 * 1024 * 1024)
)

var importURLLookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, network, host)
}

var allowedImportHostSuffixes = []string{
	// GIFs & Media
	"giphy.com", "media.giphy.com", "giphy.me",
	"tenor.com", "media.tenor.com", "media1.tenor.com",
	"imgur.com", "i.imgur.com",
	"gfycat.com", "thumbs.gfycat.com",

	// Social Media & Video
	"youtube.com", "youtu.be", "m.youtube.com", "www.youtube.com",
	"github.com", "githubusercontent.com", "githubassets.com", "raw.githubusercontent.com",
	"twitter.com", "x.com", "twimg.com", "pbs.twimg.com",
	"instagram.com", "cdninstagram.com",
	"facebook.com", "fbcdn.net",
	"tiktok.com", "v.tiktok.com",
	"reddit.com", "redditmedia.com", "redd.it", "i.redd.it", "v.redd.it",
	"discord.gg", "discordapp.com", "discordapp.net", "cdn.discordapp.com",
	"vimeo.com", "vimeocdn.com",
	"twitch.tv", "static-cdn.jtvnw.net",
	"linkedin.com", "licdn.com",
	"pinterest.com", "pinimg.com",

	// News & Articles (Major International)
	"reuters.com", "apnews.com", "bloomberg.com",
	"nytimes.com", "wsj.com", "wsj.net",
	"bbc.com", "bbc.co.uk", "bbci.co.uk",
	"cnn.com", "cnn.io",
	"theguardian.com", "guim.co.uk",
	"aljazeera.com",
	"forbes.com",
	"economist.com",
	"theatlantic.com",
	"newyorker.com",
	"nature.com",
	"science.org",
	"nationalgeographic.com",

	// Music & Audio
	"spotify.com", "scdn.co", "soundcloud.com", "sndcdn.com",
	"music.apple.com", "itunes.apple.com", "deezer.com", "dzcdn.net",
	"tidal.com", "bandcamp.com",

	// Movies & Streaming
	"netflix.com", "nflxso.net", "disneyplus.com", "disney.com",
	"hbo.com", "hbomax.com", "max.com", "primevideo.com",
	"apple.com/apple-tv-plus", "hulu.com", "imdb.com",

	// European News
	"lemonde.fr", "lefigaro.fr", "spiegel.de", "zeit.de", "dw.com",
	"elpais.com", "elmundo.es", "corriere.it", "repubblica.it",
	"euronews.com", "politico.eu", "france24.com",

	// Ukrainian News & Portals
	"pravda.com.ua", "unian.net", "ukrinform.ua", "tsn.ua", "nv.ua",
	"censor.net", "rbc.ua", "gordonua.com", "korrespondent.net",
	"interfax.com.ua", "suspilne.media", "babel.ua",

	// Russian-segment & Blogs
	"telegra.ph", "pikabu.ru", "meduza.io", "meduza.care",
	"tjournal.ru", "vc.ru", "habr.com", "dtf.ru",
	"rbc.ru", "kommersant.ru", "lenta.ru", "gazeta.ru",
	"vedomosti.ru", "ria.ru", "tass.ru",
	"snob.ru", "echo.msk.ru", "tvrain.ru", "novayagazeta.ru",
	"dzen.ru", "yandex.ru", "yastatic.net",
	"vk.com", "vk.me", "vk-cdn.net", "ok.ru",

	// Tech News & Gaming
	"techcrunch.com", "theverge.com", "wired.com",
	"arstechnica.com", "engadget.com", "gizmodo.com",
	"medium.com", "substack.com", "dev.to",
	"ign.com", "gamespot.com", "kotaku.com", "polygon.com", "eurogamer.net",
	"s3.amazonaws.com", "amazonaws.com",
	"storage.googleapis.com",
	"fastly.net", "akamaihd.net", "edgecastcdn.net",
}

var importURLClient = &http.Client{
	Timeout: importURLTimeout,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type ImportFromURLInput struct {
	UserID    string
	SourceURL string
	Filename  string
}

func normalizeImportSourceURL(raw string) string {
	source := strings.TrimSpace(raw)
	if source == "" {
		return ""
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return source
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	if host == "giphy.com" || host == "www.giphy.com" {
		segments := strings.Split(strings.Trim(strings.TrimSpace(parsed.Path), "/"), "/")
		for idx := len(segments) - 1; idx >= 0; idx-- {
			part := strings.TrimSpace(segments[idx])
			if part == "" {
				continue
			}
			pieces := strings.Split(part, "-")
			id := strings.TrimSpace(pieces[len(pieces)-1])
			if id == "" {
				continue
			}
			return "https://media.giphy.com/media/" + id + "/giphy.gif"
		}
	}
	return source
}

func inferImportedKind(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		if _, ok := allowedStreamMIMEs[mimeType]; ok {
			return "video"
		}
		return "file"
	case strings.HasPrefix(mimeType, "audio/") || mimeType == "application/ogg":
		if _, ok := allowedStreamMIMEs[mimeType]; ok {
			return "audio"
		}
		return "file"
	default:
		return "file"
	}
}

func filenameFromURL(parsed *url.URL, fallback string) string {
	if parsed == nil {
		return fallback
	}
	name := strings.TrimSpace(filepath.Base(parsed.Path))
	if name == "" || name == "." || name == "/" {
		return fallback
	}
	return name
}

func extensionFromMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/gif":
		return ".gif"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		return ""
	}
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '.', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	safe := strings.Trim(strings.TrimSpace(string(out)), "._")
	if safe == "" {
		return ""
	}
	return safe
}

func isBlockedImportHost(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return true
	}
	if hostname == "localhost" {
		return true
	}
	if strings.HasSuffix(hostname, ".local") {
		return true
	}
	if strings.HasSuffix(hostname, ".internal") {
		return true
	}
	return false
}

func isAllowedImportHost(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return false
	}
	for _, suf := range allowedImportHostSuffixes {
		suf = strings.ToLower(strings.TrimSpace(suf))
		if suf == "" {
			continue
		}
		if hostname == suf || strings.HasSuffix(hostname, "."+suf) {
			return true
		}
	}
	return false
}

func isPrivateOrLocalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	// IPv4: 0.0.0.0/8 and 100.64.0.0/10
	if v4 := ip.To4(); v4 != nil {
		a := v4[0]
		b := v4[1]
		if a == 0 {
			return true
		}
		if a == 100 && b >= 64 && b <= 127 {
			return true
		}
	}
	return false
}

func validateImportURL(ctx context.Context, parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid scheme")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("invalid host")
	}
	if parsed.User != nil {
		return fmt.Errorf("userinfo not allowed")
	}
	if strings.TrimSpace(parsed.Fragment) != "" {
		return fmt.Errorf("fragment not allowed")
	}

	hostname := strings.TrimSpace(parsed.Hostname())
	if isBlockedImportHost(hostname) {
		return fmt.Errorf("blocked host")
	}
	if !isAllowedImportHost(hostname) {
		return fmt.Errorf("host not allowed")
	}

	// Block direct IP literals.
	if ip := net.ParseIP(hostname); ip != nil {
		if isPrivateOrLocalIP(ip) {
			return fmt.Errorf("blocked ip")
		}
	}

	// Block dangerous ports (allow 80/443 only).
	if port := strings.TrimSpace(parsed.Port()); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return fmt.Errorf("invalid port")
		}
		if p != 80 && p != 443 {
			return fmt.Errorf("blocked port")
		}
	}

	// Resolve DNS and block private/local ranges.
	addrs, err := importURLLookupIP(ctx, "ip", hostname)
	if err != nil {
		return fmt.Errorf("dns lookup failed")
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no dns results")
	}
	for _, ip := range addrs {
		if isPrivateOrLocalIP(ip) {
			return fmt.Errorf("blocked ip")
		}
	}
	return nil
}

func (s *Service) ImportFromURL(ctx context.Context, input ImportFromURLInput) (GetAttachmentOutput, error) {
	userID := strings.TrimSpace(input.UserID)
	sourceURL := normalizeImportSourceURL(input.SourceURL)
	filename := strings.TrimSpace(input.Filename)
	if userID == "" || sourceURL == "" {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	parsedURL, err := url.Parse(sourceURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || strings.TrimSpace(parsedURL.Host) == "" {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}
	if err := validateImportURL(ctx, parsedURL); err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	reqCtx, cancel := context.WithTimeout(ctx, importURLTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input", Cause: err}
	}
	req.Header.Set("User-Agent", "combox-backend-media-import/1.0")

	resp, err := importURLClient.Do(req)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	tmpPath, cleanup, err := tempPath(".import")
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	defer cleanup()

	tmp, err := os.Create(tmpPath)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	var total int64
	var sniff bytes.Buffer
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			total += int64(n)
			if total > maxImportURLBytes {
				_ = tmp.Close()
				return GetAttachmentOutput{}, &Error{
					Code:       CodeInvalidArgument,
					MessageKey: "error.media.invalid_input",
					Details:    map[string]string{"size_bytes": "max_64_mb"},
				}
			}
			if sniff.Len() < 512 {
				remain := 512 - sniff.Len()
				if remain > len(chunk) {
					remain = len(chunk)
				}
				_, _ = sniff.Write(chunk[:remain])
			}
			if _, writeErr := tmp.Write(chunk); writeErr != nil {
				_ = tmp.Close()
				return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: writeErr}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = tmp.Close()
			return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: readErr}
		}
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: closeErr}
	}
	if total <= 0 {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mimeType == "" {
		mimeType = strings.ToLower(strings.TrimSpace(http.DetectContentType(sniff.Bytes())))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	if filename == "" {
		filename = filenameFromURL(parsedURL, "")
	}
	filename = sanitizeFilename(filename)
	if filename == "" {
		filename = "imported_" + uuid.NewString()
	}
	if filepath.Ext(filename) == "" {
		if ext := extensionFromMime(mimeType); ext != "" {
			filename += ext
		}
	}

	kind := inferImportedKind(mimeType)
	id := uuid.NewString()
	objectKey := fmt.Sprintf("u/%s/%s/%s", userID, id, filename)

	file, err := os.Open(tmpPath)
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}
	defer file.Close()
	if err := s.store.PutObject(ctx, objectKey, mimeType, file, total); err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	size := total
	created, err := s.repo.CreateAttachment(ctx, Attachment{
		ID:                 id,
		UserID:             userID,
		Filename:           filename,
		MimeType:           mimeType,
		Kind:               kind,
		Variant:            "original",
		IsClientCompressed: false,
		SizeBytes:          &size,
		Bucket:             s.store.Bucket(),
		ObjectKey:          objectKey,
		UploadType:         "server_import",
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	})
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	go s.processPreviewAsync(context.Background(), created)
	return s.GetAttachment(ctx, userID, created.ID)
}
