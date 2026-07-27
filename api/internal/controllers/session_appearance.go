package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wod-strategist/api/internal/db"
)

const (
	appearanceMaxStringLen = 200 // 최대 길이 (룬 기준)
)

type AppearanceInput struct {
	Appearance string `json:"appearance,omitempty"`
}

func sanitizeAppearanceValue(val string) string {
	val = strings.TrimSpace(val)
	val = strings.ReplaceAll(val, "\r", "")
	val = strings.ReplaceAll(val, "\n", " ")
	val = strings.ReplaceAll(val, "`", "")
	val = strings.TrimSpace(val)
	if utf8.RuneCountInString(val) > appearanceMaxStringLen {
		runes := []rune(val)
		val = string(runes[:appearanceMaxStringLen])
	}
	return strings.TrimSpace(val)
}

func normalizeAppearance(in *AppearanceInput) AppearanceInput {
	if in == nil {
		return AppearanceInput{}
	}
	clean := sanitizeAppearanceValue(in.Appearance)
	if clean == "" {
		return AppearanceInput{}
	}
	return AppearanceInput{Appearance: clean}
}

func appearanceDocument(in *AppearanceInput) (db.JSONDocument, error) {
	norm := normalizeAppearance(in)
	if norm.Appearance == "" {
		return db.JSONDocument("{}"), nil
	}
	data, err := json.Marshal(norm)
	if err != nil {
		return nil, fmt.Errorf("marshal appearance: %w", err)
	}
	return db.JSONDocument(data), nil
}

type legacyAppearanceDoc struct {
	Appearance string            `json:"appearance,omitempty"`
	Persistent map[string]string `json:"persistent,omitempty"`
	Session    map[string]string `json:"session,omitempty"`
	Removable  []string          `json:"removable,omitempty"`
}

func decodeAppearance(doc db.JSONDocument) string {
	if len(doc) == 0 || string(doc) == "{}" || string(doc) == "null" {
		return ""
	}

	var d legacyAppearanceDoc
	if err := json.Unmarshal(doc, &d); err == nil {
		if strings.TrimSpace(d.Appearance) != "" {
			return sanitizeAppearanceValue(d.Appearance)
		}

		var parts []string
		for _, key := range []string{"top", "bottom"} {
			if v, ok := d.Session[key]; ok && strings.TrimSpace(v) != "" {
				parts = append(parts, strings.TrimSpace(v))
			}
		}
		for _, key := range []string{"shoes", "hair", "build"} {
			if v, ok := d.Persistent[key]; ok && strings.TrimSpace(v) != "" {
				parts = append(parts, strings.TrimSpace(v))
			}
		}
		for _, item := range d.Removable {
			if strings.TrimSpace(item) != "" {
				parts = append(parts, strings.TrimSpace(item))
			}
		}
		if len(parts) > 0 {
			return sanitizeAppearanceValue(strings.Join(parts, ", "))
		}
	}

	var strVal string
	if err := json.Unmarshal(doc, &strVal); err == nil && strings.TrimSpace(strVal) != "" {
		return sanitizeAppearanceValue(strVal)
	}

	s := strings.TrimSpace(string(doc))
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") && !strings.HasPrefix(s, "\"") && s != "" {
		return sanitizeAppearanceValue(s)
	}

	return ""
}

func persistSessionAppearanceHints(ctx context.Context, dbConn *gorm.DB, sessionID string, profileID uint, hints *AppearanceInput) error {
	if dbConn == nil || sessionID == "" || hints == nil {
		return nil
	}
	norm := normalizeAppearance(hints)
	if norm.Appearance == "" {
		return nil
	}
	doc, err := appearanceDocument(&norm)
	if err != nil {
		return err
	}

	hint := db.SessionAppearanceHint{
		SessionID: sessionID,
		ProfileID: profileID,
		Hints:     doc,
	}

	return dbConn.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"hints": doc}),
	}).Create(&hint).Error
}
