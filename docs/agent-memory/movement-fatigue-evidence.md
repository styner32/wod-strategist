# Movement and Fatigue Evidence Rules

## Movement inputs are hints

- `VideoAnalysisPayload.Movements` and `Session.WODDescription` are planning
  context, not proof that a movement appears in the video.
- Keep WOD text, movement hints, and video observations conceptually separate.
- Target-person evidence wins when it contradicts a hint.
- For equipment-based labels, require target-person apparatus contact, body
  position, and a continuous motion pattern. Nearby equipment and background
  athletes are not evidence.
- Preserve visually supported movements that are absent from the hints. Use
  `Unknown` only when the target is exercising but the movement is unclear.
- Walking, rest, recovery, preparation, and equipment setup are not exercises.

## Structured fatigue contract

`observed_signals` keeps its existing JSON text column and adds these optional
keys without changing the mobile response shape:

- `activity_state`: `exercise`, `walking`, `rest_setup`, or `unknown`
- `fatigue_visually_established`: boolean
- `fatigue_evidence_types`: array containing only `rep_slowdown`,
  `cadence_loss`, `range_of_motion_loss`, or `form_breakdown`
- `fatigue_evidence`: concrete visual observations; never BPM values

The worker sanitizes these fields before persistence. Non-exercise activity,
unknown activity, missing visual observations, or unsupported evidence types
force `fatigue_visually_established=false` and empty fatigue arrays.

`heart_rate_bpm` remains the existing chunk-associated value. Its exact sample
time is unknown: do not add `heart_rate_sampled_at`, create a fatigue event from
BPM, or use BPM to time an event. BPM may only corroborate fatigue already
established visually during exercise.

Final two-pass analysis calls `buildSessionFatigueEvidenceContext`, which scans
every completed structured chunk and sends one aggregate to the final deep
segment. The aggregate omits BPM and prohibits fatigue output when no valid
visual evidence exists.
