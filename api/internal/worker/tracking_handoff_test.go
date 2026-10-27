package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

var _ = Describe("Tracking channel", func() {
	It("keeps at most three valid normalized boxes in time order", func() {
		boxes := validTrackingBoxes([]trackingBox{
			{T: 9, Box: []float64{100, 100, 900, 400}},
			{T: 1, Box: []float64{100, 100, 900, 400}},
			{T: 5, Box: []float64{500, 100, 400, 400}}, // ymin >= ymax
			{T: 2, Box: []float64{0, 0, 1200, 10}},     // out of range
			{T: 3, Box: []float64{1, 2, 3}},            // malformed
			{T: 4, Box: []float64{100, 100, 900, 400}},
			{T: 6, Box: []float64{100, 100, 900, 400}},
		})
		Expect(boxes).To(HaveLen(3))
		Expect([]float64{boxes[0].T, boxes[1].T, boxes[2].T}).To(Equal([]float64{1, 4, 6}))
	})

	It("stores box, location and station from target_person but never a model-supplied reference image", func() {
		cues, conf := parseTargetPerson("```target_person\n" + `{"matched_cues":["red shirt"],"ambiguous":false,"confidence":0.9,"boxes":[{"t":1,"box_2d":[100,200,900,500]}],"frame_region":"left","station":"rower","reference_image":"videos/9/other/tracking/1.jpg"}` + "\n```")
		Expect(conf).To(Equal(0.9))
		tp, ok := parseStoredTargetPerson(cues)
		Expect(ok).To(BeTrue())
		Expect(tp.Boxes).To(HaveLen(1))
		Expect(tp.Station).To(Equal("rower"))
		Expect(tp.ReferenceImage).To(BeEmpty())
		legacy, _ := parseTargetPerson("```target_person\n" + `{"matched_cues":["red shirt"],"ambiguous":false,"confidence":0.9}` + "\n```")
		Expect(legacy).To(Equal(`{"matched_cues":["red shirt"],"ambiguous":false,"confidence":0.9}`), "the stored shape is unchanged without tracking fields")

		// Boxes-only block without matched_cues key (e.g. [NO_EXERCISE] rest chunk).
		noCuesInput := "[NO_EXERCISE]\n```target_person\n" + `{"boxes":[{"t":2.5,"box_2d":[110,300,940,610]}],"confidence":0.88,"ambiguous":false,"frame_region":"center","station":"floor"}` + "\n```"
		noCuesJSON, noCuesConf := parseTargetPerson(noCuesInput)
		Expect(noCuesConf).To(Equal(0.88))
		tpNoCues, ok := parseStoredTargetPerson(noCuesJSON)
		Expect(ok).To(BeTrue())
		Expect(tpNoCues.MatchedCues).To(BeEmpty())
		Expect(tpNoCues.Boxes).To(HaveLen(1))
		Expect(stripTargetPerson(stripExerciseTag(noCuesInput))).To(BeEmpty())

		// Duplicate target_person blocks merge boxes instead of dropping the second block.
		dupInput := "좋은 자세입니다.\n```target_person\n" + `{"matched_cues":["black shirt"],"ambiguous":false,"confidence":0.9}` + "\n```\n```target_person\n" + `{"matched_cues":[],"ambiguous":false,"confidence":0.92,"boxes":[{"t":0.5,"box_2d":[120,400,980,620]}],"frame_region":"center","station":"barbell"}` + "\n```"
		dupJSON, dupConf := parseTargetPerson(dupInput)
		Expect(dupConf).To(Equal(0.92))
		tpDup, ok := parseStoredTargetPerson(dupJSON)
		Expect(ok).To(BeTrue())
		Expect(tpDup.MatchedCues).To(Equal([]string{"black shirt"}))
		Expect(tpDup.Boxes).To(HaveLen(1))
		Expect(tpDup.Station).To(Equal("barbell"))
		Expect(stripTargetPerson(dupInput)).To(Equal("좋은 자세입니다."))
	})

	It("emits a single unified target_person instruction without forcing confidence 0.5 when appearance cues are absent", func() {
		w := &Worker{TrackingContinuityEnabled: true}
		base := w.buildChunkAnalysisPrompt(VideoAnalysisPayload{WorkoutType: WorkoutTypeWOD})
		trackingPrompt, _ := w.trackingInputs(context.Background(), 1, "WOD-20261026-01ABC", WorkoutTypeWOD, "gs://b/videos/1/WOD-20261026-01ABC/c.mp4", 0)
		fullPrompt := base + trackingPrompt
		Expect(fullPrompt).To(ContainSubstring(`"boxes"`))
		Expect(fullPrompt).NotTo(ContainSubstring("confidence는 0.5로 두세요"))
		Expect(bytes.Count([]byte(fullPrompt), []byte("```target_person"))).To(Equal(1))

		wDisabled := &Worker{TrackingContinuityEnabled: false}
		disabledPrompt := wDisabled.buildChunkAnalysisPrompt(VideoAnalysisPayload{WorkoutType: WorkoutTypeWOD})
		Expect(bytes.Count([]byte(disabledPrompt), []byte("```target_person"))).To(Equal(1))
		Expect(disabledPrompt).NotTo(ContainSubstring("confidence는 0.5로 두세요"))
	})

	It("hands forward only tracking fields, never assessment output", func() {
		prev := db.ChunkAnalysisResult{
			Status: "COMPLETED", ExerciseType: "Row", TargetConfidence: 0.9,
			Output:          "keep your back straight",
			ObservedSignals: `{"movement":"Row","activity_state":"exercise","form_issues_seen":["rounded back"],"rep_count":12}`,
			TargetCues:      `{"matched_cues":["red shirt"],"ambiguous":false,"confidence":0.9,"frame_region":"left","station":"rower"}`,
		}
		h := buildTrackingHandoff(prev, WorkoutTypeWOD)
		raw, err := json.Marshal(h)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(Equal(`{"matched_cues":["red shirt"],"frame_region":"left","station":"rower","activity_state":"exercise","movement_hint":"Row"}`))
		prompt := trackingInputPrompt(h, true, true)
		for _, forbidden := range []string{"keep your back straight", "rounded back", "rep_count", "12"} {
			Expect(prompt).NotTo(ContainSubstring(forbidden))
		}
		Expect(prompt).To(ContainSubstring("첨부 이미지 1"))
		Expect(prompt).To(ContainSubstring("첨부 이미지 2"))
		Expect(prompt).To(ContainSubstring("다른 사람으로 바꾸지 말고"))
		Expect(prompt).To(ContainSubstring("원인이나 변명으로 쓰지 마세요"))

		prev.TargetCues = `{"matched_cues":["red shirt"],"ambiguous":true,"confidence":0.5,"station":"rower"}`
		prev.TargetConfidence = 0.5
		uncertain := buildTrackingHandoff(prev, WorkoutTypeWOD)
		Expect(uncertain.MatchedCues).To(BeEmpty(), "an uncertain target is not handed forward")
		Expect(uncertain.Station).To(BeEmpty())
		Expect(trackingInputPrompt(trackingHandoff{}, false, false)).To(BeEmpty())
	})

	It("marks the target box in pixel space regardless of resolution", func() {
		src := image.NewRGBA(image.Rect(0, 0, 200, 100))
		r := trackingBoxRect(src.Bounds(), []float64{100, 250, 900, 750})
		Expect(r).To(Equal(image.Rect(50, 10, 150, 90)))
		marked := drawTrackingBox(src, []float64{100, 250, 900, 750})
		Expect(marked.At(50, 50)).To(Equal(color.RGBA{R: 0, G: 230, B: 90, A: 255}))
		Expect(marked.At(100, 50)).To(Equal(color.RGBA{}), "the inside of the box is untouched")
	})

	It("is silent when disabled and stores references under the session prefix", func() {
		w := &Worker{}
		prompt, images := w.trackingInputs(context.Background(), 1, "WOD-20261026-01ABC", WorkoutTypeWOD, "gs://b/videos/1/WOD-20261026-01ABC/c.mp4", 10)
		Expect(prompt).To(BeEmpty())
		Expect(images).To(BeNil())
		w.TrackingContinuityEnabled = true
		prompt, images = w.trackingInputs(context.Background(), 1, "WOD-20261026-01ABC", WorkoutTypeWOD, "gs://b/videos/1/WOD-20261026-01ABC/c.mp4", 10)
		Expect(prompt).To(Equal(trackingOutputPrompt))
		Expect(images).To(BeNil())
		Expect(trackingReferenceObject(1, "WOD-20261026-01ABC", 42)).To(Equal("videos/1/WOD-20261026-01ABC/tracking/42.jpg"))
	})

	Context("with stored chunks", func() {
		var (
			w                *Worker
			storageTransport *testhelpers.MockTransport
		)

		It("sends the latest continuous reference image and the previous chunk's tracking summary", func() {
			dbConn, err := testhelpers.InitDB()
			Expect(err).NotTo(HaveOccurred())
			testhelpers.CleanupDB(dbConn)
			storageTransport = testhelpers.NewMockTransport()
			storageClient, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
			Expect(err).NotTo(HaveOccurred())
			w = &Worker{DB: dbConn, StorageClient: storageClient, BucketName: "test-bucket", logger: zap.NewNop(), TrackingContinuityEnabled: true}
			profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
			sessionID := "WOD-20261026-01M5TWZSMV81CW4DPHQY10DW75"
			start, end := 0.0, 10.0
			prev := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				SessionID: sessionID, ProfileID: profile.ID, Status: "COMPLETED", ExerciseType: "Row",
				FilePath: fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_0.mp4", profile.ID, sessionID),
				Output:   "keep your back straight", ObservedSignals: `{"movement":"Row","activity_state":"exercise","form_issues_seen":["rounded back"]}`,
				TargetConfidence: 0.9, StartSecs: &start, EndSecs: &end,
			})
			object := trackingReferenceObject(profile.ID, sessionID, prev.ID)
			Expect(dbConn.Model(&db.ChunkAnalysisResult{}).Where("id = ?", prev.ID).Update("target_cues",
				fmt.Sprintf(`{"matched_cues":["red shirt"],"ambiguous":false,"confidence":0.9,"station":"rower","reference_image":%q}`, object)).Error).To(Succeed())

			var jpegBytes bytes.Buffer
			Expect(jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)).To(Succeed())
			testhelpers.MockGCSDownloadWithBody(storageTransport, "gs://test-bucket/"+object, jpegBytes.Bytes())

			prompt, images := w.trackingInputs(context.Background(), profile.ID, sessionID, WorkoutTypeWOD,
				fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_1.mp4", profile.ID, sessionID), 10)
			Expect(storageTransport.Verify()).To(Succeed())
			Expect(images).To(HaveLen(1), "the anchor equals the latest reference, so it is sent once")
			Expect(prompt).To(ContainSubstring(trackingOutputPrompt))
			Expect(prompt).To(ContainSubstring(`"movement_hint":"Row"`))
			Expect(prompt).To(ContainSubstring(`"station":"rower"`))
			Expect(prompt).NotTo(ContainSubstring("keep your back straight"))
			Expect(prompt).NotTo(ContainSubstring("rounded back"))
		})
	})
})
