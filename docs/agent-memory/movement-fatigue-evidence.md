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

New final analysis builds `workout_progress` from independent, time-bounded
observation events and sends it to a separate final assessment. No session fatigue
aggregate or historical score is sent to a deep segment, including the last.
The existing observed-signals estimates and fatigue flags are legacy/local
interpretations, not canonical performed repetitions or training intensity.
Never use them to fill missing events in the new timeline.

Final assessment distinguishes visible pace/form/ROM change from inferred
fatigue, observed post-rest performance from biological recovery, and controlled
warmup ROM expansion from inconsistency. Video cannot establish muscle activation
or abdominal pressure. Assessment results never update source observations.
See [`workout-progress.md`](workout-progress.md).
