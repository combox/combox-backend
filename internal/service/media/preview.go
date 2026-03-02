package media

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	previewMaxEdge = 64
)

var gradientPalette = [][2]color.RGBA{
	{{R: 74, G: 120, B: 255, A: 255}, {R: 28, G: 53, B: 128, A: 255}},
	{{R: 83, G: 189, B: 160, A: 255}, {R: 28, G: 93, B: 76, A: 255}},
	{{R: 161, G: 121, B: 255, A: 255}, {R: 70, G: 40, B: 129, A: 255}},
	{{R: 244, G: 132, B: 93, A: 255}, {R: 133, G: 60, B: 35, A: 255}},
	{{R: 69, G: 178, B: 214, A: 255}, {R: 24, G: 79, B: 95, A: 255}},
}

func (s *Service) processPreviewAsync(ctx context.Context, a Attachment) {
	_ = s.repo.SetProcessing(ctx, a.ID, "processing", nil, nil, nil, nil)

	fail := func(err error) {
		msg := err.Error()
		now := time.Now().UTC()
		_ = s.repo.SetProcessing(ctx, a.ID, "failed", &msg, nil, nil, nil)
		errorCode := "hls_failed"
		_ = s.repo.FinalizeSessionByAttachment(ctx, a.ID, "failed", nil, &errorCode, &msg, now)
	}

	previewBytes, err := s.buildPreview(ctx, a)
	if err != nil {
		fail(err)
		return
	}

	var hlsMasterKey *string
	if strings.EqualFold(a.Kind, "video") || strings.EqualFold(a.Kind, "audio") {
		masterKey, hlsErr := s.buildAndUploadHLSCopy(ctx, a)
		if hlsErr != nil {
			fail(hlsErr)
			return
		}
		hlsMasterKey = &masterKey
		if shouldDeleteOriginalAfterHLS() {
			if delErr := s.store.DeleteObject(ctx, a.ObjectKey); delErr != nil {
				fail(fmt.Errorf("delete original after hls failed: %w", delErr))
				return
			}
		}
	}

	var previewKey *string
	if len(previewBytes) > 0 {
		key := fmt.Sprintf("u/%s/%s/preview/tiny.jpg", strings.TrimSpace(a.UserID), strings.TrimSpace(a.ID))
		if err := s.store.PutObject(ctx, key, "image/jpeg", bytes.NewReader(previewBytes), int64(len(previewBytes))); err != nil {
			fail(err)
			return
		}
		previewKey = &key
	}

	now := time.Now().UTC()
	_ = s.repo.SetProcessing(ctx, a.ID, "ready", nil, previewKey, hlsMasterKey, &now)
	_ = s.repo.FinalizeSessionByAttachment(ctx, a.ID, "ready", hlsMasterKey, nil, nil, now)
}

func (s *Service) buildPreview(ctx context.Context, a Attachment) ([]byte, error) {
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	switch kind {
	case "image":
		return s.buildImagePreview(ctx, a.ObjectKey)
	case "video":
		return s.buildVideoPreview(ctx, a.ObjectKey)
	case "audio":
		return s.buildAudioPreview(ctx, a.ObjectKey, a.Filename)
	default:
		return nil, nil
	}
}

func (s *Service) buildImagePreview(ctx context.Context, objectKey string) ([]byte, error) {
	rc, err := s.store.GetObject(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	raw, err := io.ReadAll(io.LimitReader(rc, 32<<20))
	if err != nil {
		return nil, err
	}
	img, err := decodeImage(raw)
	if err != nil {
		return nil, err
	}
	return encodeTinyJPEG(img)
}

func (s *Service) buildVideoPreview(ctx context.Context, objectKey string) ([]byte, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return encodeTinyJPEG(makeGradientPlaceholder(objectKey, previewMaxEdge, previewMaxEdge))
	}

	inputPath, cleanupIn, err := s.downloadToTempFile(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	defer cleanupIn()

	out, cleanupOut, err := tempPath(".jpg")
	if err != nil {
		return nil, err
	}
	defer cleanupOut()

	cmd := exec.CommandContext(
		ctx,
		ffmpegPath,
		"-y",
		"-i", inputPath,
		"-frames:v", "1",
		"-vf", "scale=64:-1:flags=fast_bilinear",
		"-q:v", "31",
		out,
	)
	if cmdErr := cmd.Run(); cmdErr != nil {
		return encodeTinyJPEG(makeGradientPlaceholder(objectKey, previewMaxEdge, previewMaxEdge))
	}
	return os.ReadFile(out)
}

func (s *Service) buildAudioPreview(ctx context.Context, objectKey, filename string) ([]byte, error) {
	ffmpegPath, ffmpegErr := exec.LookPath("ffmpeg")
	if ffmpegErr == nil {
		inputPath, cleanupIn, err := s.downloadToTempFile(ctx, objectKey)
		if err == nil {
			defer cleanupIn()

			out, cleanupOut, outErr := tempPath(".jpg")
			if outErr == nil {
				defer cleanupOut()

				cmd := exec.CommandContext(
					ctx,
					ffmpegPath,
					"-y",
					"-i", inputPath,
					"-an",
					"-frames:v", "1",
					"-vf", "scale=64:-1:flags=fast_bilinear",
					out,
				)
				if cmd.Run() == nil {
					if bytesOut, readErr := os.ReadFile(out); readErr == nil && len(bytesOut) > 0 {
						return bytesOut, nil
					}
				}
			}
		}
	}

	// Fallback for audio without embedded cover: deterministic tiny gradient image.
	return encodeTinyJPEG(makeGradientPlaceholder(filename, previewMaxEdge, previewMaxEdge))
}

func (s *Service) downloadToTempFile(ctx context.Context, objectKey string) (string, func(), error) {
	rc, err := s.store.GetObject(ctx, objectKey)
	if err != nil {
		return "", nil, err
	}

	p, cleanup, err := tempPath(filepath.Ext(objectKey))
	if err != nil {
		_ = rc.Close()
		return "", nil, err
	}

	f, err := os.Create(p)
	if err != nil {
		cleanup()
		_ = rc.Close()
		return "", nil, err
	}
	if _, err = io.Copy(f, rc); err != nil {
		_ = f.Close()
		_ = rc.Close()
		cleanup()
		return "", nil, err
	}
	_ = f.Close()
	_ = rc.Close()
	return p, cleanup, nil
}

func tempPath(ext string) (string, func(), error) {
	pattern := "combox-preview-*"
	if strings.TrimSpace(ext) != "" {
		pattern += ext
	}
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	_ = f.Close()
	cleanup := func() { _ = os.Remove(path) }
	return path, cleanup, nil
}

func decodeImage(raw []byte) (image.Image, error) {
	if img, err := jpeg.Decode(bytes.NewReader(raw)); err == nil {
		return img, nil
	}
	if img, err := png.Decode(bytes.NewReader(raw)); err == nil {
		return img, nil
	}
	if img, err := gif.Decode(bytes.NewReader(raw)); err == nil {
		return img, nil
	}
	return nil, fmt.Errorf("unsupported image format")
}

func encodeTinyJPEG(src image.Image) ([]byte, error) {
	resized := resizeToFit(src, previewMaxEdge, previewMaxEdge)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, resized, &jpeg.Options{Quality: 34}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func resizeToFit(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	sw := b.Dx()
	sh := b.Dy()
	if sw <= 0 || sh <= 0 {
		return image.NewRGBA(image.Rect(0, 0, maxW, maxH))
	}

	scale := math.Min(float64(maxW)/float64(sw), float64(maxH)/float64(sh))
	if scale > 1 {
		scale = 1
	}
	dw := int(math.Max(1, math.Round(float64(sw)*scale)))
	dh := int(math.Max(1, math.Round(float64(sh)*scale)))

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := b.Min.Y + int(float64(y)*float64(sh)/float64(dh))
		for x := 0; x < dw; x++ {
			sx := b.Min.X + int(float64(x)*float64(sw)/float64(dw))
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func makeGradientPlaceholder(seed string, w, h int) image.Image {
	pair := gradientPalette[gradientIndex(seed)]
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(maxInt(h-1, 1))
		r := uint8(float64(pair[0].R)*(1-t) + float64(pair[1].R)*t)
		g := uint8(float64(pair[0].G)*(1-t) + float64(pair[1].G)*t)
		b := uint8(float64(pair[0].B)*(1-t) + float64(pair[1].B)*t)
		draw.Draw(img, image.Rect(0, y, w, y+1), &image.Uniform{C: color.RGBA{R: r, G: g, B: b, A: 255}}, image.Point{}, draw.Src)
	}
	return img
}

func gradientIndex(seed string) int {
	sum := sha1.Sum([]byte(strings.TrimSpace(strings.ToLower(seed))))
	return int(sum[0]) % len(gradientPalette)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func shouldDeleteOriginalAfterHLS() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("MEDIA_DELETE_ORIGINAL_AFTER_HLS")))
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}
	return enabled
}

func probePrimaryVideoCodec(ctx context.Context, inputPath string) (string, error) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(
		ctx,
		ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return "", fmt.Errorf("ffprobe failed: %w (%s)", runErr, strings.TrimSpace(string(out)))
	}
	return strings.ToLower(strings.TrimSpace(string(out))), nil
}

func (s *Service) buildAndUploadHLSCopy(ctx context.Context, a Attachment) (string, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg is not available: %w", err)
	}

	inputPath, cleanupIn, err := s.downloadToTempFile(ctx, a.ObjectKey)
	if err != nil {
		return "", err
	}
	defer cleanupIn()

	workDir, err := os.MkdirTemp("", "combox-hls-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	masterPath := filepath.Join(workDir, "master.m3u8")
	segmentPattern := filepath.Join(workDir, "seg_%06d.m4s")
	args := []string{
		"-y",
		"-i", inputPath,
		"-map", "0:v?",
		"-map", "0:a?",
		"-sn",
		"-dn",
		"-c", "copy",
	}
	// Safari and some embedded players reject HEVC fMP4 tagged as "hev1".
	// Keep copy-mode, but retag to "hvc1" when the source video codec is HEVC.
	if codecName, probeErr := probePrimaryVideoCodec(ctx, inputPath); probeErr == nil && codecName == "hevc" {
		args = append(args, "-tag:v:0", "hvc1")
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", segmentPattern,
		masterPath,
	)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("ffmpeg hls copy failed: %w (%s)", runErr, strings.TrimSpace(string(out)))
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", err
	}

	baseKey := fmt.Sprintf("u/%s/%s/hls", strings.TrimSpace(a.UserID), strings.TrimSpace(a.ID))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		localPath := filepath.Join(workDir, name)
		info, statErr := os.Stat(localPath)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}

		contentType := "application/octet-stream"
		switch strings.ToLower(filepath.Ext(name)) {
		case ".m3u8":
			contentType = "application/vnd.apple.mpegurl"
		case ".m4s":
			contentType = "video/iso.segment"
		case ".mp4":
			contentType = "video/mp4"
		}

		f, openErr := os.Open(localPath)
		if openErr != nil {
			return "", openErr
		}
		storeKey := baseKey + "/" + name
		putErr := s.store.PutObject(ctx, storeKey, contentType, f, info.Size())
		_ = f.Close()
		if putErr != nil {
			return "", putErr
		}
	}

	return baseKey + "/master.m3u8", nil
}
