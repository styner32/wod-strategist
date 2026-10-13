package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/workoutprogress"
	"go.uber.org/zap"
)

var _ = Describe("Independent workout progress helpers", func() {
	seg := Segment{Start: "00:10", End: "00:30", Type: "Power Clean"}
	wrap := func(raw string) string { return "```workout_observations\n" + raw + "\n```" }
	observation := func(start, end float64, evidence string) workoutprogress.Observation {
		return workoutprogress.Observation{Start: start, End: end, Evidence: evidence}
	}
	interval := func() workoutprogress.Interval {
		return workoutprogress.Interval{ID: "set-a", Start: 10, End: 30, Activity: "exercise", Movement: "Power Clean", ObservationRefs: []string{"segment-0"}, RepEvents: []activity.Observation{{Start: 11, End: 13, Movement: "Power Clean", Unit: "reps", Complete: true, Evidence: "visible lift and catch"}}, FormObservations: []workoutprogress.Observation{observation(11, 13, "stable catch"), observation(25, 27, "stable catch")}, ROMObservations: []workoutprogress.Observation{}, PaceObservations: []workoutprogress.Observation{observation(11, 13, "two-second cycle")}}
	}
	It("retains early and late direct evidence and complete event references", func() {
		raw := `{"intervals":[{"start_secs":10,"end_secs":30,"activity":"exercise","movement":"Power Clean","target_confirmed":true,"rep_events":[{"movement":"Power Clean","unit":"reps","start_secs":11,"end_secs":13,"complete":true,"evidence":"full visible lift/catch"}],"form_observations":[{"start_secs":11,"end_secs":13,"evidence":"stable catch"},{"start_secs":25,"end_secs":27,"evidence":"late catch drops"}],"rom_observations":[],"pace_observations":[]}],"unassessed":[]}`
		items, gaps := parseWorkoutObservations(wrap(raw), seg, 2)
		Expect(items).To(HaveLen(1))
		Expect(items[0].RepEvents).To(HaveLen(1))
		Expect(items[0].FormObservations).To(HaveLen(2))
		Expect(items[0].ObservationRefs).To(ContainElement("segment-2:interval-0:event-0"))
		Expect(gaps).To(BeEmpty())
	})
	It("does not trust missing target identity, old counts or prose", func() {
		raw := `{"intervals":[{"start_secs":10,"end_secs":30,"activity":"exercise","movement":"Power Clean","rep_events":[{"movement":"Power Clean","unit":"reps","start_secs":11,"end_secs":13,"complete":true,"evidence":"lift"}]}],"unassessed":[]}`
		items, gaps := parseWorkoutObservations("previous estimated 20 reps\n"+wrap(raw), seg, 0)
		Expect(items[0].Activity).To(Equal("unknown"))
		Expect(items[0].RepEvents).To(BeEmpty())
		Expect(gaps).NotTo(BeEmpty())
		items, gaps = parseWorkoutObservations(`{"observed_signals":{"rep_count":20}}`, seg, 0)
		Expect(items).To(BeEmpty())
		Expect(gaps[0].Reason).To(Equal("observations_not_available"))
	})
	It("rejects evidence outside the exact current media window and incomplete cycles", func() {
		raw := `{"intervals":[{"start_secs":10,"end_secs":30,"activity":"exercise","movement":"Power Clean","target_confirmed":true,"rep_events":[{"movement":"Power Clean","unit":"reps","start_secs":9,"end_secs":13,"complete":true,"evidence":"outside window"}],"form_observations":[{"start_secs":9,"end_secs":13,"evidence":"outside window"}]}],"unassessed":[]}`
		items, gaps := parseWorkoutObservations(wrap(raw), seg, 0)
		Expect(items[0].RepEvents).To(BeEmpty())
		Expect(items[0].FormObservations).To(BeEmpty())
		Expect(gaps).NotTo(BeEmpty())
		raw = strings.ReplaceAll(strings.ReplaceAll(raw, `"start_secs":9`, `"start_secs":11`), `"complete":true`, `"complete":false`)
		items, gaps = parseWorkoutObservations(wrap(raw), seg, 0)
		Expect(items[0].RepEvents).To(BeEmpty())
		Expect(gaps).To(ContainElement(HaveField("Reason", "incomplete_cycle")))
	})
	It("attaches only unambiguous planned loads as confirmation candidates", func() {
		items := []workoutprogress.Interval{interval()}
		attachProgressLoadCandidates(items, `[{"movement":"Power Clean","weight_raw":"135lb"}]`)
		Expect(items[0].Load).To(Equal(&workoutprogress.Load{Value: 135, Unit: "lb", Basis: "total", Status: "pending_confirmation"}))
		items = []workoutprogress.Interval{interval()}
		attachProgressLoadCandidates(items, `[{"movement":"Power Clean","weight_raw":"135/95lb","weight_kg":61.2}]`)
		Expect(items[0].Load).To(BeNil())
		attachProgressLoadCandidates(items, `[{"movement":"Power Clean","weight_raw":"135lb"},{"movement":"Power Clean","weight_raw":"155lb"}]`)
		Expect(items[0].Load).To(BeNil())
	})
	It("preserves verified capture clocks and explicit recording gaps", func() {
		ptr := func(n float64) *float64 { return &n }
		chunks := []db.ChunkAnalysisResult{{StartSecs: ptr(20), EndSecs: ptr(40), MediaStartSecs: ptr(10), MediaEndSecs: ptr(20)}, {StartSecs: ptr(50), EndSecs: ptr(70), MediaStartSecs: ptr(20), MediaEndSecs: ptr(30)}}
		items, gaps := mapProgressCaptureTimes([]workoutprogress.Interval{interval()}, nil, chunks)
		Expect(items[0].CaptureStart).To(BeNil())
		Expect(items[0].CaptureEnd).To(BeNil())
		Expect(gaps).To(ConsistOf(workoutprogress.Gap{Start: 40, End: 50, Clock: "capture", Reason: "recording_gap"}))
		oneChunk := interval()
		oneChunk.End = 20
		items, _ = mapProgressCaptureTimes([]workoutprogress.Interval{oneChunk}, nil, chunks)
		Expect(*items[0].CaptureStart).To(Equal(20.0))
		Expect(*items[0].CaptureEnd).To(Equal(40.0))
		chunks[1].MediaStartSecs = ptr(19)
		items, _ = mapProgressCaptureTimes([]workoutprogress.Interval{interval()}, nil, chunks)
		Expect(items[0].CaptureStart).To(BeNil())
	})
	It("excludes pre-onset capture pauses and distinguishes recorded between-set time", func() {
		ptr := func(n float64) *float64 { return &n }
		chunks := []db.ChunkAnalysisResult{{StartSecs: ptr(0), EndSecs: ptr(10), MediaStartSecs: ptr(0), MediaEndSecs: ptr(10)}, {StartSecs: ptr(30), EndSecs: ptr(40), MediaStartSecs: ptr(10), MediaEndSecs: ptr(20)}, {StartSecs: ptr(50), EndSecs: ptr(60), MediaStartSecs: ptr(20), MediaEndSecs: ptr(30)}}
		in := interval()
		in.Start, in.End = 10, 20
		items, gaps := mapProgressCaptureTimes([]workoutprogress.Interval{in}, nil, chunks)
		Expect(*items[0].CaptureStart).To(Equal(30.0))
		Expect(*items[0].CaptureEnd).To(Equal(40.0))
		Expect(gaps).To(BeEmpty())
		in.End = 30
		_, gaps = mapProgressCaptureTimes([]workoutprogress.Interval{in}, nil, chunks)
		Expect(gaps).To(ConsistOf(workoutprogress.Gap{Start: 40, End: 50, Clock: "capture", Reason: "recording_gap"}))
		chunks = []db.ChunkAnalysisResult{{StartSecs: ptr(0), EndSecs: ptr(10), MediaStartSecs: ptr(0), MediaEndSecs: ptr(10)}, {StartSecs: ptr(15), EndSecs: ptr(30), MediaStartSecs: ptr(15), MediaEndSecs: ptr(30)}}
		in.Start = 0
		_, gaps = mapProgressCaptureTimes([]workoutprogress.Interval{in}, nil, chunks)
		Expect(gaps).To(BeEmpty()) // five recorded seconds are not a capture pause
	})
	It("withholds benchmark rates for conflicting relevant clocks without contaminating later onset", func() {
		ptr := func(n float64) *float64 { return &n }
		chunks := []db.ChunkAnalysisResult{{StartSecs: ptr(20), EndSecs: ptr(40), MediaStartSecs: ptr(10), MediaEndSecs: ptr(20)}, {StartSecs: ptr(35), EndSecs: ptr(55), MediaStartSecs: ptr(20), MediaEndSecs: ptr(30)}}
		items, gaps := mapProgressCaptureTimes([]workoutprogress.Interval{interval()}, nil, chunks)
		Expect(items[0].CaptureStart).To(BeNil())
		Expect(gaps).To(ContainElement(workoutprogress.Gap{Start: 10, End: 30, Clock: "media", Reason: "unverified_capture_mapping"}))
		progress := workoutprogress.Build(items, gaps, "source")
		_, err := workoutprogress.Confirm(progress, workoutprogress.Confirmation{IntervalID: items[0].ID, Start: 10, End: 30, Reps: 1, Measurement: "set", Technique: "singles"})
		Expect(err).To(HaveOccurred())
		in := interval()
		in.Start, in.End = 20, 30
		items, gaps = mapProgressCaptureTimes([]workoutprogress.Interval{in}, nil, chunks)
		Expect(*items[0].CaptureStart).To(Equal(35.0))
		Expect(*items[0].CaptureEnd).To(Equal(55.0))
		Expect(gaps).To(BeEmpty()) // pre-onset mapping does not contaminate this observed span
	})
	It("marks omitted time as unassessed rather than rest and hashes observed source", func() {
		path := filepath.Join(GinkgoT().TempDir(), "source.mp4")
		Expect(os.WriteFile(path, []byte("immutable-video"), 0600)).To(Succeed())
		w := &Worker{}
		p := VideoAnalysisPayload{SessionID: "source", WorkoutType: WorkoutTypeWarmup}
		result := w.buildWorkoutProgress(context.Background(), p, path, []workoutprogress.Interval{interval()}, nil, 40, "")
		Expect(result.Unassessed).To(ContainElements(workoutprogress.Gap{Start: 0, End: 10, Clock: "media", Reason: "not_analyzed"}, workoutprogress.Gap{Start: 30, End: 40, Clock: "media", Reason: "not_analyzed"}))
		again := w.buildWorkoutProgress(context.Background(), p, path, []workoutprogress.Interval{interval()}, nil, 40, "")
		Expect(again.SourceVersion).To(Equal(result.SourceVersion))
		Expect(os.WriteFile(path, []byte("different-video"), 0600)).To(Succeed())
		changed := w.buildWorkoutProgress(context.Background(), p, path, []workoutprogress.Interval{interval()}, nil, 40, "")
		Expect(changed.SourceVersion).NotTo(Equal(result.SourceVersion))
	})
	It("uses structured all-interval final input without mutating independent observations", func() {
		const base = "https://generativelanguage.googleapis.com"
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).NotTo(HaveOccurred())
		transport.New(base).Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").MatchBodyContains("workout_progress").MatchBodyContains("late catch").Reply(200).JSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]string{"text": `{"overall":99,"form":80,"intensity":99,"consistency":90,"movements":{"Power Clean":{"form":80},"Background Athlete":{"form":100}},"summary":"점진적 가동범위 확장"}`}}}}}})
		in := interval()
		in.FormObservations[1].Evidence = "late catch"
		progress := workoutprogress.Build([]workoutprogress.Interval{in}, nil, "source")
		before, _ := json.Marshal(progress)
		w := &Worker{GeminiClient: client}
		result := w.assessWorkoutProgress(context.Background(), VideoAnalysisPayload{WorkoutType: WorkoutTypeWarmup}, progress, "", func(string, *gemini.TokenUsage) {})
		Expect(result).To(MatchJSON(`{"overall":83,"form":80,"intensity":0,"consistency":90,"movements":{"Power Clean":{"form":80}},"summary":"점진적 가동범위 확장"}`))
		after, _ := json.Marshal(progress)
		Expect(after).To(Equal(before))
		Expect(transport.Verify()).To(Succeed())
	})
	It("keeps absent assessment evidence null and cumulative real-time input disabled", func() {
		progress := workoutprogress.Build(nil, []workoutprogress.Gap{{Start: 0, End: 20, Clock: "media", Reason: "not_analyzed"}}, "source")
		result := validateWorkoutProgressScore(`{"overall":100,"form":100,"intensity":100,"consistency":100,"movements":{"Power Clean":{"form":100}},"summary":"확인 불가"}`, VideoAnalysisPayload{WorkoutType: WorkoutTypeWOD}, progress)
		Expect(result).To(MatchJSON(`{"overall":null,"form":null,"intensity":null,"consistency":null,"movements":{},"summary":"확인 불가"}`))
		Expect(workoutObservationPrompt).To(ContainSubstring("Do not produce a session score"))
		Expect(workoutProgressAssessmentPrompt(VideoAnalysisPayload{WorkoutType: WorkoutTypeWarmup}, progress, "", "")).To(ContainSubstring("controlled progressive ROM/depth expansion"))
		Expect(baseChunkAnalysisPrompt()).To(ContainSubstring("현재 구간 척추·골반 관찰 경계"))
	})
	It("does not treat different labels for one observed repetition as a temporal comparison", func() {
		in := interval()
		in.FormObservations = in.FormObservations[:1]
		in.ROMObservations = []workoutprogress.Observation{observation(11, 13, "same repetition depth")}
		progress := workoutprogress.Build([]workoutprogress.Interval{in}, nil, "source")
		result := validateWorkoutProgressScore(`{"overall":100,"form":80,"intensity":80,"consistency":90,"movements":{"Power Clean":{"form":80,"intensity":80}},"summary":"single observed cycle"}`, VideoAnalysisPayload{WorkoutType: WorkoutTypeWOD}, progress)
		Expect(result).To(MatchJSON(`{"overall":null,"form":80,"intensity":80,"consistency":null,"movements":{"Power Clean":{"form":80,"intensity":80}},"summary":"single observed cycle"}`))
		Expect(hasComparableProgressObservations(progress)).To(BeFalse())
		in.FormObservations = append(in.FormObservations, observation(25, 27, "late distinct position"))
		other := interval()
		other.ID, other.Start, other.End, other.Movement = "pullup", 30, 40, "Pull-up"
		other.RepEvents = []activity.Observation{}
		other.FormObservations = []workoutprogress.Observation{observation(31, 33, "pull-up position")}
		other.PaceObservations = []workoutprogress.Observation{}
		progress = workoutprogress.Build([]workoutprogress.Interval{in, other}, nil, "source")
		Expect(hasComparableProgressObservations(progress)).To(BeTrue()) // early/late remains usable with other movements present
	})
	It("does not request an assessment when exercise facts are entirely unavailable", func() {
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).NotTo(HaveOccurred())
		w := &Worker{GeminiClient: client}
		progress := workoutprogress.Build(nil, []workoutprogress.Gap{{Start: 0, End: 20, Clock: "media", Reason: "not_analyzed"}}, "source")
		result := w.assessWorkoutProgress(context.Background(), VideoAnalysisPayload{WorkoutType: WorkoutTypeWOD}, progress, "", func(string, *gemini.TokenUsage) { Fail("No model usage expected without observed exercise facts") })
		Expect(result).To(MatchJSON(`{"overall":null,"form":null,"intensity":null,"consistency":null,"movements":{},"summary":"유효한 운동 수행 관찰이 없어 전체 세션을 평가할 수 없습니다."}`))
		Expect(transport.Verify()).To(Succeed())
	})
})

var _ = Describe("Workout summary source fingerprint", func() {
	It("invalidates on progress or authoritative workout type without parsing analysis prose", func() {
		a := db.AnalysisResult{Output: "workout_type:wod in untrusted prose"}
		before := summaryFingerprint(a, WorkoutTypeWarmup)
		Expect(summaryFingerprint(a, WorkoutTypeWOD)).NotTo(Equal(before))
		a.WorkoutProgress = db.NullableJSONDocument(`{"version":1,"source_version":"changed"}`)
		Expect(summaryFingerprint(a, WorkoutTypeWarmup)).NotTo(Equal(before))
	})
})
