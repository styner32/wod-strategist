package controllers

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/movement"
	"go.uber.org/zap"
)

// Tunable scoring weights
const (
	weightRecency = 0.5
	weightWeight  = 0.3
	weightMain    = 0.2
	halfLifeDays  = 30.0
)

type RelatedWODQuery struct {
	Movement        string   `json:"movement"`
	WeightKG        *float64 `json:"weight_kg,omitempty"`
	SourceSessionID string   `json:"source_session_id"`
}

type ScoreParts struct {
	Recency float64 `json:"recency"`
	Weight  float64 `json:"weight"`
	Main    float64 `json:"main"`
}

type RelatedWODItem struct {
	SessionID      string                `json:"session_id"`
	AnalysisID     uint                  `json:"analysis_id"`
	CreatedAt      time.Time             `json:"created_at"`
	WODDescription string                `json:"wod_description"`
	Movement       db.NormalizedMovement `json:"movement"`
	SessionScore   string                `json:"session_score"`
	Score          float64               `json:"score"`
	ScoreParts     ScoreParts            `json:"score_parts"`
}

type RelatedWODsResponse struct {
	Query   RelatedWODQuery  `json:"query"`
	Related []RelatedWODItem `json:"related"`
}

// ComputeRelevanceScore calculates the 0.0-1.0 relevance score for a past movement match.
func ComputeRelevanceScore(now, createdAt time.Time, targetWeightKG *float64, entry db.NormalizedMovement) (float64, ScoreParts) {
	ageDays := math.Max(0, now.Sub(createdAt).Hours()/24.0)
	recency := math.Exp(-ageDays / halfLifeDays)

	var weightMatch float64
	if targetWeightKG != nil && entry.WeightKG != nil {
		diff := math.Abs(*targetWeightKG - *entry.WeightKG)
		if diff <= 2.5 {
			weightMatch = 1.0
		} else {
			weightMatch = math.Max(0.0, 1.0-diff/20.0)
		}
	} else {
		weightMatch = 0.5
	}

	var mainBonus float64
	if entry.IsMain {
		mainBonus = 1.0
	}

	rawScore := weightRecency*recency + weightWeight*weightMatch + weightMain*mainBonus
	score := math.Round(rawScore*100) / 100

	return score, ScoreParts{
		Recency: math.Round(recency*100) / 100,
		Weight:  math.Round(weightMatch*100) / 100,
		Main:    math.Round(mainBonus*100) / 100,
	}
}

// GetRelatedWODs returns past WODs containing the specified movement, ranked by relevance.
//
// @Summary      Get Related WODs
// @Description  Returns nearest past WODs of the same profile containing the specified movement, ranked by recency, weight match, and main movement status.
// @Tags         wod
// @Produce      json
// @Param        profile_id query int true "Profile ID"
// @Param        movement query string false "Movement name (Mode A)"
// @Param        weight_kg query number false "Movement weight in kg (Mode A)"
// @Param        session_id query string false "Anchor session ID (Mode B)"
// @Param        limit query int false "Max results (default 5, max 20)"
// @Success      200 {object} RelatedWODsResponse
// @Failure      400 {object} ErrorResponse
// @Failure      403 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /related-wods [get]
func (ctl *Controller) GetRelatedWODs(c *gin.Context) {
	profileIDStr := c.Query("profile_id")
	if profileIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	profileID, err := strconv.ParseUint(profileIDStr, 10, 32)
	if err != nil || profileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile_id"})
		return
	}

	if !ctl.assertOwnsProfile(c, uint(profileID)) {
		return
	}

	movementParam := strings.TrimSpace(c.Query("movement"))
	sessionIDParam := strings.TrimSpace(c.Query("session_id"))

	if (movementParam == "" && sessionIDParam == "") || (movementParam != "" && sessionIDParam != "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "must specify either movement or session_id, but not both"})
		return
	}

	limit := 5
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			if parsedLimit > 20 {
				limit = 20
			} else {
				limit = parsedLimit
			}
		}
	}

	var targetMovement string
	var targetWeightKG *float64
	var sourceSessionID string

	if movementParam != "" {
		// Mode A: pre-session query
		targetMovement = movementParam
		if weightStr := c.Query("weight_kg"); weightStr != "" {
			if w, err := strconv.ParseFloat(weightStr, 64); err == nil && w > 0 {
				targetWeightKG = &w
			}
		}
	} else {
		// Mode B: anchor session query
		sourceSessionID = sessionIDParam
		var anchor db.AnalysisResult
		err := ctl.db.WithContext(c.Request.Context()).
			Where("profile_id = ? AND session_id = ? AND analysis_type = ? AND status = 'COMPLETED' AND archived_at IS NULL",
				uint(profileID), sourceSessionID, db.AnalysisTypeWOD).
			First(&anchor).Error
		if err != nil || anchor.NormalizedWorkout == "" {
			c.JSON(http.StatusOK, RelatedWODsResponse{
				Query: RelatedWODQuery{
					Movement:        "",
					SourceSessionID: sourceSessionID,
				},
				Related: []RelatedWODItem{},
			})
			return
		}

		var movements []db.NormalizedMovement
		if err := json.Unmarshal([]byte(anchor.NormalizedWorkout), &movements); err != nil || len(movements) == 0 {
			c.JSON(http.StatusOK, RelatedWODsResponse{
				Query: RelatedWODQuery{
					Movement:        "",
					SourceSessionID: sourceSessionID,
				},
				Related: []RelatedWODItem{},
			})
			return
		}

		// Find anchor movement (prefer is_main == true, fallback first)
		selectedAnchor := movements[0]
		for _, m := range movements {
			if m.IsMain {
				selectedAnchor = m
				break
			}
		}

		targetMovement = selectedAnchor.Movement
		targetWeightKG = selectedAnchor.WeightKG
	}

	targetKey := movement.NormalizeKey(targetMovement)
	if targetKey == "" {
		c.JSON(http.StatusOK, RelatedWODsResponse{
			Query: RelatedWODQuery{
				Movement:        targetMovement,
				WeightKG:        targetWeightKG,
				SourceSessionID: sourceSessionID,
			},
			Related: []RelatedWODItem{},
		})
		return
	}

	var candidates []db.AnalysisResult
	queryDB := ctl.db.WithContext(c.Request.Context()).
		Where("profile_id = ? AND analysis_type = ? AND status = 'COMPLETED' AND archived_at IS NULL AND normalized_workout != ''",
			uint(profileID), db.AnalysisTypeWOD).
		Where("normalized_workout ILIKE ?", "%"+targetMovement+"%")

	if sourceSessionID != "" {
		queryDB = queryDB.Where("session_id <> ?", sourceSessionID)
	}

	if err := queryDB.Order("created_at DESC").Limit(50).Find(&candidates).Error; err != nil {
		logger.Log.Error("Failed to fetch candidate analysis results for related WODs",
			zap.Uint64("profile_id", profileID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch related WODs"})
		return
	}

	now := time.Now()
	var items []RelatedWODItem

	for _, cand := range candidates {
		var movs []db.NormalizedMovement
		if err := json.Unmarshal([]byte(cand.NormalizedWorkout), &movs); err != nil {
			continue
		}

		var matchedEntry *db.NormalizedMovement
		for _, entry := range movs {
			if movement.NormalizeKey(entry.Movement) == targetKey {
				if matchedEntry == nil || entry.IsMain {
					e := entry
					matchedEntry = &e
				}
			}
		}

		if matchedEntry == nil {
			continue
		}

		score, parts := ComputeRelevanceScore(now, cand.CreatedAt, targetWeightKG, *matchedEntry)

		items = append(items, RelatedWODItem{
			SessionID:      cand.SessionID,
			AnalysisID:     cand.ID,
			CreatedAt:      cand.CreatedAt,
			WODDescription: cand.WODDescription,
			Movement:       *matchedEntry,
			SessionScore:   cand.SessionScore,
			Score:          score,
			ScoreParts:     parts,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []RelatedWODItem{}
	}

	c.JSON(http.StatusOK, RelatedWODsResponse{
		Query: RelatedWODQuery{
			Movement:        targetMovement,
			WeightKG:        targetWeightKG,
			SourceSessionID: sourceSessionID,
		},
		Related: items,
	})
}
