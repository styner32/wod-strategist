package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultSourceSession       = "WOD-20261028-01M602PXNAAZFKKNKXNSN2D61S"
	defaultMediaStartSecs      = 290.0
	defaultMediaEndSecs        = 714.7
	defaultChunkSeconds        = 10.0
	defaultFixtureDir          = "tmp/live-replay/fixture"
	targetProfileID       uint = 10
)

type MappingSegment struct {
	CaptureStartMs float64 `json:"capture_start_ms"`
	CaptureEndMs   float64 `json:"capture_end_ms"`
	MediaStartMs   float64 `json:"media_start_ms"`
	MediaEndMs     float64 `json:"media_end_ms"`
}

type ObjectReference struct {
	Object     string `json:"object"`
	Generation string `json:"generation"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
}

type SensorStats struct {
	DurationMs float64  `json:"duration_ms"`
	HRSamples  int      `json:"hr_samples"`
	ACCSamples int      `json:"acc_samples"`
	FirstHRMs  *float64 `json:"first_hr_ms,omitempty"`
	LastHRMs   *float64 `json:"last_hr_ms,omitempty"`
	FirstACCMs *float64 `json:"first_acc_ms,omitempty"`
	LastACCMs  *float64 `json:"last_acc_ms,omitempty"`
}

type ReplayChunk struct {
	File             string  `json:"file"`
	SHA256           string  `json:"sha256"`
	SizeBytes        int64   `json:"size_bytes"`
	MediaStartSecs   float64 `json:"media_start_secs"`
	MediaEndSecs     float64 `json:"media_end_secs"`
	CaptureStartSecs float64 `json:"capture_start_secs"`
	CaptureEndSecs   float64 `json:"capture_end_secs"`
	HeartRateBPM     int     `json:"heart_rate_bpm"`
}

type FixtureManifest struct {
	Version         int              `json:"version"`
	Name            string           `json:"name,omitempty"`
	PreparedAt      time.Time        `json:"prepared_at"`
	SourceSessionID string           `json:"source_session_id"`
	SourceProfileID uint             `json:"source_profile_id"`
	Bucket          string           `json:"bucket"`
	Video           ObjectReference  `json:"video_source"`
	Sensor          ObjectReference  `json:"sensor_source"`
	MediaStartSecs  float64          `json:"media_start_secs"`
	MediaEndSecs    float64          `json:"media_end_secs"`
	ChunkSeconds    float64          `json:"chunk_seconds,omitempty"`
	CaptureStartMs  float64          `json:"capture_start_ms"`
	CaptureEndMs    float64          `json:"capture_end_ms"`
	FPS             float64          `json:"fps"`
	Mapping         []MappingSegment `json:"source_mapping"`
	WODDescription  string           `json:"wod_description"`
	Movements       []string         `json:"movements"`
	AppearanceHints string           `json:"appearance_hints,omitempty"`
	SensorFile      string           `json:"sensor_file"`
	SensorSHA256    string           `json:"sensor_sha256"`
	SensorSizeBytes int64            `json:"sensor_size_bytes"`
	Stats           SensorStats      `json:"sensor_stats"`
	Chunks          []ReplayChunk    `json:"chunks"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeJSON(filename string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := filename + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(temp, filename)
}

func fixturePath(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || !filepath.IsLocal(name) {
		return "", fmt.Errorf("invalid fixture path %q", name)
	}
	return filepath.Join(dir, name), nil
}

func checkedFile(dir, name, hash string, size int64) ([]byte, error) {
	filename, err := fixturePath(dir, name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size || digest(data) != hash {
		return nil, fmt.Errorf("fixture file integrity mismatch: %s; run prepare again", name)
	}
	return data, nil
}
