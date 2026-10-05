package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"gorm.io/gorm"
)

var _ = Describe("Local video comparison", Ordered, func() {
	const sid = "WOD-20260928-01JQXYZ3K4M5N6P7Q8R9ABCDEF"
	const base = "https://example.test"
	var conn *gorm.DB
	BeforeAll(func() { var err error; conn, err = testhelpers.InitDB(); Expect(err).NotTo(HaveOccurred()) })
	AfterAll(func() {
		if conn != nil {
			sqlDB, err := conn.DB()
			Expect(err).NotTo(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())
		}
	})
	BeforeEach(func() {
		testhelpers.CleanupDB(conn)
	})

	It("compares repeated exercise timestamps without converting missing intervals to zero", func() {
		table := comparisonSegmentTable([]gemini.VideoModeResult{
			{Mode: "STATIC", Text: `[{"type":"Rowing","start":"00:10","end":"00:20"},{"type":"Rowing","start":"00:30","end":"00:40"}]`},
			{Mode: "AGENTIC", Text: `[{"type":"Rowing","start":"00:12","end":"00:19"},{"type":"Squat","start":"00:50","end":"01:00"}]`},
		})
		Expect(table).To(ContainSubstring("| Rowing | 00:10–00:20 | 00:12–00:19 | +2.000 | -1.000 |"))
		Expect(table).To(ContainSubstring("| Rowing | 00:30–00:40 | N/A | N/A | N/A |"))
		Expect(table).To(ContainSubstring("| Squat | N/A | 00:50–01:00 | N/A | N/A |"))
	})

	DescribeTable("preserves source and cleans up the experiment upload", func(failure string) {
		if !hasFfmpeg() {
			Skip("ffmpeg required")
		}
		user := testhelpers.CreateUser(conn, &db.User{Username: "compare", PasswordHash: "test"})
		profile := testhelpers.CreateProfile(conn, &db.Profile{UserID: user.ID, Name: "comparison"})
		original := testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{SessionID: sid, ProfileID: profile.ID, Status: "COMPLETED", Output: "unchanged"})
		movie := filepath.Join(GinkgoT().TempDir(), "sample.mp4")
		Expect(exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=32x32:r=2", "-t", "2", "-pix_fmt", "yuv420p", movie).Run()).To(Succeed())
		video, err := os.ReadFile(movie)
		Expect(err).NotTo(HaveOccurred())
		st := testhelpers.NewMockTransport()
		sc, err := testhelpers.NewStorageClient("test-bucket", st)
		Expect(err).NotTo(HaveOccurred())
		object := fmt.Sprintf("videos/%d/%s/merged.mp4", profile.ID, sid)
		testhelpers.MockGCSListObjects(st, "test-bucket", object, []string{object})
		st.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + object + "?alt=json&prettyPrint=false&projection=full").Reply(200).JSON(map[string]any{"name": object, "generation": "123", "size": strconv.Itoa(len(video))})
		st.New(testhelpers.GCSBaseURL).Get("/test-bucket/" + object + "?generation=123").Reply(200).Body(video)
		gt := testhelpers.NewMockTransport()
		gt.New(base).Post("/upload/v1beta/files").Reply(200).Header("X-Goog-Upload-Url", base+"/upload-session").JSON(map[string]any{})
		gt.New(base).Post("/upload-session").Reply(200).Header("X-Goog-Upload-Status", "final").Body([]byte(`{"file":{"name":"files/experiment","uri":"https://example.test/files/experiment","mimeType":"video/mp4"}}`))
		state := "ACTIVE"
		if failure == "poll" {
			state = "FAILED"
		}
		gt.New(base).Get("/v1beta/files/experiment").Reply(200).JSON(map[string]any{"name": "files/experiment", "uri": base + "/files/experiment", "state": state, "videoMetadata": map[string]any{"videoDuration": "2s"}})
		gt.New(base).Delete("/v1beta/files/experiment").Reply(200).JSON(map[string]any{})
		if failure != "poll" {
			status := 200
			body := `{"candidates":[{"content":{"parts":[{"text":"[]"}]},"finishReason":"STOP"}]}`
			if failure == "static" {
				status = 503
				body = `{"error":{"code":503,"message":"temporary","status":"UNAVAILABLE"}}`
			}
			gt.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").MatchBodyContains(`"mediaProcessing":"STATIC"`).Reply(status).Body([]byte(body))
			gt.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").MatchBodyContains(`"mediaProcessing":"AGENTIC"`).Reply(200).Body([]byte(`{"candidates":[{"content":{"parts":[{"toolCall":{"toolType":"MEDIA_PROCESSING"}},{"toolResponse":{"toolType":"MEDIA_PROCESSING"}},{"text":"[]"}]},"finishReason":"STOP"}]}`))
		}
		gc, err := gemini.NewComparisonClient(context.Background(), gemini.Options{APIKey: "test-key", BaseURL: base, HTTPClient: &http.Client{Transport: gt}, PollInterval: time.Millisecond, Sleep: func(time.Duration) {}})
		Expect(err).NotTo(HaveOccurred())
		dir := filepath.Join(GinkgoT().TempDir(), "artifacts")
		err = RunVideoComparison(context.Background(), conn, sc, "test-bucket", gc, VideoComparisonOptions{LatestCompletedWOD: true, OutputDir: dir, APIKey: "test-key"})
		if failure == "" {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}
		data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
		Expect(err).NotTo(HaveOccurred())
		var report videoComparisonReport
		Expect(json.Unmarshal(data, &report)).To(Succeed())
		Expect(report.Cleanup).To(Equal("deleted"))
		Expect(report.Source.Generation).To(Equal("123"))
		Expect(report.Source.SHA256).To(HaveLen(64))
		if failure == "poll" {
			Expect(report.Results).To(BeEmpty())
		} else {
			Expect(report.Results).To(HaveLen(2))
			Expect(report.Results[1].AgenticObserved).To(BeTrue())
			raw, err := os.ReadFile(filepath.Join(dir, "static.response.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(raw).NotTo(BeEmpty())
		}
		var after db.AnalysisResult
		Expect(conn.First(&after, original.ID).Error).To(Succeed())
		Expect(after.Output).To(Equal("unchanged"))
		Expect(after.UpdatedAt).To(Equal(original.UpdatedAt))
		var count int64
		Expect(conn.Model(&db.TokenUsage{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
		Expect(st.Verify()).To(Succeed())
		Expect(gt.Verify()).To(Succeed())
	}, Entry("success", ""), Entry("static failure with agentic still recorded", "static"), Entry("Files polling failure", "poll"))
})
