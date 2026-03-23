package storage

import (
	"context"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"google.golang.org/api/option"
)

type mockMultipartFile struct {
	*strings.Reader
}

func (f *mockMultipartFile) Close() error {
	return nil
}

var _ = Describe("GCS client", func() {
	Describe("UploadFile", func() {
		It("uploads an object through the mocked transport", func() {
			transport := testhelpers.NewMockTransport()
			transport.New("https://storage.googleapis.com").
				Post("/upload/storage/v1/b/test-bucket/o?alt=json&name=videos/demo.mp4&prettyPrint=false&projection=full&uploadType=multipart").
				Reply(http.StatusOK).
				JSON(map[string]any{"name": "videos/demo.mp4"})

			client, err := NewClient(
				context.Background(),
				"test-bucket",
				option.WithHTTPClient(&http.Client{Transport: transport}),
				option.WithoutAuthentication(),
			)
			Expect(err).NotTo(HaveOccurred())

			file := multipart.File(&mockMultipartFile{Reader: strings.NewReader("mock data")})
			gcsURI, err := client.UploadFile(context.Background(), file, "videos/demo.mp4")
			Expect(err).NotTo(HaveOccurred())
			Expect(gcsURI).To(Equal("gs://test-bucket/videos/demo.mp4"))
			Expect(transport.Verify()).To(Succeed())
			Expect(transport.Requests()).To(HaveLen(1))
		})

		It("returns an error when the upload request fails", func() {
			transport := testhelpers.NewMockTransport()
			transport.New("https://storage.googleapis.com").
				Post("/upload/storage/v1/b/test-bucket/o?alt=json&name=videos/demo.mp4&prettyPrint=false&projection=full&uploadType=multipart").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{
						"message": "upload failed",
					},
				})

			client, err := NewClient(
				context.Background(),
				"test-bucket",
				option.WithHTTPClient(&http.Client{Transport: transport}),
				option.WithoutAuthentication(),
			)
			Expect(err).NotTo(HaveOccurred())

			file := multipart.File(&mockMultipartFile{Reader: strings.NewReader("mock data")})
			_, err = client.UploadFile(context.Background(), file, "videos/demo.mp4")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Writer.Close"))
			Expect(transport.Verify()).To(Succeed())
		})
	})

	Describe("DownloadFile", func() {
		It("downloads an object through the mocked transport", func() {
			transport := testhelpers.NewMockTransport()
			transport.New("https://storage.googleapis.com").
				Get("/test-bucket/file.txt").
				Reply(http.StatusOK).
				BodyString("downloaded data")

			client, err := NewClient(
				context.Background(),
				"test-bucket",
				option.WithHTTPClient(&http.Client{Transport: transport}),
				option.WithoutAuthentication(),
			)
			Expect(err).NotTo(HaveOccurred())

			tmpDir := GinkgoT().TempDir()
			destPath := filepath.Join(tmpDir, "file.txt")

			Expect(client.DownloadFile(context.Background(), "gs://test-bucket/file.txt", destPath)).To(Succeed())
			data, err := os.ReadFile(destPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("downloaded data"))
			Expect(transport.Verify()).To(Succeed())
			Expect(transport.Requests()).To(HaveLen(1))
		})

		It("returns an error for invalid GCS URIs before making a request", func() {
			client, err := NewClient(
				context.Background(),
				"test-bucket",
				option.WithHTTPClient(&http.Client{Transport: testhelpers.NewMockTransport()}),
				option.WithoutAuthentication(),
			)
			Expect(err).NotTo(HaveOccurred())

			err = client.DownloadFile(context.Background(), "https://example.test/file.txt", filepath.Join(GinkgoT().TempDir(), "file.txt"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid GCS URI"))
		})
	})

	Describe("ParseGCSURI", func() {
		It("parses valid URIs", func() {
			bucket, object, err := ParseGCSURI("gs://test-bucket/path/to/file.txt")
			Expect(err).NotTo(HaveOccurred())
			Expect(bucket).To(Equal("test-bucket"))
			Expect(object).To(Equal("path/to/file.txt"))
		})

		It("rejects URIs without the gs scheme", func() {
			_, _, err := ParseGCSURI("https://example.test/file.txt")
			Expect(err).To(HaveOccurred())
		})

		It("rejects malformed URIs", func() {
			_, _, err := ParseGCSURI("gs://test-bucket")
			Expect(err).To(HaveOccurred())
		})
	})
})
