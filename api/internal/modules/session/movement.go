package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wod-strategist/api/internal/db"
)

func NormalizeMovementHints(movements []string) []string {
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

func MovementHintsDocument(movements []string) (db.JSONDocument, error) {
	raw, err := json.Marshal(NormalizeMovementHints(movements))
	if err != nil {
		return nil, fmt.Errorf("marshal movement hints: %w", err)
	}
	return db.JSONDocument(raw), nil
}

func DecodeMovementHints(document db.JSONDocument) ([]string, error) {
	if len(document) == 0 {
		return []string{}, nil
	}

	var hints []string
	if err := json.Unmarshal(document, &hints); err != nil {
		return nil, fmt.Errorf("decode movement hints: %w", err)
	}
	return NormalizeMovementHints(hints), nil
}
