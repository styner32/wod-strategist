package controllers

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/logger"
	"github.com/wod-strategist/api/internal/storage"
	"go.uber.org/zap"
)

const playURLExpiry = 15 * time.Minute

var workoutSessionPattern = regexp.MustCompile(`^[A-Z]+-\d{4}-\d{2}-\d{2}-\d{2}-\d{2}`)

var highlightVariantLabels = map[string]string{
	"full":        "Highlight Reel",
	"best":        "Best Forms",
	"improvement": "Areas for Improvement",
	"key":         "Key Moments",
}

type sessionAsset struct {
	SessionAssetResponse
	created time.Time
}

type sessionCatalogItem struct {
	SessionCatalogItemResponse
	latestCreatedAt time.Time
}

// @Summary      List Session Catalog
// @Description  Lists existing sessions discovered from uploaded storage assets
// @Tags         dev
// @Produce      json
// @Success      200 {object} SessionCatalogResponse
// @Failure      500 {object} ErrorResponse
// @Router       /dev/sessions [get]
func (ctl *Controller) ListSessionCatalog(c *gin.Context) {
	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}

	catalog, err := ctl.collectSessionCatalog(c.Request.Context())
	if err != nil {
		logger.Log.Error("failed to collect session catalog", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}

	c.JSON(http.StatusOK, catalog)
}

// @Summary      Get Session Assets
// @Description  Lists playable session assets for the browser QA workbench
// @Tags         dev
// @Produce      json
// @Param        session_id path string true "Session ID"
// @Success      200 {object} SessionAssetsResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /dev/sessions/{session_id}/assets [get]
func (ctl *Controller) GetSessionAssets(c *gin.Context) {
	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	response, err := ctl.collectSessionAssets(c.Request.Context(), sessionID)
	if err != nil {
		logger.Log.Error("failed to collect session assets", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch session assets"})
		return
	}

	c.JSON(http.StatusOK, response)
}

// @Summary      Get Session Play URL
// @Description  Resolves a playable signed URL for a session asset
// @Tags         dev
// @Produce      json
// @Param        session_id path string true "Session ID"
// @Param        kind query string true "Asset kind"
// @Param        variant query string false "Asset variant"
// @Success      200 {object} PlayURLResponse
// @Failure      400 {object} ErrorResponse
// @Failure      404 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /dev/sessions/{session_id}/play-url [get]
func (ctl *Controller) GetPlayURL(c *gin.Context) {
	if ctl.storageClient == nil {
		logger.Log.Error("storage client is not configured")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate play URL"})
		return
	}

	sessionID := sanitizeIdentifier(c.Param("session_id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	kind := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	if kind == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind is required"})
		return
	}
	if !isSupportedAssetKind(kind) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid kind"})
		return
	}

	variant := normalizeAssetVariant(kind, c.Query("variant"))

	response, err := ctl.collectSessionAssets(c.Request.Context(), sessionID)
	if err != nil {
		logger.Log.Error("failed to resolve play URL asset list", zap.String("session_id", sessionID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate play URL"})
		return
	}

	asset, ok := findSessionAsset(response.Assets, kind, variant)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "asset not found"})
		return
	}

	signedURL, err := ctl.storageClient.GenerateSignedURL(asset.ObjectName, http.MethodGet, playURLExpiry)
	if err != nil {
		logger.Log.Error("failed to generate playback signed URL",
			zap.String("session_id", sessionID),
			zap.String("kind", kind),
			zap.String("variant", variant),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate play URL"})
		return
	}

	c.JSON(http.StatusOK, PlayURLResponse{
		SessionID: sessionID,
		Kind:      asset.Kind,
		Variant:   asset.Variant,
		GCSURI:    asset.GCSURI,
		PublicURL: asset.PublicURL,
		SignedURL: signedURL,
		ExpiresAt: time.Now().Add(playURLExpiry).UTC().Format(time.RFC3339),
		CacheKey:  asset.CacheKey,
	})
}

func (ctl *Controller) collectSessionAssets(ctx context.Context, sessionID string) (SessionAssetsResponse, error) {
	if ctl.storageClient == nil {
		return SessionAssetsResponse{}, fmt.Errorf("storage client is not configured")
	}
	if ctl.analysisResults == nil {
		return SessionAssetsResponse{}, fmt.Errorf("analysis results repository is not configured")
	}
	if ctl.highlightResults == nil {
		return SessionAssetsResponse{}, fmt.Errorf("highlight results repository is not configured")
	}

	analysisResults, err := ctl.analysisResults.FindBySessionID(ctx, sessionID)
	if err != nil {
		return SessionAssetsResponse{}, err
	}

	chunkResults, err := ctl.analysisResults.FindChunksBySessionID(ctx, sessionID)
	if err != nil {
		return SessionAssetsResponse{}, err
	}

	highlightResults, err := ctl.highlightResults.FindBySessionID(ctx, sessionID)
	if err != nil {
		return SessionAssetsResponse{}, err
	}

	objectInfos, err := ctl.storageClient.ListObjectInfos(ctx, fmt.Sprintf("videos/%s", sessionID))
	if err != nil {
		return SessionAssetsResponse{}, err
	}

	assets := buildVideoAssets(sessionID, ctl.bucketName, objectInfos)
	assets = append(assets, buildHighlightAssets(sessionID, highlightResults)...)

	responseAssets := make([]SessionAssetResponse, len(assets))
	for i, asset := range assets {
		responseAssets[i] = asset.SessionAssetResponse
	}

	return SessionAssetsResponse{
		SessionID:         sessionID,
		SubtitleAvailable: hasSubtitleContent(chunkResults),
		ChunkSummary:      summarizeChunks(chunkResults),
		FullAnalysis:      latestFullAnalysisStatus(analysisResults),
		Assets:            responseAssets,
	}, nil
}

func (ctl *Controller) collectSessionCatalog(ctx context.Context) (SessionCatalogResponse, error) {
	videoInfos, err := ctl.storageClient.ListObjectInfos(ctx, "videos/")
	if err != nil {
		return SessionCatalogResponse{}, err
	}

	highlightInfos, err := ctl.storageClient.ListObjectInfos(ctx, "highlights/")
	if err != nil {
		return SessionCatalogResponse{}, err
	}

	catalog := map[string]*sessionCatalogItem{}

	recordAsset := func(sessionID string, createdAt time.Time, update func(*sessionCatalogItem)) {
		if sessionID == "" {
			return
		}

		item, ok := catalog[sessionID]
		if !ok {
			item = &sessionCatalogItem{
				SessionCatalogItemResponse: SessionCatalogItemResponse{
					SessionID: sessionID,
				},
			}
			catalog[sessionID] = item
		}

		if createdAt.After(item.latestCreatedAt) {
			item.latestCreatedAt = createdAt
			item.LatestCreatedAt = createdAt.UTC().Format(time.RFC3339)
		}

		update(item)
	}

	for _, info := range videoInfos {
		sessionID := sessionIDFromObjectName(info.Name)
		if sessionID == "" {
			continue
		}

		base := filepath.Base(info.Name)
		recordAsset(sessionID, info.Created, func(item *sessionCatalogItem) {
			switch {
			case strings.Contains(base, "_merged_"):
				item.HasMerged = true
			case strings.Contains(base, "_hardsubbed_"):
				item.HasHardsubbed = true
			default:
				item.ChunkCount++
			}
		})
	}

	for _, info := range highlightInfos {
		base := filepath.Base(info.Name)
		if strings.Contains(base, "_music_") {
			continue
		}

		sessionID := sessionIDFromObjectName(info.Name)
		if sessionID == "" {
			continue
		}

		recordAsset(sessionID, info.Created, func(item *sessionCatalogItem) {
			item.HighlightCount++
		})
	}

	items := make([]sessionCatalogItem, 0, len(catalog))
	for _, item := range catalog {
		items = append(items, *item)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].latestCreatedAt.After(items[j].latestCreatedAt)
	})

	response := SessionCatalogResponse{
		Sessions: make([]SessionCatalogItemResponse, len(items)),
	}
	for i, item := range items {
		response.Sessions[i] = item.SessionCatalogItemResponse
	}

	return response, nil
}

func summarizeChunks(results []db.ChunkAnalysisResult) ChunkAnalysisSummaryResponse {
	summary := ChunkAnalysisSummaryResponse{Total: len(results)}
	for _, result := range results {
		switch strings.ToUpper(strings.TrimSpace(result.Status)) {
		case "COMPLETED":
			summary.Completed++
		case "FAILED":
			summary.Failed++
		default:
			summary.Pending++
		}
	}
	return summary
}

func latestFullAnalysisStatus(results []db.AnalysisResult) *FullAnalysisStatusResponse {
	if len(results) == 0 {
		return nil
	}

	sorted := append([]db.AnalysisResult(nil), results...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	var chosen *db.AnalysisResult
	for i := len(sorted) - 1; i >= 0; i-- {
		if strings.TrimSpace(sorted[i].AnalysisType) == "" || sorted[i].AnalysisType == db.AnalysisTypeWOD {
			chosen = &sorted[i]
			break
		}
	}
	if chosen == nil {
		chosen = &sorted[len(sorted)-1]
	}

	return &FullAnalysisStatusResponse{
		AnalysisType: chosen.AnalysisType,
		Status:       chosen.Status,
		CreatedAt:    chosen.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    chosen.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func hasSubtitleContent(chunks []db.ChunkAnalysisResult) bool {
	for _, chunk := range chunks {
		if strings.EqualFold(strings.TrimSpace(chunk.Status), "COMPLETED") &&
			chunk.StartSecs != nil &&
			chunk.EndSecs != nil &&
			strings.TrimSpace(chunk.Output) != "" {
			return true
		}
	}
	return false
}

func buildVideoAssets(sessionID string, bucketName string, infos []storage.ObjectInfo) []sessionAsset {
	var chunkAssets []sessionAsset
	var mergedAsset *sessionAsset
	var hardsubbedAsset *sessionAsset

	for _, info := range infos {
		base := filepath.Base(info.Name)
		if !strings.HasPrefix(base, sessionID+"_") {
			continue
		}

		switch {
		case strings.Contains(base, "_merged_"):
			asset := makeSessionAssetFromObjectInfo(sessionID, bucketName, "merged", "latest", "Merged Video", info)
			mergedAsset = chooseLatestAsset(mergedAsset, asset)
		case strings.Contains(base, "_hardsubbed_"):
			asset := makeSessionAssetFromObjectInfo(sessionID, bucketName, "hardsubbed", "latest", "Hardsubbed Video", info)
			hardsubbedAsset = chooseLatestAsset(hardsubbedAsset, asset)
		default:
			variant := strconv.Itoa(len(chunkAssets))
			label := chunkAssetLabel(base, len(chunkAssets)+1)
			chunkAssets = append(chunkAssets, makeSessionAssetFromObjectInfo(sessionID, bucketName, "chunk", variant, label, info))
		}
	}

	assets := append([]sessionAsset(nil), chunkAssets...)
	if mergedAsset != nil {
		assets = append(assets, *mergedAsset)
	}
	if hardsubbedAsset != nil {
		assets = append(assets, *hardsubbedAsset)
	}
	return assets
}

func sessionIDFromObjectName(objectName string) string {
	base := filepath.Base(objectName)
	if match := workoutSessionPattern.FindString(base); match != "" {
		return match
	}

	for _, marker := range []string{"_merged_", "_hardsubbed_", "_hl_", "_music_", "_chunk_"} {
		if idx := strings.Index(base, marker); idx > 0 {
			return base[:idx]
		}
	}

	if idx := strings.Index(base, "_"); idx > 0 {
		return base[:idx]
	}

	return ""
}

func makeSessionAssetFromObjectInfo(sessionID, bucketName, kind, variant, label string, info storage.ObjectInfo) sessionAsset {
	return sessionAsset{
		SessionAssetResponse: SessionAssetResponse{
			Kind:       kind,
			Variant:    variant,
			Label:      label,
			GCSURI:     fmt.Sprintf("gs://%s/%s", bucketName, info.Name),
			PublicURL:  publicObjectURL(bucketName, info.Name),
			ObjectName: info.Name,
			CacheKey:   buildCacheKey(sessionID, kind, variant, info.Name),
			CreatedAt:  info.Created.UTC().Format(time.RFC3339),
		},
		created: info.Created,
	}
}

func chooseLatestAsset(current *sessionAsset, candidate sessionAsset) *sessionAsset {
	if current == nil || candidate.created.After(current.created) {
		next := candidate
		return &next
	}
	return current
}

func buildHighlightAssets(sessionID string, results []db.HighlightResult) []sessionAsset {
	latestByVariant := map[string]db.HighlightResult{}
	for _, result := range results {
		if !strings.EqualFold(strings.TrimSpace(result.Status), "COMPLETED") {
			continue
		}
		variant := highlightVariantForResult(result)
		if variant == "" || strings.TrimSpace(result.GCSURI) == "" {
			continue
		}
		current, ok := latestByVariant[variant]
		if !ok || result.CreatedAt.After(current.CreatedAt) {
			latestByVariant[variant] = result
		}
	}

	orderedVariants := []string{"full", "best", "improvement", "key"}
	assets := make([]sessionAsset, 0, len(orderedVariants))
	for _, variant := range orderedVariants {
		result, ok := latestByVariant[variant]
		if !ok {
			continue
		}

		_, objectName, err := storage.ParseGCSURI(result.GCSURI)
		if err != nil {
			continue
		}

		label := strings.TrimSpace(result.Title)
		if label == "" {
			label = highlightVariantLabels[variant]
		}

		assets = append(assets, sessionAsset{
			SessionAssetResponse: SessionAssetResponse{
				Kind:       "highlight",
				Variant:    variant,
				Label:      label,
				GCSURI:     result.GCSURI,
				PublicURL:  publicObjectURLFromGCSURI(result.GCSURI),
				ObjectName: objectName,
				CacheKey:   buildCacheKey(sessionID, "highlight", variant, objectName),
				CreatedAt:  result.CreatedAt.UTC().Format(time.RFC3339),
			},
			created: result.CreatedAt,
		})
	}

	return assets
}

func highlightVariantForResult(result db.HighlightResult) string {
	title := strings.ToLower(strings.TrimSpace(result.Title))
	switch title {
	case "highlight reel":
		return "full"
	case "best forms":
		return "best"
	case "areas for improvement":
		return "improvement"
	case "key moments":
		return "key"
	}

	_, objectName, err := storage.ParseGCSURI(result.GCSURI)
	if err != nil {
		return ""
	}

	base := filepath.Base(objectName)
	switch {
	case strings.Contains(base, "_hl_full_"):
		return "full"
	case strings.Contains(base, "_hl_best_"):
		return "best"
	case strings.Contains(base, "_hl_improvement_"):
		return "improvement"
	case strings.Contains(base, "_hl_key_"):
		return "key"
	default:
		return ""
	}
}

func buildCacheKey(sessionID, kind, variant, objectName string) string {
	cacheVariant := variant
	if cacheVariant == "" {
		cacheVariant = "default"
	}
	return fmt.Sprintf("video-qa/%s/%s/%s/%s", sessionID, kind, cacheVariant, objectName)
}

func chunkAssetLabel(base string, ordinal int) string {
	switch {
	case strings.Contains(base, "_vid_") && strings.Contains(base, "_encoded"):
		return "Uploaded Video"
	case strings.Contains(base, "_vid_"):
		return "Uploaded Video"
	default:
		return fmt.Sprintf("Chunk %d", ordinal)
	}
}

func publicObjectURL(bucketName, objectName string) string {
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectName)
}

func publicObjectURLFromGCSURI(raw string) string {
	bucket, object, err := storage.ParseGCSURI(raw)
	if err != nil {
		return ""
	}
	return publicObjectURL(bucket, object)
}

func normalizeAssetVariant(kind, raw string) string {
	variant := strings.TrimSpace(raw)
	switch kind {
	case "merged", "hardsubbed":
		if variant == "" {
			return "latest"
		}
	case "chunk":
		return variant
	case "highlight":
		return strings.ToLower(variant)
	}
	return variant
}

func isSupportedAssetKind(kind string) bool {
	switch kind {
	case "chunk", "merged", "hardsubbed", "highlight":
		return true
	default:
		return false
	}
}

func findSessionAsset(assets []SessionAssetResponse, kind, variant string) (SessionAssetResponse, bool) {
	for _, asset := range assets {
		if asset.Kind != kind {
			continue
		}

		if kind == "chunk" {
			if asset.Variant == variant || filepath.Base(asset.ObjectName) == variant {
				return asset, true
			}
			continue
		}

		if normalizeAssetVariant(kind, asset.Variant) == normalizeAssetVariant(kind, variant) {
			return asset, true
		}
	}

	return SessionAssetResponse{}, false
}
