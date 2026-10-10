package subtitle_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/subtitle"
)

const sampleFinalAnalysis = `

---
## 세그먼트 1: Snatch (0:00 ~ 0:30)

### 1. 동작 분석 및 체형 평가 (Movement & Posture Analysis)

전반적으로 스내치 동작의 기본 자세는 양호합니다.

### 2. 강점 및 약점 (Strengths & Weaknesses)

**강점:**
- Core 안정성이 잘 유지되고 있으며 상체가 흔들리지 않습니다
- 바벨 궤도가 일정하게 유지됩니다

**약점:**
- 첫 번째 풀에서 팔꿈치가 조기에 굽혀집니다
- 오버헤드 위치에서 왼쪽 어깨가 약간 앞으로 기울어집니다

### 3. 피로도 및 페이스 분석 (Fatigue Analysis)
- 0:20 시점부터 반복 속도가 눈에 띄게 감소합니다
- 호흡이 불안정해지면서 자세가 흐트러지기 시작합니다

### 4. 개선 솔루션 (Actionable Feedback)
- 스내치 풀 시 팔꿈치를 최대한 늦게 굽히세요
- 오버헤드 위치에서 양쪽 어깨 균형을 의식하세요
- 호흡 패턴을 일정하게 유지하는 연습을 하세요

` + "```highlights\n" + `[{"start":"0:10","end":"0:20","type":"best_form","movement":"Snatch","reason":"완벽한 풀 익스텐션"}]` + "\n```\n" + `

---
## 세그먼트 2: Pull-up (0:30 ~ 1:00)

### 1. 동작 분석

풀업 전반적으로 좋은 동작 범위를 보여줍니다.

### 2. 강점 및 약점

**강점:**
- 데드행 위치에서 완전한 팔 신전을 유지합니다
- 킵핑 리듬이 일정합니다

**약점:**
- 턱이 바에 도달하지 못하는 반복이 2회 관찰됩니다

### 4. 개선 솔루션
- 턱이 확실히 바 위로 올라가도록 의식하세요
- 그립 위치를 어깨 너비보다 약간 넓게 조정해보세요
`

func pf(v float64) *float64 { return &v }

var _ = Describe("Final Analysis Subtitle Generation", func() {
	Context("FormatFinalAnalysisSRT", func() {
		It("formats SRT with multiple entries, strength/improvement markers and timecodes", func() {
			srt := subtitle.FormatFinalAnalysisSRT(sampleFinalAnalysis)
			Expect(srt).NotTo(BeEmpty())

			entries := strings.Split(strings.TrimSpace(srt), "\n\n")
			Expect(len(entries)).To(BeNumerically(">=", 4))

			Expect(srt).To(ContainSubstring("[강점]"))
			Expect(srt).To(ContainSubstring("[개선]"))
			Expect(srt).To(ContainSubstring("-->"))
			Expect(srt).To(ContainSubstring("00:00:00"))
			Expect(srt).To(ContainSubstring("00:00:30"))
		})

		It("returns empty SRT for empty input", func() {
			srt := subtitle.FormatFinalAnalysisSRT("")
			Expect(srt).To(BeEmpty())
		})

		It("returns empty SRT when input contains no segments", func() {
			srt := subtitle.FormatFinalAnalysisSRT("Just some random text without segment headers")
			Expect(srt).To(BeEmpty())
		})

		It("handles fractional segment headers accurately", func() {
			input := `
## 세그먼트 1: Snatch (6:20.07 ~ 6:50.5)

### 2. 강점 및 약점
**강점:**
- Core 안정성이 잘 유지됩니다

**약점:**
- 첫 번째 풀에서 팔꿈치가 일찍 굽혀집니다
`
			srt := subtitle.FormatFinalAnalysisSRT(input)
			Expect(srt).NotTo(BeEmpty())
			Expect(srt).To(ContainSubstring("00:06:20,070"))
			Expect(srt).To(ContainSubstring("00:06:50,500"))
			Expect(srt).To(ContainSubstring("[강점] Core 안정성이 잘 유지됩니다"))
			Expect(srt).To(ContainSubstring("[개선] 첫 번째 풀에서 팔꿈치가 일찍 굽혀집니다"))
		})

		It("prevents code blocks from leaking into output", func() {
			srt := subtitle.FormatFinalAnalysisSRT(sampleFinalAnalysis)
			Expect(srt).NotTo(ContainSubstring("```"))
			Expect(srt).NotTo(ContainSubstring(`"start"`))
		})

		It("keeps subtitle line length within reasonable limits", func() {
			srt := subtitle.FormatFinalAnalysisSRT(sampleFinalAnalysis)
			for _, line := range strings.Split(srt, "\n") {
				if strings.Contains(line, "-->") || line == "" {
					continue
				}
				isNum := true
				for _, r := range line {
					if r < '0' || r > '9' {
						isNum = false
						break
					}
				}
				if isNum {
					continue
				}
				runes := []rune(line)
				Expect(len(runes)).To(BeNumerically("<=", 100), "Subtitle line too long: %s", line)
			}
		})
	})

	Context("FormatMixedSRT", func() {
		It("fills gaps between final analysis segments with chunks and excludes overlaps", func() {
			chunks := []db.ChunkAnalysisResult{
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Chunk cue in gap",
					StartSecs: pf(60.0),
					EndSecs:   pf(70.0),
				},
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Chunk cue overlapping segment 1",
					StartSecs: pf(5.0),
					EndSecs:   pf(15.0),
				},
			}

			srt := subtitle.FormatMixedSRT(sampleFinalAnalysis, chunks)
			Expect(srt).NotTo(BeEmpty())
			Expect(srt).To(ContainSubstring("Chunk cue in gap"))
			Expect(srt).NotTo(ContainSubstring("Chunk cue overlapping segment 1"))
			Expect(srt).To(ContainSubstring("[강점]"))
			Expect(srt).To(ContainSubstring("[개선]"))
		})

		It("falls back to chunk-only when final analysis is empty", func() {
			chunks := []db.ChunkAnalysisResult{
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Good form on pull-up",
					StartSecs: pf(0.0),
					EndSecs:   pf(10.0),
				},
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Keep elbows tight",
					StartSecs: pf(10.0),
					EndSecs:   pf(20.0),
				},
			}

			srt := subtitle.FormatMixedSRT("", chunks)
			Expect(srt).NotTo(BeEmpty())
			Expect(srt).To(ContainSubstring("Good form on pull-up"))
			Expect(srt).To(ContainSubstring("Keep elbows tight"))
		})

		It("formats final-only when chunks are empty", func() {
			srt := subtitle.FormatMixedSRT(sampleFinalAnalysis, nil)
			Expect(srt).NotTo(BeEmpty())
			Expect(srt).To(ContainSubstring("[강점]"))
		})

		It("returns empty SRT when both sources are empty", func() {
			srt := subtitle.FormatMixedSRT("", nil)
			Expect(srt).To(BeEmpty())
		})

		It("splits long chunk output into multiple subtitle entries", func() {
			longOutput := "수직 상승 마인드 머슬 커넥션: 하단에서 올라올 때 엉덩이가 먼저 뒤로 빠지지 않고(Good morning squat 형태의 오류가 없음), 가슴과 엉덩이가 동시에 수직으로 상승하는 리프팅 궤적이 매우 좋습니다. 코어 안정성도 잘 유지되고 있습니다."

			chunks := []db.ChunkAnalysisResult{
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    longOutput,
					StartSecs: pf(0.0),
					EndSecs:   pf(10.0),
				},
			}

			srt := subtitle.FormatMixedSRT("", chunks)
			Expect(srt).NotTo(BeEmpty())
			entries := strings.Split(strings.TrimSpace(srt), "\n\n")
			Expect(len(entries)).To(BeNumerically(">=", 2))
		})

		It("extends short subtitle to at least 2 seconds if there is a gap", func() {
			chunks := []db.ChunkAnalysisResult{
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Quick fix cue",
					StartSecs: pf(0.0),
					EndSecs:   pf(0.5),
				},
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Next cue after gap",
					StartSecs: pf(5.0),
					EndSecs:   pf(15.0),
				},
			}

			srt := subtitle.FormatMixedSRT("", chunks)
			Expect(srt).NotTo(BeEmpty())
			Expect(srt).To(ContainSubstring("00:00:02,000"))
		})

		It("caps short subtitle extension to avoid overlapping next entry", func() {
			chunks := []db.ChunkAnalysisResult{
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Short cue",
					StartSecs: pf(0.0),
					EndSecs:   pf(0.5),
				},
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Following cue",
					StartSecs: pf(1.0),
					EndSecs:   pf(10.0),
				},
			}

			srt := subtitle.FormatMixedSRT("", chunks)
			Expect(srt).NotTo(BeEmpty())
			Expect(srt).To(ContainSubstring("00:00:01,000"))
			Expect(srt).NotTo(ContainSubstring("00:00:02,000"))
		})

		It("extends short subtitle freely when it is the last entry", func() {
			chunks := []db.ChunkAnalysisResult{
				{
					SessionID: "test",
					Status:    "COMPLETED",
					Output:    "Only cue",
					StartSecs: pf(10.0),
					EndSecs:   pf(10.3),
				},
			}

			srt := subtitle.FormatMixedSRT("", chunks)
			Expect(srt).NotTo(BeEmpty())
			Expect(srt).To(ContainSubstring("00:00:12,000"))
		})
	})
})
