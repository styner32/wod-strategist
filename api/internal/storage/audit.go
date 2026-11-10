package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

type ObjectCategory string

const (
	CategorySource       ObjectCategory = "source"
	CategoryPrepared     ObjectCategory = "prepared"
	CategoryMergeStaging ObjectCategory = "merge_staging"
	CategoryPlayback     ObjectCategory = "playback"
	CategoryHardsub      ObjectCategory = "hardsub"
	CategoryOther        ObjectCategory = "other"
)

type ReferenceStatus string

const (
	ReferenceReferenced   ReferenceStatus = "referenced"
	ReferenceUnreferenced ReferenceStatus = "unreferenced"
	ReferenceAmbiguous    ReferenceStatus = "ambiguous"
)

type AuditBucketSummary struct {
	ObjectCount int   `json:"object_count"`
	TotalBytes  int64 `json:"total_bytes"`
}

type AuditReport struct {
	Bucket       string                                                    `json:"bucket"`
	Prefix       string                                                    `json:"prefix"`
	TotalObjects int                                                       `json:"total_objects"`
	TotalBytes   int64                                                     `json:"total_bytes"`
	ByCategory   map[ObjectCategory]AuditBucketSummary                     `json:"by_category"`
	ByReference  map[ReferenceStatus]AuditBucketSummary                    `json:"by_reference"`
	Matrix       map[ObjectCategory]map[ReferenceStatus]AuditBucketSummary `json:"matrix"`
}

func ClassifyObjectCategory(objectName string) ObjectCategory {
	trimmed := strings.TrimSpace(objectName)
	lower := strings.ToLower(trimmed)
	parts := strings.Split(trimmed, "/")
	// Expected canonical layout: videos/{pid}/{sid}/{rest...}
	var rel string
	if len(parts) >= 4 && parts[0] == "videos" {
		rel = strings.Join(parts[3:], "/")
	} else {
		rel = trimmed
	}
	relLower := strings.ToLower(rel)

	switch {
	case strings.HasPrefix(relLower, "prepared/") || strings.Contains(lower, "/prepared/"):
		return CategoryPrepared
	case strings.HasPrefix(relLower, "staging/") || strings.Contains(lower, "/staging/") || strings.Contains(relLower, "merge-staging") || strings.Contains(relLower, "merge_staging") || strings.Contains(relLower, "merged.staging"):
		return CategoryMergeStaging
	case strings.Contains(relLower, "hardsub"):
		return CategoryHardsub
	case relLower == "merged.mp4" || strings.HasPrefix(relLower, "highlights/") || strings.Contains(lower, "/highlights/"):
		return CategoryPlayback
	case strings.HasPrefix(relLower, "chunk") || strings.HasPrefix(relLower, "raw/") || strings.HasPrefix(relLower, "sensor/") || strings.HasSuffix(relLower, ".mp4") && !strings.Contains(relLower, "/"):
		return CategorySource
	default:
		return CategoryOther
	}
}

func parseVideosSessionKey(objectName string) (uint, string, string, bool) {
	parts := strings.Split(strings.TrimSpace(objectName), "/")
	if len(parts) < 4 || parts[0] != "videos" {
		return 0, "", "", false
	}
	pid64, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || pid64 == 0 || strings.TrimSpace(parts[2]) == "" {
		return 0, "", "", false
	}
	return uint(pid64), parts[2], strings.Join(parts[3:], "/"), true
}

func normalizeObjectRef(bucket, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "gs://") {
		b, obj, err := ParseGCSURI(trimmed)
		if err != nil {
			return ""
		}
		if bucket != "" && b != bucket {
			return ""
		}
		return obj
	}
	return strings.TrimPrefix(trimmed, "/")
}

// AuditBucketStorage performs a strictly read-only measurement of GCS objects under
// prefix against PostgreSQL metadata. It NEVER deletes, modifies, or moves any GCS
// object or DB row (including ambiguous or unreferenced files).
func AuditBucketStorage(ctx context.Context, client *Client, conn *gorm.DB, prefix string) (AuditReport, error) {
	if strings.TrimSpace(prefix) == "" {
		prefix = "videos/"
	}
	bucket := ""
	var objects []SizedObjectInfo
	if client != nil {
		bucket = client.BucketName()
		var err error
		objects, err = client.ListSizedObjectInfos(ctx, prefix)
		if err != nil {
			return AuditReport{}, err
		}
	}
	return AuditObjectInfosAgainstDB(ctx, bucket, prefix, objects, conn)
}

// AuditObjectInfosAgainstDB classifies pre-listed object metadata against read-only DB queries.
func AuditObjectInfosAgainstDB(ctx context.Context, bucket, prefix string, objects []SizedObjectInfo, conn *gorm.DB) (AuditReport, error) {
	report := AuditReport{
		Bucket:      bucket,
		Prefix:      prefix,
		ByCategory:  map[ObjectCategory]AuditBucketSummary{},
		ByReference: map[ReferenceStatus]AuditBucketSummary{},
		Matrix:      map[ObjectCategory]map[ReferenceStatus]AuditBucketSummary{},
	}
	for _, cat := range []ObjectCategory{CategorySource, CategoryPrepared, CategoryMergeStaging, CategoryPlayback, CategoryHardsub, CategoryOther} {
		report.ByCategory[cat] = AuditBucketSummary{}
		report.Matrix[cat] = map[ReferenceStatus]AuditBucketSummary{
			ReferenceReferenced:   {},
			ReferenceUnreferenced: {},
			ReferenceAmbiguous:    {},
		}
	}
	for _, st := range []ReferenceStatus{ReferenceReferenced, ReferenceUnreferenced, ReferenceAmbiguous} {
		report.ByReference[st] = AuditBucketSummary{}
	}

	referencedObjects := map[string]bool{}
	knownSessions := map[string]bool{}
	activeRunSessions := map[string]bool{}

	addRef := func(raw string) {
		if obj := normalizeObjectRef(bucket, raw); obj != "" {
			referencedObjects[obj] = true
		}
	}
	sessionKey := func(pid uint, sid string) string {
		return fmt.Sprintf("%d/%s", pid, strings.TrimSpace(sid))
	}

	if conn != nil {
		sessionHasTerminalOwner := map[string]bool{}
		startedSessions := map[string]bool{}

		var sessions []db.Session
		if err := conn.WithContext(ctx).Select("profile_id", "session_id", "status").Find(&sessions).Error; err != nil {
			return report, err
		}
		for _, s := range sessions {
			key := sessionKey(s.ProfileID, s.SessionID)
			knownSessions[key] = true
			if s.Status == db.SessionStatusStarted {
				startedSessions[key] = true
			}
		}

		var analyses []db.AnalysisResult
		if err := conn.WithContext(ctx).Select("profile_id", "session_id", "status", "available_videos", "sensor_processing").Find(&analyses).Error; err != nil {
			return report, err
		}
		for _, a := range analyses {
			key := sessionKey(a.ProfileID, a.SessionID)
			knownSessions[key] = true
			upperStatus := strings.ToUpper(strings.TrimSpace(a.Status))
			if upperStatus == "COMPLETED" || upperStatus == "FAILED" {
				sessionHasTerminalOwner[key] = true
			} else if upperStatus != "" {
				activeRunSessions[key] = true
			}
			for _, v := range a.AvailableVideos {
				switch strings.ToLower(strings.TrimSpace(v)) {
				case "merged":
					addRef(fmt.Sprintf("videos/%d/%s/merged.mp4", a.ProfileID, a.SessionID))
				case "hardsub":
					addRef(fmt.Sprintf("videos/%d/%s/hardsub.mp4", a.ProfileID, a.SessionID))
				}
			}
			if len(a.SensorProcessing) > 0 {
				var sp struct {
					Object string `json:"object"`
				}
				if json.Unmarshal(a.SensorProcessing, &sp) == nil && sp.Object != "" {
					addRef(sp.Object)
				}
			}
		}

		var runs []db.RecordingMediaRun
		if err := conn.WithContext(ctx).Select("profile_id", "session_id", "state", "merged_object", "merged_staging_object").Find(&runs).Error; err != nil {
			return report, err
		}
		for _, r := range runs {
			key := sessionKey(r.ProfileID, r.SessionID)
			knownSessions[key] = true
			upperState := strings.ToUpper(strings.TrimSpace(r.State))
			isTerminalRun := upperState == "COMPLETED" || upperState == "FAILED" || upperState == "CANCELLED" || upperState == "LEGACY"
			if isTerminalRun {
				sessionHasTerminalOwner[key] = true
			}
			if r.MergedObject != "" {
				addRef(r.MergedObject)
			}
			if r.MergedStagingObject != "" && !isTerminalRun {
				addRef(r.MergedStagingObject)
			}
			if !isTerminalRun && upperState != "" {
				activeRunSessions[key] = true
			}
		}

		for key := range startedSessions {
			if !sessionHasTerminalOwner[key] {
				activeRunSessions[key] = true
			}
		}

		var recChunks []db.RecordingChunk
		if err := conn.WithContext(ctx).Select("profile_id", "session_id", "source_object", "source_uri").Find(&recChunks).Error; err != nil {
			return report, err
		}
		for _, rc := range recChunks {
			knownSessions[sessionKey(rc.ProfileID, rc.SessionID)] = true
			addRef(rc.SourceObject)
			addRef(rc.SourceURI)
		}

		var preps []db.ChunkMediaPreparation
		if err := conn.WithContext(ctx).Select("output_object", "state").Find(&preps).Error; err != nil {
			return report, err
		}
		for _, p := range preps {
			if p.OutputObject != "" {
				addRef(p.OutputObject)
			}
		}

		var analysisPreps []db.AnalysisVideoPreparation
		if err := conn.WithContext(ctx).Select("output_object", "state").Find(&analysisPreps).Error; err != nil {
			return report, err
		}
		for _, ap := range analysisPreps {
			if ap.OutputObject != "" {
				addRef(ap.OutputObject)
			}
		}

		var chunkResults []db.ChunkAnalysisResult
		if err := conn.WithContext(ctx).Select("profile_id", "session_id", "file_path", "target_cues").Find(&chunkResults).Error; err != nil {
			return report, err
		}
		for _, cr := range chunkResults {
			knownSessions[sessionKey(cr.ProfileID, cr.SessionID)] = true
			addRef(cr.FilePath)
			if strings.TrimSpace(cr.TargetCues) != "" {
				var tp struct {
					ReferenceImage string `json:"reference_image"`
				}
				if json.Unmarshal([]byte(cr.TargetCues), &tp) == nil && tp.ReferenceImage != "" {
					addRef(tp.ReferenceImage)
				}
			}
		}

		var highlights []db.HighlightResult
		if err := conn.WithContext(ctx).Select("profile_id", "session_id", "gcs_uri", "music_gcs_uri").Find(&highlights).Error; err != nil {
			return report, err
		}
		for _, hl := range highlights {
			knownSessions[sessionKey(hl.ProfileID, hl.SessionID)] = true
			addRef(hl.GCSURI)
			addRef(hl.MusicGCSURI)
		}
	}

	for _, obj := range objects {
		cat := ClassifyObjectCategory(obj.Name)
		var refStatus ReferenceStatus
		if conn == nil {
			refStatus = ReferenceAmbiguous
		} else if referencedObjects[obj.Name] {
			refStatus = ReferenceReferenced
		} else {
			pid, sid, rel, ok := parseVideosSessionKey(obj.Name)
			if !ok {
				refStatus = ReferenceAmbiguous
			} else {
				sKey := sessionKey(pid, sid)
				if !knownSessions[sKey] {
					refStatus = ReferenceUnreferenced
				} else if activeRunSessions[sKey] {
					refStatus = ReferenceAmbiguous
				} else if cat == CategoryMergeStaging {
					refStatus = ReferenceUnreferenced
				} else if strings.EqualFold(rel, "merged.mp4") || strings.Contains(strings.ToLower(rel), "hardsub") {
					// Session exists in DB even if available_videos omitted the tag; treat as referenced/ambiguous safely.
					refStatus = ReferenceAmbiguous
				} else {
					refStatus = ReferenceUnreferenced
				}
			}
		}

		report.TotalObjects++
		report.TotalBytes += obj.Size

		catSum := report.ByCategory[cat]
		catSum.ObjectCount++
		catSum.TotalBytes += obj.Size
		report.ByCategory[cat] = catSum

		refSum := report.ByReference[refStatus]
		refSum.ObjectCount++
		refSum.TotalBytes += obj.Size
		report.ByReference[refStatus] = refSum

		cell := report.Matrix[cat][refStatus]
		cell.ObjectCount++
		cell.TotalBytes += obj.Size
		report.Matrix[cat][refStatus] = cell
	}

	return report, nil
}
