package gemini

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

var _ = Describe("Gemini client", func() {
	Describe("NewClientWithOptions", func() {
		It("returns an error when API key is empty", func() {
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{APIKey: ""})
			Expect(err).To(HaveOccurred())
			Expect(client).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("GEMINI_API_KEY is not set"))
		})
	})

	Describe("AnalyzeVideo", func() {
		const (
			baseURL = "https://example.test"
			apiKey  = "test-api-key"
		)

		var (
			transport *testhelpers.MockTransport
			client    *Client
			videoPath string
			videoData []byte
			mimeType  string
		)

		BeforeEach(func() {
			transport = testhelpers.NewMockTransport()
			videoData = []byte{
				0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2',
				0x00, 0x00, 0x00, 0x00, 'm', 'p', '4', '2', 'i', 's', 'o', 'm',
			}
			mimeType = http.DetectContentType(videoData)

			tmpDir := GinkgoT().TempDir()
			videoPath = filepath.Join(tmpDir, "video.mp4")
			Expect(os.WriteFile(videoPath, videoData, 0o644)).To(Succeed())

			var err error
			client, err = NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:       apiKey,
				BaseURL:      baseURL,
				HTTPClient:   &http.Client{Transport: transport},
				PollInterval: time.Millisecond,
				Sleep:        func(time.Duration) {},
			})
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				Expect(transport.Verify()).To(Succeed())
			})
		})

		It("uploads, polls until active, and generates content", func() {
			transport.New(baseURL).
				Post("/upload/v1beta/files").
				MatchHeader("X-Goog-Upload-Protocol", "resumable").
				MatchHeader("X-Goog-Upload-Command", "start").
				MatchHeader("X-Goog-Upload-Header-Content-Type", mimeType).
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", baseURL+"/upload-session").
				JSON(map[string]any{})

			transport.New(baseURL).
				Post("/upload-session").
				MatchHeader("X-Goog-Upload-Command", "upload, finalize").
				MatchHeader("X-Goog-Upload-Offset", "0").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Status", "final").
				JSON(map[string]any{
					"file": map[string]any{
						"name":     "files/mock-file",
						"uri":      "https://example.test/files/mock-file",
						"mimeType": mimeType,
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-file",
					"state": "PROCESSING",
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-file",
					"state": "ACTIVE",
				})

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{
						{
							"content": map[string]any{
								"parts": []map[string]any{
									{"text": "first "},
									{"text": "second"},
								},
							},
						},
					},
				})

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "analyze this")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("first second"))
			Expect(geminiFile).To(Equal("files/mock-file"))
			Expect(transport.Requests()).To(HaveLen(5))
		})

		It("uploads, polls until active, and generates content with explicit model using AnalyzeVideoWithModel", func() {
			transport.New(baseURL).
				Post("/upload/v1beta/files").
				MatchHeader("X-Goog-Upload-Protocol", "resumable").
				MatchHeader("X-Goog-Upload-Command", "start").
				MatchHeader("X-Goog-Upload-Header-Content-Type", mimeType).
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", baseURL+"/upload-session").
				JSON(map[string]any{})

			transport.New(baseURL).
				Post("/upload-session").
				MatchHeader("X-Goog-Upload-Command", "upload, finalize").
				MatchHeader("X-Goog-Upload-Offset", "0").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Status", "final").
				JSON(map[string]any{
					"file": map[string]any{
						"name":     "files/mock-file",
						"uri":      "https://example.test/files/mock-file",
						"mimeType": mimeType,
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-file",
					"state": "PROCESSING",
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-file",
					"state": "ACTIVE",
				})

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelFlash35 + ":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{
						{
							"content": map[string]any{
								"parts": []map[string]any{
									{"text": "flash "},
									{"text": "3.5 response"},
								},
							},
						},
					},
				})

			result, geminiFile, _, err := client.AnalyzeVideoWithModel(context.Background(), videoPath, "analyze this", ModelFlash35)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("flash 3.5 response"))
			Expect(geminiFile).To(Equal("files/mock-file"))
			Expect(transport.Requests()).To(HaveLen(5))
		})

		It("returns an error when the local file does not exist", func() {
			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), filepath.Join(GinkgoT().TempDir(), "missing.mp4"), "prompt")
			Expect(err).To(HaveOccurred())
			Expect(result).To(BeEmpty())
			Expect(geminiFile).To(BeEmpty())
			Expect(transport.Requests()).To(BeEmpty())
		})

		It("returns an error when upload fails", func() {
			transport.New(baseURL).
				Post("/upload/v1beta/files").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", baseURL+"/upload-session").
				JSON(map[string]any{})

			transport.New(baseURL).
				Post("/upload-session").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{
						"message": "upload failed",
					},
				})

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to upload file"))
			Expect(result).To(BeEmpty())
			Expect(geminiFile).To(BeEmpty())
		})

		It("returns an error when polling file state fails", func() {
			transport.New(baseURL).
				Post("/upload/v1beta/files").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", baseURL+"/upload-session").
				JSON(map[string]any{})

			transport.New(baseURL).
				Post("/upload-session").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Status", "final").
				JSON(map[string]any{
					"file": map[string]any{
						"name": "files/mock-file",
						"uri":  "https://example.test/files/mock-file",
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{
						"message": "poll failed",
					},
				})

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get file info"))
			Expect(result).To(BeEmpty())
			Expect(geminiFile).To(Equal("files/mock-file"))
		})

		It("returns an error when Gemini marks the file as failed", func() {
			transport.New(baseURL).
				Post("/upload/v1beta/files").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", baseURL+"/upload-session").
				JSON(map[string]any{})

			transport.New(baseURL).
				Post("/upload-session").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Status", "final").
				JSON(map[string]any{
					"file": map[string]any{
						"name": "files/mock-file",
						"uri":  "https://example.test/files/mock-file",
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-file",
					"state": "FAILED",
				})

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("file processing failed"))
			Expect(result).To(BeEmpty())
			Expect(geminiFile).To(Equal("files/mock-file"))
		})

		It("returns an error when generate content fails", func() {
			transport.New(baseURL).
				Post("/upload/v1beta/files").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", baseURL+"/upload-session").
				JSON(map[string]any{})

			transport.New(baseURL).
				Post("/upload-session").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Status", "final").
				JSON(map[string]any{
					"file": map[string]any{
						"name": "files/mock-file",
						"uri":  "https://example.test/files/mock-file",
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-file",
					"state": "ACTIVE",
				})

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{
						"message": "generation failed",
					},
				})

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to generate content"))
			Expect(result).To(BeEmpty())
			Expect(geminiFile).To(Equal("files/mock-file"))
		})

		It("returns an error when no content is generated", func() {
			transport.New(baseURL).
				Post("/upload/v1beta/files").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Url", baseURL+"/upload-session").
				JSON(map[string]any{})

			transport.New(baseURL).
				Post("/upload-session").
				Reply(http.StatusOK).
				Header("X-Goog-Upload-Status", "final").
				JSON(map[string]any{
					"file": map[string]any{
						"name": "files/mock-file",
						"uri":  "https://example.test/files/mock-file",
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-file").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-file",
					"state": "ACTIVE",
				})

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{},
				})

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no content generated"))
			Expect(result).To(BeEmpty())
			Expect(geminiFile).To(Equal("files/mock-file"))
		})
	})

	Describe("DeleteFile", func() {
		const (
			baseURL = "https://example.test"
			apiKey  = "test-api-key"
		)

		It("deletes the remote file", func() {
			transport := testhelpers.NewMockTransport()
			transport.New(baseURL).
				Delete("/v1beta/files/mock-file").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(client.DeleteFile(context.Background(), "files/mock-file")).To(Succeed())
			Expect(transport.Verify()).To(Succeed())
		})

		It("returns an error when delete fails", func() {
			transport := testhelpers.NewMockTransport()
			transport.New(baseURL).
				Delete("/v1beta/files/mock-file").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{
						"message": "delete failed",
					},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			err = client.DeleteFile(context.Background(), "files/mock-file")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to delete file"))
			Expect(transport.Verify()).To(Succeed())
		})
	})

	Describe("IndexVideo", func() {
		const (
			baseURL = "https://example.test"
			apiKey  = "test-api-key"
		)

		It("returns the raw model output from Flash", func() {
			transport := testhelpers.NewMockTransport()

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": `[{"start":"0:30","end":"1:00","type":"Snatch"}]`}},
						},
					}},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			result, _, err := client.IndexVideo(context.Background(), "https://example.test/files/mock-file", "video/mp4", "Index this video")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(ContainSubstring("Snatch"))
			Expect(transport.Verify()).To(Succeed())
		})

		It("returns an error on failure", func() {
			transport := testhelpers.NewMockTransport()

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{"message": "indexing failed"},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			result, _, err := client.IndexVideo(context.Background(), "https://example.test/files/mock-file", "video/mp4", "Index this")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to index video"))
			Expect(result).To(BeEmpty())
			Expect(transport.Verify()).To(Succeed())
		})
	})

	Describe("AnalyzeSegment", func() {
		const (
			baseURL = "https://example.test"
			apiKey  = "test-api-key"
		)

		It("generates content with VideoMetadata for a specific segment", func() {
			transport := testhelpers.NewMockTransport()

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": "deep biomechanical analysis"}},
						},
					}},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			result, _, err := client.AnalyzeSegment(
				context.Background(),
				"https://example.test/files/mock-file", "video/mp4",
				30*time.Second, 60*time.Second,
				"Analyze this segment",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("deep biomechanical analysis"))
			Expect(transport.Verify()).To(Succeed())
		})

		It("returns an error on failure", func() {
			transport := testhelpers.NewMockTransport()

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelPro31Preview + ":generateContent").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{"message": "analysis failed"},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			result, _, err := client.AnalyzeSegment(
				context.Background(),
				"https://example.test/files/mock-file", "video/mp4",
				30*time.Second, 60*time.Second,
				"Analyze",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to analyze segment"))
			Expect(result).To(BeEmpty())
			Expect(transport.Verify()).To(Succeed())
		})
	})

	Describe("Model option", func() {
		It("defaults to "+ModelPro31Preview, func() {
			transport := testhelpers.NewMockTransport()
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     "test-key",
				BaseURL:    "https://example.test",
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(client.model).To(Equal(ModelPro31Preview))
		})

		It("uses the configured model", func() {
			transport := testhelpers.NewMockTransport()
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     "test-key",
				BaseURL:    "https://example.test",
				Model:      ModelFlash30Preview,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(client.model).To(Equal(ModelFlash30Preview))
		})
	})
})
