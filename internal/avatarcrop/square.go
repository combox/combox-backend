// Package avatarcrop normalises freshly uploaded avatars to a center
// square before they are stored. The crop runs best-effort at upload time,
// so every NEW avatar object is square while the archived history is left
// untouched.
package avatarcrop

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"strings"
)

// maxSide bounds the decoded geometry: uploads above it keep their original
// bytes (a hostile multi-hundred-megapixel image must not blow the decoder
// memory just to be cropped).
const maxSide = 8192

// jpegQuality is the re-encode quality of a cropped JPEG: visually
// transparent for an avatar, far below the source data-URL weight.
const jpegQuality = 90

// Square returns the center-square crop of payload when it decodes as JPEG
// or PNG. Everything else passes through untouched: GIF stays animated,
// WebP/SVG/unknown have no decoder wired here, and any decode/encode
// failure also returns the original bytes — cropping must never fail an
// upload, the uncropped object is still a valid avatar.
func Square(contentType string, payload []byte) (string, []byte) {
	if len(payload) == 0 {
		return contentType, payload
	}
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return squareJPEG(payload)
	case "image/png":
		return squarePNG(payload)
	default:
		return contentType, payload
	}
}

func squareJPEG(payload []byte) (string, []byte) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(payload))
	if err != nil || !bounded(cfg.Width, cfg.Height) {
		return "image/jpeg", payload
	}
	img, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		return "image/jpeg", payload
	}
	cropped, ok := centerSquare(img)
	if !ok {
		return "image/jpeg", payload
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, cropped, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return "image/jpeg", payload
	}
	return "image/jpeg", buf.Bytes()
}

func squarePNG(payload []byte) (string, []byte) {
	cfg, err := png.DecodeConfig(bytes.NewReader(payload))
	if err != nil || !bounded(cfg.Width, cfg.Height) {
		return "image/png", payload
	}
	img, err := png.Decode(bytes.NewReader(payload))
	if err != nil {
		return "image/png", payload
	}
	cropped, ok := centerSquare(img)
	if !ok {
		return "image/png", payload
	}
	// NRGBA preserves alpha (opaque pixels encode identically); RGBA would
	// premultiply and shift semi-transparent edges.
	out := image.NewNRGBA(image.Rect(0, 0, cropped.Bounds().Dx(), cropped.Bounds().Dy()))
	draw.Draw(out, out.Bounds(), cropped, cropped.Bounds().Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return "image/png", payload
	}
	return "image/png", buf.Bytes()
}

func bounded(w, h int) bool {
	return w > 0 && h > 0 && w <= maxSide && h <= maxSide
}

// centerSquare returns the centered side×side view of img (square inputs
// return themselves, no copy). Every image type the stdlib decoders produce
// (*RGBA, *NRGBA, *YCbCr, *Gray, …) implements SubImage.
func centerSquare(img image.Image) (image.Image, bool) {
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	sub, ok := img.(subImager)
	if !ok {
		return nil, false
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil, false
	}
	s := w
	if h < s {
		s = h
	}
	x0 := bounds.Min.X + (w-s)/2
	y0 := bounds.Min.Y + (h-s)/2
	return sub.SubImage(image.Rect(x0, y0, x0+s, y0+s)), true
}
