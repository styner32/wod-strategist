# On-device environment and behavior journal

## Scope and switches
- `environmentObservation` defaults OFF; independent of `onDeviceAi` (posture) and MoveNet.
- `environmentAnalysis` (and `visualQuestions`) defaults false inside the enabled journal. Visual LLM questions (camera, behavior, space) are OFF by default, avoiding NPU load and segment read races. False retains motion/power/events and sound analysis without visual LLM questions.
- `observationIntervalSeconds`: 120, 300 (default for new/reset settings), or 600. Backward compatible with 30 and 60. Setup persists and passes all parameters to `visionTestPage`.
- Periodic WeatherKit probe in recorder is disabled until Apple Developer capability and entitlement are provisioned.
- Native protocol `environmentVersion = 1`; posture remains `promptVersion = 3`. Older binaries report `module_missing` rather than accepting ignored parameters.
- Only within-recording exercise/rest/space interactions. No equipment cleanup, character, intent, inferred bystander emotions, repetition totals or unobserved actions.

## Scheduling and evidence
- `EnvironmentRecorder`: camera -> behavior -> sound -> space, one attempt per interval. First attempt after the interval; uses only the latest completed chunk if its completion is no older than one interval. No backlog/catch-up.
- `acquireAppleAiSlot` is shared with posture capture/preparation/inference and explicit history reanalysis. An environment attempt skips a busy slot. Native AppleAiEngine is a second guard; cancellation holds the slot until native work settles.
- Extract beginning/middle/end (maximum three full-frame JPEGs, maximum 640px) of ONE already completed chunk via AVAssetImageGenerator. No added camera frame processor, continuous image detector or candidate-frame scan. `environmentFrames` returns `{error:"no_video_track", frames:[]}` for a chunk without a video track; the recorder must record that as `outcome:"error"` and never call `observeEnvironment` with zero frames (native AppleAiEngine also rejects 0 frames as `invalid_frames`).
- Preserve actual media offsets returned by the generator. `captureStart/captureEnd` include callback/wall timing and are NOT merged-video positions. Source filename is the local original chunk identity, not a claim about a compressed remote filename.
- Sound: at most first five seconds from the completed chunk, AVAssetExportSession -> m4a -> SoundAnalysis `version1`. Top three labels per sound window and RMS/peak dBFS; not calibrated SPL, no transcript. `no_audio_track` is not silence.
- Auxiliary native extraction/classification uses a non-queuing runner, 15-second cancellation deadline. Model deadline is 30 seconds. Stop drains these operations before local chunk cleanup.
- Motion: CoreMotion device attitude/rotation/acceleration at 1Hz; records device motion, NOT athlete motion. Includes process physical footprint bytes, not AI-specific memory consumption. Device identifier is `hw.machine`.
- Power queries use a shared five-second cache with posture (`observedAt` retained); no second power polling timer. Weather at start/5min uses approximate location, current outdoor conditions and Apple attribution. Location permission is requested before recording begins; the recording-time fetch never opens a permission prompt. Location request deadline 15s; weather cancellation deadline 25s. No route tracking. Indoor ambient temperature/humidity is not measured.
- Unsupported exposure/focus metadata is explicitly listed; do not infer ISO, light in lux, or lens focus distance from configured preferences.
- Existing sensor/MoveNet provider snapshots and source links are recorded without enabling disabled pipelines.

## Storage and upload
- Local bundles: Documents/environment/{ULID}/, version-1 session.json, immutable environment_{recordId}.json, environment_{recordId}_{index}.jpg/m4a, environment_{bundleId}_events.ndjson.
- NdjsonWriter now accepts optional `directoryName` (default remains sensor). Environment flushes every 5s, bounded by the existing writer retry buffer. Stop stores writer completeness. Interrupted sessions have complete=false.
- Remote files stay under videos/{profileId}/{sessionId}/environment_* using the existing upload URL and generic asset uploader; no chunk-complete calls. Evidence first, record JSON next, session metadata last. Local uploaded markers avoid redundant retry.
- Uploads are blocked while ANY app recording is active. Flush after stop/auth completion, login and foreground. Local evidence is retained after upload for user review and reanalysis.
- Metadata pins ownerUserId/profileId/sessionId; scanning and upload cannot switch to a different logged-in account. Paths resolve under the current Documents directory; no persisted absolute sandbox path.
- Each observation saves source API/model, actual prompt body AND exported system instruction, input context and files, raw answer, validated answer, preparation/inference duration, before/after cached power samples. Unknown exact model revision/tokens/execution hardware remain null.
- Raw captions, invalid JSON and failures remain evidence; no AI answer contributes to habits without explicit user target and behavior confirmation; template-only answers additionally need a user-corrected observation.

## Review and history
- History does NO inference or image loading on entry. Opening a journal reads recent 30 session records; images/audio mount only when the corresponding evidence is opened. Observation list reveals 30 rows at a time.
- One habit occurrence per session per code. Three distinct sessions = repeated observation, not a verified habit. Never divide by unobserved opportunities or infer improvement from absence.
- Review records are immutable; latest review per observation wins. Only verdict=correct AND targetConfirmed=true AND explicitly selected confirmedBehaviors count. Legacy generic correct reviews do not count. Optional correctedObservation is user evidence, never an overwritten AI answer. Notes alone do not change behavior identity. Empty/template-only facts require a non-template user correction.
- Reanalysis copies identical input evidence, links parentId, uses the current versioned prompt, original.context (not current session.context), original questionId when available, and cannot inflate habit counts. Summary uses bounded rule-aggregated evidence and runs only when explicitly requested. Both share the native slot and reject active recording; background/memory warning cancels.
- Supported behavior codes: grip_reset, foot_reset, rest_support, rest_walk, position_shift, transition, path_overlap, give_space.

## Deletion and signing
- `DELETE /api/v1/sessions/:session_id/environment?profile_id=...` requires authenticated profile ownership and deletes only the exact session's environment_ object prefix. No video/sensor deletion. Idempotent; partial failures are retryable.
- Client persists a `deleting` tombstone; pending deletes suppress further upload and retry on next flush. Local evidence is removed only after remote success. History archive remains a soft hide, not deletion.
- This new deletion endpoint requires backend deployment. General account-level GCS cleanup is a pre-existing backend TODO; this feature does not claim that TODO is solved.
- WeatherKit entitlement and location/motion descriptions are in app.json and the iOS target. Apple Developer App ID capability and provisioning must permit WeatherKit on a signed device; unsigned compilation cannot establish that. Attribution mark and legal source link render alongside weather evidence.

## Validation boundary
- Automated tests cover parsing/identity constraints, session counting and review revisions, slot serialization, interval/pause/stop/low-power/memory guards, upload order/retry/account isolation/crash recovery/deletion retry, and the authenticated deletion route with real clients + MockTransport.
- Unsigned iOS build and typecheck are compile checks only. Real iPhone observation accuracy, permissions, WeatherKit signing/service access, battery/thermal and recording OFF/ON comparison remain device acceptance work.
- Compare same device/settings at 30s, then 60s/120s if needed. If unacceptable, set environmentAnalysis=false. Never call a passed build proof of acceptable power use.

### Commands used for this change
- `npm run typecheck`
- `npm test -- --runInBand features/environment/__tests__ features/ai-coach/__tests__ features/video/__tests__` — 10 suites / 81 tests passed, including lazy history loading and measurements-only mode.
- `npm test -- --runInBand` — 38 suites / 367 assertions passed; process exit 1 from Expo lazy-fetch teardown warnings (`Cannot log after tests are done`), not a failed assertion. Keep this distinction in reporting.
- From `api/`, with isolated test PostgreSQL configured: `go test -p 1 ./internal/controllers ./internal/server ./internal/storage -ginkgo.focus='DELETE /api/v1/sessions/:session_id/environment|registers|GCS client'` — passed.
- `pod install` in `ios/`; `xcodebuild -workspace ios/wodstrategist.xcworkspace -scheme wodstrategist -configuration Debug -destination 'generic/platform=iOS' -derivedDataPath /private/tmp/wod-apple-ai-build CODE_SIGNING_ALLOWED=NO build` — passed.

## Web archive viewer (2026-09-22)
- Session detail → **온디바이스 AI 관찰 기록 → 기록 확인** lazily mounts `web/src/history/components/OnDeviceAiPanel.tsx`. Both legacy `apple_ai_*.json` (schema 1/2) and `environment_*.json` are supported. Selected/cropped preparation frames take precedence over original candidate frames. No new AI call, deletion or review mutation is issued by the web viewer.
- `GET /api/v1/sessions/:session_id/on-device-ai?profile_id=...&after=...` lists 50 manifest/telemetry filenames per page, lexicographically; `next_cursor` is empty at end. It uses existing per-session GCS listing, not a DB migration. List order is filename order, not guaranteed capture-time order; detail displays stored capture time.
- `GET .../on-device-ai/asset?profile_id=...&filename=...` requires JWT and exact profile ownership for every request (including media). Filenames are flat `apple_ai_`/`environment_` assets only. JSON is proxied with a 2 MiB read limit and `private, no-store`; image/audio/NDJSON requests redirect to 15-minute signed URLs. Bucket JSON CORS is not required. No signed URLs are persisted in journals.
- UI reads one selected JSON at a time, shows raw/parsed response, context, prompt version, source, preparation/inference time, power, review and reanalysis references by original record ID, metadata and evidence. SoundAnalysis/WeatherKit are explicitly separate from Foundation Models. Telemetry opens as the original NDJSON; no web sensor chart or cross-session habit aggregation is added.
- Deploy both API and web to expose this viewer. An older server's 404 is shown separately from an empty uploaded journal. Existing uploaded files are readable without another iOS build.

### Web viewer validation
- `cd web && npm run build` passed (existing >500 kB bundle warning).
- `npm test -- --runInBand web/src/history/__tests__/onDeviceRecord.test.ts`: 4 tests passed.
- From `api/` with isolated test PostgreSQL: `go test -p 1 ./internal/controllers ./internal/server -ginkgo.focus='GET /api/v1/sessions/:session_id/on-device-ai|registers'` passed, covering ownership, legacy IDs, pagination, JSON bounds and session-scoped media signing.
- Browser QA used synthetic local records: expand journal, response/evidence rendering and context disclosure confirmed. Live uploaded production records and deployment remain unverified.
- Targeted ESLint could not start due to the installed TypeScript/parser incompatibility (`Cannot read properties of undefined (reading Cjs)`). No toolchain dependencies changed.

## Observation v2 (2026-09-23)
- Natural-language one-question requests replace JSON/example completion. Each camera turn cycles occlusion/framing/lighting; behavior cycles posture/support/position; space asks relative_position. Sound remains unchanged. No extra inference or image sampling.
- Optional record fields: questionId (camera.occlusion, camera.framing, camera.lighting, behavior.posture, behavior.support, behavior.position, space.relative_position), responseFormat=text, targetContext=provided|missing, quality={version:1,status:flagged|unchecked,flags:[empty_response|example_copy]}.
- v2 keeps raw unchanged, parsed=null, validation=not_applicable. outcome=success means API response completion only; quality=unchecked means no mechanical warning, NOT verified accuracy. Failure/cancellation raw responses are also retained. Existing v1 JSON stays readable.
- Optional review fields: targetConfirmed:boolean, confirmedBehaviors:Behavior[], correctedObservation:string (max 2000 characters); verdict/note remain compatible. Latest successful review wins. Target/behavior controls start unselected; user corrections and verdicts are separate immutable records. Review may identify a target even if original appearance hints were missing.
- Both old and new behavior records require explicit review for aggregation. Limit to the newest 30 sessions and one occurrence per behavior/session, including reviewed reanalyses; reanalysis timestamps do not make an old session recent.
- Setup's actual WOD/movement/appearance preview is shown for posture OR enabled environment analysis, with explicit target-missing text. It does not reuse an old outfit automatically.
- Mobile history displays original/reanalysis prompts, raw responses, inference times and latest reviews. Web comparison is lazy-loaded; selecting a child fetches only its original JSON. 'Load comparisons and reviews' explicitly scans the session's paginated environment JSON records (four concurrent reads, no media), then compares directly linked records and latest reviews. A failed scan is incomplete, never 'unreviewed'. No web inference or review writes.
- No native protocol/dependency change. Reanalysis rejects active recording/background/power/thermal states and preserves cancellation evidence; persistence failure must release the shared slot.
- Device acceptance remains manual: user-selected 10–20 saved inputs, v1/v2 feedback review, and matched OFF/ON recordings at 60s, then 120s or measurements-only if needed. Automated tests do not establish accuracy or energy improvement.

### v2 validation
- `NODE_PATH=__mocks__ npx jest features/environment features/ai-coach/__tests__ web/src/history/__tests__ --runInBand --silent`: 91 tests passed across 12 suites.
- `npm run typecheck` passed; `cd web && npm run build` passed (pre-existing main bundle >500 kB warning). Comparison UI is split into a lazy chunk.
- Local browser QA with synthetic records confirmed original/child side-by-side prompts, responses, durations, template-copy warning, explicit review loading and English/Korean switching. No real on-device inference was run; fixture timings are not performance measurements.
- No deployment or real-device accuracy/thermal comparison performed by this implementation.
