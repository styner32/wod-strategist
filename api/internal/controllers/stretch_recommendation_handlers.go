package controllers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

type analysisResultStretchRow struct {
	ID                     uint      `gorm:"column:id"`
	SessionID              string    `gorm:"column:session_id"`
	AnalysisType           string    `gorm:"column:analysis_type"`
	StretchRecommendations string    `gorm:"column:stretch_recommendations"`
	CreatedAt              time.Time `gorm:"column:created_at"`
}

type stretchGroup struct {
	key               string
	catalogStretch    *db.Stretch
	firstSeenName     string
	firstSeenTarget   string
	lastRecommendedAt time.Time
	seenSessions      map[string]struct{}
	sessions          []RecommendedStretchSession
}

type rawStretchRecItem struct {
	Stretch      string `json:"stretch"`
	TargetArea   string `json:"target_area"`
	Reason       string `json:"reason"`
	DurationHint string `json:"duration_hint"`
	Caution      string `json:"caution"`
	Provisional  bool   `json:"provisional"`
}

// ListRecommendedStretches handles GET /api/v1/stretches/recommended.
func (ctl *Controller) ListRecommendedStretches(c *gin.Context) {
	pidStr := c.Query("profile_id")
	if pidStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "profile_id is required"})
		return
	}
	pid, err := strconv.ParseUint(pidStr, 10, 32)
	if err != nil || pid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile_id"})
		return
	}
	profileID := uint(pid)

	if !ctl.assertOwnsProfile(c, profileID) {
		return
	}

	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	if limit > 200 {
		limit = 200
	}

	ctx := c.Request.Context()

	var rows []analysisResultStretchRow
	err = ctl.db.WithContext(ctx).Model(&db.AnalysisResult{}).
		Select("id", "session_id", "analysis_type", "stretch_recommendations", "created_at").
		Where("profile_id = ? AND archived_at IS NULL", profileID).
		Where("stretch_recommendations <> '' AND stretch_recommendations <> '[]'").
		Order("created_at DESC").Limit(limit).Find(&rows).Error
	if err != nil {
		logger.Log.Error("failed to query analysis results for recommended stretches", zap.Uint("profile_id", profileID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch stretch recommendations"})
		return
	}

	stretches, err := db.ListStretches(ctx, ctl.db)
	if err != nil {
		logger.Log.Error("failed to list stretches", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load stretch catalog"})
		return
	}

	aliases, err := db.ListStretchAliases(ctx, ctl.db)
	if err != nil {
		logger.Log.Error("failed to list stretch aliases", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load stretch catalog"})
		return
	}

	stretchByID := make(map[uint64]db.Stretch, len(stretches))
	aliasMap := make(map[uint64][]string)
	catalogLookup := make(map[string]db.Stretch)

	for _, s := range stretches {
		stretchByID[s.ID] = s
		normKey := db.NormalizeStretchKey(s.Name)
		if normKey != "" {
			catalogLookup[normKey] = s
		}
		if s.NormalizedKey != "" && s.NormalizedKey != normKey {
			catalogLookup[s.NormalizedKey] = s
		}
	}

	for _, a := range aliases {
		aliasMap[a.StretchID] = append(aliasMap[a.StretchID], a.Alias)
		normKey := db.NormalizeStretchKey(a.Alias)
		if s, ok := stretchByID[a.StretchID]; ok {
			if normKey != "" {
				catalogLookup[normKey] = s
			}
			if a.NormalizedKey != "" && a.NormalizedKey != normKey {
				catalogLookup[a.NormalizedKey] = s
			}
		}
	}

	groups := make(map[string]*stretchGroup)

	for _, row := range rows {
		var recItems []rawStretchRecItem
		if err := json.Unmarshal([]byte(row.StretchRecommendations), &recItems); err != nil {
			continue
		}

		for _, item := range recItems {
			rawName := strings.TrimSpace(item.Stretch)
			itemKey := db.NormalizeStretchKey(rawName)
			if itemKey == "" {
				continue
			}

			var groupKey string
			var matchedStretch *db.Stretch

			if s, ok := catalogLookup[itemKey]; ok {
				matchedStretch = &s
				groupKey = s.NormalizedKey
				if groupKey == "" {
					groupKey = db.NormalizeStretchKey(s.Name)
				}
			} else {
				groupKey = itemKey
			}

			grp, exists := groups[groupKey]
			if !exists {
				grp = &stretchGroup{
					key:               groupKey,
					catalogStretch:    matchedStretch,
					firstSeenName:     rawName,
					firstSeenTarget:   strings.TrimSpace(item.TargetArea),
					lastRecommendedAt: row.CreatedAt,
					seenSessions:      make(map[string]struct{}),
					sessions:          make([]RecommendedStretchSession, 0),
				}
				groups[groupKey] = grp
			}

			if _, alreadySeen := grp.seenSessions[row.SessionID]; alreadySeen {
				continue
			}
			grp.seenSessions[row.SessionID] = struct{}{}

			targetArea := item.TargetArea
			if matchedStretch != nil && matchedStretch.TargetArea != "" {
				targetArea = matchedStretch.TargetArea
			}

			grp.sessions = append(grp.sessions, RecommendedStretchSession{
				SessionID:    row.SessionID,
				AnalysisID:   row.ID,
				AnalysisType: row.AnalysisType,
				TargetArea:   targetArea,
				Reason:       item.Reason,
				DurationHint: item.DurationHint,
				Caution:      item.Caution,
				Provisional:  item.Provisional,
				CreatedAt:    row.CreatedAt,
			})

			if row.CreatedAt.After(grp.lastRecommendedAt) {
				grp.lastRecommendedAt = row.CreatedAt
			}
		}
	}

	resp := make([]RecommendedStretchResponse, 0, len(groups))
	for _, grp := range groups {
		var stretchResp StretchResponse
		var inCatalog bool

		if grp.catalogStretch != nil {
			stretchResp = ctl.buildStretchResponse(*grp.catalogStretch, aliasMap[grp.catalogStretch.ID])
			inCatalog = true
		} else {
			stretchResp = StretchResponse{
				ID:           0,
				Name:         grp.firstSeenName,
				TargetArea:   grp.firstSeenTarget,
				Description:  "",
				DurationHint: "",
				Caution:      "",
				ImageURL:     "",
				VideoURL:     "",
				Aliases:      []string{},
				CreatedAt:    time.Time{},
				UpdatedAt:    time.Time{},
			}
			inCatalog = false
		}

		resp = append(resp, RecommendedStretchResponse{
			StretchResponse:   stretchResp,
			NormalizedKey:     grp.key,
			InCatalog:         inCatalog,
			SessionCount:      len(grp.sessions),
			LastRecommendedAt: grp.lastRecommendedAt,
			Sessions:          grp.sessions,
		})
	}

	sort.Slice(resp, func(i, j int) bool {
		if !resp[i].LastRecommendedAt.Equal(resp[j].LastRecommendedAt) {
			return resp[i].LastRecommendedAt.After(resp[j].LastRecommendedAt)
		}
		return strings.ToLower(resp[i].Name) < strings.ToLower(resp[j].Name)
	})

	keyParam := strings.TrimSpace(c.Query("key"))
	if keyParam != "" {
		normalizedKeyParam := db.NormalizeStretchKey(keyParam)
		targetKey := normalizedKeyParam
		if s, ok := catalogLookup[normalizedKeyParam]; ok {
			targetKey = s.NormalizedKey
			if targetKey == "" {
				targetKey = db.NormalizeStretchKey(s.Name)
			}
		}

		filtered := make([]RecommendedStretchResponse, 0, 1)
		for _, item := range resp {
			if item.NormalizedKey == targetKey {
				filtered = append(filtered, item)
				break
			}
		}
		resp = filtered
	}

	c.JSON(http.StatusOK, resp)
}
