package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

var _ = Describe("Pass 2 continuous windows", func() {
	It("observes a continuous bout up to 150 seconds in one call", func() {
		windows := splitPass2Windows([]Segment{{Start: "18:20.37", End: "19:30.37", Type: "Row"}})
		Expect(windows).To(HaveLen(1))
		Expect(windows[0].Split).To(BeFalse())
		Expect(windows[0].Seg.Start).To(Equal("18:20.37"))
	})

	It("splits a long bout with 5-second overlap and contiguous cores", func() {
		windows := splitPass2Windows([]Segment{{Start: "0:00", End: "6:40", Type: "Thruster"}})
		Expect(windows).To(HaveLen(3))
		for i, w := range windows {
			Expect(w.Split).To(BeTrue())
			Expect(w.CoreEnd - w.CoreStart).To(BeNumerically("<=", maxPass2WindowSecs))
			if i > 0 {
				Expect(w.CoreStart).To(BeNumerically("~", windows[i-1].CoreEnd, 1e-9))
				Expect(convertToSeconds(w.Seg.Start).Seconds()).To(BeNumerically("~", w.CoreStart-pass2OverlapSecs, 0.01))
			}
		}
		Expect(windows[0].Seg.Start).To(Equal("0:00"))
		Expect(windows[2].Seg.End).To(Equal("6:40"))
	})

	It("counts a repetition crossing an internal split exactly once and stitches the windows back", func() {
		windows := splitPass2Windows([]Segment{{Start: "0:00", End: "5:00", Type: "Thruster"}})
		Expect(windows).To(HaveLen(2))
		split := windows[0].CoreEnd
		rep := func(start, end float64) activity.Observation {
			return activity.Observation{Movement: "Thruster", Unit: "reps", Start: start, End: end, Complete: true, Evidence: "front squat to overhead lockout"}
		}
		crossing := rep(split-1, split+1)
		first := workoutprogress.Interval{ID: "w0", Start: 0, End: split + 5, Activity: "exercise", Movement: "Thruster", RepEvents: []activity.Observation{rep(10, 13), crossing}}
		second := workoutprogress.Interval{ID: "w1", Start: split - 5, End: 300, Activity: "exercise", Movement: "Thruster", RepEvents: []activity.Observation{crossing, rep(200, 203)}}
		a, _ := ownWindowObservations([]workoutprogress.Interval{first}, nil, windows[0])
		b, _ := ownWindowObservations([]workoutprogress.Interval{second}, nil, windows[1])
		Expect(a[0].RepEvents).To(HaveLen(1), "the crossing rep completes in the second core")
		Expect(b[0].RepEvents).To(HaveLen(2))
		Expect(a[0].End).To(Equal(b[0].Start))

		merged, gaps := workoutprogress.NormalizeContinuity(append(a, b...), nil, nil)
		progress := workoutprogress.Build(merged, gaps, "video")
		Expect(progress.Intervals).To(HaveLen(1))
		Expect(progress.Intervals[0].RepEvents).To(HaveLen(3))
	})

	It("passes only movement candidates, never chunk coaching text, to the observer prompt", func() {
		seg := Segment{Type: "Row", Description: candidateMovementsPrefix + "Row, Burpee | keep your back straight"}
		Expect(segmentCandidateMovements(seg)).To(Equal([]string{"Row", "Burpee"}))
		Expect(segmentCandidateMovements(Segment{Description: "keep your back straight"})).To(BeEmpty())
		prompt := (&Worker{}).buildSegmentAnalysisPrompt(VideoAnalysisPayload{}, seg, "", "", false)
		Expect(prompt).To(ContainSubstring("청크 후보 종목(확정 아님): Row, Burpee"))
		Expect(prompt).To(ContainSubstring("직전 운동이나 누적 수행을 원인이나 변명으로 쓰지 마세요"))
		Expect(prompt).NotTo(ContainSubstring("keep your back straight"))
	})

	It("decodes several wod_evidence entries for one continuous bout", func() {
		seg := Segment{Start: "0:00", End: "1:00"}
		text := "```wod_evidence\n" + `[{"target_confirmed":true,"movement":"Row","start_secs":0,"end_secs":30},{"target_confirmed":true,"movement":"Burpee","start_secs":30,"end_secs":60}]` + "\n```"
		items, ok := decodeWODEvidenceList(text, seg)
		Expect(ok).To(BeTrue())
		Expect(items).To(HaveLen(2))
		_, ok = decodeWODEvidenceList("```wod_evidence\n"+`{"target_confirmed":false,"movement":"Row","start_secs":0,"end_secs":30}`+"\n```", seg)
		Expect(ok).To(BeFalse())
	})
})
