package gemini

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"bytes"
	"testing"
)

func makeTestImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("failed to encode JPEG: %v", err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode PNG: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizeImage_SmallJPEGUnchangedDimensions(t *testing.T) {
	// A 640x480 image is below maxImageDimension, so no resize should occur.
	img := makeTestImage(640, 480)
	raw := encodeJPEG(t, img)

	result, mime, err := NormalizeImage(raw, "image/jpeg")
	if err != nil {
		t.Fatalf("NormalizeImage failed: %v", err)
	}

	if mime != "image/jpeg" {
		t.Errorf("expected mime image/jpeg, got %s", mime)
	}

	// Decode result and check dimensions are unchanged
	decoded, err := jpeg.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != 640 || bounds.Dy() != 480 {
		t.Errorf("expected 640x480, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestNormalizeImage_LargeImageResized(t *testing.T) {
	// A 4000x3000 image should be resized to 1024x768.
	img := makeTestImage(4000, 3000)
	raw := encodeJPEG(t, img)

	result, mime, err := NormalizeImage(raw, "image/jpeg")
	if err != nil {
		t.Fatalf("NormalizeImage failed: %v", err)
	}

	if mime != "image/jpeg" {
		t.Errorf("expected mime image/jpeg, got %s", mime)
	}

	decoded, err := jpeg.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != 1024 {
		t.Errorf("expected width 1024, got %d", bounds.Dx())
	}
	if bounds.Dy() != 768 {
		t.Errorf("expected height 768, got %d", bounds.Dy())
	}
}

func TestNormalizeImage_TallImageResized(t *testing.T) {
	// A 1500x3000 image — longest side is height, should resize to 512x1024.
	img := makeTestImage(1500, 3000)
	raw := encodeJPEG(t, img)

	result, _, err := NormalizeImage(raw, "image/jpeg")
	if err != nil {
		t.Fatalf("NormalizeImage failed: %v", err)
	}

	decoded, err := jpeg.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	bounds := decoded.Bounds()
	if bounds.Dy() != 1024 {
		t.Errorf("expected height 1024, got %d", bounds.Dy())
	}
	if bounds.Dx() != 512 {
		t.Errorf("expected width 512, got %d", bounds.Dx())
	}
}

func TestNormalizeImage_PNGInput(t *testing.T) {
	img := makeTestImage(2048, 1536)
	raw := encodePNG(t, img)

	result, mime, err := NormalizeImage(raw, "image/png")
	if err != nil {
		t.Fatalf("NormalizeImage failed: %v", err)
	}

	if mime != "image/jpeg" {
		t.Errorf("expected output mime image/jpeg, got %s", mime)
	}

	decoded, err := jpeg.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != 1024 {
		t.Errorf("expected width 1024, got %d", bounds.Dx())
	}
}

func TestNormalizeImage_ReducesFileSize(t *testing.T) {
	// A large image should produce a smaller output.
	img := makeTestImage(4000, 3000)
	raw := encodeJPEG(t, img)

	result, _, err := NormalizeImage(raw, "image/jpeg")
	if err != nil {
		t.Fatalf("NormalizeImage failed: %v", err)
	}

	if len(result) >= len(raw) {
		t.Errorf("expected output (%d bytes) to be smaller than input (%d bytes)", len(result), len(raw))
	}
}

func TestNormalizeImage_InvalidInput(t *testing.T) {
	_, _, err := NormalizeImage([]byte("not an image"), "image/jpeg")
	if err == nil {
		t.Error("expected error for invalid image data")
	}
}

func TestDetectImageMIME_JPEG(t *testing.T) {
	img := makeTestImage(10, 10)
	raw := encodeJPEG(t, img)
	if got := DetectImageMIME(raw); got != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", got)
	}
}

func TestDetectImageMIME_PNG(t *testing.T) {
	img := makeTestImage(10, 10)
	raw := encodePNG(t, img)
	if got := DetectImageMIME(raw); got != "image/png" {
		t.Errorf("expected image/png, got %s", got)
	}
}

func TestDetectImageMIME_Unknown(t *testing.T) {
	if got := DetectImageMIME([]byte("hello")); got != "" {
		t.Errorf("expected empty string, got %s", got)
	}
}

func TestDetectImageMIME_TooShort(t *testing.T) {
	if got := DetectImageMIME([]byte{0x89}); got != "" {
		t.Errorf("expected empty string for short input, got %s", got)
	}
}
