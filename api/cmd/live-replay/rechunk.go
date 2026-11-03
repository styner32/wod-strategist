package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/storage"
)

// Reuse immutable source provenance and the already cropped sensor data. The
// old video chunks are deliberately not consumed: they are what is being fixed.
func prepareFromFixture(ctx context.Context, opts PrepareOptions) error {
	if strings.TrimSpace(opts.OutputDir) == "" {
		return fmt.Errorf("rechunk requires --wod or --fixture for the destination directory")
	}
	if err := fixtureDestinationAvailable(opts.OutputDir); err != nil {
		return err
	}
	if !finite(opts.ChunkSeconds) || opts.ChunkSeconds <= 0 {
		return fmt.Errorf("chunk seconds must be positive")
	}
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("%s is required to regenerate the replay fixture", binary)
		}
	}
	m, raw, err := loadRechunkSource(opts.FromFixtureDir)
	if err != nil {
		return err
	}
	gen, _ := strconv.ParseInt(m.Video.Generation, 10, 64)
	client, err := storage.NewClient(ctx, m.Bucket)
	if err != nil {
		return fmt.Errorf("create pinned source reader: %w", err)
	}
	parent := filepath.Dir(filepath.Clean(opts.OutputDir))
	if err = os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".live-replay-rechunk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	sourcePath := filepath.Join(stage, "source.mp4")
	ref, err := downloadPinnedObject(ctx, client, m.Video.Object, gen, sourcePath, m.Video.SizeBytes)
	if err != nil {
		return err
	}
	if ref != m.Video {
		return fmt.Errorf("pinned source video no longer matches the saved generation, hash or size")
	}
	duration, fps, err := probeVideo(ctx, sourcePath)
	if err != nil {
		return err
	}
	if duration+0.001 < m.MediaEndSecs || math.Abs(fps-m.FPS) > 0.001 {
		return fmt.Errorf("source video duration or FPS differs from the existing fixture")
	}
	opts.MediaStartSecs, opts.MediaEndSecs = m.MediaStartSecs, m.MediaEndSecs
	chunks, err := prepareVideoChunks(ctx, sourcePath, stage, opts, m.Mapping, m.CaptureStartMs, raw, fps)
	if err != nil {
		return err
	}
	if opts.Name != "" {
		m.Name = opts.Name
	}
	m.Chunks, m.ChunkSeconds, m.PreparedAt = chunks, opts.ChunkSeconds, time.Now().UTC()
	// Keep the measured sensor file byte-for-byte identical, including its hash.
	m.SensorFile = "sensor.ndjson"
	if err = os.WriteFile(filepath.Join(stage, m.SensorFile), raw, 0600); err != nil {
		return err
	}
	if _, err = validateFixture(stage, m); err != nil {
		return fmt.Errorf("validate regenerated fixture: %w", err)
	}
	if err = writeJSON(filepath.Join(stage, "manifest.json"), m); err != nil {
		return err
	}
	if err = os.Remove(sourcePath); err != nil {
		return err
	}
	if err = fixtureDestinationAvailable(opts.OutputDir); err != nil {
		return err
	}
	if err = os.Rename(stage, opts.OutputDir); err != nil {
		return err
	}
	fmt.Printf("Regenerated %d chunks without DB access; preserved %d HR and %d ACC samples at %s\n", len(chunks), m.Stats.HRSamples, m.Stats.ACCSamples, opts.OutputDir)
	return nil
}

func loadRechunkSource(dir string) (FixtureManifest, []byte, error) {
	var m FixtureManifest
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, nil, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, nil, err
	}
	gen, genErr := strconv.ParseInt(m.Video.Generation, 10, 64)
	object := fmt.Sprintf("videos/%d/%s/merged.mp4", m.SourceProfileID, m.SourceSessionID)
	if m.Version != 1 || m.SourceProfileID == 0 || m.SourceSessionID == "" || m.Bucket == "" || m.Video.Object != object || genErr != nil || gen <= 0 || len(m.Video.SHA256) != 64 || m.Video.SizeBytes <= 0 {
		return m, nil, fmt.Errorf("existing fixture has invalid pinned video provenance")
	}
	if err = validateMapping(m.Mapping, m.MediaStartSecs*1000, m.MediaEndSecs*1000); err != nil {
		return m, nil, err
	}
	raw, err := checkedFile(dir, m.SensorFile, m.SensorSHA256, m.SensorSizeBytes)
	if err != nil {
		return m, nil, err
	}
	if _, err = telemetryBaseEpoch(raw, m.SourceProfileID, m.SourceSessionID); err != nil {
		return m, nil, err
	}
	stats, err := telemetryStats(raw)
	if err != nil {
		return m, nil, err
	}
	if err = sameSensorStats(stats, m.Stats); err != nil {
		return m, nil, err
	}
	if err = validatePreparedTelemetry(raw, m.SourceProfileID, m.SourceSessionID); err != nil {
		return m, nil, err
	}
	return m, raw, nil
}
