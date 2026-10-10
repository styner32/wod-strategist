package gemini

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func encodeJPEG(img image.Image) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	Expect(err).NotTo(HaveOccurred())
	return buf.Bytes()
}

func encodePNG(img image.Image) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	Expect(err).NotTo(HaveOccurred())
	return buf.Bytes()
}

var _ = Describe("NormalizeImage", func() {
	It("keeps small JPEG dimensions unchanged", func() {
		// A 640x480 image is below maxImageDimension, so no resize should occur.
		img := makeTestImage(640, 480)
		raw := encodeJPEG(img)

		result, mime, err := NormalizeImage(raw, "image/jpeg")
		Expect(err).NotTo(HaveOccurred())
		Expect(mime).To(Equal("image/jpeg"))

		// Decode result and check dimensions are unchanged
		decoded, err := jpeg.Decode(bytes.NewReader(result))
		Expect(err).NotTo(HaveOccurred())

		bounds := decoded.Bounds()
		Expect(bounds.Dx()).To(Equal(640))
		Expect(bounds.Dy()).To(Equal(480))
	})

	It("resizes large image to fit within max dimensions", func() {
		// A 4000x3000 image should be resized to 1024x768.
		img := makeTestImage(4000, 3000)
		raw := encodeJPEG(img)

		result, mime, err := NormalizeImage(raw, "image/jpeg")
		Expect(err).NotTo(HaveOccurred())
		Expect(mime).To(Equal("image/jpeg"))

		decoded, err := jpeg.Decode(bytes.NewReader(result))
		Expect(err).NotTo(HaveOccurred())

		bounds := decoded.Bounds()
		Expect(bounds.Dx()).To(Equal(1024))
		Expect(bounds.Dy()).To(Equal(768))
	})

	It("resizes tall image based on height as longest side", func() {
		// A 1500x3000 image — longest side is height, should resize to 512x1024.
		img := makeTestImage(1500, 3000)
		raw := encodeJPEG(img)

		result, _, err := NormalizeImage(raw, "image/jpeg")
		Expect(err).NotTo(HaveOccurred())

		decoded, err := jpeg.Decode(bytes.NewReader(result))
		Expect(err).NotTo(HaveOccurred())

		bounds := decoded.Bounds()
		Expect(bounds.Dy()).To(Equal(1024))
		Expect(bounds.Dx()).To(Equal(512))
	})

	It("converts PNG input to JPEG", func() {
		img := makeTestImage(2048, 1536)
		raw := encodePNG(img)

		result, mime, err := NormalizeImage(raw, "image/png")
		Expect(err).NotTo(HaveOccurred())
		Expect(mime).To(Equal("image/jpeg"))

		decoded, err := jpeg.Decode(bytes.NewReader(result))
		Expect(err).NotTo(HaveOccurred())

		bounds := decoded.Bounds()
		Expect(bounds.Dx()).To(Equal(1024))
	})

	It("reduces file size for large images", func() {
		// A large image should produce a smaller output.
		img := makeTestImage(4000, 3000)
		raw := encodeJPEG(img)

		result, _, err := NormalizeImage(raw, "image/jpeg")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(result)).To(BeNumerically("<", len(raw)))
	})

	It("returns error for invalid input", func() {
		_, _, err := NormalizeImage([]byte("not an image"), "image/jpeg")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("DetectImageMIME", func() {
	It("detects JPEG mime", func() {
		img := makeTestImage(10, 10)
		raw := encodeJPEG(img)
		Expect(DetectImageMIME(raw)).To(Equal("image/jpeg"))
	})

	It("detects PNG mime", func() {
		img := makeTestImage(10, 10)
		raw := encodePNG(img)
		Expect(DetectImageMIME(raw)).To(Equal("image/png"))
	})

	It("returns empty string for unknown input", func() {
		Expect(DetectImageMIME([]byte("hello"))).To(BeEmpty())
	})

	It("returns empty string for input that is too short", func() {
		Expect(DetectImageMIME([]byte{0x89})).To(BeEmpty())
	})
})
