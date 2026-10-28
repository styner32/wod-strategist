package gemini

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

		It("exposes the configured segment-analysis model", func() {
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey: "test-api-key",
				Model:  ModelFlash38,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(client.Model()).To(Equal(ModelFlash38))
			Expect((*Client)(nil).Model()).To(BeEmpty())
		})

		It("configures thinking level and budget for supported models", func() {
			budget := int32(4096)
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:         "test-api-key",
				Model:          ModelFlash38,
				ThinkingLevel:  "HIGH",
				ThinkingBudget: &budget,
			})
			Expect(err).NotTo(HaveOccurred())

			// Flash 3.8 supports thinking
			tc := client.thinkingConfigForModel(ModelFlash38)
			Expect(tc).NotTo(BeNil())
			Expect(string(tc.ThinkingLevel)).To(Equal("HIGH"))
			Expect(tc.ThinkingBudget).To(Equal(&budget))

			// Pro 3.1 preview does not support thinking config
			tcPro := client.thinkingConfigForModel(ModelPro31Preview)
			Expect(tcPro).To(BeNil())
		})

		It("configures distinct thinking level for chunk analysis and falls back when unset", func() {
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:             "test-api-key",
				Model:              ModelFlash38,
				ThinkingLevel:      "HIGH",
				ThinkingLevelChunk: "LOW",
			})
			Expect(err).NotTo(HaveOccurred())

			tcFull := client.thinkingConfigForModel(ModelFlash38)
			Expect(tcFull).NotTo(BeNil())
			Expect(string(tcFull.ThinkingLevel)).To(Equal("HIGH"))

			tcChunk := client.thinkingConfigForModelAndLevel(ModelFlash38, client.thinkingLevelChunk)
			Expect(tcChunk).NotTo(BeNil())
			Expect(string(tcChunk.ThinkingLevel)).To(Equal("LOW"))

			// Fallback behavior when ThinkingLevelChunk is empty
			clientFallback, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:        "test-api-key",
				Model:         ModelFlash38,
				ThinkingLevel: "MEDIUM",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(clientFallback.thinkingLevelChunk).To(Equal("MEDIUM"))
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
				Post("/v1beta/models/"+ModelArgon4+":generateContent").
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
				Post("/v1beta/models/"+ModelFlash38+":generateContent").
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

			result, geminiFile, _, err := client.AnalyzeVideoWithModel(context.Background(), videoPath, "analyze this", ModelFlash38)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("flash 3.5 response"))
			Expect(geminiFile).To(Equal("files/mock-file"))
			Expect(transport.Requests()).To(HaveLen(5))
		})

		It("uploads, polls until active, and generates content with AnalyzeChunkVideo", func() {
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
						"name":     "files/mock-chunk",
						"uri":      "https://example.test/files/mock-chunk",
						"mimeType": mimeType,
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-chunk").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-chunk",
					"state": "ACTIVE",
				})

			transport.New(baseURL).
				Post("/v1beta/models/"+ModelFlash38+":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{
						{
							"content": map[string]any{
								"parts": []map[string]any{
									{"text": "chunk response"},
								},
							},
						},
					},
				})

			result, geminiFile, _, err := client.AnalyzeChunkVideo(context.Background(), videoPath, "analyze chunk", ModelFlash38)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("chunk response"))
			Expect(geminiFile).To(Equal("files/mock-chunk"))
			Expect(transport.Requests()).To(HaveLen(4))
		})

		It("places video part before reference images and prompt in AnalyzeChunkVideoWithImages", func() {
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
						"name":     "files/mock-chunk-img",
						"uri":      "https://example.test/files/mock-chunk-img",
						"mimeType": mimeType,
					},
				})

			transport.New(baseURL).
				Get("/v1beta/files/mock-chunk-img").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-chunk-img",
					"state": "ACTIVE",
				})

			transport.New(baseURL).
				Post("/v1beta/models/"+ModelFlash38+":generateContent").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": "[EXERCISE: Deadlift]"}},
						},
					}},
				})

			ref := encodeJPEG(makeTestImage(32, 32))
			result, geminiFile, _, err := client.AnalyzeChunkVideoWithImages(
				context.Background(), videoPath, "analyze chunk with ref", ModelFlash38, [][]byte{ref},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("[EXERCISE: Deadlift]"))
			Expect(geminiFile).To(Equal("files/mock-chunk-img"))

			genBody := string(transport.Requests()[3].Body)
			videoIdx := strings.Index(genBody, `"fileUri":"https://example.test/files/mock-chunk-img"`)
			imgIdx := strings.Index(genBody, `"mimeType":"image/jpeg"`)
			promptIdx := strings.Index(genBody, `"text":"analyze chunk with ref"`)
			Expect(videoIdx).To(BeNumerically(">", 0))
			Expect(imgIdx).To(BeNumerically(">", videoIdx), "reference image must follow video part")
			Expect(promptIdx).To(BeNumerically(">", imgIdx), "prompt text must follow reference image")
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

		It("returns an error when polling file state repeatedly fails with 500", func() {
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

			for i := 0; i <= maxConsecutiveFileGetErrors; i++ {
				transport.New(baseURL).
					Get("/v1beta/files/mock-file").
					Reply(http.StatusInternalServerError).
					JSON(map[string]any{
						"error": map[string]any{
							"code":    500,
							"status":  "INTERNAL",
							"message": "poll failed",
						},
					})
			}

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get file info"))
			Expect(result).To(BeEmpty())
			Expect(geminiFile).To(Equal("files/mock-file"))
		})

		It("retries transient 500 error while polling and succeeds when active", func() {
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
						"name": "files/mock-retry-file",
						"uri":  "https://example.test/files/mock-retry-file",
					},
				})

			// First poll: transient 500 error
			transport.New(baseURL).
				Get("/v1beta/files/mock-retry-file").
				Reply(http.StatusInternalServerError).
				JSON(map[string]any{
					"error": map[string]any{
						"message": "internal server error",
					},
				})

			// Second poll: recovers and reports ACTIVE
			transport.New(baseURL).
				Get("/v1beta/files/mock-retry-file").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name":  "files/mock-retry-file",
					"state": "ACTIVE",
				})

			transport.New(baseURL).
				Post("/v1beta/models/" + defaultModel + ":generateContent").
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{
						{
							"content": map[string]any{
								"parts": []map[string]any{
									{"text": "recovered response"},
								},
							},
						},
					},
				})

			result, geminiFile, _, err := client.AnalyzeVideo(context.Background(), videoPath, "prompt")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("recovered response"))
			Expect(geminiFile).To(Equal("files/mock-retry-file"))
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
				Post("/v1beta/models/" + ModelArgon4 + ":generateContent").
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
				Post("/v1beta/models/" + ModelArgon4 + ":generateContent").
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
				Post("/v1beta/models/"+ModelFlash38+":generateContent").
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
				Post("/v1beta/models/" + ModelFlash38 + ":generateContent").
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
				Post("/v1beta/models/"+ModelArgon4+":generateContent").
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
				30*time.Second+250*time.Millisecond, 60*time.Second+750*time.Millisecond,
				"Analyze this segment",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("deep biomechanical analysis"))
			Expect(transport.Verify()).To(Succeed())
			Expect(transport.Requests()).To(HaveLen(1))
			Expect(string(transport.Requests()[0].Body)).To(ContainSubstring(`"startOffset":"30.250s"`))
			Expect(string(transport.Requests()[0].Body)).To(ContainSubstring(`"endOffset":"60.750s"`))
		})

		It("rejects an invalid interval before sending a request", func() {
			transport := testhelpers.NewMockTransport()
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			_, _, err = client.AnalyzeSegment(
				context.Background(),
				"https://example.test/files/mock-file", "video/mp4",
				10*time.Second, 10*time.Second,
				"Analyze",
			)
			Expect(err).To(MatchError(ContainSubstring("invalid segment interval")))
			Expect(transport.Requests()).To(BeEmpty())
		})

		It("returns an error on failure", func() {
			transport := testhelpers.NewMockTransport()

			transport.New(baseURL).
				Post("/v1beta/models/" + ModelArgon4 + ":generateContent").
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
		It("defaults to "+ModelArgon4, func() {
			transport := testhelpers.NewMockTransport()
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     "test-key",
				BaseURL:    "https://example.test",
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(client.model).To(Equal(ModelArgon4))
		})

		It("uses the configured model", func() {
			transport := testhelpers.NewMockTransport()
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     "test-key",
				BaseURL:    "https://example.test",
				Model:      ModelFlash35Lite,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(client.model).To(Equal(ModelFlash35Lite))
		})
	})

	Describe("AnalyzeSegmentsBatch", func() {
		const (
			baseURL = "https://example.test"
			apiKey  = "test-api-key"
		)

		It("submits an inline batch job with exact fractional videoMetadata offsets and returns completed responses", func() {
			transport := testhelpers.NewMockTransport()

			transport.New(baseURL).
				Post("/v1beta/models/"+ModelArgon4+":batchGenerateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"name": "batches/mock-batch-1",
					"metadata": map[string]any{
						"state": "JOB_STATE_SUCCEEDED",
						"output": map[string]any{
							"inlinedResponses": map[string]any{
								"inlinedResponses": []map[string]any{
									{
										"response": map[string]any{
											"candidates": []map[string]any{{
												"content": map[string]any{
													"parts": []map[string]any{{"text": "segment 1 analysis"}},
												},
											}},
											"usageMetadata": map[string]any{
												"promptTokenCount":     100,
												"candidatesTokenCount": 40,
												"totalTokenCount":      140,
											},
										},
									},
									{
										"response": map[string]any{
											"candidates": []map[string]any{{
												"content": map[string]any{
													"parts": []map[string]any{{"text": "segment 2 analysis"}},
												},
											}},
											"usageMetadata": map[string]any{
												"promptTokenCount":     120,
												"candidatesTokenCount": 50,
												"totalTokenCount":      170,
											},
										},
									},
								},
							},
						},
					},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:     apiKey,
				BaseURL:    baseURL,
				HTTPClient: &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			results, err := client.AnalyzeSegmentsBatch(context.Background(), ModelArgon4, []BatchSegmentRequest{
				{
					FileURI:  "https://example.test/files/mock-file",
					MIMEType: "video/mp4",
					Start:    10*time.Second + 250*time.Millisecond,
					End:      20*time.Second + 500*time.Millisecond,
					Prompt:   "Analyze segment 1",
				},
				{
					FileURI:  "https://example.test/files/mock-file",
					MIMEType: "video/mp4",
					Start:    30 * time.Second,
					End:      45*time.Second + 750*time.Millisecond,
					Prompt:   "Analyze segment 2",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(2))
			Expect(results[0].Analysis).To(Equal("segment 1 analysis"))
			Expect(results[0].Usage).NotTo(BeNil())
			Expect(results[0].Usage.TotalTokens).To(Equal(int32(140)))
			Expect(results[1].Analysis).To(Equal("segment 2 analysis"))
			Expect(results[1].Usage).NotTo(BeNil())
			Expect(results[1].Usage.TotalTokens).To(Equal(int32(170)))
			Expect(transport.Verify()).To(Succeed())

			body := string(transport.Requests()[0].Body)
			Expect(body).To(ContainSubstring(`"startOffset":"10.250s"`))
			Expect(body).To(ContainSubstring(`"endOffset":"20.500s"`))
			Expect(body).To(ContainSubstring(`"startOffset":"30s"`))
			Expect(body).To(ContainSubstring(`"endOffset":"45.750s"`))
		})
	})

	Describe("AnalyzeFrameGrids", func() {
		const (
			baseURL = "https://example.test"
			apiKey  = "test-api-key"
		)

		It("sends multiple inline frame-grid images with HIGH media resolution and thinking config", func() {
			transport := testhelpers.NewMockTransport()
			transport.New(baseURL).
				Post("/v1beta/models/"+ModelArgon4+":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{
								{"text": "internal reasoning", "thought": true},
								{"text": "Burpee Over Bar 4 reps observed"},
							},
						},
					}},
					"usageMetadata": map[string]any{
						"promptTokenCount":     2240,
						"candidatesTokenCount": 120,
						"totalTokenCount":      2360,
					},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:        apiKey,
				BaseURL:       baseURL,
				Model:         ModelArgon4,
				ThinkingLevel: "HIGH",
				HTTPClient:    &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			sheet1 := encodeJPEG(makeTestImage(64, 32))
			sheet2 := encodeJPEG(makeTestImage(64, 16))

			result, usage, err := client.AnalyzeFrameGrids(
				context.Background(),
				[][]byte{sheet1, sheet2},
				"image/jpeg",
				"Analyze these frame grids",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("Burpee Over Bar 4 reps observed"))
			Expect(usage).NotTo(BeNil())
			Expect(usage.PromptTokens).To(Equal(int32(2240)))
			Expect(usage.CandidateTokens).To(Equal(int32(120)))
			Expect(usage.TotalTokens).To(Equal(int32(2360)))
			Expect(transport.Verify()).To(Succeed())

			body := string(transport.Requests()[0].Body)
			Expect(body).To(ContainSubstring(`"mediaResolution":"MEDIA_RESOLUTION_HIGH"`))
			Expect(body).To(ContainSubstring(`"thinkingLevel":"HIGH"`))
			Expect(body).To(ContainSubstring(`"mimeType":"image/jpeg"`))
			Expect(body).To(ContainSubstring(`"text":"Analyze these frame grids"`))
		})

		It("returns validation errors when images slice is empty or contains empty image data", func() {
			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey: apiKey,
			})
			Expect(err).NotTo(HaveOccurred())

			_, _, err = client.AnalyzeFrameGrids(context.Background(), nil, "image/jpeg", "prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least one frame grid image is required"))

			_, _, err = client.AnalyzeFrameGridsWithModel(context.Background(), [][]byte{nil}, "image/jpeg", "prompt", ModelFlash38)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("frame grid image at index 0 is empty"))
		})

		It("honors per-call WithThinkingLevel override on AnalyzeFrameGridsWithModel", func() {
			transport := testhelpers.NewMockTransport()
			transport.New(baseURL).
				Post("/v1beta/models/"+ModelFlash38+":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{
								{"text": "ok"},
							},
						},
					}},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:        apiKey,
				BaseURL:       baseURL,
				Model:         ModelArgon4,
				ThinkingLevel: "HIGH",
				HTTPClient:    &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			sheet := encodeJPEG(makeTestImage(64, 32))
			ctx := WithThinkingLevel(context.Background(), "MEDIUM")
			result, _, err := client.AnalyzeFrameGridsWithModel(ctx, [][]byte{sheet}, "image/jpeg", "prompt", ModelFlash38)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("ok"))
			Expect(transport.Verify()).To(Succeed())

			body := string(transport.Requests()[0].Body)
			Expect(body).To(ContainSubstring(`"thinkingLevel":"MEDIUM"`))
		})

		It("honors per-call WithThinkingLevel override on AnalyzeSegmentWithModelAndImages", func() {
			transport := testhelpers.NewMockTransport()
			transport.New(baseURL).
				Post("/v1beta/models/"+ModelFlash38+":generateContent").
				MatchHeader("X-Goog-Api-Key", apiKey).
				Reply(http.StatusOK).
				JSON(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{
								{"text": "segment ok"},
							},
						},
					}},
				})

			client, err := NewClientWithOptions(context.Background(), zap.NewNop(), Options{
				APIKey:        apiKey,
				BaseURL:       baseURL,
				Model:         ModelArgon4,
				ThinkingLevel: "HIGH",
				HTTPClient:    &http.Client{Transport: transport},
			})
			Expect(err).NotTo(HaveOccurred())

			ref := encodeJPEG(makeTestImage(32, 32))
			ctx := WithThinkingLevel(context.Background(), "MEDIUM")
			result, _, err := client.AnalyzeSegmentWithModelAndImages(
				ctx, "https://example.test/files/vid1", "video/mp4", 10*time.Second, 20*time.Second, "prompt", ModelFlash38, [][]byte{ref},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("segment ok"))
			Expect(transport.Verify()).To(Succeed())

			body := string(transport.Requests()[0].Body)
			Expect(body).To(ContainSubstring(`"thinkingLevel":"MEDIUM"`))
			videoIdx := strings.Index(body, `"fileUri":"https://example.test/files/vid1"`)
			imgIdx := strings.Index(body, `"mimeType":"image/jpeg"`)
			promptIdx := strings.Index(body, `"text":"prompt"`)
			Expect(videoIdx).To(BeNumerically(">", 0))
			Expect(imgIdx).To(BeNumerically(">", videoIdx), "reference image must follow video segment")
			Expect(promptIdx).To(BeNumerically(">", imgIdx), "prompt text must follow reference image")
		})
	})
})
