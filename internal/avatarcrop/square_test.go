package avatarcrop

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func solidRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8((x * 37) % 256), G: uint8((y * 53) % 256), B: 128, A: 255})
		}
	}
	return img
}

func decodeDims(t *testing.T, contentType string, payload []byte) (int, int) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		// image.Decode only knows JPEG/PNG/GIF/TIFF/BMP here; the helper is
		// used for JPEG/PNG assertions only.
		t.Fatalf("decode cropped payload: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestSquareWidePNG(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, solidRGBA(60, 40)); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	ct, out := Square("image/png", buf.Bytes())
	if ct != "image/png" {
		t.Fatalf("content type changed: %q", ct)
	}
	if w, h := decodeDims(t, ct, out); w != 40 || h != 40 {
		t.Fatalf("wide PNG cropped to %dx%d, want 40x40", w, h)
	}
}

func TestSquareTallJPEG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solidRGBA(40, 60), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	ct, out := Square("image/jpeg", buf.Bytes())
	if ct != "image/jpeg" {
		t.Fatalf("content type changed: %q", ct)
	}
	if w, h := decodeDims(t, ct, out); w != 40 || h != 40 {
		t.Fatalf("tall JPEG cropped to %dx%d, want 40x40", w, h)
	}
}

func TestSquareAlreadySquareKeepsType(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solidRGBA(32, 32), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	ct, out := Square("image/jpeg", buf.Bytes())
	if ct != "image/jpeg" || len(out) == 0 {
		t.Fatalf("square JPEG must round-trip, got type %q len %d", ct, len(out))
	}
	if w, h := decodeDims(t, ct, out); w != 32 || h != 32 {
		t.Fatalf("square JPEG changed to %dx%d", w, h)
	}
}

func TestSquareLeavesGIFAlone(t *testing.T) {
	paletted := image.NewPaletted(image.Rect(0, 0, 48, 24), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, paletted, nil); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	ct, out := Square("image/gif", buf.Bytes())
	if ct != "image/gif" || !bytes.Equal(out, buf.Bytes()) {
		t.Fatal("GIF must pass through byte-identical (animation preserved)")
	}
}

func TestSquarePassesThroughUnknown(t *testing.T) {
	payload := []byte("not-an-image")
	for _, ct := range []string{"image/webp", "image/svg+xml", "application/octet-stream", "image/jpeg"} {
		gotCT, out := Square(ct, payload)
		if gotCT != ct || !bytes.Equal(out, payload) {
			t.Fatalf("type %q: undecodable payload must pass through", ct)
		}
	}
	var empty []byte
	if _, out := Square("image/png", empty); len(out) != 0 {
		t.Fatal("empty payload must stay empty")
	}
}
