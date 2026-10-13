package wodeval

import (
	"encoding/json"
	"math"
	"time"
)

const Version = 1

type Start struct {
	ElapsedSeconds *float64 `json:"elapsed_seconds,omitempty"`
	Seconds        *float64 `json:"seconds"`
	EndSeconds     *float64 `json:"end_seconds"`
	Source         string   `json:"source"`
	Status         string   `json:"status"`
	Evidence       string   `json:"evidence"`
}

type Score struct {
	EvaluationVersion int                       `json:"evaluation_version"`
	Overall           *int                      `json:"overall"`
	Form              *int                      `json:"form"`
	Intensity         *int                      `json:"intensity"`
	Consistency       *int                      `json:"consistency"`
	Movements         map[string]map[string]int `json:"movements"`
	Summary           string                    `json:"summary"`
	WODStart          Start                     `json:"wod_start"`
	EvidenceStatus    string                    `json:"evidence_status"`
}

func Decode(raw string) (Score, bool) {
	var s Score
	err := json.Unmarshal([]byte(raw), &s)
	return s, err == nil && s.EvaluationVersion == Version
}

func ValidWindow(start Start) bool {
	return start.Status == "confirmed" && start.Seconds != nil && start.EndSeconds != nil &&
		!math.IsNaN(*start.Seconds) && !math.IsInf(*start.Seconds, 0) &&
		!math.IsNaN(*start.EndSeconds) && !math.IsInf(*start.EndSeconds, 0) &&
		*start.Seconds >= 0 && *start.EndSeconds > *start.Seconds
}

// Missing dimensions remain null; zero is a real, assessable score.
func Normalize(s *Score, skill bool) {
	s.EvaluationVersion = Version
	s.Overall = nil
	for _, p := range []**int{&s.Form, &s.Intensity, &s.Consistency} {
		if *p != nil && (**p < 0 || **p > 100) {
			*p = nil
		}
	}
	if !ValidWindow(s.WODStart) {
		s.Intensity = nil
		s.Consistency = nil
	}
	if s.Form != nil && s.Intensity != nil && s.Consistency != nil {
		f, i, c := .5, .3, .2
		if skill {
			f, i, c = .7, .15, .15
		}
		n := int(math.Round(float64(*s.Form)*f + float64(*s.Intensity)*i + float64(*s.Consistency)*c))
		s.Overall = &n
	}
}

type Workout struct {
	At     time.Time
	Effort *int
	Start  Start
}

type Load struct {
	Days            int     `json:"days"`
	Total           float64 `json:"total"`
	DailyAverage    float64 `json:"daily_average"`
	Included        int     `json:"included"`
	MissingEffort   int     `json:"missing_effort"`
	MissingDuration int     `json:"missing_duration"`
}

type Comparison struct {
	Seven             Load      `json:"seven"`
	TwentyEight       Load      `json:"twenty_eight"`
	ChangePercent     *float64  `json:"change_percent"`
	Status            string    `json:"status"`
	TrackingStartedAt time.Time `json:"tracking_started_at"`
}

func SessionLoad(e Workout) *float64 {
	if e.Effort == nil || *e.Effort < 1 || *e.Effort > 10 || !ValidWindow(e.Start) {
		return nil
	}
	seconds := *e.Start.EndSeconds - *e.Start.Seconds
	if e.Start.ElapsedSeconds != nil {
		seconds = *e.Start.ElapsedSeconds
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return nil
	}
	v := seconds / 60 * float64(*e.Effort)
	return &v
}

// Both windows include today and use calendar days in the requested timezone.
// An unrated/unresolved workout is missing evidence, never a zero-load workout.
func Compare(entries []Workout, now, trackingStart time.Time, location *time.Location) Comparison {
	day := func(t time.Time) time.Time {
		t = t.In(location)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, location)
	}
	today := day(now)
	out := Comparison{Seven: Load{Days: 7}, TwentyEight: Load{Days: 28}, Status: "collecting_baseline", TrackingStartedAt: trackingStart}
	for _, entry := range entries {
		if entry.At.After(now) {
			continue
		}
		d := day(entry.At)
		for _, l := range []*Load{&out.Seven, &out.TwentyEight} {
			if d.Before(today.AddDate(0, 0, -l.Days+1)) {
				continue
			}
			if entry.Effort == nil {
				l.MissingEffort++
			}
			if !ValidWindow(entry.Start) {
				l.MissingDuration++
			}
			if v := SessionLoad(entry); v != nil {
				l.Total += *v
				l.Included++
			}
		}
	}
	out.Seven.DailyAverage = out.Seven.Total / 7
	out.TwentyEight.DailyAverage = out.TwentyEight.Total / 28
	if out.TwentyEight.MissingEffort > 0 || out.TwentyEight.MissingDuration > 0 {
		out.Status = "incomplete_records"
		return out
	}
	if trackingStart.IsZero() || day(trackingStart).After(today.AddDate(0, 0, -27)) {
		return out
	}
	if out.TwentyEight.DailyAverage == 0 {
		out.Status = "no_baseline"
		return out
	}
	v := (out.Seven.DailyAverage/out.TwentyEight.DailyAverage - 1) * 100
	out.ChangePercent = &v
	out.Status = "available"
	return out
}
