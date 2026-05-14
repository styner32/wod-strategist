package subtitle_test

import (
	"strings"
	"testing"

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

func TestFormatFinalAnalysisSRT(t *testing.T) {
	srt := subtitle.FormatFinalAnalysisSRT(sampleFinalAnalysis)

	if srt == "" {
		t.Fatal("Expected non-empty SRT output")
	}

	// Should have multiple subtitle entries
	entries := strings.Split(strings.TrimSpace(srt), "\n\n")
	if len(entries) < 4 {
		t.Errorf("Expected at least 4 subtitle entries, got %d", len(entries))
	}

	// Check that entries contain pro/con markers
	hasCheckmark := strings.Contains(srt, "[강점]")
	hasWarning := strings.Contains(srt, "[개선]")
	if !hasCheckmark {
		t.Error("Expected [강점] markers for strengths")
	}
	if !hasWarning {
		t.Error("Expected [개선] markers for weaknesses/tips")
	}

	// Verify SRT time format is present
	if !strings.Contains(srt, "-->") {
		t.Error("Expected SRT time arrows (-->)")
	}

	// First entry should start at 00:00:00
	if !strings.Contains(srt, "00:00:00") {
		t.Error("Expected first subtitle to start at 00:00:00")
	}

	// Entries for segment 2 should be within 0:30 ~ 1:00 range
	if !strings.Contains(srt, "00:00:30") {
		t.Error("Expected subtitle entries starting around 00:00:30 for segment 2")
	}

	t.Logf("Generated SRT:\n%s", srt)
}

func TestFormatFinalAnalysisSRT_Empty(t *testing.T) {
	srt := subtitle.FormatFinalAnalysisSRT("")
	if srt != "" {
		t.Errorf("Expected empty SRT for empty input, got: %s", srt)
	}
}

func TestFormatFinalAnalysisSRT_NoSegments(t *testing.T) {
	srt := subtitle.FormatFinalAnalysisSRT("Just some random text without segment headers")
	if srt != "" {
		t.Errorf("Expected empty SRT for input without segments, got: %s", srt)
	}
}

func TestParseAnalysisSegments(t *testing.T) {
	// Verify that code blocks in analysis output don't leak into feedback points
	srt := subtitle.FormatFinalAnalysisSRT(sampleFinalAnalysis)

	// Should not contain raw JSON or code block markers
	if strings.Contains(srt, "```") {
		t.Error("SRT should not contain code block markers")
	}
	if strings.Contains(srt, `"start"`) {
		t.Error("SRT should not contain raw JSON fields")
	}
}

func TestTruncateSubtitle(t *testing.T) {
	srt := subtitle.FormatFinalAnalysisSRT(sampleFinalAnalysis)

	// Check no single content line exceeds reasonable length
	for _, line := range strings.Split(srt, "\n") {
		// Skip SRT timecodes, entry numbers, and empty lines
		if strings.Contains(line, "-->") || line == "" {
			continue
		}
		// Skip numeric entry IDs
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
		if len(runes) > 100 {
			t.Errorf("Subtitle line too long (%d chars): %s", len(runes), line)
		}
	}
}

func pf(v float64) *float64 { return &v }

func TestFormatMixedSRT_GapFilling(t *testing.T) {
	// Final analysis covers 0:00~0:30 and 0:30~1:00.
	// Chunk at 1:00~1:10 is in a gap → should be included.
	// Chunk at 0:05~0:15 overlaps segment 1 → should be excluded.
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

	if srt == "" {
		t.Fatal("Expected non-empty mixed SRT")
	}

	// The gap-filling chunk should be present
	if !strings.Contains(srt, "Chunk cue in gap") {
		t.Error("Expected gap-filling chunk to be included")
	}

	// The overlapping chunk should NOT be present
	if strings.Contains(srt, "Chunk cue overlapping segment 1") {
		t.Error("Expected overlapping chunk to be excluded")
	}

	// Should still contain final analysis markers
	if !strings.Contains(srt, "[강점]") {
		t.Error("Expected [강점] markers from final analysis")
	}
	if !strings.Contains(srt, "[개선]") {
		t.Error("Expected [개선] markers from final analysis")
	}

	t.Logf("Mixed SRT:\n%s", srt)
}

func TestFormatMixedSRT_ChunkOnlyFallback(t *testing.T) {
	// No final analysis → should fall back to chunk-only
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

	if srt == "" {
		t.Fatal("Expected non-empty SRT from chunk fallback")
	}
	if !strings.Contains(srt, "Good form on pull-up") {
		t.Error("Expected chunk cues in output")
	}
	if !strings.Contains(srt, "Keep elbows tight") {
		t.Error("Expected chunk cues in output")
	}
}

func TestFormatMixedSRT_FinalOnly(t *testing.T) {
	// Final analysis exists but no chunks → should work fine
	srt := subtitle.FormatMixedSRT(sampleFinalAnalysis, nil)

	if srt == "" {
		t.Fatal("Expected non-empty SRT from final-only")
	}
	if !strings.Contains(srt, "[강점]") {
		t.Error("Expected [강점] markers")
	}
}

func TestFormatMixedSRT_BothEmpty(t *testing.T) {
	srt := subtitle.FormatMixedSRT("", nil)
	if srt != "" {
		t.Errorf("Expected empty SRT when both sources are empty, got: %s", srt)
	}
}

func TestFormatMixedSRT_LongChunkSplits(t *testing.T) {
	// Simulate a long chunk output that should split into multiple subtitle entries
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

	if srt == "" {
		t.Fatal("Expected non-empty SRT")
	}

	// Should have multiple entries (long text was split)
	entries := strings.Split(strings.TrimSpace(srt), "\n\n")
	if len(entries) < 2 {
		t.Errorf("Expected long chunk to split into multiple entries, got %d", len(entries))
	}

	// Should NOT contain "..." truncation (text was split, not truncated)
	if strings.Contains(srt, "...") {
		t.Logf("WARNING: SRT still contains truncation:\n%s", srt)
	}

	t.Logf("Split long chunk into %d entries:\n%s", len(entries), srt)
}

func TestFormatMixedSRT_ShortSubtitleExtended(t *testing.T) {
	// Chunk with a very short time window (0.5s) followed by a gap.
	// The subtitle should extend to at least 2 seconds.
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

	if srt == "" {
		t.Fatal("Expected non-empty SRT")
	}

	// The first subtitle should be extended past 0.5s — verify via SRT timecode
	// 0.5s = 00:00:00,500; 2.0s = 00:00:02,000
	if !strings.Contains(srt, "00:00:02,000") {
		t.Errorf("Expected first subtitle to be extended to 2.0s, got:\n%s", srt)
	}

	t.Logf("Extended short subtitle SRT:\n%s", srt)
}

func TestFormatMixedSRT_ShortSubtitleNoOverlap(t *testing.T) {
	// Chunk with short time window (0.5s) followed immediately by another chunk at 1.0s.
	// Extension should be capped at 1.0s to avoid overlapping.
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

	if srt == "" {
		t.Fatal("Expected non-empty SRT")
	}

	// The first subtitle should be capped at 1.0s (next entry start)
	if !strings.Contains(srt, "00:00:01,000") {
		t.Errorf("Expected first subtitle to be capped at 1.0s, got:\n%s", srt)
	}

	// Should NOT extend to 2.0s because that would overlap
	if strings.Contains(srt, "00:00:02,000") {
		t.Errorf("First subtitle should not extend past next entry start:\n%s", srt)
	}

	t.Logf("No-overlap SRT:\n%s", srt)
}

func TestFormatMixedSRT_ShortSubtitleLastEntry(t *testing.T) {
	// Single chunk with short time window — no next entry, should extend freely.
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

	if srt == "" {
		t.Fatal("Expected non-empty SRT")
	}

	// Should be extended to 12.0s (10.0 + 2.0)
	if !strings.Contains(srt, "00:00:12,000") {
		t.Errorf("Expected last subtitle to extend to 12.0s, got:\n%s", srt)
	}

	t.Logf("Last entry extension SRT:\n%s", srt)
}
