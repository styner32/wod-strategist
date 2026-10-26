package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"go.uber.org/zap"
	"golang.org/x/image/font"
	"golang.org/x/image/font/inconsolata"
	"golang.org/x/image/math/fixed"
)

// Two-channel contract. Observers may receive the Tracking channel — who the
// target is, where they are, what they were doing — so one person is followed
// continuously. The Assessment channel (form/ROM/tempo observations, counts,
// coaching text, fatigue, scores) is never handed forward: movement quality is
// judged from the current window only, so a later breakdown is not excused by
// earlier work.

const (
	trackingMinConfidence        = 0.7
	trackingMaxBoxes             = 3
	trackingFallbackWindowSecs   = 60.0
	trackingCaptureToleranceSecs = 0.5
	trackingReferenceMaxWidth    = 960
)

// trackingBox is the target's box at chunk-local second T, in Gemini's
// normalized [ymin, xmin, ymax, xmax] 0..1000 format (resolution independent).
type trackingBox struct {
	T   float64   `json:"t"`
	Box []float64 `json:"box_2d"`
}

func validTrackingBoxes(in []trackingBox) []trackingBox {
	out := []trackingBox{}
	for _, b := range in {
		if math.IsNaN(b.T) || math.IsInf(b.T, 0) || b.T < 0 || len(b.Box) != 4 {
			continue
		}
		ok := true
		for _, v := range b.Box {
			if math.IsNaN(v) || v < 0 || v > 1000 {
				ok = false
			}
		}
		if !ok || b.Box[0] >= b.Box[2] || b.Box[1] >= b.Box[3] {
			continue
		}
		out = append(out, trackingBox{T: b.T, Box: append([]float64(nil), b.Box...)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	if len(out) > trackingMaxBoxes {
		out = out[:trackingMaxBoxes]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizeTrackingLabel(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "`", ""))
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	return s
}

// trackingOutputPrompt asks the chunk observer to report where the target is.
const trackingOutputPrompt = `

## 대상 인물 위치 기록 (추적 연속성)
운동 여부와 관계없이 대상 인물이 보이면 target_person 블록을 반드시 출력하고 아래 필드를 추가하세요.
- boxes: 클립 처음·가운데·끝 근처에서 대상 인물이 보이는 시점마다 최대 3개. t는 클립 시작 기준 초, box_2d는 대상 인물 전신을 감싸는 [ymin, xmin, ymax, xmax] 0~1000 정규화 좌표입니다.
- frame_region: 대상 인물의 화면 위치 left, center, right 중 하나.
- station: 대상 인물이 사용 중인 기구나 위치(예: rower, barbell, box, pull-up bar, floor). 모르면 빈 문자열.
대상 인물을 확신할 수 없으면 ambiguous를 true로 두고 boxes는 빈 배열로 두세요. 다른 사람에게 박스를 그리지 마세요.
` + "```target_person\n" + `{"matched_cues":[],"ambiguous":false,"confidence":0.9,"boxes":[{"t":0.5,"box_2d":[120,400,980,620]}],"frame_region":"center","station":"rower"}` + "\n```\n"

// trackingHandoff is the whitelisted Tracking channel. It has no field that
// can carry form, ROM, tempo, counts, coaching text, fatigue or scores.
type trackingHandoff struct {
	MatchedCues   []string `json:"matched_cues,omitempty"`
	FrameRegion   string   `json:"frame_region,omitempty"`
	Station       string   `json:"station,omitempty"`
	ActivityState string   `json:"activity_state,omitempty"` // exercise | rest | unknown
	MovementHint  string   `json:"movement_hint,omitempty"`
}

func (h trackingHandoff) empty() bool {
	return len(h.MatchedCues) == 0 && h.FrameRegion == "" && h.Station == "" && h.ActivityState == "" && h.MovementHint == ""
}

func parseStoredTargetPerson(raw string) (targetPersonOutput, bool) {
	var tp targetPersonOutput
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &tp) != nil {
		return tp, false
	}
	return tp, true
}

// buildTrackingHandoff copies only Tracking fields from the previous chunk.
func buildTrackingHandoff(prev db.ChunkAnalysisResult, workoutType string) trackingHandoff {
	h := trackingHandoff{}
	if tp, ok := parseStoredTargetPerson(prev.TargetCues); ok && !tp.Ambiguous && prev.TargetConfidence >= trackingMinConfidence {
		for _, cue := range tp.MatchedCues {
			if c := sanitizeTrackingLabel(cue); c != "" {
				h.MatchedCues = append(h.MatchedCues, c)
			}
		}
		h.FrameRegion = sanitizeTrackingLabel(tp.FrameRegion)
		h.Station = sanitizeTrackingLabel(tp.Station)
	}
	class, movement := classifyChunkActivity(prev, workoutType)
	switch class {
	case chunkActive:
		h.ActivityState = "exercise"
		if isConcreteMovement(movement) {
			h.MovementHint = sanitizeTrackingLabel(movement)
		}
	case chunkExplicitRest:
		h.ActivityState = "rest"
	default:
		h.ActivityState = "unknown"
	}
	return h
}

// trackingInputPrompt explains the attached reference images (in order) and
// the handoff, and fixes the boundary between tracking and assessment.
func trackingInputPrompt(h trackingHandoff, hasPrevious, hasAnchor bool) string {
	if h.empty() && !hasPrevious && !hasAnchor {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## 대상 인물 추적 정보 (추적용 참고, 확정 아님)\n")
	index := 1
	if hasPrevious {
		fmt.Fprintf(&b, "- 첨부 이미지 %d: 직전 구간에서 대상 인물에 초록 박스와 TARGET 라벨을 표시한 프레임입니다.\n", index)
		index++
	}
	if hasAnchor {
		fmt.Fprintf(&b, "- 첨부 이미지 %d: 이 세션에서 처음 대상 인물을 확인한 기준 프레임입니다(같은 표시).\n", index)
	}
	if !h.empty() {
		raw, _ := json.Marshal(h)
		fmt.Fprintf(&b, "- 직전 구간 추적 요약: %s\n", raw)
	}
	b.WriteString("- 이 정보는 대상 인물 식별과 활동 연속성 확인에만 사용하세요. 현재 영상과 모순되면 현재 영상을 따르세요.\n")
	b.WriteString("- 표시된 사람이 현재 영상에 보이지 않으면 다른 사람으로 바꾸지 말고 대상이 보이지 않는다고 기록하세요.\n")
	b.WriteString("- 자세·가동범위·템포 평가는 현재 영상에서 보이는 것만 기록하고, 직전 운동이나 누적 수행을 원인이나 변명으로 쓰지 마세요.\n")
	return b.String()
}

// trackingReferenceObject keeps tracking images under the session prefix.
func trackingReferenceObject(profileID uint, sessionID string, chunkID uint) string {
	return fmt.Sprintf("videos/%d/%s/tracking/%d.jpg", profileID, sessionID, chunkID)
}

// renderTrackingReference extracts the frame at box.T and marks the target.
func renderTrackingReference(ctx context.Context, videoPath string, box trackingBox) ([]byte, error) {
	args := []string{
		"-v", "error",
		"-ss", fmt.Sprintf("%.3f", box.T),
		"-i", videoPath,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", trackingReferenceMaxWidth),
		"-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "3", "-",
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("extract tracking frame: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	frame, err := jpeg.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("decode tracking frame: %w", err)
	}
	marked := drawTrackingBox(frame, box.Box)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, marked, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("encode tracking frame: %w", err)
	}
	return out.Bytes(), nil
}

// trackingBoxRect converts a normalized box to pixel bounds inside the image.
func trackingBoxRect(bounds image.Rectangle, box []float64) image.Rectangle {
	w, h := float64(bounds.Dx()), float64(bounds.Dy())
	r := image.Rect(
		bounds.Min.X+int(math.Round(box[1]/1000*w)), bounds.Min.Y+int(math.Round(box[0]/1000*h)),
		bounds.Min.X+int(math.Round(box[3]/1000*w)), bounds.Min.Y+int(math.Round(box[2]/1000*h)),
	)
	return r.Intersect(bounds)
}

func drawTrackingBox(src image.Image, box []float64) *image.RGBA {
	canvas := image.NewRGBA(src.Bounds())
	draw.Draw(canvas, canvas.Bounds(), src, src.Bounds().Min, draw.Src)
	r := trackingBoxRect(canvas.Bounds(), box)
	if r.Empty() {
		return canvas
	}
	green := image.NewUniform(color.RGBA{R: 0, G: 230, B: 90, A: 255})
	const t = 4
	for _, edge := range []image.Rectangle{
		image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+t), image.Rect(r.Min.X, r.Max.Y-t, r.Max.X, r.Max.Y),
		image.Rect(r.Min.X, r.Min.Y, r.Min.X+t, r.Max.Y), image.Rect(r.Max.X-t, r.Min.Y, r.Max.X, r.Max.Y),
	} {
		draw.Draw(canvas, edge.Intersect(canvas.Bounds()), green, image.Point{}, draw.Src)
	}
	face := inconsolata.Bold8x16
	label := "TARGET"
	lx0, ly1 := r.Min.X, r.Min.Y
	if ly1-20 < canvas.Bounds().Min.Y {
		ly1 = r.Min.Y + 20
	}
	labelRect := image.Rect(lx0, ly1-20, lx0+len(label)*8+8, ly1).Intersect(canvas.Bounds())
	draw.Draw(canvas, labelRect, green, image.Point{}, draw.Src)
	d := &font.Drawer{Dst: canvas, Src: image.NewUniform(color.RGBA{A: 255}), Face: face, Dot: fixed.P(labelRect.Min.X+4, labelRect.Min.Y+2+face.Ascent)}
	d.DrawString(label)
	return canvas
}

// trackingInputs returns the tracking prompt and reference images for a chunk
// starting at captureStart. It never waits: a missing or late previous chunk
// falls back to the latest reference within trackingFallbackWindowSecs, then
// to text-only appearance context.
func (w *Worker) trackingInputs(ctx context.Context, profileID uint, sessionID, workoutType, filePath string, captureStart float64) (string, [][]byte) {
	if !w.TrackingContinuityEnabled {
		return "", nil
	}
	prompt := trackingOutputPrompt
	if w.DB == nil || captureStart <= 0 {
		return prompt, nil
	}
	var rows []db.ChunkAnalysisResult
	if err := w.DB.WithContext(ctx).
		Where("session_id = ? AND profile_id = ? AND status = ? AND file_path <> ? AND start_secs IS NOT NULL AND end_secs IS NOT NULL", sessionID, profileID, "COMPLETED", filePath).
		Order("start_secs ASC, id ASC").Find(&rows).Error; err != nil || len(rows) == 0 {
		return prompt, nil
	}
	var previous *db.ChunkAnalysisResult
	for i := range rows {
		if math.Abs(*rows[i].EndSecs-captureStart) <= trackingCaptureToleranceSecs {
			previous = &rows[i]
		}
	}
	handoff := trackingHandoff{}
	if previous != nil {
		handoff = buildTrackingHandoff(*previous, workoutType)
	}
	recentObject, anchorObject := "", ""
	for i := range rows {
		tp, ok := parseStoredTargetPerson(rows[i].TargetCues)
		if !ok || tp.ReferenceImage == "" {
			continue
		}
		if anchorObject == "" {
			anchorObject = tp.ReferenceImage
		}
		end := *rows[i].EndSecs
		if end <= captureStart+trackingCaptureToleranceSecs && end >= captureStart-trackingFallbackWindowSecs {
			recentObject = tp.ReferenceImage
		}
	}
	if anchorObject == recentObject {
		anchorObject = ""
	}
	var images [][]byte
	hasPrevious, hasAnchor := false, false
	if img := w.downloadTrackingReference(ctx, recentObject); img != nil {
		images, hasPrevious = append(images, img), true
	}
	if img := w.downloadTrackingReference(ctx, anchorObject); img != nil {
		images, hasAnchor = append(images, img), true
	}
	return prompt + trackingInputPrompt(handoff, hasPrevious, hasAnchor), images
}

func (w *Worker) downloadTrackingReference(ctx context.Context, object string) []byte {
	if object == "" || w.StorageClient == nil || w.BucketName == "" {
		return nil
	}
	local, err := createTempFile("tracking-ref", ".jpg")
	if err != nil {
		return nil
	}
	defer os.Remove(local)
	if err := w.StorageClient.DownloadFile(ctx, "gs://"+w.BucketName+"/"+object, local); err != nil {
		w.logger.Warn("tracking reference unavailable; continuing without it", zap.String("object", object), zap.Error(err))
		return nil
	}
	raw, err := os.ReadFile(local)
	if err != nil || len(raw) == 0 {
		return nil
	}
	return raw
}

// saveTrackingReference renders and stores the marked frame for the next
// chunk. Failures are logged and never fail chunk analysis.
func (w *Worker) saveTrackingReference(ctx context.Context, videoPath string, chunk *db.ChunkAnalysisResult) {
	if !w.TrackingContinuityEnabled || w.StorageClient == nil || w.DB == nil || chunk == nil || chunk.ID == 0 {
		return
	}
	tp, ok := parseStoredTargetPerson(chunk.TargetCues)
	if !ok || tp.Ambiguous || chunk.TargetConfidence < trackingMinConfidence || len(tp.Boxes) == 0 || tp.ReferenceImage != "" {
		return
	}
	box := tp.Boxes[len(tp.Boxes)-1]
	rendered, err := renderTrackingReference(ctx, videoPath, box)
	if err != nil {
		w.logger.Warn("tracking reference render failed", zap.Uint("chunk_id", chunk.ID), zap.Error(err))
		return
	}
	local, err := createTempFile("tracking-out", ".jpg")
	if err != nil {
		return
	}
	defer os.Remove(local)
	if err := os.WriteFile(local, rendered, 0o600); err != nil {
		return
	}
	object := trackingReferenceObject(chunk.ProfileID, chunk.SessionID, chunk.ID)
	if _, err := w.StorageClient.UploadFromFile(ctx, local, object); err != nil {
		w.logger.Warn("tracking reference upload failed", zap.Uint("chunk_id", chunk.ID), zap.Error(err))
		return
	}
	tp.ReferenceImage = object
	raw, err := json.Marshal(tp)
	if err != nil {
		return
	}
	if err := w.DB.WithContext(ctx).Model(&db.ChunkAnalysisResult{}).Where("id = ?", chunk.ID).Update("target_cues", string(raw)).Error; err != nil {
		w.logger.Warn("tracking reference reference not recorded", zap.Uint("chunk_id", chunk.ID), zap.Error(err))
		return
	}
	chunk.TargetCues = string(raw)
}

// analyzeChunkVideo sends tracking reference images when available.
func (w *Worker) analyzeChunkVideo(ctx context.Context, localPath, prompt, model string, images [][]byte) (string, string, *gemini.TokenUsage, error) {
	if len(images) > 0 {
		return w.GeminiClient.AnalyzeChunkVideoWithImages(ctx, localPath, prompt, model, images)
	}
	return w.GeminiClient.AnalyzeChunkVideo(ctx, localPath, prompt, model)
}

type trackingReferenceRef struct {
	Start, End float64 // verified merged-media seconds of the source chunk
	Object     string
}

// loadTrackingReferences lists chunk reference images on the merged-media
// timeline. Chunks without verified media offsets are never used.
func (w *Worker) loadTrackingReferences(ctx context.Context, sessionID string, profileID uint) []trackingReferenceRef {
	if !w.TrackingContinuityEnabled || w.DB == nil {
		return nil
	}
	var rows []db.ChunkAnalysisResult
	if err := w.DB.WithContext(ctx).Where("session_id = ? AND profile_id = ? AND status = ?", sessionID, profileID, "COMPLETED").
		Order("media_start_secs ASC NULLS LAST, id ASC").Find(&rows).Error; err != nil {
		return nil
	}
	refs := []trackingReferenceRef{}
	for _, row := range rows {
		if !debugMediaIntervalValid(row.MediaStartSecs, row.MediaEndSecs) {
			continue
		}
		if tp, ok := parseStoredTargetPerson(row.TargetCues); ok && tp.ReferenceImage != "" {
			refs = append(refs, trackingReferenceRef{Start: *row.MediaStartSecs, End: *row.MediaEndSecs, Object: tp.ReferenceImage})
		}
	}
	return refs
}

const pass2TrackingPrompt = `

## 대상 인물 추적 기준 (추적용 참고, 확정 아님)
- 첨부된 기준 이미지에는 대상 인물이 초록 박스와 TARGET 라벨로 표시되어 있습니다(이 구간 부근과 세션 첫 확인 프레임).
- 이 사람만 관찰하세요. 현재 영상에서 보이지 않으면 다른 사람으로 바꾸지 말고 unknown 또는 unassessed로 기록하세요.
- 기준 이미지는 대상 식별용이며 자세·가동범위·템포 평가의 근거가 아닙니다.
`

// pass2TrackingInputs returns the reference nearest the window start plus the
// session anchor, and the prompt that explains them.
func (w *Worker) pass2TrackingInputs(ctx context.Context, refs []trackingReferenceRef, start, end float64) (string, [][]byte) {
	if len(refs) == 0 {
		return "", nil
	}
	near := ""
	for _, ref := range refs {
		if ref.End > start && ref.Start < end {
			near = ref.Object
			break
		}
	}
	anchor := refs[0].Object
	var images [][]byte
	if img := w.downloadTrackingReference(ctx, near); img != nil {
		images = append(images, img)
	}
	if anchor != near {
		if img := w.downloadTrackingReference(ctx, anchor); img != nil {
			images = append(images, img)
		}
	}
	if len(images) == 0 {
		return "", nil
	}
	return pass2TrackingPrompt, images
}
