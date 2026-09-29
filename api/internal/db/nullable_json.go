package db

import (
	"database/sql/driver"
	"encoding/json"
)

// NullableJSONDocument preserves SQL NULL for optional, unevaluated results.
// JSONDocument intentionally defaults to {}; that is unsuitable for opt-in evidence.
type NullableJSONDocument json.RawMessage

func (j NullableJSONDocument) Value() (driver.Value, error) {
	if len(j) == 0 || string(j) == "null" {
		return nil, nil
	}
	return JSONDocument(j).Value()
}
func (j *NullableJSONDocument) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	var doc JSONDocument
	if err := doc.Scan(value); err != nil {
		return err
	}
	*j = NullableJSONDocument(doc)
	return nil
}
func (j NullableJSONDocument) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return JSONDocument(j).MarshalJSON()
}
func (j *NullableJSONDocument) UnmarshalJSON(data []byte) error {
	var doc JSONDocument
	if err := doc.UnmarshalJSON(data); err != nil {
		return err
	}
	*j = NullableJSONDocument(doc)
	return nil
}
