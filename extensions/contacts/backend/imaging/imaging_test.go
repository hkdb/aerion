package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// makePNG renders a solid-color square PNG of the given size for use as test
// input. Returns the encoded bytes.
func makePNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestResizeToJPEG_ShrinksLargeImage(t *testing.T) {
	raw := makePNG(t, 1024, 768, color.RGBA{R: 255, A: 255})
	out, mediaType, err := ResizeToJPEG(raw, ResizeOptions{MaxEdge: 256})
	if err != nil {
		t.Fatalf("ResizeToJPEG: %v", err)
	}
	if mediaType != "image/jpeg" {
		t.Errorf("mediaType = %q, want image/jpeg", mediaType)
	}
	if len(out) == 0 {
		t.Fatal("empty output")
	}
	// Decode back to verify dimensions.
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 256 || b.Dy() > 256 {
		t.Errorf("output exceeds max edge: %dx%d", b.Dx(), b.Dy())
	}
	// Aspect ratio preserved (1024/768 = 4/3). At MaxEdge=256, width should
	// be 256, height ~192.
	if b.Dx() != 256 {
		t.Errorf("width = %d, want 256 (long edge)", b.Dx())
	}
	if b.Dy() < 190 || b.Dy() > 194 {
		t.Errorf("height = %d, want ~192 (preserved aspect)", b.Dy())
	}
}

func TestResizeToJPEG_PreservesSmallImage(t *testing.T) {
	raw := makePNG(t, 100, 100, color.RGBA{B: 255, A: 255})
	out, _, err := ResizeToJPEG(raw, ResizeOptions{MaxEdge: 256})
	if err != nil {
		t.Fatalf("ResizeToJPEG: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 100 || b.Dy() != 100 {
		t.Errorf("small image was resized: %dx%d, want 100x100", b.Dx(), b.Dy())
	}
}

func TestResizeToJPEG_RejectsEmpty(t *testing.T) {
	if _, _, err := ResizeToJPEG(nil, ResizeOptions{}); err == nil {
		t.Error("expected error on empty input")
	}
}

func TestResizeToJPEG_DefaultsApplied(t *testing.T) {
	raw := makePNG(t, 512, 512, color.RGBA{G: 255, A: 255})
	out, _, err := ResizeToJPEG(raw, ResizeOptions{}) // both MaxEdge and Quality default
	if err != nil {
		t.Fatalf("ResizeToJPEG: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 256 || b.Dy() > 256 {
		t.Errorf("default MaxEdge=256 not applied: %dx%d", b.Dx(), b.Dy())
	}
}
