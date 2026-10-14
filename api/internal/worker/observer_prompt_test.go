package worker

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"go.uber.org/zap"
)

var _ = Describe("Observation-only prompt boundaries", func() {
	var w *Worker

	BeforeEach(func() {
		w = NewWorker(nil, nil, "", nil, nil, zap.NewNop())
	})

	// Check assembled prompts: policy/context appended after a base prompt must
	// not restore the old instruction to diagnose physiology from movement.
	checkBoundary := func(prompt string) {
		Expect(prompt).To(ContainSubstring("관찰 전용 경계: 보이는 현상과 원인을 분리"))
		Expect(prompt).To(ContainSubstring("피로의 유무·수준·시작·원인"))
		Expect(prompt).To(ContainSubstring("가능성"))
		Expect(prompt).To(ContainSubstring("모든 JSON 문자열에 동일하게 적용"))
		Expect(prompt).To(ContainSubstring("여러 세트를 하나의 긴 휴식으로 덮지 마세요"))
		Expect(strings.HasSuffix(prompt, ObservationOnlyOutputCheckPrompt)).To(BeTrue())
		for _, obsolete := range []string{
			"피로가 자세에 어떤 영향을", "힘의 누수(Force Leakage)",
			"피로 신호로 인정", "피로는 운동 반복 중에만 판정",
			"약한 근육을 강화", "피로를 이겨내", "fatigue_onset(피로",
			"호흡 유도, 호흡과 결합된", "준비도(Readiness)",
		} {
			Expect(prompt).NotTo(ContainSubstring(obsolete))
		}
	}

	DescribeTable("preserves complete timed evidence across deep and legacy workout routes",
		func(workoutType string, legacy bool) {
			p := VideoAnalysisPayload{WorkoutType: workoutType, Movements: []string{"Power Clean"}, Injuries: []string{"Knee"}}
			var prompt string
			if legacy {
				prompt = w.buildAnalysisPrompt(p, 60)
			} else {
				prompt = w.buildSegmentAnalysisPrompt(p, Segment{Start: "0:10.25", End: "0:30.75", Type: "Power Clean"}, "", "", false)
			}
			checkBoundary(prompt)
			Expect(prompt).To(ContainSubstring("rep_events"))
			Expect(prompt).To(ContainSubstring("form_observations"))
			Expect(prompt).To(ContainSubstring("rom_observations"))
			Expect(prompt).To(ContainSubstring("pace_observations"))
			Expect(prompt).To(ContainSubstring("완료된 반복별 시각"))
			Expect(prompt).To(ContainSubstring("fatigue_onset와 fatigue_point는 관찰 단계에서 출력하지 마세요"))
			Expect(prompt).To(ContainSubstring("type: positive_form("))
			Expect(prompt).To(ContainSubstring("limited_*는 현재 동작에서 보인 범위만"))
		},
		Entry("WOD segment", WorkoutTypeWOD, false),
		Entry("warmup segment", WorkoutTypeWarmup, false),
		Entry("cooldown segment", WorkoutTypeCooldown, false),
		Entry("accessory segment", WorkoutTypeAccessory, false),
		Entry("WOD legacy", WorkoutTypeWOD, true),
		Entry("warmup legacy", WorkoutTypeWarmup, true),
		Entry("cooldown legacy", WorkoutTypeCooldown, true),
		Entry("accessory legacy", WorkoutTypeAccessory, true),
	)

	DescribeTable("keeps chunk metrics unknown for physiological judgments after context injection",
		func(route string) {
			var prompt string
			switch route {
			case "chunk":
				prompt = w.buildChunkAnalysisPrompt(VideoAnalysisPayload{HeartRateBPM: 168, WODDescription: "Power Clean"})
			case "session chunk":
				prompt = w.buildChunkAnalysisWithSessionPrompt(VideoAnalysisWithSessionPayload{HeartRateBPM: 168}, &db.Profile{}, &db.Session{WODDescription: "Power Clean"})
			case "debug chunk":
				prompt = w.buildChunkDebugReanalysisPrompt(&chunkDebugTarget{HeartRateBPM: 168, WODDescription: "Power Clean"})
			case "recovery chunk":
				prompt = w.buildChunkAnalysisPrompt(VideoAnalysisPayload{WorkoutType: WorkoutTypeWarmup, HeartRateBPM: 168})
			}
			checkBoundary(prompt)
			Expect(prompt).To(ContainSubstring("168 bpm"))
			Expect(prompt).To(ContainSubstring(`"exertion_estimate": "unknown"`))
			Expect(prompt).To(ContainSubstring(`"fatigue_evidence_types": []`))
			Expect(prompt).To(ContainSubstring("이는 피로가 없다는 뜻이 아니라 관찰 단계에서 피로를 판정하지 않았다는 뜻"))
		},
		Entry("chunk", "chunk"),
		Entry("session chunk", "session chunk"),
		Entry("debug chunk", "debug chunk"),
		Entry("recovery chunk", "recovery chunk"),
	)

	DescribeTable("does not reintroduce muscle weakness or effort prescriptions through fitness policy",
		func(level string) {
			prompt := baseChunkAnalysisPrompt() + levelPolicyForFitnessLevel(level) + ObservedSignalsPrompt + ObservationOnlyOutputCheckPrompt
			checkBoundary(prompt)
			Expect(prompt).NotTo(ContainSubstring("피로를 무시"))
			Expect(prompt).NotTo(ContainSubstring("수행 여유가 있어 보이면"))
		},
		Entry("beginner", "beginner"), Entry("intermediate", "intermediate"), Entry("advanced", "advanced"),
	)

	It("applies the same observation boundary to Agentic highlight reasons without changing JSON output", func() {
		prompt := agenticHighlightPrompt(AgenticHighlights{Duration: 60, Person: "foreground athlete"}, AgenticHighlight{Highlight: HighlightSegment{Start: "0:10", End: "0:15"}})
		checkBoundary(prompt)
		Expect(prompt).To(ContainSubstring("one JSON object, without markdown fences"))
		Expect(prompt).To(ContainSubstring("Prior labels and judgments are deliberately omitted"))
		Expect(prompt).To(ContainSubstring("Evidence must be inside the context window"))
		Expect(prompt).To(ContainSubstring(`"direct_observation"`))
	})

	It("keeps injury-focused supplementary video analysis observational after profile and timestamp context", func() {
		prompt := w.buildInjuryAnalysisPrompt(InjuryAnalysisPayload{Injuries: []string{"Knee"}, FocusTimestamps: `[{"start":"0:10","end":"0:15","reason":"무릎 내전"}]`})
		checkBoundary(prompt)
		Expect(prompt).To(ContainSubstring("0:10"))
		Expect(prompt).To(ContainSubstring("Knee"))
		Expect(prompt).To(ContainSubstring("통증 유무나 안전한 복귀 여부를 영상으로 판정하지"))
		Expect(prompt).NotTo(ContainSubstring("부상 부위를 보호하기 위한 보상 작용"))
		Expect(prompt).NotTo(ContainSubstring("팁 3가지"))
	})

	It("keeps observable equipment-free warmup exercise distinct from no exercise on both live routes", func() {
		prompts := []string{
			w.buildChunkAnalysisPrompt(VideoAnalysisPayload{WorkoutType: WorkoutTypeWarmup}),
			w.buildChunkAnalysisWithSessionPrompt(VideoAnalysisWithSessionPayload{}, &db.Profile{}, &db.Session{WorkoutType: WorkoutTypeWarmup}),
		}
		for _, prompt := range prompts {
			checkBoundary(prompt)
			Expect(prompt).To(ContainSubstring("기구 없이 수행하는 맨몸 동작"))
			Expect(prompt).To(ContainSubstring("기구를 들고 있지 않다는 이유로 [NO_EXERCISE]를 출력하지"))
			Expect(prompt).To(ContainSubstring("운동은 보이지만 정확한 종목이 불명확하면 [EXERCISE: Unknown]"))
		}
	})
})
