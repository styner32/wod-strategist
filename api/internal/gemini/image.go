package gemini

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
)

const (
	// maxImageDimension is the longest-side target for whiteboard photos.
	// 1024px preserves text readability while keeping file size ~100-300KB.
	maxImageDimension = 1024

	// jpegQuality controls re-encoding quality. 85 is a good balance of
	// file size vs. text legibility for OCR-style tasks.
	jpegQuality = 85
)

// NormalizeImage decodes an image from raw bytes, resizes it so the longest
// side is at most maxImageDimension, and re-encodes as JPEG.
// Returns the JPEG bytes and "image/jpeg" MIME type.
//
// Supported input MIME types: image/jpeg, image/png.
// HEIC images must be converted to JPEG by the client before upload.
func NormalizeImage(raw []byte, mimeType string) ([]byte, string, error) {
	var img image.Image
	var err error

	r := bytes.NewReader(raw)
	switch {
	case strings.HasPrefix(mimeType, "image/jpeg"):
		img, err = jpeg.Decode(r)
	case strings.HasPrefix(mimeType, "image/png"):
		img, err = png.Decode(r)
	default:
		// Try generic decode as fallback (handles formats registered via init())
		img, _, err = image.Decode(r)
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image (%s): %w", mimeType, err)
	}

	img = resizeToFit(img, maxImageDimension)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", fmt.Errorf("failed to re-encode image as JPEG: %w", err)
	}

	return buf.Bytes(), "image/jpeg", nil
}

// resizeToFit scales img so its longest side is at most maxDim.
// If the image is already smaller, it is returned unchanged.
// Uses BiLinear interpolation which preserves text edges well.
func resizeToFit(img image.Image, maxDim int) image.Image {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	longest := w
	if h > w {
		longest = h
	}

	if longest <= maxDim {
		return img
	}

	ratio := float64(maxDim) / float64(longest)
	newW := int(float64(w) * ratio)
	newH := int(float64(h) * ratio)

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	return dst
}

// DetectImageMIME sniffs the MIME type from image bytes using magic bytes.
// Returns empty string if unrecognized.
func DetectImageMIME(data []byte) string {
	if len(data) < 4 {
		return ""
	}

	// JPEG: FF D8 FF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}

	// PNG: 89 50 4E 47
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}

	// WebP: RIFF....WEBP
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}

	return ""
}
