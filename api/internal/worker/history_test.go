package worker

import (
	"strings"
	"testing"
)

// ── parseSessionScore ──────────────────────────────────────────────────────────

func TestParseSessionScore_Valid(t *testing.T) {
	input := `분석 완료.

` + "```score\n" + `{"overall":74,"form":68,"intensity":82,"consistency":72,"movements":{"Snatch":{"form":65,"intensity":80}},"summary":"스내치 풀 익스텐션이 개선되었습니다."}
` + "```"

	got := parseSessionScore(input)
	if got == "{}" {
		t.Fatal("expected non-empty score JSON, got {}")
	}
	if !strings.Contains(got, "74") {
		t.Errorf("expected overall=74 in output, got: %s", got)
	}
	if !strings.Contains(got, "Snatch") {
		t.Errorf("expected Snatch movement in output, got: %s", got)
	}
}

func TestParseSessionScore_Missing(t *testing.T) {
	input := "분석만 있고 점수 블록 없음."
	got := parseSessionScore(input)
	if got != "{}" {
		t.Errorf("expected '{}' when no score block, got: %s", got)
	}
}

func TestParseSessionScore_MalformedJSON(t *testing.T) {
	input := "```score\n{not valid json}\n```"
	got := parseSessionScore(input)
	if got != "{}" {
		t.Errorf("expected '{}' for malformed JSON, got: %s", got)
	}
}

func TestParseSessionScore_ZeroScoreSkipped(t *testing.T) {
	// A valid JSON with all-zero values still parses (zero is a valid score)
	input := "```score\n{\"overall\":0,\"form\":0,\"intensity\":0,\"consistency\":0,\"movements\":{},\"summary\":\"\"}\n```"
	got := parseSessionScore(input)
	// Should parse successfully and return the zero-score JSON
	if got == "{}" {
		t.Errorf("expected valid JSON output for zero scores, got: %s", got)
	}
}

// ── buildWODContext ───────────────────────────────────────────────────────────

func TestBuildWODContext_Empty(t *testing.T) {
	got := buildWODContext("")
	if got != "" {
		t.Errorf("expected empty string for empty WOD, got: %q", got)
	}
}

func TestBuildWODContext_Whitespace(t *testing.T) {
	got := buildWODContext("   ")
	if got != "" {
		t.Errorf("expected empty string for whitespace-only WOD, got: %q", got)
	}
}

func TestBuildWODContext_NamedFran(t *testing.T) {
	got := buildWODContext("Fran")
	if !strings.Contains(got, "21-15-9") {
		t.Errorf("expected Fran benchmark text, got: %s", got)
	}
	if !strings.Contains(got, "For Time") {
		t.Errorf("expected 'For Time' hint in Fran context, got: %s", got)
	}
}

func TestBuildWODContext_NamedGrace(t *testing.T) {
	got := buildWODContext("Grace")
	if !strings.Contains(got, "Clean & Jerks") {
		t.Errorf("expected Grace benchmark text, got: %s", got)
	}
}

func TestBuildWODContext_CustomForTime(t *testing.T) {
	got := buildWODContext("For Time: 5 rounds of 10 Deadlifts + 15 Box Jumps")
	if !strings.Contains(got, "For Time") {
		t.Errorf("expected 'For Time' context, got: %s", got)
	}
}

func TestBuildWODContext_CustomAMRAP(t *testing.T) {
	got := buildWODContext("AMRAP 20: 5 Pull-ups, 10 Push-ups, 15 Air Squats")
	if !strings.Contains(got, "AMRAP") {
		t.Errorf("expected 'AMRAP' context, got: %s", got)
	}
}

func TestBuildWODContext_CustomEMOM(t *testing.T) {
	got := buildWODContext("EMOM 12: 8 KB Swings")
	if !strings.Contains(got, "EMOM") {
		t.Errorf("expected 'EMOM' context, got: %s", got)
	}
	if !strings.Contains(got, "form") {
		t.Errorf("expected form emphasis for EMOM, got: %s", got)
	}
}

// ── buildHistoryContext (nil-safe checks) ─────────────────────────────────────

func TestBuildHistoryContext_NilDB(t *testing.T) {
	w := &Worker{DB: nil}
	got := w.buildHistoryContext(1, 5)
	if got != "" {
		t.Errorf("expected empty string when DB is nil, got: %q", got)
	}
}

func TestBuildHistoryContext_ZeroProfileID(t *testing.T) {
	w := &Worker{DB: nil}
	got := w.buildHistoryContext(0, 5)
	if got != "" {
		t.Errorf("expected empty string when profileID is 0, got: %q", got)
	}
}
