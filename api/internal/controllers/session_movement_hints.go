package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wod-strategist/api/internal/db"
)

func normalizeMovementHints(movements []string) []string {
	hints := make([]string, 0, len(movements))
	seen := make(map[string]struct{}, len(movements))
	for _, movement := range movements {
		movement = strings.TrimSpace(movement)
		if movement == "" {
			continue
		}

		key := strings.ToLower(movement)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		hints = append(hints, movement)
	}
	return hints
}

func movementHintsDocument(movements []string) (db.JSONDocument, error) {
	raw, err := json.Marshal(normalizeMovementHints(movements))
	if err != nil {
		return nil, fmt.Errorf("marshal movement hints: %w", err)
	}
	return db.JSONDocument(raw), nil
}

func decodeMovementHints(document db.JSONDocument) ([]string, error) {
	if len(document) == 0 {
		return []string{}, nil
	}

	var hints []string
	if err := json.Unmarshal(document, &hints); err != nil {
		return nil, fmt.Errorf("decode movement hints: %w", err)
	}
	return normalizeMovementHints(hints), nil
}

// persistSessionMovementHints dual-writes movement input for sessions created
// by newer clients. A nil slice means an older caller omitted the field and
// leaves existing hints untouched. Legacy sessions without a sessions row are
// intentionally left unchanged.
func (ctl *Controller) persistSessionMovementHints(ctx context.Context, sessionID string, profileID uint, movements []string) error {
	if movements == nil {
		return nil
	}

	document, err := movementHintsDocument(movements)
	if err != nil {
		return err
	}

	result := ctl.db.WithContext(ctx).
		Model(&db.Session{}).
		Where("session_id = ? AND profile_id = ?", sessionID, profileID).
		Update("movement_hints", document)
	if result.Error != nil {
		return fmt.Errorf("persist session movement hints: %w", result.Error)
	}
	return nil
}
