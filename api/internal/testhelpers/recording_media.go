package testhelpers

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	g "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"gorm.io/gorm"
)

func CreateRecordingMediaRun(conn *gorm.DB, attr *db.RecordingMediaRun) db.RecordingMediaRun {
	run := *attr
	if run.ProfileID == 0 {
		run.ProfileID = CreateProfile(conn, &db.Profile{}).ID
	}
	if run.SessionID == "" {
		run.SessionID = "WOD-20261103-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if run.State == "" {
		run.State = "LEGACY"
		if run.Version == db.RecordingMediaVersion {
			run.State = "RECORDING"
		}
	}
	if len(run.Manifest) == 0 {
		run.Manifest = db.JSONDocument(`[]`)
	}
	if len(run.Intervals) == 0 {
		run.Intervals = db.JSONDocument(`[]`)
	}
	g.Expect(conn.Create(&run).Error).To(g.Succeed())
	return run
}

func CreateRecordingChunk(conn *gorm.DB, attr *db.RecordingChunk) db.RecordingChunk {
	chunk := *attr
	var run db.RecordingMediaRun
	if chunk.RunID == 0 {
		run = CreateRecordingMediaRun(conn, &db.RecordingMediaRun{ProfileID: chunk.ProfileID, SessionID: chunk.SessionID, Version: db.RecordingMediaVersion})
		chunk.RunID = run.ID
	} else {
		g.Expect(conn.First(&run, chunk.RunID).Error).To(g.Succeed())
	}
	if chunk.ProfileID == 0 {
		chunk.ProfileID = run.ProfileID
	}
	if chunk.SessionID == "" {
		chunk.SessionID = run.SessionID
	}
	if chunk.ClientChunkID == "" {
		chunk.ClientChunkID = uuid.NewString()
	}
	if chunk.CaptureOrder == 0 {
		var latest int
		g.Expect(conn.Model(&db.RecordingChunk{}).Where("run_id = ?", run.ID).Select("COALESCE(MAX(capture_order), 0)").Scan(&latest).Error).To(g.Succeed())
		chunk.CaptureOrder = latest + 1
	}
	if chunk.SourceObject == "" {
		chunk.SourceObject = fmt.Sprintf("videos/%d/%s/%s.mp4", chunk.ProfileID, chunk.SessionID, chunk.ClientChunkID)
	}
	if chunk.SourceURI == "" {
		chunk.SourceURI = "gs://test-bucket/" + chunk.SourceObject
	}
	if chunk.SourceGeneration == 0 {
		chunk.SourceGeneration = 1
	}
	if chunk.SourceBytes == 0 {
		chunk.SourceBytes = 1024
	}
	if chunk.CaptureEndSecs == 0 {
		chunk.CaptureEndSecs = chunk.CaptureStartSecs + 10
	}
	if chunk.AIType == "" {
		chunk.AIType = "chunk:analysis"
	}
	g.Expect(conn.Create(&chunk).Error).To(g.Succeed())
	return chunk
}

func CreateChunkMediaPreparation(conn *gorm.DB, attr *db.ChunkMediaPreparation) db.ChunkMediaPreparation {
	prep := *attr
	var chunk db.RecordingChunk
	if prep.ChunkID == 0 {
		chunk = CreateRecordingChunk(conn, &db.RecordingChunk{RunID: prep.RunID})
		prep.ChunkID = chunk.ID
	} else {
		g.Expect(conn.First(&chunk, prep.ChunkID).Error).To(g.Succeed())
	}
	if prep.RunID == 0 {
		prep.RunID = chunk.RunID
	}
	if prep.SourceGeneration == 0 {
		prep.SourceGeneration = chunk.SourceGeneration
	}
	if prep.RecipeVersion == "" {
		prep.RecipeVersion = "concat-v1"
	}
	if prep.ProfileKey == "" {
		prep.ProfileKey = "native"
	}
	if prep.State == "" {
		prep.State = "PENDING"
	}
	g.Expect(conn.Create(&prep).Error).To(g.Succeed())
	return prep
}

func CreateAnalysisVideoPreparation(conn *gorm.DB, attr *db.AnalysisVideoPreparation) db.AnalysisVideoPreparation {
	prep := *attr
	var run db.RecordingMediaRun
	if prep.RunID == 0 {
		run = CreateRecordingMediaRun(conn, &db.RecordingMediaRun{Version: db.RecordingMediaVersion})
		prep.RunID = run.ID
	} else {
		g.Expect(conn.First(&run, prep.RunID).Error).To(g.Succeed())
	}
	if prep.SourceObject == "" {
		prep.SourceObject = fmt.Sprintf("videos/%d/%s/merged.mp4", run.ProfileID, run.SessionID)
	}
	if prep.SourceGeneration == 0 {
		prep.SourceGeneration = 1
	}
	if prep.PresetVersion == "" {
		prep.PresetVersion = "analysis-v1"
	}
	if prep.State == "" {
		prep.State = "PENDING"
	}
	g.Expect(conn.Create(&prep).Error).To(g.Succeed())
	return prep
}
