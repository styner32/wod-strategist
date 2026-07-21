package worker

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("highlight event consolidation", func() {
	normalize := func(raw string, options HighlightNormalizeOptions) []HighlightSegment {
		segments, err := NormalizeHighlightSegmentsJSON(raw, options)
		Expect(err).NotTo(HaveOccurred())
		return segments
	}

	It("merges adjacent best-form observations into one playback event", func() {
		segments := normalize(`[
			{"start":"05:52","end":"05:56","type":"best_form","movement":"Shoulder to Overhead","reason":"stable dip and drive","confidence":0.93},
			{"start":"05:57","end":"06:02","type":"best_form","movement":"Shoulder to Overhead","reason":"efficient rebound"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 900})

		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Version).To(Equal(2))
		Expect(segments[0].Type).To(Equal("best_form"))
		start, err := parseTimestampToSeconds(segments[0].Start)
		Expect(err).NotTo(HaveOccurred())
		end, err := parseTimestampToSeconds(segments[0].End)
		Expect(err).NotTo(HaveOccurred())
		Expect(start).To(Equal(352.0))
		Expect(end).To(Equal(362.0))
		Expect(segments[0].Observations).To(HaveLen(2))
		Expect(segments[0].Observations[0].Confidence).NotTo(BeNil())
		Expect(*segments[0].Observations[0].Confidence).To(Equal(0.93))
	})

	It("merges adjacent v2 parent fragments without dropping either observation", func() {
		segments := normalize(`[
			{"version":2,"start":"0:00","end":"0:05","type":"best_form","movement":"Press","observations":[{"start":"0:01","end":"0:02","type":"positive_form","reason":"dip"}]},
			{"version":2,"start":"0:01","end":"0:06","type":"best_form","movement":"Press","observations":[{"start":"0:03","end":"0:04","type":"positive_form","reason":"drive"}]}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 20})

		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Observations).To(HaveLen(2))
		Expect(segments[0].Reason).To(ContainSubstring("dip"))
		Expect(segments[0].Reason).To(ContainSubstring("drive"))
	})

	It("does not fabricate evidence from an empty v2 parent", func() {
		segments := normalize(`[
			{"version":2,"start":"0:10","end":"0:15","type":"best_form","movement":"Press","reason":"padded parent only","observations":[]}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 20})

		Expect(segments).To(BeEmpty())
	})

	It("normalizes the oldest numeric timestamp aliases as a technique event", func() {
		segments := normalize(`[
			{"start_time":1.5,"end_time":4.5,"description":"legacy good rep"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 20})

		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("key_moment"))
		Expect(segments[0].Observations).To(HaveLen(1))
		Expect(segments[0].Observations[0].Type).To(Equal(HighlightObservationTechnique))
	})

	It("combines fatigue onset and form breakdown into one improvement event", func() {
		segments := normalize(`[
			{"start":"06:04","end":"06:07","type":"fatigue_point","movement":"Shoulder to Overhead","reason":"cadence slows"},
			{"start":"06:08","end":"06:11","type":"worst_form","movement":"Shoulder to Overhead","reason":"bar drifts forward"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 900})

		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("worst_form"))
		Expect(segments[0].Observations).To(ConsistOf(
			HaveField("Type", HighlightObservationFatigueOnset),
			HaveField("Type", HighlightObservationFormIssue),
		))
	})

	It("absorbs an overlapping key moment as a tag while keeping exact evidence", func() {
		segments := normalize(`[
			{"start":"11:11.9","end":"11:16.1","type":"key_moment","movement":"Toes to Bar","reason":"decisive rhythm transition"},
			{"start":"11:13.1","end":"11:16.1","type":"best_form","movement":"Toes to Bar","reason":"controlled kip"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 900})

		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("best_form"))
		Expect(HighlightSegmentHasTag(segments[0], HighlightTagKeyMoment)).To(BeTrue())
		Expect(segments[0].Observations).To(HaveLen(2))
		Expect(HighlightSegmentHasObservationType(segments[0], HighlightObservationTechnique)).To(BeTrue())
	})

	It("preserves a 0.2-second issue inside a five-second parent", func() {
		segments := normalize(`[
			{"start":"12:27.0","end":"12:27.2","type":"worst_form","movement":"Box Jump","reason":"brief knee collapse"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 900})

		Expect(segments).To(HaveLen(1))
		start, err := parseTimestampToSeconds(segments[0].Start)
		Expect(err).NotTo(HaveOccurred())
		end, err := parseTimestampToSeconds(segments[0].End)
		Expect(err).NotTo(HaveOccurred())
		Expect(end - start).To(BeNumerically("==", 5))
		observationStart, err := parseTimestampToSeconds(segments[0].Observations[0].Start)
		Expect(err).NotTo(HaveOccurred())
		observationEnd, err := parseTimestampToSeconds(segments[0].Observations[0].End)
		Expect(err).NotTo(HaveOccurred())
		Expect(observationEnd - observationStart).To(BeNumerically("~", 0.2, 0.000001))
	})

	It("uses a technique phase to bridge setup, pull, and lockout evidence", func() {
		segments := normalize(`[
			{"start":"01:59","end":"02:02","type":"worst_form","movement":"Clean","reason":"setup correction"},
			{"start":"02:02","end":"02:05","type":"key_moment","movement":"Clean","reason":"initial pull"},
			{"start":"02:04","end":"02:08","type":"best_form","movement":"Clean","reason":"stable lockout"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 300})

		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Type).To(Equal("mixed_form"))
		Expect(segments[0].Observations).To(HaveLen(3))
		Expect(HighlightSegmentHasTag(segments[0], HighlightTagKeyMoment)).To(BeTrue())
	})

	It("splits a chain longer than twenty seconds at its largest observation gap", func() {
		segments := normalize(`[
			{"start":"00:00","end":"00:04","type":"best_form","movement":"Snatch","reason":"one"},
			{"start":"00:05","end":"00:09","type":"best_form","movement":"Snatch","reason":"two"},
			{"start":"00:10.5","end":"00:14.5","type":"worst_form","movement":"Snatch","reason":"three"},
			{"start":"00:15","end":"00:19","type":"worst_form","movement":"Snatch","reason":"four"},
			{"start":"00:20","end":"00:24","type":"worst_form","movement":"Snatch","reason":"five"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 60})

		Expect(segments).To(HaveLen(2))
		firstEnd, err := parseTimestampToSeconds(segments[0].End)
		Expect(err).NotTo(HaveOccurred())
		secondStart, err := parseTimestampToSeconds(segments[1].Start)
		Expect(err).NotTo(HaveOccurred())
		Expect(firstEnd).To(Equal(9.0))
		Expect(secondStart).To(Equal(10.5))
		for _, segment := range segments {
			start, _ := parseTimestampToSeconds(segment.Start)
			end, _ := parseTimestampToSeconds(segment.End)
			Expect(end - start).To(BeNumerically("<=", 20))
		}
	})

	It("does not merge across an intervening source-segment boundary", func() {
		first := candidatesFromLegacySegment(HighlightSegment{
			Start: "00:10", End: "00:12", Type: "best_form", Movement: "Thruster",
		}, highlightSource{Index: 0, Start: 0, End: 12, Movement: "Thruster", HasBounds: true, HardGapBoundary: true})[0]
		second := candidatesFromLegacySegment(HighlightSegment{
			Start: "00:12.5", End: "00:14", Type: "worst_form", Movement: "Thruster",
		}, highlightSource{Index: 1, Start: 12.5, End: 20, Movement: "Thruster", HasBounds: true, HardGapBoundary: true})[0]

		groups := groupHighlightCandidates([]highlightCandidate{first, second}, HighlightNormalizeOptions{}.withDefaults())
		Expect(groups).To(HaveLen(2))
	})

	It("limits each movement to one positive, one improvement, and one technique card", func() {
		segments := normalize(`[
			{"start":"00:00","end":"00:05","type":"best_form","movement":"Deadlift","reason":"best"},
			{"start":"00:10","end":"00:15","type":"worst_form","movement":"Deadlift","reason":"issue"},
			{"start":"00:20","end":"00:25","type":"fatigue_point","movement":"Deadlift","reason":"fatigue"},
			{"start":"00:30","end":"00:35","type":"key_moment","movement":"Deadlift","reason":"transition"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 60})

		Expect(segments).To(HaveLen(3))
		Expect(segments).To(ConsistOf(
			HaveField("Type", "best_form"),
			HaveField("Type", Or(Equal("worst_form"), Equal("fatigue_point"))),
			HaveField("Type", "key_moment"),
		))
	})

	It("is idempotent for normalized v2 events and preserves confidence", func() {
		confidence := 0.92
		initial := []HighlightSegment{{
			Version: 2, Start: "00:08", End: "00:13", Type: "best_form", Movement: "Pull-up",
			Tags: []string{HighlightTagKeyMoment, "unsupported"},
			Observations: []HighlightObservation{
				{Start: "00:09", End: "00:11", Type: HighlightObservationPositiveForm, Reason: "good", Confidence: &confidence},
				{Start: "00:10", End: "00:10.5", Type: HighlightObservationTechnique, Reason: "transition", Confidence: &confidence},
			},
		}}
		firstJSON := MarshalHighlightSegments(initial)
		first := normalize(firstJSON, HighlightNormalizeOptions{VideoEndSeconds: 60})
		second := normalize(MarshalHighlightSegments(first), HighlightNormalizeOptions{VideoEndSeconds: 60})

		firstBytes, err := json.Marshal(first)
		Expect(err).NotTo(HaveOccurred())
		secondBytes, err := json.Marshal(second)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondBytes).To(MatchJSON(firstBytes))
		Expect(*second[0].Observations[0].Confidence).To(Equal(confidence))
		Expect(second[0].Tags).To(Equal([]string{HighlightTagKeyMoment}))
	})

	It("drops v2 evidence for non-exercise movement labels", func() {
		segments := normalize(`[
			{"version":2,"start":"0:00","end":"0:05","type":"key_moment","movement":"Unknown","observations":[{"start":"0:01","end":"0:02","type":"technique_event","reason":"unclear"}]},
			{"version":2,"start":"0:05","end":"0:10","type":"best_form","movement":"Rest","observations":[{"start":"0:06","end":"0:07","type":"positive_form","reason":"not exercise"}]}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 20})

		Expect(segments).To(BeEmpty())
	})

	It("drops invalid, reversed, and out-of-range evidence", func() {
		segments := normalize(`[
			{"start":"00:05","end":"00:06","type":"best_form","movement":"Row","reason":"valid"},
			{"start":"00:08","end":"00:07","type":"worst_form","movement":"Row","reason":"reversed"},
			{"start":"00:10","end":"00:11","type":"celebration","movement":"Row","reason":"invalid type"},
			{"start":"00:19","end":"00:21","type":"best_form","movement":"Row","reason":"outside video"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 20})

		Expect(segments).To(HaveLen(1))
		Expect(segments[0].Observations).To(HaveLen(1))
		Expect(segments[0].Observations[0].Reason).To(Equal("valid"))
	})

	It("rejects heart-rate-only fatigue_onset evidence from the new prompt contract", func() {
		segments := normalize(`[
			{"start":"0:05","end":"0:07","type":"fatigue_onset","movement":"Burpee","reason":"heart rate reached 180 bpm"}
		]`, HighlightNormalizeOptions{VideoEndSeconds: 20})

		Expect(segments).To(BeEmpty())
	})

	It("uses the available source length when a bounded segment is shorter than five seconds", func() {
		input := "```highlights\n" +
			`[{"start":"01:40.5","end":"01:41.5","type":"best_form","movement":"Press","reason":"stable"}]` +
			"\n```"
		candidates := parseHighlightCandidates(input, highlightSource{
			Index: 0, Start: 100, End: 103, Movement: "Press", HasBounds: true,
		})
		segments := consolidateHighlightCandidates(candidates, HighlightNormalizeOptions{VideoEndSeconds: 200})

		Expect(segments).To(HaveLen(1))
		start, err := parseTimestampToSeconds(segments[0].Start)
		Expect(err).NotTo(HaveOccurred())
		end, err := parseTimestampToSeconds(segments[0].End)
		Expect(err).NotTo(HaveOccurred())
		Expect(start).To(Equal(100.0))
		Expect(end).To(Equal(103.0))
	})

	It("does not pad beyond known legacy evidence when video end is unavailable", func() {
		segments := normalize(`[
			{"start":"0:59","end":"1:00","type":"best_form","movement":"Press","reason":"last rep"}
		]`, HighlightNormalizeOptions{ConservativeUnknownVideoEnd: true})

		Expect(segments).To(HaveLen(1))
		start, err := parseTimestampToSeconds(segments[0].Start)
		Expect(err).NotTo(HaveOccurred())
		end, err := parseTimestampToSeconds(segments[0].End)
		Expect(err).NotTo(HaveOccurred())
		Expect(start).To(Equal(55.0))
		Expect(end).To(Equal(60.0))
	})
})
