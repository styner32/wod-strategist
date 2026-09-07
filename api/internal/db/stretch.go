package db

import (
	"context"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

type Stretch struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name          string    `json:"name"`
	NormalizedKey string    `gorm:"uniqueIndex" json:"-"`
	TargetArea    string    `json:"target_area"`
	Description   string    `json:"description"`
	DurationHint  string    `json:"duration_hint"`
	Caution       string    `json:"caution"`
	ImageObject   string    `json:"-"`
	VideoObject   string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (Stretch) TableName() string {
	return "stretches"
}

type StretchAlias struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	StretchID     uint64    `json:"stretch_id"`
	Alias         string    `json:"alias"`
	NormalizedKey string    `gorm:"uniqueIndex" json:"-"`
	CreatedAt     time.Time `json:"created_at"`
}

func (StretchAlias) TableName() string {
	return "stretch_aliases"
}

var stretchSpacesOrHyphens = regexp.MustCompile(`[\s-]+`)

// NormalizeStretchKey converts a raw stretch or alias string into a normalized matching key
// (lowercased, trimmed, hyphens converted to spaces, consecutive whitespace collapsed).
func NormalizeStretchKey(raw string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return ""
	}
	return stretchSpacesOrHyphens.ReplaceAllString(trimmed, " ")
}

func ListStretches(ctx context.Context, dbConn *gorm.DB) ([]Stretch, error) {
	var stretches []Stretch
	if err := dbConn.WithContext(ctx).Order("name ASC").Find(&stretches).Error; err != nil {
		return nil, err
	}
	return stretches, nil
}

func ListStretchAliases(ctx context.Context, dbConn *gorm.DB) ([]StretchAlias, error) {
	var aliases []StretchAlias
	if err := dbConn.WithContext(ctx).Order("alias ASC").Find(&aliases).Error; err != nil {
		return nil, err
	}
	return aliases, nil
}
