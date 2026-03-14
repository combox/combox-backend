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

// importURLClient is configured with a custom DialContext to prevent SSRF and DNS Rebinding.
// It resolves the IP once, validates it, and connects directly to that IP.
var importURLClient = &http.Client{
	Timeout: importURLTimeout,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		// Stop redirects to prevent bypassing host validation via 3xx responses.
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			// Perform DNS lookup manually to validate the resulting IP before connection.
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}

			var safeIP net.IP
			for _, ip := range ips {
				// Block private, loopback, and local IP ranges.
				if isPrivateOrLocalIP(ip) {
					return nil, fmt.Errorf("SSRF prevention: blocked connection to private IP %s", ip.String())
				}
				if safeIP == nil {
					safeIP = ip
				}
			}

			if safeIP == nil {
				return nil, fmt.Errorf("no valid public IP addresses found for host %s", host)
			}

			// Connect to the specific validated IP to prevent DNS Rebinding between check and use.
			dialer := &net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(safeIP.String(), port))
		},
	},
}

var allowedImportHostSuffixes = []string{
	"giphy.com", "media.giphy.com", "giphy.me",
	"tenor.com", "media.tenor.com", "media1.tenor.com",
	"imgur.com", "i.imgur.com",
	"gfycat.com", "thumbs.gfycat.com",
	"youtube.com", "youtu.be", "m.youtube.com", "www.youtube.com",
	"github.com", "githubusercontent.com", "githubassets.com", "raw.githubusercontent.com",
	"twitter.com", "x.com", "twimg.com", "pbs.twimg.com",
	"instagram.com", "cdninstagram.com",
	"facebook.com", "fbcdn.net",
	"tiktok.com", "v.tiktok.com",
	"reddit.com", "redditmedia.com", "redd.it", "i.redd.it", "v.redd.it",
	"discord.gg", "discordapp.com", "discordapp.net", "cddn.discordapp.com",
	"vimeo.com", "vimdeocdn.com",
	"twitch.tv", "static-cdn.jtvnw.net",
	"linkedin.com", "licdn.com",
	"pinterest.com", "pinimg.com",
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
	"spotify.com", "scdn.co", "soundcloud.com", "sndcdn.com",
	"music.apple.com", "itunes.apple.com", "deezer.com", "dzcdn.net",
	"tidal.com", "bandcamp.com",
	"netflix.com", "nflxso.net", "disneyplus.com", "disney.com",
	"hbo.com", "hbomax.com", "max.com", "primevideo.com",
	"hulu.com", "imdb.com",
	"lemonde.fr", "lefigaro.fr", "spiegel.de", "zeit.de", "dw.com",
	"elpais.com", "elmundo.es", "corriere.it", "repubblica.it",
	"euronews.com", "politico.eu", "france24.com",
	"pravda.com.ua", "unian.net", "ukrinform.ua", "tsn.ua", "nv.ua",
	"censor.net", "rbc.ua", "gordonua.com", "korrespondent.net",
	"interfax.com.ua", "suspilne.media", "babel.ua",
	"telegra.ph", "pikabu.ru", "meduza.io", "meduza.care",
	"tjournal.ru", "vc.ru", "habr.com", "dtf.ru",
	"rbc.ru", "kommersant.ru", "lenta.ru", "gazeta.ru",
	"vedomosti.ru", "ria.ru", "tass.ru",
	"snob.ru", "echo.msk.ru", "tvrain.ru", "novayagazeta.ru",
	"dzen.ru", "yandex.ru", "yastatic.net",
	"vk.com", "vk.me", "vk-cdn.net", "ok.ru",
	"techcrunch.com", "theverge.com", "wired.com",
	"arstechnica.com", "engadget.com", "gizmodo.com",
	"medium.com", "substack.com", "dev.to",
	"ign.com", "gamespot.com", "kotaku.com", "polygon.com", "eurogamer.net",
	"s3.amazonaws.com", "amazonaws.com",
	"storage.googleapis.com",
	"fastly.net", "akamaihd.net", "edgecastcdn.net",
}

type ImportFromURLInput struct {
	UserID    string
	SourceURL string
	Filename  string
}

// normalizeImportSourceURL extracts direct media links from known platforms like Giphy.
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
		return "video"
	case strings.HasPrefix(mimeType, "audio/") || mimeType == "application/ogg":
		return "audio"
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
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			out = append(out, r)
		case r == '.', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	safe := strings.Trim(strings.TrimSpace(string(out)), "._")
	return safe
}

func isAllowedImportHost(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return false
	}
	for _, suf := range allowedImportHostSuffixes {
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
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		a, b := v4[0], v4[1]
		if a == 0 || (a == 100 && b >= 64 && b <= 127) {
			return true
		}
	}
	return false
}

// validateImportURL checks URL structure and allowlist before making any network calls.
func validateImportURL(parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme")
	}

	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".local") {
		return fmt.Errorf("prohibited host")
	}

	if !isAllowedImportHost(hostname) {
		return fmt.Errorf("host not in allowlist")
	}

	if parsed.User != nil {
		return fmt.Errorf("authentication in URL is not allowed")
	}

	if port := parsed.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || (p != 80 && p != 443) {
			return fmt.Errorf("prohibited port")
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
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	// Step 1: Structural and Allowlist Validation
	if err := validateImportURL(parsedURL); err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	// Step 2: Strip sensitive components (query, fragments) to prevent injection.
	validatedURL := url.URL{
		Scheme: parsedURL.Scheme,
		Host:   parsedURL.Host,
		Path:   parsedURL.Path,
	}

	reqCtx, cancel := context.WithTimeout(ctx, importURLTimeout)
	defer cancel()

	// Step 3: Create the request. Security logic is handled by importURLClient.Transport.
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, validatedURL.String(), nil)
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

	// Step 4: Stream response body to a temporary file while checking size and sniffing MIME type.
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
				return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
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
	_ = tmp.Close()

	if total <= 0 {
		return GetAttachmentOutput{}, &Error{Code: CodeInvalidArgument, MessageKey: "error.media.invalid_input"}
	}

	// Step 5: Detect MIME type and finalize filename.
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mimeType == "" {
		mimeType = http.DetectContentType(sniff.Bytes())
	}

	if filename == "" {
		filename = filenameFromURL(parsedURL, "imported_file")
	}
	filename = sanitizeFilename(filename)
	if filepath.Ext(filename) == "" {
		filename += extensionFromMime(mimeType)
	}

	// Step 6: Store file and record in database.
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

	attachmentSize := total
	created, err := s.repo.CreateAttachment(ctx, Attachment{
		ID:         id,
		UserID:     userID,
		Filename:   filename,
		MimeType:   mimeType,
		Kind:       kind,
		Variant:    "original",
		SizeBytes:  &attachmentSize,
		Bucket:     s.store.Bucket(),
		ObjectKey:  objectKey,
		UploadType: "server_import",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return GetAttachmentOutput{}, &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: err}
	}

	go s.processPreviewAsync(context.Background(), created)

	return s.GetAttachment(ctx, userID, created.ID)
}
