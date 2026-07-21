package worker

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("structured fatigue evidence", func() {
	It("formats repeated corrections as weak personal hints", func() {
		context := formatPersonalMovementHintsContext([]db.PersonalMovementHint{{
			PredictedMovement: "Rope Climb",
			CorrectedMovement: "Pull-up",
			SessionCount:      2,
		}})

		Expect(context).To(ContainSubstring("약한 힌트"))
		Expect(context).To(ContainSubstring("Rope Climb"))
		Expect(context).To(ContainSubstring("Pull-up"))
		Expect(context).To(ContainSubstring("2"))
		Expect(context).To(ContainSubstring("무시하세요"))
	})

	It("rejects fatigue attached to walking, rest, setup, or recovery", func() {
		raw := `{"movement":"Walking","activity_state":"walking","fatigue_visually_established":true,"fatigue_evidence_types":["rep_slowdown"],"fatigue_evidence":["걸음이 느려짐"]}`

		var got map[string]any
		Expect(json.Unmarshal([]byte(sanitizeObservedSignals(raw)), &got)).To(Succeed())
		Expect(got["fatigue_visually_established"]).To(BeFalse())
		Expect(got["fatigue_evidence_types"]).To(BeEmpty())
		Expect(got["fatigue_evidence"]).To(BeEmpty())
	})

	It("rejects BPM-only fatigue evidence", func() {
		raw := `{"movement":"Burpee","activity_state":"exercise","fatigue_visually_established":true,"fatigue_evidence_types":["heart_rate"],"fatigue_evidence":["170 bpm"]}`

		var got map[string]any
		Expect(json.Unmarshal([]byte(sanitizeObservedSignals(raw)), &got)).To(Succeed())
		Expect(got["fatigue_visually_established"]).To(BeFalse())
		Expect(got["fatigue_evidence_types"]).To(BeEmpty())
	})

	It("rejects BPM text even when it is paired with an allowed evidence type", func() {
		raw := `{"movement":"Burpee","activity_state":"exercise","fatigue_visually_established":true,"fatigue_evidence_types":["rep_slowdown"],"fatigue_evidence":["heart rate reached 170 bpm"]}`

		var got map[string]any
		Expect(json.Unmarshal([]byte(sanitizeObservedSignals(raw)), &got)).To(Succeed())
		Expect(got["fatigue_visually_established"]).To(BeFalse())
		Expect(got["fatigue_evidence"]).To(BeEmpty())
	})

	It("retains only allowed visible fatigue evidence", func() {
		raw := `{"movement":"Snatch","activity_state":"exercise","fatigue_visually_established":true,"fatigue_evidence_types":["form_breakdown","heart_rate","form_breakdown"],"fatigue_evidence":["반복마다 허리가 더 굽음"]}`

		var got map[string]any
		Expect(json.Unmarshal([]byte(sanitizeObservedSignals(raw)), &got)).To(Succeed())
		Expect(got["fatigue_visually_established"]).To(BeTrue())
		Expect(got["fatigue_evidence_types"]).To(ConsistOf("form_breakdown"))
		Expect(got["fatigue_evidence"]).To(ConsistOf("반복마다 허리가 더 굽음"))
	})

	It("aggregates every structured chunk without using heart rate as evidence", func() {
		start0, end0 := 0.0, 10.0
		start1, end1 := 10.0, 20.0
		chunks := []db.ChunkAnalysisResult{
			{
				ExerciseType:    "Snatch",
				StartSecs:       &start0,
				EndSecs:         &end0,
				MediaStartSecs:  &start0,
				MediaEndSecs:    &end0,
				HeartRateBPM:    170,
				ObservedSignals: `{"movement":"Snatch","activity_state":"exercise","fatigue_visually_established":true,"fatigue_evidence_types":["rep_slowdown"],"fatigue_evidence":["세 반복 동안 속도가 계속 감소함"]}`,
			},
			{
				ExerciseType:    "Snatch",
				StartSecs:       &start1,
				EndSecs:         &end1,
				MediaStartSecs:  &start1,
				MediaEndSecs:    &end1,
				HeartRateBPM:    175,
				ObservedSignals: `{"movement":"Snatch","activity_state":"exercise","fatigue_visually_established":true,"fatigue_evidence_types":["form_breakdown"],"fatigue_evidence":["락아웃이 반복마다 짧아짐"]}`,
			},
			{
				ExerciseType:    "Walking",
				HeartRateBPM:    180,
				ObservedSignals: `{"movement":"Walking","activity_state":"walking","fatigue_visually_established":true,"fatigue_evidence_types":["rep_slowdown"],"fatigue_evidence":["걷는 속도가 느림"]}`,
			},
			{
				ExerciseType:    "Pull-up",
				ObservedSignals: `{"movement":"Pull-up","activity_state":"exercise","fatigue_visually_established":false,"fatigue_evidence_types":[],"fatigue_evidence":[]}`,
			},
		}

		context := buildFatigueEvidenceContext(chunks)
		Expect(context).To(ContainSubstring("구조화 청크: 4, 운동: 3, 걷기/휴식/준비/회복: 1"))
		Expect(context).To(ContainSubstring("시각적으로 피로가 확립된 운동 청크: 2"))
		Expect(context).To(ContainSubstring("Snatch: 2개 청크"))
		Expect(context).To(ContainSubstring("최초 근거 0.0~10.0초"))
		Expect(context).NotTo(ContainSubstring("170"))
		Expect(context).NotTo(ContainSubstring("175"))
		Expect(context).NotTo(ContainSubstring("180"))
	})

	It("prohibits a final fatigue event when no chunk has valid visual evidence", func() {
		chunks := []db.ChunkAnalysisResult{{
			ExerciseType:    "Rest",
			ObservedSignals: `{"movement":"Rest","activity_state":"rest_setup","fatigue_visually_established":true,"fatigue_evidence_types":["heart_rate"],"fatigue_evidence":["high bpm"]}`,
		}}

		context := buildFatigueEvidenceContext(chunks)
		Expect(context).To(ContainSubstring("허용된 시각적 피로 근거가 없습니다"))
		Expect(context).To(ContainSubstring("피로 이벤트나 피로 시작 시점을 생성하지 마세요"))
	})

	It("does not present capture-clock timestamps as merged-media fatigue timing", func() {
		captureStart, captureEnd := 120.0, 130.0
		context := buildFatigueEvidenceContext([]db.ChunkAnalysisResult{{
			ExerciseType: "Burpee",
			StartSecs:    &captureStart,
			EndSecs:      &captureEnd,
			ObservedSignals: `{"movement":"Burpee","activity_state":"exercise","fatigue_visually_established":true,` +
				`"fatigue_evidence_types":["rep_slowdown"],"fatigue_evidence":["반복 속도가 지속적으로 감소함"]}`,
		}})

		Expect(context).To(ContainSubstring("최초 근거 시간 불명"))
		Expect(context).NotTo(ContainSubstring("120.0"))
		Expect(context).NotTo(ContainSubstring("130.0"))
	})
})
