package fatigue

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/movement"
)

// 6 Functional Muscle Groups / Movement Patterns
const (
	GroupShouldersPush   = "shoulders_push"   // 어깨 / 상체 밀기
	GroupUpperPullGrip   = "upper_pull_grip"  // 등·광배 / 상체 당기기·악력
	GroupPosteriorChain  = "posterior_chain"  // 허리 / 후면사슬
	GroupQuadsSquat      = "quads_squat"      // 하체 / 스쿼트
	GroupCoreMidline     = "core_midline"     // 코어 / 미드라인
	GroupCardioMetabolic = "cardio_metabolic" // 심폐 / 전신 유산소
)

var AllMuscleGroups = []string{
	GroupShouldersPush,
	GroupUpperPullGrip,
	GroupPosteriorChain,
	GroupQuadsSquat,
	GroupCoreMidline,
	GroupCardioMetabolic,
}

var MuscleGroupNamesKO = map[string]string{
	GroupShouldersPush:   "어깨 / 상체 밀기",
	GroupUpperPullGrip:   "등·광배 / 상체 당기기·악력",
	GroupPosteriorChain:  "허리 / 후면사슬",
	GroupQuadsSquat:      "하체 / 스쿼트",
	GroupCoreMidline:     "코어 / 미드라인",
	GroupCardioMetabolic: "심폐 / 전신 유산소",
}

// HalfLifeHours defines biological recovery half-life in hours for each muscle group.
var HalfLifeHours = map[string]float64{
	GroupShouldersPush:   30.0,
	GroupUpperPullGrip:   28.0,
	GroupPosteriorChain:  42.0, // Spinal erectors & heavy CNS load takes longest
	GroupQuadsSquat:      36.0,
	GroupCoreMidline:     24.0,
	GroupCardioMetabolic: 18.0, // Cardiovascular system recovers fastest
}

// MovementMuscleWeights represents load contributions (0.0 to 1.0) of a movement across 6 groups.
type MovementMuscleWeights struct {
	ShouldersPush   float64 `json:"shoulders_push"`
	UpperPullGrip   float64 `json:"upper_pull_grip"`
	PosteriorChain  float64 `json:"posterior_chain"`
	QuadsSquat      float64 `json:"quads_squat"`
	CoreMidline     float64 `json:"core_midline"`
	CardioMetabolic float64 `json:"cardio_metabolic"`
}

// movementLoadCatalog maps normalized movement keys to their muscle group contribution weights.
var movementLoadCatalog = map[string]MovementMuscleWeights{
	// Barbell
	"power snatch":      {ShouldersPush: 0.5, UpperPullGrip: 0.4, PosteriorChain: 0.9, QuadsSquat: 0.4, CoreMidline: 0.6, CardioMetabolic: 0.6},
	"snatch":            {ShouldersPush: 0.6, UpperPullGrip: 0.4, PosteriorChain: 0.9, QuadsSquat: 0.7, CoreMidline: 0.7, CardioMetabolic: 0.6},
	"hang power snatch": {ShouldersPush: 0.5, UpperPullGrip: 0.4, PosteriorChain: 0.8, QuadsSquat: 0.3, CoreMidline: 0.6, CardioMetabolic: 0.6},
	"squat snatch":      {ShouldersPush: 0.6, UpperPullGrip: 0.4, PosteriorChain: 0.9, QuadsSquat: 0.8, CoreMidline: 0.7, CardioMetabolic: 0.6},
	"clean":             {ShouldersPush: 0.3, UpperPullGrip: 0.5, PosteriorChain: 0.9, QuadsSquat: 0.8, CoreMidline: 0.7, CardioMetabolic: 0.6},
	"power clean":       {ShouldersPush: 0.3, UpperPullGrip: 0.5, PosteriorChain: 0.9, QuadsSquat: 0.5, CoreMidline: 0.6, CardioMetabolic: 0.6},
	"hang power clean":  {ShouldersPush: 0.3, UpperPullGrip: 0.5, PosteriorChain: 0.8, QuadsSquat: 0.4, CoreMidline: 0.6, CardioMetabolic: 0.5},
	"clean & jerk":      {ShouldersPush: 0.8, UpperPullGrip: 0.5, PosteriorChain: 0.9, QuadsSquat: 0.8, CoreMidline: 0.7, CardioMetabolic: 0.7},
	"clean and jerk":    {ShouldersPush: 0.8, UpperPullGrip: 0.5, PosteriorChain: 0.9, QuadsSquat: 0.8, CoreMidline: 0.7, CardioMetabolic: 0.7},
	"deadlift":          {ShouldersPush: 0.1, UpperPullGrip: 0.6, PosteriorChain: 1.0, QuadsSquat: 0.4, CoreMidline: 0.6, CardioMetabolic: 0.4},
	"back squat":        {ShouldersPush: 0.1, UpperPullGrip: 0.0, PosteriorChain: 0.6, QuadsSquat: 1.0, CoreMidline: 0.5, CardioMetabolic: 0.4},
	"front squat":       {ShouldersPush: 0.3, UpperPullGrip: 0.1, PosteriorChain: 0.5, QuadsSquat: 0.9, CoreMidline: 0.8, CardioMetabolic: 0.4},
	"overhead squat":    {ShouldersPush: 0.8, UpperPullGrip: 0.3, PosteriorChain: 0.5, QuadsSquat: 0.8, CoreMidline: 0.9, CardioMetabolic: 0.5},
	"thruster":          {ShouldersPush: 0.9, UpperPullGrip: 0.2, PosteriorChain: 0.5, QuadsSquat: 0.9, CoreMidline: 0.7, CardioMetabolic: 0.8},
	"strict press":      {ShouldersPush: 1.0, UpperPullGrip: 0.1, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 0.6, CardioMetabolic: 0.2},
	"push press":        {ShouldersPush: 0.9, UpperPullGrip: 0.1, PosteriorChain: 0.3, QuadsSquat: 0.4, CoreMidline: 0.5, CardioMetabolic: 0.4},
	"push jerk":         {ShouldersPush: 0.9, UpperPullGrip: 0.1, PosteriorChain: 0.3, QuadsSquat: 0.5, CoreMidline: 0.6, CardioMetabolic: 0.5},
	"bench press":       {ShouldersPush: 0.9, UpperPullGrip: 0.2, PosteriorChain: 0.1, QuadsSquat: 0.0, CoreMidline: 0.3, CardioMetabolic: 0.2},

	// Dumbbell & Kettlebell
	"db snatch":        {ShouldersPush: 0.6, UpperPullGrip: 0.5, PosteriorChain: 0.8, QuadsSquat: 0.4, CoreMidline: 0.6, CardioMetabolic: 0.7},
	"dumbbell snatch":  {ShouldersPush: 0.6, UpperPullGrip: 0.5, PosteriorChain: 0.8, QuadsSquat: 0.4, CoreMidline: 0.6, CardioMetabolic: 0.7},
	"db clean":         {ShouldersPush: 0.3, UpperPullGrip: 0.5, PosteriorChain: 0.8, QuadsSquat: 0.5, CoreMidline: 0.5, CardioMetabolic: 0.6},
	"db thruster":      {ShouldersPush: 0.9, UpperPullGrip: 0.2, PosteriorChain: 0.5, QuadsSquat: 0.9, CoreMidline: 0.7, CardioMetabolic: 0.8},
	"kb swing":         {ShouldersPush: 0.3, UpperPullGrip: 0.6, PosteriorChain: 0.9, QuadsSquat: 0.3, CoreMidline: 0.6, CardioMetabolic: 0.7},
	"kettlebell swing": {ShouldersPush: 0.3, UpperPullGrip: 0.6, PosteriorChain: 0.9, QuadsSquat: 0.3, CoreMidline: 0.6, CardioMetabolic: 0.7},
	"devil press":      {ShouldersPush: 0.8, UpperPullGrip: 0.5, PosteriorChain: 0.8, QuadsSquat: 0.5, CoreMidline: 0.7, CardioMetabolic: 0.9},

	// Gymnastics
	"pull up":           {ShouldersPush: 0.2, UpperPullGrip: 1.0, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 0.5, CardioMetabolic: 0.5},
	"pull-up":           {ShouldersPush: 0.2, UpperPullGrip: 1.0, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 0.5, CardioMetabolic: 0.5},
	"chest to bar":      {ShouldersPush: 0.2, UpperPullGrip: 1.0, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 0.6, CardioMetabolic: 0.6},
	"bar muscle up":     {ShouldersPush: 0.7, UpperPullGrip: 0.9, PosteriorChain: 0.3, QuadsSquat: 0.0, CoreMidline: 0.8, CardioMetabolic: 0.7},
	"ring muscle up":    {ShouldersPush: 0.8, UpperPullGrip: 0.9, PosteriorChain: 0.3, QuadsSquat: 0.0, CoreMidline: 0.8, CardioMetabolic: 0.7},
	"toes to bar":       {ShouldersPush: 0.2, UpperPullGrip: 0.7, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 1.0, CardioMetabolic: 0.5},
	"toes-to-bar":       {ShouldersPush: 0.2, UpperPullGrip: 0.7, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 1.0, CardioMetabolic: 0.5},
	"handstand push up": {ShouldersPush: 1.0, UpperPullGrip: 0.1, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 0.7, CardioMetabolic: 0.5},
	"handstand push-up": {ShouldersPush: 1.0, UpperPullGrip: 0.1, PosteriorChain: 0.2, QuadsSquat: 0.0, CoreMidline: 0.7, CardioMetabolic: 0.5},
	"handstand walk":    {ShouldersPush: 0.9, UpperPullGrip: 0.2, PosteriorChain: 0.3, QuadsSquat: 0.0, CoreMidline: 0.8, CardioMetabolic: 0.6},
	"rope climb":        {ShouldersPush: 0.2, UpperPullGrip: 1.0, PosteriorChain: 0.2, QuadsSquat: 0.3, CoreMidline: 0.8, CardioMetabolic: 0.7},
	"ring dip":          {ShouldersPush: 0.9, UpperPullGrip: 0.2, PosteriorChain: 0.1, QuadsSquat: 0.0, CoreMidline: 0.6, CardioMetabolic: 0.4},
	"wall walk":         {ShouldersPush: 0.9, UpperPullGrip: 0.2, PosteriorChain: 0.3, QuadsSquat: 0.2, CoreMidline: 0.8, CardioMetabolic: 0.7},
	"ghd sit up":        {ShouldersPush: 0.0, UpperPullGrip: 0.0, PosteriorChain: 0.3, QuadsSquat: 0.4, CoreMidline: 1.0, CardioMetabolic: 0.4},

	// Bodyweight & Plyo
	"burpee":        {ShouldersPush: 0.6, UpperPullGrip: 0.1, PosteriorChain: 0.3, QuadsSquat: 0.5, CoreMidline: 0.6, CardioMetabolic: 1.0},
	"push up":       {ShouldersPush: 0.8, UpperPullGrip: 0.1, PosteriorChain: 0.1, QuadsSquat: 0.0, CoreMidline: 0.6, CardioMetabolic: 0.4},
	"push-up":       {ShouldersPush: 0.8, UpperPullGrip: 0.1, PosteriorChain: 0.1, QuadsSquat: 0.0, CoreMidline: 0.6, CardioMetabolic: 0.4},
	"air squat":     {ShouldersPush: 0.0, UpperPullGrip: 0.0, PosteriorChain: 0.4, QuadsSquat: 0.9, CoreMidline: 0.4, CardioMetabolic: 0.5},
	"sit up":        {ShouldersPush: 0.0, UpperPullGrip: 0.0, PosteriorChain: 0.1, QuadsSquat: 0.2, CoreMidline: 0.9, CardioMetabolic: 0.3},
	"sit-up":        {ShouldersPush: 0.0, UpperPullGrip: 0.0, PosteriorChain: 0.1, QuadsSquat: 0.2, CoreMidline: 0.9, CardioMetabolic: 0.3},
	"lunge":         {ShouldersPush: 0.0, UpperPullGrip: 0.0, PosteriorChain: 0.5, QuadsSquat: 0.9, CoreMidline: 0.5, CardioMetabolic: 0.5},
	"box jump":      {ShouldersPush: 0.1, UpperPullGrip: 0.0, PosteriorChain: 0.6, QuadsSquat: 0.8, CoreMidline: 0.4, CardioMetabolic: 0.8},
	"box step-up":   {ShouldersPush: 0.1, UpperPullGrip: 0.0, PosteriorChain: 0.5, QuadsSquat: 0.8, CoreMidline: 0.4, CardioMetabolic: 0.6},
	"wallball shot": {ShouldersPush: 0.8, UpperPullGrip: 0.1, PosteriorChain: 0.4, QuadsSquat: 0.9, CoreMidline: 0.6, CardioMetabolic: 0.8},
	"wall ball":     {ShouldersPush: 0.8, UpperPullGrip: 0.1, PosteriorChain: 0.4, QuadsSquat: 0.9, CoreMidline: 0.6, CardioMetabolic: 0.8},

	// Cardio
	"row":          {ShouldersPush: 0.1, UpperPullGrip: 0.8, PosteriorChain: 0.8, QuadsSquat: 0.7, CoreMidline: 0.6, CardioMetabolic: 0.9},
	"echo bike":    {ShouldersPush: 0.4, UpperPullGrip: 0.3, PosteriorChain: 0.5, QuadsSquat: 0.9, CoreMidline: 0.5, CardioMetabolic: 1.0},
	"bike erg":     {ShouldersPush: 0.1, UpperPullGrip: 0.1, PosteriorChain: 0.4, QuadsSquat: 0.9, CoreMidline: 0.4, CardioMetabolic: 0.9},
	"skierg":       {ShouldersPush: 0.6, UpperPullGrip: 0.8, PosteriorChain: 0.5, QuadsSquat: 0.3, CoreMidline: 0.8, CardioMetabolic: 0.9},
	"run":          {ShouldersPush: 0.0, UpperPullGrip: 0.0, PosteriorChain: 0.5, QuadsSquat: 0.7, CoreMidline: 0.4, CardioMetabolic: 0.9},
	"double under": {ShouldersPush: 0.4, UpperPullGrip: 0.4, PosteriorChain: 0.4, QuadsSquat: 0.6, CoreMidline: 0.4, CardioMetabolic: 0.8},
	"double-under": {ShouldersPush: 0.4, UpperPullGrip: 0.4, PosteriorChain: 0.4, QuadsSquat: 0.6, CoreMidline: 0.4, CardioMetabolic: 0.8},
}

var sortedCatalogKeys []string

func init() {
	sortedCatalogKeys = make([]string, 0, len(movementLoadCatalog))
	for k := range movementLoadCatalog {
		sortedCatalogKeys = append(sortedCatalogKeys, k)
	}
	sort.Slice(sortedCatalogKeys, func(i, j int) bool {
		li, lj := len(sortedCatalogKeys[i]), len(sortedCatalogKeys[j])
		if li != lj {
			return li > lj // longest catalogKey first
		}
		return sortedCatalogKeys[i] < sortedCatalogKeys[j] // tie-break lexicographically
	})
}

// GetMovementWeights returns load weights for a given movement name.
func GetMovementWeights(name string) MovementMuscleWeights {
	key := movement.NormalizeKey(name)
	if w, ok := movementLoadCatalog[key]; ok {
		return w
	}

	// Deterministic partial match fallback: longest match first, then lexicographical
	for _, catalogKey := range sortedCatalogKeys {
		if strings.Contains(key, catalogKey) || strings.Contains(catalogKey, key) {
			return movementLoadCatalog[catalogKey]
		}
	}

	// Default general distribution
	return MovementMuscleWeights{
		ShouldersPush:   0.3,
		UpperPullGrip:   0.3,
		PosteriorChain:  0.3,
		QuadsSquat:      0.3,
		CoreMidline:     0.3,
		CardioMetabolic: 0.5,
	}
}

// SessionLoadRecord represents one past completed session's loads.
type SessionLoadRecord struct {
	SessionID   string             `json:"session_id"`
	CreatedAt   time.Time          `json:"created_at"`
	MuscleLoads map[string]float64 `json:"muscle_loads"`
}

// MuscleReadinessStatus represents the current state of a single muscle group.
type MuscleReadinessStatus struct {
	Group        string `json:"group"`
	NameKO       string `json:"name_ko"`
	FatigueScore int    `json:"fatigue_score"` // 0 - 100
	State        string `json:"state"`         // fresh, moderate, fatigued, exhausted
	StateKO      string `json:"state_ko"`      // 신선, 보통, 피로 주의, 극심한 피로
}

// ProfileReadinessState represents the overall multi-muscle readiness.
type ProfileReadinessState struct {
	OverallFatigueScore int                              `json:"overall_fatigue_score"` // 0 - 100
	OverallState        string                           `json:"overall_state"`
	OverallStateKO      string                           `json:"overall_state_ko"`
	Muscles             map[string]MuscleReadinessStatus `json:"muscles"`
	LastWorkoutAt       *time.Time                       `json:"last_workout_at,omitempty"`
}

// StateFromFatigueScore maps 0-100 fatigue score to state string.
func StateFromFatigueScore(score int) (string, string) {
	switch {
	case score <= 25:
		return "fresh", "신선"
	case score <= 50:
		return "moderate", "보통"
	case score <= 75:
		return "fatigued", "피로 주의"
	default:
		return "exhausted", "극심한 피로"
	}
}

// ComputeSessionMuscleLoads calculates 0-100 base muscle loads for a single session.
func ComputeSessionMuscleLoads(
	sessionScoreJSON string,
	chunkSignals []string,
	heartRateBPM int,
) map[string]float64 {
	loads := make(map[string]float64)
	for _, g := range AllMuscleGroups {
		loads[g] = 0.0
	}

	type scoreParsed struct {
		Intensity int                       `json:"intensity"`
		Movements map[string]map[string]int `json:"movements"`
	}

	var score scoreParsed
	if sessionScoreJSON != "" && sessionScoreJSON != "{}" {
		_ = json.Unmarshal([]byte(sessionScoreJSON), &score)
	}

	intensityFactor := 1.0
	if score.Intensity > 0 {
		intensityFactor = math.Max(0.5, math.Min(1.5, float64(score.Intensity)/70.0))
	}

	// Parse chunk signals for rep counts, cadence drop, and visual fatigue
	movementVolume := make(map[string]float64)
	movementFatigueCount := make(map[string]int)

	for _, sigRaw := range chunkSignals {
		if strings.TrimSpace(sigRaw) == "" || sigRaw == "{}" {
			continue
		}
		var sig map[string]any
		if err := json.Unmarshal([]byte(sigRaw), &sig); err != nil {
			continue
		}

		mov, _ := sig["movement"].(string)
		mov = strings.TrimSpace(mov)
		if mov == "" || strings.EqualFold(mov, "unknown") || strings.EqualFold(mov, "walking") || strings.EqualFold(mov, "rest") {
			continue
		}

		reps, _ := sig["rep_count"].(float64)
		if reps <= 0 {
			reps = 3.0 // default estimate per active chunk
		}
		movementVolume[mov] += reps

		if fatigueEst, _ := sig["fatigue_visually_established"].(bool); fatigueEst {
			movementFatigueCount[mov]++
		}
	}

	// If no chunk signals, fall back to movements in sessionScore
	if len(movementVolume) == 0 && len(score.Movements) > 0 {
		for mov := range score.Movements {
			movementVolume[mov] = 20.0 // reasonable baseline per movement
		}
	}

	// If still empty, give a modest baseline
	if len(movementVolume) == 0 {
		baseLoad := 30.0 * intensityFactor
		for _, g := range AllMuscleGroups {
			loads[g] = math.Round(baseLoad*10) / 10
		}
		return loads
	}

	// Calculate accumulated loads per muscle group
	for mov, volume := range movementVolume {
		weights := GetMovementWeights(mov)
		fatigueMultiplier := 1.0
		if count := movementFatigueCount[mov]; count > 0 {
			fatigueMultiplier = 1.0 + math.Min(0.5, float64(count)*0.15)
		}

		// Base movement strain = (volume / 25.0) * 35.0 capped reasonably
		movStrain := math.Min(50.0, (volume/20.0)*30.0) * intensityFactor * fatigueMultiplier

		loads[GroupShouldersPush] += movStrain * weights.ShouldersPush
		loads[GroupUpperPullGrip] += movStrain * weights.UpperPullGrip
		loads[GroupPosteriorChain] += movStrain * weights.PosteriorChain
		loads[GroupQuadsSquat] += movStrain * weights.QuadsSquat
		loads[GroupCoreMidline] += movStrain * weights.CoreMidline
		loads[GroupCardioMetabolic] += movStrain * weights.CardioMetabolic
	}

	// Heart rate adjustment if available
	if heartRateBPM > 140 {
		hrBonus := math.Min(20.0, float64(heartRateBPM-140)*0.5)
		loads[GroupCardioMetabolic] += hrBonus
	}

	// Cap individual session loads at 100
	for _, g := range AllMuscleGroups {
		loads[g] = math.Round(math.Min(100.0, loads[g])*10) / 10
	}

	return loads
}

// ComputeCurrentReadiness applies exponential decay to past session loads and computes current readiness.
func ComputeCurrentReadiness(records []SessionLoadRecord, now time.Time) ProfileReadinessState {
	accumulatedFatigue := make(map[string]float64)
	for _, g := range AllMuscleGroups {
		accumulatedFatigue[g] = 0.0
	}

	var lastWorkoutAt *time.Time

	for _, rec := range records {
		if rec.CreatedAt.After(now) {
			continue
		}
		if lastWorkoutAt == nil || rec.CreatedAt.After(*lastWorkoutAt) {
			t := rec.CreatedAt
			lastWorkoutAt = &t
		}

		hoursPassed := now.Sub(rec.CreatedAt).Hours()
		if hoursPassed < 0 {
			hoursPassed = 0
		}

		// If workout was more than 7 days ago, its fatigue is practically 0
		if hoursPassed > 24*7 {
			continue
		}

		for _, g := range AllMuscleGroups {
			halfLife := HalfLifeHours[g]
			if halfLife <= 0 {
				halfLife = 24.0
			}
			loadVal := rec.MuscleLoads[g]
			// Decay formula: Load * 2^(-hours / halfLife)
			decayFactor := math.Pow(2.0, -hoursPassed/halfLife)
			accumulatedFatigue[g] += loadVal * decayFactor
		}
	}

	muscles := make(map[string]MuscleReadinessStatus)
	var totalFatigue float64

	for _, g := range AllMuscleGroups {
		score := int(math.Round(math.Min(100.0, accumulatedFatigue[g])))
		state, stateKO := StateFromFatigueScore(score)
		muscles[g] = MuscleReadinessStatus{
			Group:        g,
			NameKO:       MuscleGroupNamesKO[g],
			FatigueScore: score,
			State:        state,
			StateKO:      stateKO,
		}
		totalFatigue += float64(score)
	}

	overallScore := int(math.Round(totalFatigue / float64(len(AllMuscleGroups))))
	overallState, overallStateKO := StateFromFatigueScore(overallScore)

	return ProfileReadinessState{
		OverallFatigueScore: overallScore,
		OverallState:        overallState,
		OverallStateKO:      overallStateKO,
		Muscles:             muscles,
		LastWorkoutAt:       lastWorkoutAt,
	}
}
