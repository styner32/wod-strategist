# Analysis Feedback

## Invariants

- `analysis_feedback` is append-only. Edits and undo insert a revision with the
  same `feedback_key`; they never update or delete an earlier event.
- The server captures `original_prediction`. Clients may submit only the
  structured correction, optional note, consent, and optional completed debug
  re-analysis reference.
- Production `analysis_results` and `chunk_analysis_results` are immutable with
  respect to feedback. Legacy mobile endpoints continue to return originals.
- Chunk feedback targets `chunk_analysis_results.id`, and the chunk must match
  the owned `session_id` and `profile_id` exactly.
- `client_request_id` is required for every create, edit, and retraction and is
  unique per profile. Reusing it with different content returns `409`.
- PATCH and DELETE append revision `expected_revision + 1`; stale revisions
  return `409`. PATCH may change `category` within the same revision chain.
- Notes are limited to 500 characters. Notes and fatigue corrections are never
  inserted into personal movement hints.

## Correction values

- Targets: `session`, `chunk`.
- Categories: `session_accuracy`, `movement`, `activity`, `fatigue`, `other`.
- Activity: `exercise`, `walking`, `rest_setup`, `not_exercise`, `unknown`.
- Fatigue: `fatigued`, `not_fatigued`, `walking_rest`, `unknown`.
- Movement names use the existing custom movement validation: maximum 100
  characters and safe prompt characters. They do not need to belong to the
  movement suggestion list.

## Personal hints and consent

- A movement confusion becomes a weak personal hint only when the same
  case/whitespace-normalized predicted-to-corrected pair occurs in at least two
  distinct sessions among the latest 20 feedback-bearing sessions.
- Return at most five hints, exclude the current session and retracted events,
  and never let hints override direct visual evidence.
- `consent_to_improve` is for separately reviewed offline evaluation. It is not
  automatic model training and is independent from private profile-scoped
  personalization.
