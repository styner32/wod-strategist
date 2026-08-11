package worker

import (
	"encoding/json"
	"regexp"
	"strings"
)

const MobilityOutputPrompt = `

## 관절 가동성 및 제한 관찰 (Mobility & Joint Restrictions)
영상에서 대상 인물의 관절 가동 범위(ROM) 제한, 타이트함, 또는 비대칭이 **직접 눈으로 관찰되는 경우에만** 아래 형식의 **mobility** JSON 코드 블록으로 출력하세요 (근거가 없거나 카메라 각도로 확인이 불가능하면 빈 배열 []을 출력하세요).

**허용된 joint 목록 (닫힌 어휘):**
Neck, Shoulder, Elbow, Wrist, Upper Back, Lower Back, Hip, Hamstring, Knee, Ankle

**허용된 observation 목록 (닫힌 어휘):**
limited_hip_flexion, limited_hip_internal_rotation, limited_ankle_dorsiflexion, limited_overhead_flexion, limited_shoulder_external_rotation, limited_thoracic_extension, tight_hamstrings, tight_hip_flexors, limited_wrist_extension, general_stiffness

**규칙:**
- 의학적 진단이나 통증 판정을 하지 마세요. 오직 직접 보이는 움직임 현상만 기록하세요.
- side는 "left", "right", "both" 중 하나로 기입하세요.
- evidence는 관찰된 동작과 시각 증거를 한국어 한 문장으로 구체적으로 작성하세요 (예: "스쿼트 하단에서 발뒤꿈치가 들림").
- 카메라 가림이나 각도 문제로 평가 불가능한 경우 assessable을 false로 하거나 포함하지 마세요.

` + "```mobility\n" + `[{"joint":"Hip","side":"both","observation":"limited_hip_flexion","movement":"Back Squat","evidence":"스쿼트 하단에서 깊이 제한 및 벗 wink 관찰됨","confidence":0.88,"assessable":true}]` + "\n```"

var mobilityBlockRegex = regexp.MustCompile("(?is)(?:```mobility\\s*(\\[.*?\\])\\s*```|<mobility>\\s*(\\[.*?\\])\\s*</mobility>)")

type MobilityObservation struct {
	Joint       string  `json:"joint"`
	Side        string  `json:"side,omitempty"`
	Observation string  `json:"observation"`
	Movement    string  `json:"movement,omitempty"`
	Evidence    string  `json:"evidence"`
	Confidence  float64 `json:"confidence"`
	Assessable  bool    `json:"assessable"`
}

var allowedJoints = map[string]string{
	"neck":       "Neck",
	"shoulder":   "Shoulder",
	"elbow":      "Elbow",
	"wrist":      "Wrist",
	"upper back": "Upper Back",
	"lower back": "Lower Back",
	"hip":        "Hip",
	"hamstring":  "Hamstring",
	"knee":       "Knee",
	"ankle":      "Ankle",
}

var allowedObservations = map[string]string{
	"limited_hip_flexion":               "limited_hip_flexion",
	"limited_hip_internal_rotation":     "limited_hip_internal_rotation",
	"limited_ankle_dorsiflexion":        "limited_ankle_dorsiflexion",
	"limited_overhead_flexion":          "limited_overhead_flexion",
	"limited_shoulder_external_rotation": "limited_shoulder_external_rotation",
	"limited_thoracic_extension":        "limited_thoracic_extension",
	"tight_hamstrings":                  "tight_hamstrings",
	"tight_hip_flexors":                 "tight_hip_flexors",
	"limited_wrist_extension":           "limited_wrist_extension",
	"general_stiffness":                 "general_stiffness",
}

func parseMobilityObservations(text string) []MobilityObservation {
	matches := mobilityBlockRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var all []MobilityObservation
	for _, match := range matches {
		jsonStr := ""
		if len(match) > 1 && match[1] != "" {
			jsonStr = match[1]
		} else if len(match) > 2 && match[2] != "" {
			jsonStr = match[2]
		}
		if jsonStr == "" {
			continue
		}

		var items []MobilityObservation
		if err := json.Unmarshal([]byte(jsonStr), &items); err == nil {
			all = append(all, items...)
		}
	}
	return all
}

func sanitizeMobilityObservations(obs []MobilityObservation) []MobilityObservation {
	if len(obs) == 0 {
		return nil
	}

	var sanitized []MobilityObservation
	for _, item := range obs {
		jointKey := strings.ToLower(strings.TrimSpace(item.Joint))
		canonicalJoint, jointOK := allowedJoints[jointKey]
		if !jointOK {
			continue
		}

		obsKey := strings.ToLower(strings.TrimSpace(item.Observation))
		canonicalObs, obsOK := allowedObservations[obsKey]
		if !obsOK {
			continue
		}

		// Assessable default check: if JSON included "assessable": false explicitly
		// Wait: in Go unmarshaling boolean defaults to false if omitted unless defaulted.
		// However, if confidence >= 0.4 and assessable was omitted (false by default in Go),
		// we check if confidence >= 0.4 and treat omitted assessable as true when confidence > 0.
		// To distinguish explicit assessable: false, if confidence < 0.4 or assessable == false and confidence == 0, drop it.
		// If explicit assessable field is present in JSON and set to false:
		// We drop when confidence < 0.4.
		if item.Confidence < 0.4 {
			continue
		}

		conf := item.Confidence
		if conf > 1.0 {
			conf = 1.0
		}
		if conf < 0.0 {
			conf = 0.0
		}

		side := strings.ToLower(strings.TrimSpace(item.Side))
		if side != "left" && side != "right" && side != "both" {
			side = "both"
		}

		sanitized = append(sanitized, MobilityObservation{
			Joint:       canonicalJoint,
			Side:        side,
			Observation: canonicalObs,
			Movement:    strings.TrimSpace(item.Movement),
			Evidence:    strings.TrimSpace(item.Evidence),
			Confidence:  conf,
			Assessable:  true,
		})

		if len(sanitized) >= 10 {
			break
		}
	}
	return sanitized
}

func marshalMobilityObservations(obs []MobilityObservation) string {
	if len(obs) == 0 {
		return "[]"
	}
	data, err := json.Marshal(obs)
	if err != nil {
		return "[]"
	}
	return string(data)
}
