package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	geminiPkg "github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/logger"
	"go.uber.org/zap"
)

var workoutBlockRegex = regexp.MustCompile(
	"(?is)```(?:workout|json)?\\s*\\n?(\\{[^`]*?\\})\\s*\\n?```")

// parseWorkoutBlock extracts the structured workout JSON from Gemini's output.
func parseWorkoutBlock(output string) (*ParseWorkoutImageResponse, error) {
	matches := workoutBlockRegex.FindStringSubmatch(output)
	if len(matches) < 2 {
		start := strings.Index(output, "{")
		end := strings.LastIndex(output, "}")
		if start != -1 && end > start {
			matches = []string{output, output[start : end+1]}
		} else {
			return nil, fmt.Errorf("no workout JSON block found in output")
		}
	}

	var resp ParseWorkoutImageResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(matches[1])), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse workout JSON: %w", err)
	}

	return &resp, nil
}

// maxImageUploadSize is the maximum allowed image upload size (10 MB).
const maxImageUploadSize = 10 << 20 // 10 MB

// wodParsePrompt is the prompt for extracting workout info from a whiteboard photo.
const wodParsePrompt = `당신은 크로스핏 박스의 화이트보드 사진을 읽고 오늘의 운동(WOD)을 구조화된 형식으로 추출하는 전문가입니다.

## 작업
1. 화이트보드에 보이는 모든 텍스트를 읽으세요.
2. 운동 유형을 식별하세요: 이름이 있는 벤치마크(Fran, Grace 등), For Time, AMRAP, EMOM, 또는 기타 형식.
3. 개별 운동 종목을 추출하세요.
4. **오타 및 약어를 교정하세요**: "Thuster" → "Thruster", "PU" → "Pull-up", "DL" → "Deadlift", "KB" → "Kettlebell", "BJ" → "Box Jump", "HSPUs" → "Handstand Push-up", "C2B" → "Chest to Bar", "T2B" → "Toes to Bar", "MU" → "Muscle-up", "DU" → "Double Under", "SU" → "Single Under", "WB" → "Wall Ball", "S2OH" → "Shoulder to Overhead", "G2OH" → "Ground to Overhead", "PC" → "Power Clean", "SC" → "Squat Clean", "PP" → "Push Press", "PJ" → "Push Jerk", "SJ" → "Split Jerk", "FS" → "Front Squat", "BS" → "Back Squat", "OHS" → "Overhead Squat", "SDHP" → "Sumo Deadlift High Pull", "RDL" → "Romanian Deadlift"
5. 영어와 한국어 모두 인식하세요.

## 출력 형식
반드시 아래 JSON 형식을 ` + "```workout```" + ` 코드 블록 안에 작성하세요:

` + "```workout" + `
{
  "wod_description": "운동 설명 (예: Fran, For Time: 5 rounds of..., AMRAP 20 min: ...)",
  "movements": ["운동종목1", "운동종목2"],
  "raw_text": "화이트보드에서 읽은 원본 텍스트 그대로"
}
` + "```" + `

## 규칙
- wod_description과 raw_text는 가독성과 모바일 앱에서의 쉬운 편집을 위해 **반드시 한 줄로 합치지 말고, 줄바꿈 문자(\\n)를 활용하여 여러 줄(multi-line)로 포맷팅**하여 반환하세요.
- wod_description은 가능한 한 구체적으로 작성하세요 (세트, 반복수, 무게 포함). 각 운동이나 라운드 정보 사이에는 쉼표 대신 줄바꿈(\\n)을 포함하여 구조화하세요.
- movements에는 교정된 공식 운동 이름만 포함하세요.
- raw_text에는 화이트보드 원본 레이아웃의 줄바꿈과 줄 번호 등을 그대로 살려 교정 전 원본 텍스트를 포함하세요.
- 화이트보드를 읽을 수 없으면 빈 JSON을 반환하세요: {"wod_description": "", "movements": [], "raw_text": ""}
- 운동과 관련 없는 텍스트(날짜, 공지사항 등)는 wod_description에서 제외하세요.`

// ParseWorkoutImage reads a whiteboard photo, sends it to Gemini Flash for
// OCR + typo correction, and returns structured WOD data.
//
// @Summary      Parse Workout Image
// @Description  Extracts workout description and movements from a whiteboard photo
// @Tags         workout
// @Accept       multipart/form-data
// @Produce      json
// @Param        image formData file true "Whiteboard photo (JPEG/PNG, max 10MB)"
// @Success      200 {object} ParseWorkoutImageResponse
// @Failure      400 {object} ErrorResponse
// @Failure      422 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Router       /parse-workout-image [post]
func (ctl *Controller) ParseWorkoutImage(c *gin.Context) {
	if ctl.imageParser == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "image parsing is not configured"})
		return
	}

	// Limit request body size
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageUploadSize)

	file, header, err := c.Request.FormFile("image")
	if err != nil {
		logger.Log.Warn("failed to read image from form", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "image file is required"})
		return
	}
	defer file.Close()

	logger.Log.Info("Received workout image",
		zap.String("filename", header.Filename),
		zap.Int64("size_bytes", header.Size))

	// Read all bytes
	imageBytes := make([]byte, header.Size)
	if _, err := file.Read(imageBytes); err != nil {
		logger.Log.Error("failed to read image bytes", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read image"})
		return
	}

	// Detect MIME type from content
	mimeType := geminiPkg.DetectImageMIME(imageBytes)
	if mimeType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported image format; use JPEG or PNG"})
		return
	}

	// Normalize: resize to max 1024px, re-encode as JPEG
	normalized, normalizedMIME, err := geminiPkg.NormalizeImage(imageBytes, mimeType)
	if err != nil {
		logger.Log.Error("failed to normalize image", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to process image"})
		return
	}

	logger.Log.Info("Image normalized",
		zap.Int("original_bytes", len(imageBytes)),
		zap.Int("normalized_bytes", len(normalized)))

	// Call Gemini Flash for parsing
	output, usage, err := ctl.imageParser.ParseImage(c.Request.Context(), normalized, normalizedMIME, wodParsePrompt)
	ctl.recordTokenUsage(c, 0, "image:workout", usage)
	if err != nil {
		logger.Log.Error("Gemini ParseImage failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to analyze image"})
		return
	}

	// Parse the ```workout { ... } ``` JSON block from Gemini output
	resp, err := parseWorkoutBlock(output)
	if err != nil {
		logger.Log.Warn("failed to parse workout block from Gemini output",
			zap.Error(err),
			zap.String("raw_output", output))
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":      "could not extract workout from image",
			"raw_output": output,
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

const appearanceParsePrompt = `이 사진에 있는 사람의 외형 특징(상의, 하의, 신발, 머리 스타일, 착용 장비 등)을 식별용으로 요약 기술하세요.

규칙:
- 한국어로 100자 이내의 짧은 명사구/문구로 기술하세요 (예: "검은 반팔, 회색 반바지, 빨간 신발, 무릎보호대").
- 얼굴 생김새, 인종, 나이 추정은 출력하지 마세요.

반드시 아래 형식의 appearance JSON 코드 블록으로만 출력하세요:
` + "```appearance" + `
{"appearance": "검은 반팔, 회색 반바지, 빨간 신발"}
` + "```"

var appearanceBlockRegex = regexp.MustCompile(
	"(?is)```(?:appearance|json)?\\s*\\n?(\\{[^`]*?\\})\\s*\\n?```")

func parseAppearanceBlock(output string) (AppearanceInput, error) {
	matches := appearanceBlockRegex.FindStringSubmatch(output)
	if len(matches) < 2 {
		start := strings.Index(output, "{")
		end := strings.LastIndex(output, "}")
		if start != -1 && end > start {
			matches = []string{output, output[start : end+1]}
		} else {
			return AppearanceInput{}, fmt.Errorf("no appearance JSON block found in output")
		}
	}
	var raw AppearanceInput
	if err := json.Unmarshal([]byte(matches[1]), &raw); err != nil {
		return AppearanceInput{}, fmt.Errorf("unmarshal appearance block: %w", err)
	}
	return normalizeAppearance(&raw), nil
}

// @Summary      Parse Appearance Image
// @Description  Extracts person visual appearance cues from a photo
// @Tags         workout
// @Accept       multipart/form-data
// @Produce      json
// @Param        image formData file true "Person photo (JPEG/PNG, max 10MB)"
// @Success      200 {object} AppearanceInput
// @Failure      400 {object} ErrorResponse
// @Failure      422 {object} ErrorResponse
// @Failure      503 {object} ErrorResponse
// @Router       /appearance-from-image [post]
func (ctl *Controller) ParseAppearanceImage(c *gin.Context) {
	if ctl.imageParser == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "image parsing is not configured"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageUploadSize)

	file, header, err := c.Request.FormFile("image")
	if err != nil {
		logger.Log.Warn("failed to read image from form", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "image file is required"})
		return
	}
	defer file.Close()

	logger.Log.Info("Received appearance image",
		zap.String("filename", header.Filename),
		zap.Int64("size_bytes", header.Size))

	imageBytes := make([]byte, header.Size)
	if _, err := file.Read(imageBytes); err != nil {
		logger.Log.Error("failed to read image bytes", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read image"})
		return
	}

	mimeType := geminiPkg.DetectImageMIME(imageBytes)
	if mimeType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported image format; use JPEG or PNG"})
		return
	}

	normalized, normalizedMIME, err := geminiPkg.NormalizeImage(imageBytes, mimeType)
	if err != nil {
		logger.Log.Error("failed to normalize image", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to process image"})
		return
	}

	output, usage, err := ctl.imageParser.ParseImage(c.Request.Context(), normalized, normalizedMIME, appearanceParsePrompt)
	ctl.recordTokenUsage(c, 0, "image:appearance", usage)
	if err != nil {
		logger.Log.Error("Gemini ParseAppearanceImage failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to analyze image"})
		return
	}

	res, err := parseAppearanceBlock(output)
	if err != nil {
		logger.Log.Warn("failed to parse appearance block from Gemini output",
			zap.Error(err),
			zap.String("raw_output", output))
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":      "could not extract appearance from image",
			"raw_output": output,
		})
		return
	}

	c.JSON(http.StatusOK, res)
}
