package worker

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/workoutprogress"
)

var _ = Describe("Pass 2 missed-set recovery and long-bout natural splitting", func() {
	fPtr := func(v float64) *float64 { return &v }

	makeChunkObs := func(duration float64, targetState, activityState string, events []activity.Observation) db.NullableJSONDocument {
		raw, err := json.Marshal(activity.Observations{
			Version:       activity.Version,
			DurationSecs:  duration,
			TargetState:   targetState,
			ActivityState: activityState,
			Events:        events,
			Unassessed:    []activity.Interval{},
		})
		Expect(err).NotTo(HaveOccurred())
		return raw
	}

	Describe("WOD-20261106-01M6Q6TEDCR6VJRGJMEHW0YYZ2 30:05~30:16 Wallball Shot regression", func() {
		It("splits Pass 2's 35.0s false rest interval around verified Pass 1 Wallball Shot reps and stitches adjacent chunks", func() {
			// Pass 2 missed the occluded left-pillar Wallball Shot set (1805.93..1815.30s)
			// and merged [pre-rest + missed set + post-rest] into a single 35.0s rest interval.
			pass2Intervals := []workoutprogress.Interval{
				{
					ID:              "segment-10-set-1",
					Start:           1760.00,
					End:             1793.60,
					Activity:        "exercise",
					Movement:        "Wallball Shot",
					ObservationRefs: []string{"segment-10:interval-0"},
					RepEvents: []activity.Observation{
						{Movement: "Wallball Shot", Unit: "reps", Start: 1788.0, End: 1791.5, Complete: true, Evidence: "squat and throw to target"},
					},
				},
				{
					ID:              "segment-10-false-rest",
					Start:           1793.60,
					End:             1828.60,
					Activity:        "rest",
					ObservationRefs: []string{"segment-10:interval-1"},
				},
				{
					ID:              "segment-10-set-3",
					Start:           1828.60,
					End:             1855.00,
					Activity:        "exercise",
					Movement:        "Wallball Shot",
					ObservationRefs: []string{"segment-10:interval-2"},
					RepEvents: []activity.Observation{
						{Movement: "Wallball Shot", Unit: "reps", Start: 1830.0, End: 1833.2, Complete: true, Evidence: "squat and throw to target"},
					},
				},
			}

			chunks := []db.ChunkAnalysisResult{
				{
					ID:               37737,
					SessionID:        "WOD-20261106-01M6Q6TEDCR6VJRGJMEHW0YYZ2",
					ProfileID:        10,
					Status:           "COMPLETED",
					FilePath:         "gs://bucket/videos/10/WOD-20261106-01M6Q6TEDCR6VJRGJMEHW0YYZ2/chunk_37737.mp4",
					ExerciseType:     "Wallball Shot",
					MediaStartSecs:   fPtr(1802.63),
					MediaEndSecs:     fPtr(1812.53),
					TargetConfidence: 0.95,
					TargetCues:       `{"matched_cues":["black shirt"],"ambiguous":false,"confidence":0.95,"frame_region":"left","station":"wall"}`,
					MovementObservations: makeChunkObs(9.90, "identified", "exercise", []activity.Observation{
						{Movement: "Wallball Shot", Unit: "reps", Start: 3.30, End: 5.30, Complete: true, Evidence: "wallball rep 1"},
						{Movement: "Wallball Shot", Unit: "reps", Start: 5.40, End: 7.40, Complete: true, Evidence: "wallball rep 2"},
						{Movement: "Wallball Shot", Unit: "reps", Start: 7.50, End: 9.55, Complete: true, Evidence: "wallball rep 3"},
					}),
				},
				{
					ID:               37738,
					SessionID:        "WOD-20261106-01M6Q6TEDCR6VJRGJMEHW0YYZ2",
					ProfileID:        10,
					Status:           "COMPLETED",
					FilePath:         "gs://bucket/videos/10/WOD-20261106-01M6Q6TEDCR6VJRGJMEHW0YYZ2/chunk_37738.mp4",
					ExerciseType:     "Wallball Shot",
					MediaStartSecs:   fPtr(1812.53),
					MediaEndSecs:     fPtr(1822.43),
					TargetConfidence: 0.95,
					TargetCues:       `{"matched_cues":["black shirt"],"ambiguous":false,"confidence":0.95,"frame_region":"left","station":"wall"}`,
					MovementObservations: makeChunkObs(9.90, "identified", "exercise", []activity.Observation{
						{Movement: "Wallball Shot", Unit: "reps", Start: 0.00, End: 2.77, Complete: true, Evidence: "wallball rep 4"},
					}),
				},
			}

			recovered, gaps := mergeChunkFallbackObservationsWithOptions(pass2Intervals, nil, chunks, WorkoutTypeWOD, true)
			Expect(gaps).To(BeEmpty())

			normalized, normGaps := workoutprogress.NormalizeContinuity(recovered, gaps, nil)
			Expect(normGaps).To(BeEmpty())
			Expect(normalized).To(HaveLen(5))

			// [0] Set 1
			Expect(normalized[0].Activity).To(Equal("exercise"))
			Expect(normalized[0].Movement).To(Equal("Wallball Shot"))

			// [1] Pre-rest: 1793.60 -> 1805.93 (~12.33s)
			Expect(normalized[1].Activity).To(Equal("rest"))
			Expect(normalized[1].Start).To(BeNumerically("~", 1793.60, 1e-6))
			Expect(normalized[1].End).To(BeNumerically("~", 1805.93, 1e-6))

			// [2] Recovered Wallball Shot set: 1805.93 -> 1815.30 (4 reps stitched across chunks 37737 + 37738)
			Expect(normalized[2].Activity).To(Equal("exercise"))
			Expect(normalized[2].Movement).To(Equal("Wallball Shot"))
			Expect(normalized[2].Start).To(BeNumerically("~", 1805.93, 1e-6))
			Expect(normalized[2].End).To(BeNumerically("~", 1815.30, 1e-6))
			Expect(normalized[2].RepEvents).To(HaveLen(4))

			// [3] Post-rest: 1815.30 -> 1828.60 (~13.30s)
			Expect(normalized[3].Activity).To(Equal("rest"))
			Expect(normalized[3].Start).To(BeNumerically("~", 1815.30, 1e-6))
			Expect(normalized[3].End).To(BeNumerically("~", 1828.60, 1e-6))

			// [4] Set 3
			Expect(normalized[4].Activity).To(Equal("exercise"))
			Expect(normalized[4].Movement).To(Equal("Wallball Shot"))

			// Rollback flag verification: disabling PASS2_REST_FALLBACK_SPLIT_ENABLED preserves legacy behavior.
			unsplit, _ := mergeChunkFallbackObservationsWithOptions(pass2Intervals, nil, chunks, WorkoutTypeWOD, false)
			Expect(unsplit).To(HaveLen(3))
			Expect(unsplit[1].Activity).To(Equal("rest"))
			Expect(unsplit[1].Start).To(Equal(1793.60))
			Expect(unsplit[1].End).To(Equal(1828.60))
		})

		It("enforces false-positive guardrails (ambiguous target, low confidence, isolated 1-rep mismatch) while allowing 1+1 cross-chunk sets", func() {
			baseIntervals := []workoutprogress.Interval{
				{ID: "prev", Start: 0, End: 20, Activity: "exercise", Movement: "Box Step-up", RepEvents: []activity.Observation{{Movement: "Box Step-up", Unit: "reps", Start: 15, End: 19, Complete: true, Evidence: "step"}}},
				{ID: "rest", Start: 20, End: 50, Activity: "rest"},
				{ID: "next", Start: 50, End: 80, Activity: "exercise", Movement: "Wallball Shot", RepEvents: []activity.Observation{{Movement: "Wallball Shot", Unit: "reps", Start: 52, End: 55, Complete: true, Evidence: "wb"}}},
			}

			// 1. Ambiguous target must NOT split rest.
			ambiguousChunk := []db.ChunkAnalysisResult{{
				ID: 1, Status: "COMPLETED", ExerciseType: "Wallball Shot", MediaStartSecs: fPtr(30), MediaEndSecs: fPtr(40),
				TargetConfidence: 0.92,
				TargetCues:       `{"ambiguous":true,"confidence":0.92}`,
				MovementObservations: makeChunkObs(10, "identified", "exercise", []activity.Observation{
					{Movement: "Wallball Shot", Unit: "reps", Start: 1, End: 4, Complete: true, Evidence: "r1"},
					{Movement: "Wallball Shot", Unit: "reps", Start: 5, End: 8, Complete: true, Evidence: "r2"},
				}),
			}}
			got, _ := mergeChunkFallbackObservationsWithOptions(baseIntervals, nil, ambiguousChunk, WorkoutTypeWOD, true)
			Expect(got).To(HaveLen(3), "ambiguous chunk must not split rest")

			// 2. Low target confidence (< 0.7) must NOT split rest.
			lowConfChunk := []db.ChunkAnalysisResult{{
				ID: 2, Status: "COMPLETED", ExerciseType: "Wallball Shot", MediaStartSecs: fPtr(30), MediaEndSecs: fPtr(40),
				TargetConfidence: 0.55,
				TargetCues:       `{"ambiguous":false,"confidence":0.55}`,
				MovementObservations: makeChunkObs(10, "identified", "exercise", []activity.Observation{
					{Movement: "Wallball Shot", Unit: "reps", Start: 1, End: 4, Complete: true, Evidence: "r1"},
					{Movement: "Wallball Shot", Unit: "reps", Start: 5, End: 8, Complete: true, Evidence: "r2"},
				}),
			}}
			got, _ = mergeChunkFallbackObservationsWithOptions(baseIntervals, nil, lowConfChunk, WorkoutTypeWOD, true)
			Expect(got).To(HaveLen(3), "low confidence chunk must not split rest")

			// 3. Isolated 1-rep chunk whose movement mismatches both neighbors must NOT split rest.
			isolatedMismatch := []db.ChunkAnalysisResult{{
				ID: 3, Status: "COMPLETED", ExerciseType: "Burpee", MediaStartSecs: fPtr(30), MediaEndSecs: fPtr(40),
				TargetConfidence: 0.90,
				TargetCues:       `{"ambiguous":false,"confidence":0.90}`,
				MovementObservations: makeChunkObs(10, "identified", "exercise", []activity.Observation{
					{Movement: "Burpee", Unit: "reps", Start: 3, End: 7, Complete: true, Evidence: "single burpee"},
				}),
			}}
			got, _ = mergeChunkFallbackObservationsWithOptions(baseIntervals, nil, isolatedMismatch, WorkoutTypeWOD, true)
			Expect(got).To(HaveLen(3), "isolated 1-rep unrelated movement must not split rest")

			// 4. Two adjacent chunks with 1 + 1 verified reps of the same movement (>= 2 total) MUST split rest and bridge.
			crossChunkPair := []db.ChunkAnalysisResult{
				{
					ID: 4, Status: "COMPLETED", ExerciseType: "Burpee", MediaStartSecs: fPtr(25), MediaEndSecs: fPtr(35),
					TargetConfidence: 0.92,
					TargetCues:       `{"ambiguous":false,"confidence":0.92}`,
					MovementObservations: makeChunkObs(10, "identified", "exercise", []activity.Observation{
						{Movement: "Burpee", Unit: "reps", Start: 7.0, End: 9.8, Complete: true, Evidence: "burpee 1"},
					}),
				},
				{
					ID: 5, Status: "COMPLETED", ExerciseType: "Burpee", MediaStartSecs: fPtr(35), MediaEndSecs: fPtr(45),
					TargetConfidence: 0.92,
					TargetCues:       `{"ambiguous":false,"confidence":0.92}`,
					MovementObservations: makeChunkObs(10, "identified", "exercise", []activity.Observation{
						{Movement: "Burpee", Unit: "reps", Start: 0.5, End: 3.5, Complete: true, Evidence: "burpee 2"},
					}),
				},
			}
			got, _ = mergeChunkFallbackObservationsWithOptions(baseIntervals, nil, crossChunkPair, WorkoutTypeWOD, true)
			normalized, _ := workoutprogress.NormalizeContinuity(got, nil, nil)
			Expect(normalized).To(HaveLen(5))
			Expect(normalized[2].Activity).To(Equal("exercise"))
			Expect(normalized[2].Movement).To(Equal("Burpee"))
			Expect(normalized[2].RepEvents).To(HaveLen(2))
		})
	})

	Describe("Mid-segment station/region/movement transition tracking references", func() {
		It("preserves 1~2 references on single-station segments and attaches up to 4 on mid-segment station transitions while skipping ambiguous refs", func() {
			refs := []trackingReferenceRef{
				{Object: "videos/10/s/tracking/100.jpg", Start: 0, End: 10, Active: true, NonAmbiguous: true, FrameRegion: "center", Station: "barbell", MovementKey: "thruster"},
				{Object: "videos/10/s/tracking/200.jpg", Start: 1650, End: 1660, Active: true, NonAmbiguous: true, FrameRegion: "center", Station: "box", MovementKey: "box step up"},
				{Object: "videos/10/s/tracking/201.jpg", Start: 1660, End: 1670, Active: true, NonAmbiguous: true, FrameRegion: "center", Station: "box", MovementKey: "box step up"},
				// Ambiguous transition candidate must be ignored:
				{Object: "videos/10/s/tracking/205.jpg", Start: 1795, End: 1805, Active: true, NonAmbiguous: false, FrameRegion: "left", Station: "wall", MovementKey: "wallball shot"},
				// Verified left-pillar Wallball station transition:
				{Object: "videos/10/s/tracking/206.jpg", Start: 1805, End: 1815, Active: true, NonAmbiguous: true, FrameRegion: "left", Station: "wall", MovementKey: "wallball shot"},
				// Subsequent same-station chunk must not duplicate:
				{Object: "videos/10/s/tracking/207.jpg", Start: 1825, End: 1835, Active: true, NonAmbiguous: true, FrameRegion: "left", Station: "wall", MovementKey: "wallball shot"},
				// Second transition inside same segment:
				{Object: "videos/10/s/tracking/210.jpg", Start: 1860, End: 1870, Active: true, NonAmbiguous: true, FrameRegion: "right", Station: "rower", MovementKey: "row"},
				// Third transition beyond max 4 total must be capped:
				{Object: "videos/10/s/tracking/215.jpg", Start: 1880, End: 1890, Active: true, NonAmbiguous: true, FrameRegion: "center", Station: "rig", MovementKey: "pull up"},
			}

			// Single-station window [1650, 1675]: only segment-start + session-initial (2 refs).
			singleStation := selectPass2TrackingObjects(refs, 1650, 1675)
			Expect(singleStation).To(Equal([]string{
				"videos/10/s/tracking/200.jpg",
				"videos/10/s/tracking/100.jpg",
			}))

			// Multi-station window [1650, 1895]: segment-start + up to 2 distinct verified transitions + session-initial (capped at 4 total), skipping ambiguous 205.
			multiStation := selectPass2TrackingObjects(refs, 1650, 1895)
			Expect(multiStation).To(Equal([]string{
				"videos/10/s/tracking/200.jpg",
				"videos/10/s/tracking/206.jpg",
				"videos/10/s/tracking/210.jpg",
				"videos/10/s/tracking/100.jpg",
			}))
		})
	})

	Describe("Natural chunk-boundary splitting for > 150s continuous bouts", func() {
		It("splits a 247.7s multi-movement bout at the movement/bridged-rest boundary instead of mid-movement", func() {
			// 1649.07s ~ 1896.77s (247.70s): Box Step-up (1649.07..1759.07) -> bridged rest (1759.07..1769.07) -> Wallball Shot (1769.07..1896.77)
			var timeline []timelineChunk
			for s := 1649.07; s < 1759.07-0.01; s += 10.0 {
				end := s + 10.0
				if end > 1759.07 {
					end = 1759.07
				}
				timeline = append(timeline, timelineChunk{Start: s, End: end, Class: chunkActive, Movement: "Box Step-up", Candidates: []string{"Box Step-up"}})
			}
			timeline = append(timeline, timelineChunk{Start: 1759.07, End: 1769.07, Class: chunkActive, BridgedRest: true})
			for s := 1769.07; s < 1896.77-0.01; s += 10.0 {
				end := s + 10.0
				if end > 1896.77 {
					end = 1896.77
				}
				timeline = append(timeline, timelineChunk{Start: s, End: end, Class: chunkActive, Movement: "Wallball Shot", Candidates: []string{"Wallball Shot"}})
			}

			segments := []Segment{{
				Start:       "27:29.07",
				End:         "31:36.77",
				Type:        "Box Step-up",
				Description: candidateMovementsPrefix + "Box Step-up, Wallball Shot",
			}}

			windows := splitPass2WindowsWithNaturalBoundaries(segments, nil, nil, 0, timeline)
			Expect(windows).To(HaveLen(2))

			// Instead of mechanical 1772.92s midpoint, split aligns to the Box Step-up -> bridged rest / Wallball Shot boundary!
			Expect(windows[0].CoreStart).To(BeNumerically("~", 1649.07, 1e-2))
			Expect(windows[0].CoreEnd).To(BeNumerically("~", 1769.07, 1e-2))
			Expect(windows[0].CoreEnd - windows[0].CoreStart).To(BeNumerically("<=", maxPass2WindowSecs))
			Expect(windows[0].Seg.Type).To(Equal("Box Step-up"))
			prompt0 := (&Worker{}).buildSegmentAnalysisPrompt(VideoAnalysisPayload{}, windows[0].Seg, "", "", false)
			Expect(prompt0).To(ContainSubstring("이 구간의 이전 청크 라벨은 'Box Step-up'입니다"))
			Expect(prompt0).NotTo(ContainSubstring("'Wallball Shot'"))

			Expect(windows[1].CoreStart).To(BeNumerically("~", windows[0].CoreEnd, 1e-6))
			Expect(windows[1].CoreEnd).To(BeNumerically("~", 1896.77, 1e-2))
			Expect(windows[1].CoreEnd - windows[1].CoreStart).To(BeNumerically("<=", maxPass2WindowSecs))
			Expect(windows[1].Seg.Type).To(Equal("Wallball Shot"))
			prompt1 := (&Worker{}).buildSegmentAnalysisPrompt(VideoAnalysisPayload{}, windows[1].Seg, "", "", false)
			Expect(prompt1).To(ContainSubstring("이 구간의 이전 청크 라벨은 'Wallball Shot'입니다"))
			Expect(prompt1).NotTo(ContainSubstring("'Box Step-up'"))
		})
	})
})
