package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wod-strategist/api/internal/gemini"
	"golang.org/x/image/font"
	"golang.org/x/image/font/inconsolata"
	"golang.org/x/image/math/fixed"
)

const (
	defaultFrameGridFPS         = 4.0
	minFrameGridFPS             = 0.25
	defaultFrameGridCols        = 8
	defaultFrameGridRows        = 4
	defaultFrameGridMaxSheets   = 12
	defaultFrameGridTileWidth   = 240
	defaultFrameGridJPEGQuality = 85
	defaultFrameGridBorderPx    = 2

	// Below this rate a contact sheet no longer shows individual repetitions.
	// Longer reanalysis intervals use VideoMetadata video analysis instead.
	minReanalysisFrameGridFPS = 2.0
	// Each extraction holds up to MaxSheets*Cols*Rows decoded frames in memory.
	maxConcurrentFrameGridExtractions = 2
)

var (
	errFrameGridIntervalTooLong = errors.New("frame grid interval exceeds the sheet budget at the minimum reanalysis FPS")
	frameGridExtractionSlots    = make(chan struct{}, maxConcurrentFrameGridExtractions)
)

// FrameGridOptions configures how a video interval is sampled and tiled into
// contact-sheet JPEG images for reanalysis. Zero values use defaults
// (4 FPS, 8x4 = 32 frames / 8s per sheet, max 12 sheets, 240px tile width).
type FrameGridOptions struct {
	FPS         float64
	Cols        int
	Rows        int
	MaxSheets   int
	TileWidth   int
	JPEGQuality int
	// TimestampOffset overrides the base timestamp burned into the first extracted
	// frame. When nil, the start offset passed to GenerateFrameGrids is used.
	TimestampOffset *time.Duration
}

// FrameGridSheet holds one generated contact-sheet image and its covered media interval.
type FrameGridSheet struct {
	ImageBytes []byte
	MIMEType   string
	Start      time.Duration
	End        time.Duration
	FrameCount int
	Cols       int
	Rows       int
}

func normalizeFrameGridOptions(opts FrameGridOptions) FrameGridOptions {
	if opts.FPS <= 0 || math.IsNaN(opts.FPS) || math.IsInf(opts.FPS, 0) {
		opts.FPS = defaultFrameGridFPS
	}
	if opts.Cols <= 0 {
		opts.Cols = defaultFrameGridCols
	}
	if opts.Rows <= 0 {
		opts.Rows = defaultFrameGridRows
	}
	if opts.MaxSheets <= 0 {
		opts.MaxSheets = defaultFrameGridMaxSheets
	}
	if opts.TileWidth <= 0 {
		opts.TileWidth = defaultFrameGridTileWidth
	}
	if opts.TileWidth%2 != 0 {
		opts.TileWidth++
	}
	if opts.JPEGQuality <= 0 || opts.JPEGQuality > 100 {
		opts.JPEGQuality = defaultFrameGridJPEGQuality
	}
	return opts
}

func resolveFrameGridOptionsForDuration(duration time.Duration, opts FrameGridOptions) FrameGridOptions {
	opts = normalizeFrameGridOptions(opts)
	if duration <= 0 {
		return opts
	}
	maxFrames := opts.Cols * opts.Rows * opts.MaxSheets
	if maxFrames <= 0 {
		return opts
	}
	estimatedFrames := duration.Seconds() * opts.FPS
	if estimatedFrames > float64(maxFrames) {
		opts.FPS = float64(maxFrames) / duration.Seconds()
		if opts.FPS < minFrameGridFPS {
			opts.FPS = minFrameGridFPS
		}
	}
	return opts
}

// frameGridEligible reports whether a reanalysis interval fits the sheet budget
// at minReanalysisFrameGridFPS or faster.
func frameGridEligible(duration time.Duration, opts FrameGridOptions) bool {
	if duration <= 0 {
		return false
	}
	return resolveFrameGridOptionsForDuration(duration, opts).FPS >= minReanalysisFrameGridFPS-1e-9
}

// formatFrameTimestamp formats a duration as HH:MM:SS.mmm (e.g. "00:10:24.250")
// for burning into the top-left corner of each frame tile.
func formatFrameTimestamp(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalMillis := int64(d.Round(time.Millisecond) / time.Millisecond)
	millis := totalMillis % 1000
	totalSeconds := totalMillis / 1000
	seconds := totalSeconds % 60
	totalMinutes := totalSeconds / 60
	minutes := totalMinutes % 60
	hours := totalMinutes / 60
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, millis)
}

func formatFFmpegSeconds(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%.3f", d.Seconds())
}

// FrameGridImageBytes extracts the raw image byte slices from a slice of FrameGridSheet
// for passing to GeminiClient.AnalyzeFrameGridsWithModel.
func FrameGridImageBytes(sheets []FrameGridSheet) [][]byte {
	if len(sheets) == 0 {
		return nil
	}
	out := make([][]byte, 0, len(sheets))
	for _, s := range sheets {
		if len(s.ImageBytes) > 0 {
			out = append(out, s.ImageBytes)
		}
	}
	return out
}

// GenerateFrameGrids extracts frames from videoPath over [start, end] at opts.FPS,
// burns the exact media timestamp (HH:MM:SS.mmm) into the top-left corner of each
// frame, and tiles them into one or more Cols x Rows contact-sheet JPEG images.
// If the interval produces more than Cols*Rows frames (default 32 frames / 8s at 4 FPS),
// multiple sheets are returned in chronological order.
func GenerateFrameGrids(ctx context.Context, videoPath string, start, end time.Duration, opts FrameGridOptions) ([]FrameGridSheet, error) {
	if strings.TrimSpace(videoPath) == "" {
		return nil, errors.New("video path is required")
	}
	if start < 0 || end <= start {
		return nil, fmt.Errorf("invalid frame grid interval: start=%s end=%s", start, end)
	}
	duration := end - start
	opts = resolveFrameGridOptionsForDuration(duration, opts)

	tmpDir, err := createTempDir("frame-grid")
	if err != nil {
		return nil, fmt.Errorf("create frame grid temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	framePattern := filepath.Join(tmpDir, "frame_%05d.jpg")
	args := []string{
		"-v", "error",
		"-ss", formatFFmpegSeconds(start),
		"-t", formatFFmpegSeconds(duration),
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=%.4f,scale=%d:-2", opts.FPS, opts.TileWidth),
		"-q:v", "2",
		framePattern,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame extraction failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("read extracted frames dir: %w", err)
	}
	var frameFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "frame_") && strings.HasSuffix(entry.Name(), ".jpg") {
			frameFiles = append(frameFiles, filepath.Join(tmpDir, entry.Name()))
		}
	}
	sort.Strings(frameFiles)
	if len(frameFiles) == 0 {
		return nil, errors.New("ffmpeg frame extraction produced no frames")
	}

	frames := make([]image.Image, 0, len(frameFiles))
	for _, file := range frameFiles {
		raw, readErr := os.ReadFile(file)
		if readErr != nil {
			return nil, fmt.Errorf("read extracted frame %s: %w", filepath.Base(file), readErr)
		}
		img, decErr := jpeg.Decode(bytes.NewReader(raw))
		if decErr != nil {
			return nil, fmt.Errorf("decode extracted frame %s: %w", filepath.Base(file), decErr)
		}
		frames = append(frames, img)
	}

	baseOffset := start
	if opts.TimestampOffset != nil {
		baseOffset = *opts.TimestampOffset
	}
	return composeFrameGridImages(frames, baseOffset, duration, opts)
}

// composeFrameGridImages stamps HH:MM:SS.mmm onto each frame and tiles them into
// one or more Cols x Rows contact sheets.
func composeFrameGridImages(frames []image.Image, baseOffset, totalDuration time.Duration, opts FrameGridOptions) ([]FrameGridSheet, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames to compose")
	}
	opts = normalizeFrameGridOptions(opts)

	firstBounds := frames[0].Bounds()
	tileW := firstBounds.Dx()
	tileH := firstBounds.Dy()
	if tileW <= 0 || tileH <= 0 {
		return nil, errors.New("invalid frame dimensions")
	}

	framesPerSheet := opts.Cols * opts.Rows
	maxFrames := framesPerSheet * opts.MaxSheets
	if maxFrames > 0 && len(frames) > maxFrames {
		sampled := make([]image.Image, maxFrames)
		for i := 0; i < maxFrames; i++ {
			srcIdx := i * len(frames) / maxFrames
			sampled[i] = frames[srcIdx]
		}
		frames = sampled
		if totalDuration > 0 {
			opts.FPS = float64(maxFrames) / totalDuration.Seconds()
		}
	}

	numSheets := (len(frames) + framesPerSheet - 1) / framesPerSheet
	sheets := make([]FrameGridSheet, 0, numSheets)
	frameStep := time.Duration(math.Round(float64(time.Second) / opts.FPS))
	maxEnd := baseOffset + totalDuration

	for sheetIdx := 0; sheetIdx < numSheets; sheetIdx++ {
		batchStart := sheetIdx * framesPerSheet
		batchEnd := batchStart + framesPerSheet
		if batchEnd > len(frames) {
			batchEnd = len(frames)
		}
		batch := frames[batchStart:batchEnd]
		activeRows := (len(batch) + opts.Cols - 1) / opts.Cols

		canvasW := opts.Cols*tileW + (opts.Cols-1)*defaultFrameGridBorderPx
		canvasH := activeRows*tileH + (activeRows-1)*defaultFrameGridBorderPx
		canvas := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
		draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{R: 0, G: 0, B: 0, A: 255}}, image.Point{}, draw.Src)

		var sheetStart, sheetEnd time.Duration
		for i, frame := range batch {
			globalIdx := batchStart + i
			frameOffset := time.Duration(math.Round(float64(globalIdx) * float64(time.Second) / opts.FPS))
			frameTS := baseOffset + frameOffset
			if i == 0 {
				sheetStart = frameTS
			}
			sheetEnd = frameTS + frameStep
			if totalDuration > 0 && sheetEnd > maxEnd {
				sheetEnd = maxEnd
			}

			col := i % opts.Cols
			row := i / opts.Cols
			x0 := col * (tileW + defaultFrameGridBorderPx)
			y0 := row * (tileH + defaultFrameGridBorderPx)
			cellRect := image.Rect(x0, y0, x0+tileW, y0+tileH)

			draw.Draw(canvas, cellRect, frame, frame.Bounds().Min, draw.Src)
			drawFrameTimestampBadge(canvas, cellRect, formatFrameTimestamp(frameTS))
		}

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: opts.JPEGQuality}); err != nil {
			return nil, fmt.Errorf("encode frame grid sheet %d: %w", sheetIdx, err)
		}

		sheets = append(sheets, FrameGridSheet{
			ImageBytes: buf.Bytes(),
			MIMEType:   "image/jpeg",
			Start:      sheetStart,
			End:        sheetEnd,
			FrameCount: len(batch),
			Cols:       opts.Cols,
			Rows:       activeRows,
		})
	}

	return sheets, nil
}

// drawFrameTimestampBadge renders a dark semi-transparent pill and white bold
// monospace timestamp text (HH:MM:SS.mmm) in the top-left corner of cellRect.
func drawFrameTimestampBadge(canvas *image.RGBA, cellRect image.Rectangle, label string) {
	face := inconsolata.Bold8x16
	const (
		charW  = 8
		charH  = 16
		margin = 2
		padX   = 4
		padY   = 2
	)
	textW := len(label) * charW
	boxW := textW + padX*2
	boxH := charH + padY*2

	bx0 := cellRect.Min.X + margin
	by0 := cellRect.Min.Y + margin
	bx1 := bx0 + boxW
	by1 := by0 + boxH
	if bx1 > cellRect.Max.X {
		bx1 = cellRect.Max.X
	}
	if by1 > cellRect.Max.Y {
		by1 = cellRect.Max.Y
	}

	badgeBg := image.NewUniform(color.RGBA{R: 0, G: 0, B: 0, A: 175})
	draw.Draw(canvas, image.Rect(bx0, by0, bx1, by1), badgeBg, image.Point{}, draw.Over)

	d := &font.Drawer{
		Dst:  canvas,
		Src:  image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 255}),
		Face: face,
		Dot:  fixed.P(bx0+padX, by0+padY+face.Ascent),
	}
	d.DrawString(label)
}

func buildFrameGridPromptContext(sheets []FrameGridSheet, opts FrameGridOptions) string {
	opts = normalizeFrameGridOptions(opts)
	if len(sheets) == 0 {
		return ""
	}
	startLabel := formatFrameTimestamp(sheets[0].Start)
	endLabel := formatFrameTimestamp(sheets[len(sheets)-1].End)
	totalFrames := 0
	for _, s := range sheets {
		totalFrames += s.FrameCount
	}
	return fmt.Sprintf(
		"\n\n[프레임 그리드(Contact Sheet) 입력 안내]\n"+
			"- 제공된 %d장의 이미지는 영상 구간(%s ~ %s)을 %.2g FPS(%.0fms 간격)로 추출한 총 %d개 프레임의 연속 타일(가로 %d열 × 세로 최대 %d행, 좌→우 후 상→하 순서)입니다.\n"+
			"- 각 타일 좌측 상단에 각인된 타임스탬프(HH:MM:SS.mmm)를 직접 읽어 동작 시작·종료 시각과 반복(Rep) 횟수를 정확히 기록하세요.",
		len(sheets), startLabel, endLabel, opts.FPS, 1000.0/opts.FPS, totalFrames, opts.Cols, opts.Rows,
	)
}

// AnalyzeVideoIntervalWithFrameGrids extracts contact-sheet frame grids from a
// local video file over [start, end] and runs reanalysis via GeminiClient.AnalyzeFrameGridsWithModel.
func (w *Worker) AnalyzeVideoIntervalWithFrameGrids(
	ctx context.Context,
	videoPath string,
	start, end time.Duration,
	prompt, model string,
	opts FrameGridOptions,
) (string, []FrameGridSheet, *gemini.TokenUsage, error) {
	if w == nil || w.GeminiClient == nil {
		return "", nil, nil, errors.New("gemini client is not configured")
	}
	if !frameGridEligible(end-start, opts) {
		return "", nil, nil, errFrameGridIntervalTooLong
	}
	opts = resolveFrameGridOptionsForDuration(end-start, opts)
	select {
	case frameGridExtractionSlots <- struct{}{}:
	case <-ctx.Done():
		return "", nil, nil, ctx.Err()
	}
	sheets, err := GenerateFrameGrids(ctx, videoPath, start, end, opts)
	<-frameGridExtractionSlots
	if err != nil {
		return "", nil, nil, err
	}
	images := FrameGridImageBytes(sheets)
	fullPrompt := prompt + buildFrameGridPromptContext(sheets, opts)
	analysis, usage, err := w.GeminiClient.AnalyzeFrameGridsWithModel(ctx, images, "image/jpeg", fullPrompt, model)
	if err != nil {
		return "", sheets, usage, err
	}
	return analysis, sheets, usage, nil
}
