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
		Expect(prompt).To(ContainSubstring("## 운동 종목: Burpee"))
		Expect(prompt).To(ContainSubstring("## 알려진 부상 사항: Shoulder"))
	})

	It("includes injury timestamp section when injuries are present", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Back Squat"},
			Injuries:    []string{"Lower Back"},
		})

		Expect(prompt).To(ContainSubstring("부상 관련 타임스탬프"))
		Expect(prompt).To(ContainSubstring("부상 부위: Lower Back"))
		Expect(prompt).To(ContainSubstring("json"))
	})

	It("does NOT include injury timestamp section when no injuries", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			Movements:   []string{"Burpee"},
		})

		Expect(prompt).NotTo(ContainSubstring("부상 관련 타임스탬프"))
	})

	It("uses the default profile when ProfileID is 0", func() {
		prompt := w.buildAnalysisPrompt(VideoAnalysisPayload{
			WorkoutType: WorkoutTypeWOD,
			ProfileID:   0,
		})

		Expect(prompt).To(ContainSubstring("1984년 10월 17일"))
	})

	It("normalizes any workout type to wod", func() {
		Expect(NormalizeWorkoutType("rehab")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("REHAB")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("wod")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("anything")).To(Equal(WorkoutTypeWOD))
		Expect(NormalizeWorkoutType("")).To(Equal(WorkoutTypeWOD))
	})
})

var _ = Describe("buildChunkAnalysisPrompt", func() {
	var w *Worker

	BeforeEach(func() {
		logger.Log = zap.NewNop()
		w = &Worker{}
	})

	It("includes movements, injuries, and profile in the chunk prompt", func() {
		prompt := w.buildChunkAnalysisPrompt(VideoAnalysisPayload{
			Movements: []string{"Deadlift"},
			Injuries:  []string{"Knee"},
		})

		Expect(prompt).To(ContainSubstring("실시간 피드백"))
		Expect(prompt).To(ContainSubstring("## 운동 종목"))
		Expect(prompt).To(ContainSubstring("Deadlift"))
		Expect(prompt).To(ContainSubstring("## 알려진 부상 사항"))
		Expect(prompt).To(ContainSubstring("Knee"))
		Expect(prompt).To(ContainSubstring("반드시 경고하세요"))
		Expect(prompt).To(ContainSubstring("## 개인 프로필"))
	})

	It("still includes profile even without movements/injuries", func() {
		prompt := w.buildChunkAnalysisPrompt(VideoAnalysisPayload{})

		Expect(prompt).To(ContainSubstring("## 개인 프로필"))
		Expect(prompt).NotTo(ContainSubstring("## 운동 종목"))
		Expect(prompt).NotTo(ContainSubstring("## 알려진 부상 사항"))
	})
})

var _ = Describe("buildInjuryAnalysisPrompt", func() {
	var w *Worker

	BeforeEach(func() {
		logger.Log = zap.NewNop()
		w = &Worker{}
	})

	It("includes focus timestamps when provided", func() {
		prompt := w.buildInjuryAnalysisPrompt(InjuryAnalysisPayload{
			Injuries:        []string{"Knee"},
			FocusTimestamps: `[{"start":"0:32","end":"0:45","reason":"무릎 내전"}]`,
		})

		Expect(prompt).To(ContainSubstring("부상 부위 집중 분석"))
		Expect(prompt).To(ContainSubstring("집중 분석 구간 (Focus Timestamps)"))
		Expect(prompt).To(ContainSubstring("0:32"))
		Expect(prompt).To(ContainSubstring("Knee"))
	})

	It("uses fallback text when timestamps are empty", func() {
		prompt := w.buildInjuryAnalysisPrompt(InjuryAnalysisPayload{
			Injuries:        []string{"Shoulder"},
			FocusTimestamps: "",
		})

		Expect(prompt).To(ContainSubstring("타임스탬프가 제공되지 않았습니다"))
		Expect(prompt).To(ContainSubstring("Shoulder"))
	})
})

var _ = Describe("parseInjuryTimestamps", func() {
	It("extracts valid JSON from fenced code block", func() {
		input := "Some analysis text...\n```injury_timestamps\n[{\"start\":\"0:32\",\"end\":\"0:45\",\"reason\":\"무릎 내전\"}]\n```\nMore text."
		result := parseInjuryTimestamps(input)
		Expect(result).To(ContainSubstring("0:32"))
	})

	It("returns empty for no JSON block", func() {
		result := parseInjuryTimestamps("No JSON here")
		Expect(result).To(BeEmpty())
	})

	It("returns empty for invalid JSON in block", func() {
		input := "```injury_timestamps\nnot valid json\n```"
		result := parseInjuryTimestamps(input)
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("NewVideoAnalysisTask", func() {
	BeforeEach(func() {
		logger.Log = zap.NewNop()
	})

	It("normalizes workout type to wod and keeps injuries in the payload", func() {
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
		Expect(payload.WorkoutType).To(Equal(WorkoutTypeWOD))
		Expect(payload.Movements).To(Equal([]string{"Burpee"}))
		Expect(payload.Injuries).To(Equal([]string{"Knee"}))
		Expect(payload.ProfileID).To(Equal(uint(42)))
	})
})

var _ = Describe("NewChunkAnalysisTask", func() {
	BeforeEach(func() {
		logger.Log = zap.NewNop()
	})

	It("includes timing fields in the payload", func() {
		task, err := NewChunkAnalysisTask(
			"session-1",
			"gs://bucket/chunk.mp4",
			"wod",
			[]string{"Deadlift"},
			nil,
			7,
			10.5,
			20.5,
		)
		Expect(err).NotTo(HaveOccurred())

		var payload VideoAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.StartSecs).To(Equal(10.5))
		Expect(payload.EndSecs).To(Equal(20.5))
		Expect(payload.Movements).To(Equal([]string{"Deadlift"}))
		Expect(payload.ProfileID).To(Equal(uint(7)))
	})
})

var _ = Describe("NewInjuryAnalysisTask", func() {
	BeforeEach(func() {
		logger.Log = zap.NewNop()
	})

	It("creates a task with the correct payload", func() {
		timestamps := `[{"start":"1:00","end":"1:15","reason":"test"}]`
		task, err := NewInjuryAnalysisTask(
			"session-1",
			"gs://bucket/video.mp4",
			[]string{"Knee", "Shoulder"},
			5,
			timestamps,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Type()).To(Equal(TypeInjuryAnalysis))

		var payload InjuryAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.FilePath).To(Equal("gs://bucket/video.mp4"))
		Expect(payload.Injuries).To(Equal([]string{"Knee", "Shoulder"}))
		Expect(payload.ProfileID).To(Equal(uint(5)))
		Expect(payload.FocusTimestamps).To(Equal(timestamps))
	})
})
