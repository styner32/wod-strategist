package workoutprogress

// ComparisonFor selects a prior baseline for one confirmed current record.
// Callers provide the eligible history; selected-session exports omit other
// records from that session so earlier sets cannot replace a prior workout.
func ComparisonFor(current Record, history []Record) (Comparison, bool) {
	if !comparableRecord(current) || current.PerformedAt == nil {
		return Comparison{}, false
	}
	var direct, reference *Record
	for i := range history {
		prior := &history[i]
		if prior.ID == current.ID || !comparableRecord(*prior) || prior.Movement != current.Movement || !recordBefore(*prior, current) {
			continue
		}
		if reference == nil || recordBefore(*reference, *prior) {
			reference = prior
		}
		if len(conditionDifferences(current, *prior)) == 0 && (direct == nil || recordBefore(*direct, *prior)) {
			direct = prior
		}
	}
	base := direct
	kind := "direct"
	if base == nil {
		base = reference
		kind = "reference"
	}
	if base == nil {
		return Comparison{}, false
	}
	comparison := Comparison{CurrentRecordID: current.ID, BaselineRecordID: base.ID, Kind: kind, ConditionDifferences: conditionDifferences(current, *base)}
	if kind == "direct" && base.RepsPerMinute != nil && *base.RepsPerMinute > 0 {
		change := (*current.RepsPerMinute / *base.RepsPerMinute - 1) * 100
		comparison.RateChangePercent = &change
	}
	// Performed-load gain uses its own latest equal-rep lower-load baseline,
	// independent of the same-load pace comparison.
	var heavierBaseline *Record
	for i := range history {
		prior := &history[i]
		if !comparableRecord(*prior) || prior.Movement != current.Movement || !recordBefore(*prior, current) || !sameReps(current.Reps, prior.Reps) || current.LoadKG == nil || prior.LoadKG == nil || current.Load == nil || prior.Load == nil || current.Load.Basis != prior.Load.Basis || *current.LoadKG <= *prior.LoadKG {
			continue
		}
		compatible := true
		for _, d := range conditionDifferences(current, *prior) {
			if d != "load" {
				compatible = false
				break
			}
		}
		if compatible && (heavierBaseline == nil || recordBefore(*heavierBaseline, *prior)) {
			heavierBaseline = prior
		}
	}
	if heavierBaseline != nil {
		gain := *current.LoadKG - *heavierBaseline.LoadKG
		id := heavierBaseline.ID
		comparison.HeavierLoadKG = &gain
		comparison.HeavierLoadBaselineRecordID = &id
	}
	return comparison, true
}
