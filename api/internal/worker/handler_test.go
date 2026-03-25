package worker

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

var _ = Describe("buildAnalysisPrompt", func() {
	var w *Worker

	BeforeEach(func() {
		logger.Log = zap.NewNop()
		w = &Worker{}
	})

	It("uses the WOD prompt and keeps injury context when the workout type is wod", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Burpee"},
			Injuries:    []string{"Shoulder"},
		})

		Expect(prompt).To(ContainSubstring("# 운동 영상 분석 요청"))
		Expect(prompt).NotTo(ContainSubstring("# 운동 영상 분석 요청 (재활 및 안전 복귀)"))
		Expect(prompt).To(ContainSubstring("## 운동 종목: Burpee"))
		Expect(prompt).To(ContainSubstring("## 알려진 부상 사항: Shoulder"))
	})

	It("uses the rehab prompt for rehab workouts", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeRehab,
			Movements:   []string{"Back Squat"},
			Injuries:    []string{"Lower Back"},
		})

		Expect(prompt).To(ContainSubstring("# 운동 영상 분석 요청 (재활 및 안전 복귀)"))
		Expect(prompt).To(ContainSubstring("## 운동 종목: Back Squat"))
		Expect(prompt).To(ContainSubstring("## 알려진 부상 사항: Lower Back"))
	})

	It("uses the default profile when ProfileID is 0", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			ProfileID:   0,
		})

		Expect(prompt).To(ContainSubstring("1984년 10월 17일"))
	})
})

var _ = Describe("NewVideoAnalysisTask", func() {
	BeforeEach(func() {
		logger.Log = zap.NewNop()
	})

	It("normalizes workout type and keeps injuries in the payload", func() {
		task, err := NewVideoAnalysisTask(
			"session-1",
			"gs://bucket/video.mp4",
			"REHAB",
			[]string{"Burpee"},
			[]string{"Knee"},
			42,
		)
		Expect(err).NotTo(HaveOccurred())

		var payload VideoAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.WorkoutType).To(Equal(WorkoutTypeRehab))
		Expect(payload.Movements).To(Equal([]string{"Burpee"}))
		Expect(payload.Injuries).To(Equal([]string{"Knee"}))
		Expect(payload.ProfileID).To(Equal(uint(42)))
	})
})

