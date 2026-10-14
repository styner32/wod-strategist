package worker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/oklog/ulid/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/wodeval"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"go.uber.org/zap"
	"net/http"
	"strings"
	"time"
)

var _ = Describe("WOD full-session evaluation", func() {
	It("rebases final evidence without changing independent observations or carrying pre-WOD capture coordinates", func() {
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).To(Succeed())
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": `{"form":80,"intensity":70,"consistency":75,"movements":{},"summary":"관찰된 수행을 비교했습니다."}`}}}}}})
		start, captureEnd := 120.0, 180.0
		progress := workoutprogress.Build([]workoutprogress.Interval{{ID: "set", Start: 120, End: 180, Activity: "exercise", Movement: "Power Clean", CaptureStart: &start, CaptureEnd: &captureEnd, RepEvents: []activity.Observation{{Movement: "Power Clean", Unit: "reps", Start: 130, End: 132, Complete: true, Evidence: "complete catch and return"}}, FormObservations: []workoutprogress.Observation{{Start: 130, End: 132, Evidence: "visible neutral trunk"}}}}, nil, "source")
		before, err := json.Marshal(progress)
		Expect(err).To(Succeed())
		w := &Worker{GeminiClient: client, logger: zap.NewNop()}
		w.synthesizeWODWithProgress(context.Background(), VideoAnalysisPayload{WorkoutType: "wod"}, wodeval.Start{Seconds: &start, Status: "confirmed"}, []wodSegmentEvidence{{TargetConfirmed: true, Movement: "Power Clean", StartSecs: 120, EndSecs: 180, Form: []string{"visible neutral trunk"}, Pace: []string{"observed cycle"}}}, true, progress, "")
		after, err := json.Marshal(progress)
		Expect(err).To(Succeed())
		Expect(after).To(Equal(before))
		Expect(transport.Verify()).To(Succeed())
		var request struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		Expect(json.Unmarshal(transport.Requests()[0].Body, &request)).To(Succeed())
		prompt := request.Contents[0].Parts[0].Text
		Expect(prompt).To(ContainSubstring(`"start_secs":10`))
		Expect(prompt).NotTo(ContainSubstring(`"start_secs":130`))
		Expect(prompt).NotTo(ContainSubstring(`"capture_start_secs"`))
	})
	It("keeps historical sessions opted out and enables only newly registered WODs", func() {
		conn, err := testhelpers.InitDB()
		Expect(err).To(Succeed())
		testhelpers.CleanupDB(conn)
		profile := testhelpers.CreateProfile(conn, &db.Profile{})
		legacy := testhelpers.CreateSession(conn, &db.Session{SessionID: "WOD-20261013-LEGACY", ProfileID: profile.ID, WorkoutType: "wod"})
		w := &Worker{DB: conn, logger: zap.NewNop()}
		p := VideoAnalysisPayload{SessionID: legacy.SessionID, ProfileID: profile.ID, WorkoutType: "wod"}
		Expect(w.ensureWODEvaluationSession(context.Background(), p)).To(Succeed())
		Expect(w.wodEvaluationVersion(context.Background(), p)).To(Equal(0))
		p.SessionID = "WOD-20261013-NEW"
		Expect(w.ensureWODEvaluationSession(context.Background(), p)).To(Succeed())
		Expect(w.wodEvaluationVersion(context.Background(), p)).To(Equal(1))
		oldTime := time.Now().Add(-48 * time.Hour)
		location, err := time.LoadLocation("Asia/Seoul")
		Expect(err).To(Succeed())
		p.SessionID = fmt.Sprintf("WOD-%s-%s", oldTime.In(location).Format("20060102"), ulid.MustNew(ulid.Timestamp(oldTime), rand.Reader).String())
		Expect(w.ensureWODEvaluationSession(context.Background(), p)).To(Succeed())
		Expect(w.wodEvaluationVersion(context.Background(), p)).To(Equal(0))
		p.WorkoutType = "warmup"
		Expect(w.wodEvaluationVersion(context.Background(), p)).To(Equal(0))
	})
	It("requires visible target/onset evidence and accepts a valid start at zero", func() {
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).To(Succeed())
		result := `{"seconds":0,"target_confirmed":true,"preceding_activity":"unknown","evidence":"The target begins the first burpee on the first frame"}`
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": result}}}}}})
		w := &Worker{GeminiClient: client, logger: zap.NewNop()}
		got := w.detectWODStart(context.Background(), VideoAnalysisPayload{}, &gemini.UploadResult{FileURI: "https://example.com/video", MIMEType: "video/mp4", VideoDuration: time.Minute}, []Segment{{Start: "00:00", End: "00:30", Type: "Burpee"}}, nil)
		Expect(got.Status).To(Equal("confirmed"))
		Expect(got.Seconds).NotTo(BeNil())
		Expect(*got.Seconds).To(Equal(0.0))
		Expect(transport.Verify()).To(Succeed())
	})
	It("clips a segment at the corrected start and preserves later rest gaps", func() {
		seconds := 15.0
		got := trimWODSegments([]Segment{{Start: "00:00", End: "00:10", Type: "Burpee"}, {Start: "00:10", End: "00:20", Type: "Burpee"}, {Start: "00:30", End: "00:40", Type: "Burpee"}}, wodeval.Start{Seconds: &seconds, Status: "confirmed"})
		Expect(got).To(HaveLen(2))
		Expect(convertToSeconds(got[0].Start).Seconds()).To(Equal(15.0))
		Expect(got[1].Start).To(Equal("00:30"))
	})
	It("rejects background activity and evidence beyond the exact segment", func() {
		seg := Segment{Start: "00:10", End: "00:20"}
		_, ok := decodeWODEvidence("```wod_evidence\n"+`{"target_confirmed":false,"movement":"Burpee","start_secs":10,"end_secs":20}`+"\n```", seg)
		Expect(ok).To(BeFalse())
		_, ok = decodeWODEvidence("```wod_evidence\n"+`{"target_confirmed":true,"movement":"Burpee","start_secs":10,"end_secs":21}`+"\n```", seg)
		Expect(ok).To(BeFalse())
	})
	It("synthesizes all observations without video, history, or pre-WOD waiting", func() {
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).NotTo(HaveOccurred())
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": `{"form":80,"intensity":60,"consistency":90,"movements":{"Burpee":{"form":80,"intensity":60},"Walking":{"form":10}},"summary":"좋습니다."}`}}}}}})
		w := &Worker{GeminiClient: client, logger: zap.NewNop()}
		start := 120.0
		end := 180.0
		evidence := []wodSegmentEvidence{{TargetConfirmed: true, Movement: "Burpee", StartSecs: 120, EndSecs: 130, Pace: []string{"early pace"}}, {TargetConfirmed: true, Movement: "Burpee", StartSecs: 170, EndSecs: 180, Pace: []string{"late pace"}}}
		raw := w.synthesizeWOD(context.Background(), VideoAnalysisPayload{WorkoutType: "wod"}, wodeval.Start{Seconds: &start, EndSeconds: &end, Status: "confirmed"}, evidence, true)
		score, ok := wodeval.Decode(raw)
		Expect(ok).To(BeTrue())
		Expect(*score.Overall).To(Equal(76))
		Expect(score.Movements).NotTo(HaveKey("Walking"))
		Expect(transport.Verify()).To(Succeed())
		requests := transport.Requests()
		Expect(requests).NotTo(BeEmpty())
		var body map[string]any
		Expect(json.Unmarshal([]byte(requests[0].Body), &body)).To(Succeed())
		text := string(requests[0].Body)
		Expect(text).To(ContainSubstring("early pace"))
		Expect(text).To(ContainSubstring("late pace"))
		Expect(strings.Contains(text, "fileUri")).To(BeFalse())
		Expect(text).NotTo(ContainSubstring(`\"start_secs\":120`))
	})
	It("stays complete across post-WOD footage and verified recording pauses, matching movement names by catalog key", func() {
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).NotTo(HaveOccurred())
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": `{"form":80,"intensity":60,"consistency":90,"movements":{"power clean":{"form":80,"intensity":60}},"summary":"좋습니다."}`}}}}}})
		set := func(id string, from, to float64) workoutprogress.Interval {
			return workoutprogress.Interval{ID: id, Start: from, End: to, Activity: "exercise", Movement: "Power Clean", FormObservations: []workoutprogress.Observation{{Start: from, End: from + 2, Evidence: "stable catch"}}}
		}
		progress := workoutprogress.Build([]workoutprogress.Interval{set("early", 120, 150), set("late", 150, 180)}, []workoutprogress.Gap{{Start: 180, End: 180.03, Clock: "media", Reason: "not_analyzed"}, {Start: 200, End: 230, Clock: "capture", Reason: "recording_gap"}}, "source")
		evidence := []wodSegmentEvidence{{TargetConfirmed: true, Movement: "Power Clean", StartSecs: 120, EndSecs: 150, Pace: []string{"early pace"}}, {TargetConfirmed: true, Movement: "Power Clean", StartSecs: 150, EndSecs: 180, Pace: []string{"late pace"}}}
		start := 120.0
		w := &Worker{GeminiClient: client, logger: zap.NewNop()}
		score, ok := wodeval.Decode(w.synthesizeWODWithProgress(context.Background(), VideoAnalysisPayload{WorkoutType: "wod"}, wodeval.Start{Seconds: &start, Status: "confirmed"}, evidence, true, progress, "", func(string, *gemini.TokenUsage) {}))
		Expect(ok).To(BeTrue())
		Expect(score.EvidenceStatus).To(Equal("complete"))
		Expect(score.WODStart.EndSeconds).NotTo(BeNil())
		Expect(*score.WODStart.EndSeconds).To(Equal(180.0))
		Expect(score.Intensity).NotTo(BeNil())
		Expect(score.Overall).NotTo(BeNil())
		Expect(score.Movements).To(HaveKey("power clean"))
		Expect(transport.Verify()).To(Succeed())
	})
	It("preserves nullable scores through the public parser and suppresses partial intensity", func() {
		w := &Worker{logger: zap.NewNop()}
		start := 10.0
		end := 100.0
		raw := w.synthesizeWOD(context.Background(), VideoAnalysisPayload{}, wodeval.Start{Seconds: &start, EndSeconds: &end, Status: "confirmed"}, nil, false)
		parsed := ParseSessionScore("```score\n" + raw + "\n```")
		score, ok := wodeval.Decode(parsed)
		Expect(ok).To(BeTrue())
		Expect(score.Intensity).To(BeNil())
		Expect(score.Overall).To(BeNil())
		Expect(score.WODStart.EndSeconds).To(BeNil())
	})

	It("reuses a previously confirmed auto-detected WODStart without calling Gemini when covered by an exercise segment", func() {
		conn, err := testhelpers.InitDB()
		Expect(err).To(Succeed())
		testhelpers.CleanupDB(conn)
		profile := testhelpers.CreateProfile(conn, &db.Profile{})
		session := testhelpers.CreateSession(conn, &db.Session{
			SessionID:   "WOD-20261014-01JREUSEWODSTART00",
			ProfileID:   profile.ID,
			WorkoutType: "wod",
		})
		testhelpers.CreateAnalysisResult(conn, &db.AnalysisResult{
			SessionID:    session.SessionID,
			ProfileID:    profile.ID,
			Status:       "COMPLETED",
			SessionScore: `{"evaluation_version":1,"wod_start":{"seconds":12.5,"end_seconds":30,"status":"confirmed","source":"auto"," evidence":"first rep"}}`,
		})

		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{
			APIKey:     "test",
			HTTPClient: &http.Client{Transport: transport},
		})
		Expect(err).To(Succeed())
		w := &Worker{DB: conn, GeminiClient: client, logger: zap.NewNop()}

		got := w.detectWODStart(
			context.Background(),
			VideoAnalysisPayload{SessionID: session.SessionID, ProfileID: profile.ID, WorkoutType: "wod"},
			&gemini.UploadResult{FileURI: "https://example.com/video", MIMEType: "video/mp4", VideoDuration: time.Minute},
			[]Segment{{Start: "00:10", End: "00:30", Type: "Burpee"}},
			nil,
		)
		Expect(got.Status).To(Equal("confirmed"))
		Expect(got.Seconds).NotTo(BeNil())
		Expect(*got.Seconds).To(Equal(12.5))
		Expect(transport.Requests()).To(BeEmpty())
	})
})

