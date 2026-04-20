# Storage and Session Format Memory

## Session ID format
Current format:
`{TYPE}-{YYYYMMDD}-{ULID}`

Example:
`WOD-20260407-01JQXYZ3K4M5N6P7Q8R9ABCDEF`

Rules:
- never embed profile ID in session ID
- use profile ID as a GCS path prefix
- support both old and new formats when parsing

## Legacy format
Old format:
`P{profileId}-WOD-YYYY-MM-DD-HH-MM`

Both formats must remain supported.

## GCS layout
Current layout:
`videos/{profileId}/{sessionId}/{filename}`

Examples:
- chunk files
- `merged.mp4`
- `hardsubbed.mp4`
- highlight outputs
- generated audio assets

Rules:
- always pass `profile_id` where required
- place new session-scoped assets under `videos/{pid}/{sid}/`
- do not create new top-level prefixes for session artifacts

## API requirements
`profile_id` is required for upload/download and chunk/merge flows unless explicitly handled as legacy behavior.

## Backward compatibility
Existing code supports both:
- directory-per-session layout
- legacy flat object naming

Preserve both unless a migration is explicitly requested.