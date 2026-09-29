package controllers

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/worker"
	"net/http"
	"strings"
)

func (ctl *Controller) enrichmentWorker() *worker.Worker {
	w := worker.NewWorker(ctl.db, nil, "", nil, ctl.queueClient, logger.Log)
	w.AgenticHighlightsEnabled = ctl.enableAgenticHighlights
	return w
}
func (ctl *Controller) GetAgenticHighlights(c *gin.Context) {
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}
	var a db.AnalysisResult
	if err := ctl.db.WithContext(c.Request.Context()).Where("session_id = ? AND profile_id = ?", sessionID, profileID).First(&a).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "analysis not found"})
		return
	}
	var state worker.AgenticHighlights
	_ = json.Unmarshal(a.AgenticHighlightAnalysis, &state)
	if state.RunID != "" && !ctl.enrichmentWorker().AgenticSourceCurrent(a) {
		state.Status = "stale"
		state.Items = nil
		state.Error = "원본이 변경되어 이전 결과를 현재 구간에 연결하지 않습니다."
	}
	state.FileURI = ""
	state.FileName = ""
	state.MIMEType = ""
	state.OwnedUpload = false
	state.CleanupPending = false
	state.CleanupAttempts = 0
	c.JSON(http.StatusOK, gin.H{"enabled": ctl.enableAgenticHighlights, "analysis": state, "summary": a.AnalysisSummary})
}
func (ctl *Controller) CreateAgenticHighlights(c *gin.Context) { ctl.createEnrichment(c, true) }
func (ctl *Controller) CreateAnalysisSummary(c *gin.Context)   { ctl.createEnrichment(c, false) }
func (ctl *Controller) createEnrichment(c *gin.Context, agentic bool) {
	sessionID, ok := feedbackSessionID(c)
	if !ok {
		return
	}
	profileID, ok := ctl.resolveFeedbackSession(c, sessionID)
	if !ok {
		return
	}
	var owned db.AnalysisResult
	if err := ctl.db.WithContext(c.Request.Context()).Where("session_id = ? AND profile_id = ?", sessionID, profileID).First(&owned).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "analysis not found"})
		return
	}
	if agentic && !ctl.enableAgenticHighlights {
		c.JSON(http.StatusConflict, gin.H{"error": "Agentic highlight analysis is disabled"})
		return
	}
	if ctl.queueClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "analysis queue unavailable"})
		return
	}
	id, err := ctl.enrichmentWorker().ScheduleAnalysisEnrichment(c.Request.Context(), sessionID, agentic, true)
	if errors.Is(err, worker.ErrEnrichmentCleanupPending) {
		c.JSON(http.StatusConflict, gin.H{"error": "이전 추가 분석의 업로드를 정리하는 중입니다. 잠시 후 다시 시도하세요."})
		return
	}
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "completed analysis with valid highlights required"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"run_id": id})
}

func compactAnalysisSummaries(results []db.AnalysisResult) []db.AnalysisResult {
	for i := range results {
		var s worker.AnalysisSummary
		if json.Unmarshal(results[i].AnalysisSummary, &s) == nil {
			// Omit empty fields so clients fall back to the legacy overall_summary.
			compact := map[string]any{}
			if s.Status != "" {
				compact["status"] = s.Status
			}
			overview := ""
			if s.Result != nil {
				overview = s.Result.Overview
			}
			if strings.TrimSpace(overview) == "" && s.LastSuccess != nil {
				overview = s.LastSuccess.Overview
			}
			if strings.TrimSpace(overview) != "" {
				compact["result"] = map[string]string{"overview": overview}
			}
			raw, _ := json.Marshal(compact)
			results[i].AnalysisSummary = db.JSONDocument(raw)
		}
	}
	return results
}
