package sensor

type EventHeader struct {
	Kind string  `json:"k"`
	Time float64 `json:"t"`
}

type MetaEvent struct {
	Kind             string         `json:"k"`
	SchemaVersion    string         `json:"schema_version"`
	WorkoutSessionID string         `json:"workout_session_id"`
	ProfileID        uint           `json:"profile_id"`
	ClockSource      string         `json:"clock_source"`
	BaseEpochMs      int64          `json:"base_epoch_ms"`
	Requested        map[string]any `json:"requested_sampling,omitempty"`
	Device           any            `json:"device,omitempty"`
	App              map[string]any `json:"app,omitempty"`
}

type StreamStartEvent struct {
	Kind        string `json:"k"`
	Time        float64 `json:"t"`
	StreamID    int64  `json:"stream_id"`
	Sampling    struct {
		ACCHz             float64 `json:"acc_hz"`
		ACCRangeG         int     `json:"acc_range_g"`
		ACCResolutionBits int     `json:"acc_resolution_bits"`
		FrameType         int     `json:"frame_type"`
		DeltaCompressed   bool    `json:"delta_compressed"`
	} `json:"sampling"`
	ClockAnchor struct {
		DeviceTimestampNs deviceTimestampNS `json:"device_timestamp_ns"`
		CaptureOffsetMs   float64           `json:"capture_offset_ms"`
		Method            string            `json:"method"`
	} `json:"clock_anchor"`
}

type AccEvent struct {
	Kind     string      `json:"k"`
	Time     float64     `json:"t"`
	StreamID int64       `json:"stream_id"`
	Dt       float64     `json:"dt"`
	V        [][]float64 `json:"v"`
}

type HREvent struct {
	Contact *bool     `json:"contact,omitempty"`
	Kind    string    `json:"k"`
	Time    float64   `json:"t"`
	BPM     int       `json:"bpm"`
	RR      []float64 `json:"rr,omitempty"`
}

type GapStartEvent struct {
	Kind   string  `json:"k"`
	Time   float64 `json:"t"`
	GapID  int64   `json:"gap_id"`
	Reason string  `json:"reason,omitempty"`
}

type GapEndEvent struct {
	Kind  string  `json:"k"`
	Time  float64 `json:"t"`
	GapID int64   `json:"gap_id"`
}

type PauseInterval struct {
	StartOffsetMs float64 `json:"start_offset_ms"`
	EndOffsetMs   float64 `json:"end_offset_ms"`
}

type EndEvent struct {
	Kind           string          `json:"k"`
	Time           float64         `json:"t"`
	PauseIntervals []PauseInterval `json:"pause_intervals"`
	Device         struct {
		BatteryPercentEnd *int `json:"battery_percent_end"`
	} `json:"device"`
	Summary struct {
		HRSamples      int  `json:"hr_samples"`
		ACCSamples      int  `json:"acc_samples"`
		DroppedPackets *int `json:"dropped_packets"`
		Gaps           int  `json:"gaps"`
		WriteFailures  int  `json:"write_failures"`
		DroppedLines   int  `json:"dropped_lines"`
	} `json:"summary"`
}

const (
	HybridAlgorithmVersion      = "hybrid-0.75-0.25-gamma2"
	DefaultBootstrapENMORestG   = 0.01
	DefaultBootstrapENMORefG    = 0.30
	BootstrapCalibrationVersion = "bootstrap_chest_v1"
	PersonalCalibrationVersion  = "personal_chest_v1"
)

type ParseOptions struct {
	CalculationVersion int
	ExpectedProfileID  uint
	ExpectedSessionID  string
	ExpectedSHA256     string
	ExpectedSizeBytes  int64
	Age                *int
	EstimatedMaxHR     *int
	RestingHR          *int
	ENMORestG          *float64
	ENMORefG           *float64
	CalibrationVersion string
}

type HeartRateZones struct {
	Zone1Seconds float64 `json:"z1_seconds"`
	Zone2Seconds float64 `json:"z2_seconds"`
	Zone3Seconds float64 `json:"z3_seconds"`
	Zone4Seconds float64 `json:"z4_seconds"`
	Zone5Seconds float64 `json:"z5_seconds"`
	Zone1Ratio   float64 `json:"z1_ratio"`
	Zone2Ratio   float64 `json:"z2_ratio"`
	Zone3Ratio   float64 `json:"z3_ratio"`
	Zone4Ratio   float64 `json:"z4_ratio"`
	Zone5Ratio   float64 `json:"z5_ratio"`
}

type HRMetrics struct {
	ExcludedSeconds  float64            `json:"excluded_seconds"`
	ExcludedByReason map[string]float64 `json:"excluded_by_reason,omitempty"`
	LowBPMSeconds    float64            `json:"low_bpm_seconds"`
	ContactCoverage  *float64           `json:"contact_coverage,omitempty"`
	ValidHR          bool               `json:"valid_hr"`
	WeightedMeanBPM  *float64           `json:"weighted_mean_bpm,omitempty"`
	MinBPM           *int               `json:"min_bpm,omitempty"`
	PeakBPM          *int               `json:"peak_bpm,omitempty"`
	ValidSeconds     float64            `json:"valid_seconds"`
	UnknownSeconds   float64            `json:"unknown_seconds"`
	Coverage         float64            `json:"coverage"`
	HasHRZones       bool               `json:"has_hr_zones"`
	EstimatedMaxHR   *int               `json:"estimated_max_hr,omitempty"`
	Zones            *HeartRateZones    `json:"zones,omitempty"`
}

type ACCMetrics struct {
	LowMovementSeconds     float64 `json:"low_movement_seconds"`
	OtherMovementSeconds   float64 `json:"other_movement_seconds"`
	UnknownMovementSeconds float64 `json:"unknown_movement_seconds"`
	TotalWindows           int     `json:"total_windows"`
}

type QualityReport struct {
	Status     string   `json:"status"` // "ok", "incomplete", "corrupt", "unusable"
	IsComplete bool     `json:"is_complete"`
	HRCoverage float64  `json:"hr_coverage"`
	ValidHR    bool     `json:"valid_hr"`
	Errors     []string `json:"errors,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type HybridMetrics struct {
	HeartIntensity     *float64 `json:"heart_intensity"`
	MotionIntensity    *float64 `json:"motion_intensity"`
	HybridIntensity    *float64 `json:"hybrid_intensity"`
	HybridLoadAU       *float64 `json:"hybrid_load_au"`
	ValidHybridSeconds float64  `json:"valid_hybrid_seconds"`
	ValidACCSeconds    float64  `json:"valid_acc_seconds"`
	DataCoverage       float64  `json:"data_coverage"`
	ENMOP05G           *float64 `json:"enmo_p05_g,omitempty"`
	ENMOP95G           *float64 `json:"enmo_p95_g,omitempty"`
	PeakExceededMaxHR  bool     `json:"peak_exceeded_max_hr,omitempty"`
	QualityFlags       []string `json:"quality_flags"`
	CalibrationVersion string   `json:"calibration_version"`
	AlgorithmVersion   string   `json:"algorithm_version"`
}

type SensorMetrics struct {
	DurationSeconds float64        `json:"duration_seconds"`
	PauseSeconds    float64        `json:"pause_seconds"`
	HR              HRMetrics      `json:"hr"`
	ACC             ACCMetrics     `json:"acc"`
	Hybrid          *HybridMetrics `json:"hybrid,omitempty"`
}

type CalculationInputs struct {
	CalculationVersion int      `json:"calculation_version"`
	Age                *int     `json:"age,omitempty"`
	MaxHR              *int     `json:"max_hr,omitempty"`
	MaxHRSource        *string  `json:"max_hr_source,omitempty"`
	HasHRZones         bool     `json:"has_hr_zones"`
	RestingHR          *int     `json:"resting_hr,omitempty"`
	ENMORestG          *float64 `json:"enmo_rest_g,omitempty"`
	ENMORefG           *float64 `json:"enmo_ref_g,omitempty"`
	CalibrationVersion string   `json:"calibration_version,omitempty"`
}

type SensorSummaryResult struct {
	DeviceName         string            `json:"device_name,omitempty"`
	Version            int64             `json:"version"`
	RequestID          string            `json:"request_id"`
	SourceGeneration   string            `json:"source_generation"`
	CalculationVersion int               `json:"calculation_version"`
	CalculationInputs  CalculationInputs `json:"calculation_inputs"`
	Quality            QualityReport     `json:"quality"`
	Metrics            SensorMetrics     `json:"metrics"`
	HRBonus            float64           `json:"hr_bonus"`
	CalculatedAt       string            `json:"calculated_at"`
}
