package sensor

import (
	"encoding/json"
	"fmt"
)

// Mobile writes device nanoseconds as a decimal string to preserve values above
// JavaScript's safe integer range. Older telemetry used JSON integer numbers.
type deviceTimestampNS int64

func (t *deviceTimestampNS) UnmarshalJSON(data []byte) error {
	var value json.Number
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid device_timestamp_ns: %w", err)
	}
	if value == "" { // Preserve encoding/json's existing null behavior for int64.
		return nil
	}
	ns, err := value.Int64()
	if err != nil {
		return fmt.Errorf("invalid device_timestamp_ns: %w", err)
	}
	*t = deviceTimestampNS(ns)
	return nil
}
