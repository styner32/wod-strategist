# Camera-original retention and gallery save

## Contract

- `app/workout/visionTestPage.tsx` records directly into `documentDirectory/originals/{profileId}/{sessionId}/sources/{order}/`. The Camera `path` option is a pre-created decoded absolute **directory**, not a filename.
- `features/video/originalVideoStore.ts` owns append-only manifest revisions, account/profile identity checks, ordered expected sources, completion evidence, Photos state and eligible cleanup. Persist relative paths because the iOS application container UUID can change.
- Camera-original quality means the camera's initial encode at the selected format. Gallery preparation never performs another lossy encode. Analysis compression and server processing remain separate.
- Do not add `expo-sharing`. Gallery uses existing `expo-media-library/legacy`, write-only photo permission and `saveToLibraryAsync`.
- Request photo-add permission before capture; denial must not prevent camera recording. Complete sessions save one full video automatically; History provides independent device-original recovery even when no server row exists. With no active profile after offline hydration, display only the authenticated owner's local sessions and their profile IDs.

## Gallery filenames (2026-09-30)

- New sessions reserve optional `galleryFileStem` at `prepareOriginalSession`: `{WARMUP|WOD|ACCESSORY|COOLDOWN}-{YYYYMMDD}-{N}`. The date uses device-local `createdAt`, not save time or the UTC date. Type comes from the session ID; session IDs/server paths are unchanged.
- `N` starts at 1 per day and is shared across all workout types, profiles and accounts in this app installation. Serialize reservations across sessions. Store immutable, non-personal markers under `Documents/originals/filename-sequences/{YYYYMMDD}/{N}.json`; interrupted `.json.tmp` markers also consume a number. Do not prune markers during video cleanup or reuse failed-start numbers. Reinstallation/removing app data removes this local sequence history; no cross-device uniqueness is promised.
- Persist the reserved stem in the initial manifest before camera start. Generate `output/{galleryFileStem}.mp4`; preserve the native merger's actual `.mp4`/`.mov` extension. Photos receives that named file directly, without an extra video encode or staging copy. Restart, date/time-zone change and gallery retry retain the assigned stem.
- Manifests without `galleryFileStem` keep their previous `outputPath` or `output/original.mp4` fallback. Never retroactively rename prior pending/saved videos, and keep existing uncertain-save confirmation and source-retention rules.
- Automated tests cover concurrent type/profile allocation, local midnight, restart, account changes, failed starts/partial reservations, save retry, MP4/MOV filenames and old manifests. Physical iPhone Photos filename display and Mac export of a new named video still require device acceptance; mocks verify only the URL supplied to the existing save API.

Validation on 2026-09-30: 4 suites / 62 tests passed, along with type checking and diff whitespace checks:

```sh
TZ=Asia/Seoul npm test -- --runInBand features/video/originalVideoStore.test.ts features/video/OriginalVideosPending.test.tsx app/workout/__tests__/visionTestPage.original.test.tsx features/wod/__tests__/mergeChunksLocal.test.ts
npm run typecheck
git diff --check
```

## Retention and recovery

- Register an expected source before starting native capture. Register the closed, nonempty source only from its finalization callback. Keep valid very short tails.
- Never infer a complete workout from whatever files happen to exist. Missing, corrupt, unknown or unfinalized sources become `needs_attention` and remain on disk.
- Source-finalization timeout/error, native dropped buffers, tail-drain timeout or interrupted recording prevents a complete-original claim.
- Write a durable `saving` state **before** the non-idempotent Photos operation. On restart it becomes `uncertain`; require gallery confirmation or explicit duplicate-aware retry. Never auto-replay uncertain saves.
- Cleanup requires a persisted `saved` state and zero `holdOriginalFiles` consumers. Hold throughout queued compression/upload, native derivative preparation and environment reads. `release` is idempotent. Never directly delete source paths in the recording screen.
- `onRecordingSourceFinalized` closes the source barrier. `onRecordingFinished` closes the analysis-preparation barrier. Gallery saving can begin at the first barrier; cleanup waits for the second and all readers.
- Unexpected screen unmount marks an active original incomplete and drains readers before releasing its hold. Native view detach stops both the legacy and segmented writer if the JS view reference has already disappeared; it does not cancel/delete the recording. Manifest-write failure must not leave the same-process recording marker permanently active.
- The server merge is withheld if any analysis input failed or uploads still remain after the existing wait limit. This does not prevent local original saving.
- Users can discard unconfirmed or failed original sessions from History (`OriginalVideosPending.tsx`) via `discardOriginalSession(ref)` with explicit confirmation. Discarding recursively removes the session's originals directory, cleans memory state, and notifies listeners without touching global sequence counters. Active recording, active Photos save, or held sessions cannot be discarded.

## Local merge

- `modules/video-merger` never skips bad inputs, never deletes sources and never deletes a previous result before trying again.
- Generate a unique temporary sibling, validate media, then atomically replace the destination. Return the actual output URI (MP4 or MOV), not the requested extension unconditionally.
- `inputCount` is required completion evidence. Reject an older native result without it, retaining originals until the updated native app is installed.
- iOS tries passthrough MP4 and MOV compatibility fallback. If media cannot be preserved, fail and retain the source files.
- Preserve complete sample durations, audio tails, dimensions and orientation; do not advance the timeline by `maxPTS + 1ms` or discard sub-200ms camera files.
- AAC packet counts alone do not prove audible preservation because packets may include priming/padding. Validate decoded, presented PCM and its placement (canonicalizing only IEEE signed zero), alongside compressed-media format, video sample counts and track endpoints. Verification may decode audio; the saved output must still copy compressed media without encoding.
- `AVAssetExportSession` can round movie/edit-list durations to a 600-tick clock and omit sub-tick empty edits. `PassthroughMovieTimeline` restores exact composition timing and those verified empty edits in the unpublished file. If metadata grows, append the new `moov` and mark the previous one `free`, preserving every media byte offset. Reject ambiguous/coalesced edit mappings, unsupported metadata or overflow rather than publishing a partial result.
- On iOS, a valid source with no audio track is a silent interval, not a reason to omit its video or reject the session. Preserve later audio at its original offset; incompatible audio formats and unreadable existing tracks still fail. This does not recreate audio that the camera never recorded.
- Compare the ordered hashes of compressed video samples exposed by AVFoundation as well as sample counts. Raw container packet totals can include unused preroll outside edit lists; they are not interchangeable with presented frame counts.

## Experimental continuous recording

- Route/preference `continuousRecording` defaults to `false`, supported only by the iOS native patch. The setup screen labels it experimental. No resolution/device has yet been approved for default activation.
- Reproducible dependency changes live in `patches/react-native-vision-camera+4.7.3.patch`; retain the pre-existing patch content. Run `pod install --deployment` after installing the patched package so new native source files enter the Pods project. Do not upgrade dependencies for this feature.
- A single continuous encoder emits fragmented media; the archive queue appends its bytes to the original. About 10-second boundaries do **not** stop the recording. Separate serial preparation creates standalone zero-based analysis videos from the encoded stream.
- Pause stops and closes one active run; resume starts another. Final archive merges runs losslessly. Do not call native pause/resume on the segmented writer.
- Native events carry run/source identity, sequence, actual capture start/end and preparation success/failure. Terminal `VideoFile.segments` additionally marks `isLast`; live events cannot know the final fragment before stopping. Missing live events are replayed once by index from terminal metadata. Keep finalization and analysis drain distinct; never apply the legacy five-second stop timeout to continuous recording.
- Video offsets use native capture timestamps anchored to epoch time, not export callback time. `captureClockOffsetMs` maps iOS `Frame.timestamp` (native PTS in milliseconds) into that same epoch clock. BLE readings retain their own `receivedAt` clock. The sensor footer retains its independent stop time.
- Analysis failures must never terminate original capture. Fatal original-write failures retain all committed bytes and report incompleteness.

## Validation boundary

Automated filesystem/Photos mocks prove state transitions and deletion guards, not physical gallery behavior. Native generated-file tests prove only the tested codec/container/sample cases. A simulator Release build proves compilation, not camera continuity, thermal behavior or Photos preservation.

Before enabling any default continuous-recording setting, test a physical iPhone Release build at 720p/1080p for 10 and 30 minutes, stop immediately before/after segment boundaries, pause/resume and background/foreground. Compare source and exported dimensions, codec, sample/frame counts, decoded-frame hashes, duration, orientation, audio and capture timestamps. Verify video/audio continuity across every boundary.

Also exercise denied/revoked photo permission, low storage, save/export failure, restart during recording/merge/Photos save, save retry and uploads still reading source files during gallery completion. Test 4K and simultaneous AI separately. Previously lost or deleted footage cannot be recovered by this change.

Apple references: [fragmented MP4 authoring](https://developer.apple.com/videos/play/wwdc2020/10011/), [asset-writer file profiles](https://developer.apple.com/documentation/avfoundation/avassetwriter/outputfiletypeprofile).

## Repeatable local checks

From the repository root:

```sh
npm test -- --runInBand app/workout/__tests__/visionTestPage.original.test.tsx features/wod/__tests__ features/video/originalVideoStore.test.ts features/video/OriginalVideosPending.test.tsx features/environment/__tests__/recorder.test.ts features/health/polar/__tests__/polarSensorRecorder.test.ts
npm run typecheck
scripts/native-video-tests/run-segmented-smoke.sh
python3 modules/video-merger/tests/verify_passthrough.py
git diff --check
```

The capture harness prints its fixture directory. Pass its two nonzero-audio `original.mp4` paths to the merger harness as repeated `--native-source` arguments to check actual native fragmented output as well as generated legacy fixtures. Both harnesses use temporary files; the capture harness requires access to macOS codec services. See `scripts/native-video-tests/README.md` for the limits of this evidence.

After adding native source files, run `RCT_USE_PREBUILT_RNCORE=1 RCT_USE_RN_DEP=1 pod install --deployment` from `ios/`, retaining the lockfile. Then build from the repository root:

```sh
xcodebuild -workspace ios/wodstrategist.xcworkspace -scheme wodstrategist -configuration Release -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath /tmp/wod-original-video-build ARCHS=arm64 ONLY_ACTIVE_ARCH=YES CODE_SIGNING_ALLOWED=NO build
```

No backend API, schema migration, dependency upgrade, deployment or physical-device acceptance is part of these local checks.

## Implementation and validation record (2026-09-29)

- Capture/setup integration: `app/workout/visionTestPage.tsx`, `app/workout/setup.tsx`, the VisionCamera patch and `features/wod/chunkRecordingCompletion.ts`.
- Persistent originals and recovery UI: `features/video/originalVideoStore.ts`, `features/video/OriginalVideosPending.tsx`, `features/wod/ui/HistoryList.tsx`, both `features/i18n/locales/{en,ko}.json`.
- Lossless merge: `features/wod/mergeChunksLocal.ts`, the `modules/video-merger` bridge, iOS Swift and Android Kotlin implementations.
- Capture-window metrics: `features/wod/captureWindow.ts` and `features/ai-coach/frame-processors/usePoseDetection.ts`.
- Added/updated tests cover recording start/stop/unmount races, source-finalization versus analysis-drain barriers, final short sources, permission/save/restart uncertainty, ownership, cleanup holds, merge failures and capture-window metrics. The existing Polar fixture now mocks optional native-module lookup so Expo's teardown-time lazy initialization does not fail unrelated tests.

The combined Jest command passed **10 suites / 95 tests** with exit code 0, including older native binaries missing completion evidence. TypeScript checking and the final arm64 iOS Release simulator build passed. Native synthetic checks confirmed continuous fragmented capture, short tails, isolated derivative failure, dropped-tail reporting, final-event replay and duplicate legacy stop callbacks. Native merge checks compare compressed sample hashes, presented PCM and timing, including real native-writer single runs and a two-run pause seam. Inputs whose waveform cannot be preserved are rejected with sources and prior output retained. Android's core merger compiled against SDK 36.1 and its pure timeline checks passed; see `modules/video-merger/tests/README.md` for that check's scope.

Physical iPhone camera/Photos behavior, 10/30-minute runs, thermal/load behavior, 4K/HDR/HEVC combinations and Android device playback remain unverified. No configuration is approved for default continuous-recording activation yet; the default stop/start recording path still has its existing boundary gaps.

## Device save-failure follow-up (2026-09-29)

A rebuilt iPhone app repeatedly failed before Photos saving. Its retained manifest identified `VIDEO_MERGE_ERROR`, but Expo's debug bridge rendered the generic exception as `undefined reason`. Use `VideoMergeException: GenericException<String>` overriding `reason` and `code`, rather than `promise.reject(code, description)`, to keep the actual native cause visible.

With permission, four real 4K HEVC MOV originals were copied from the device for local diagnosis. The first had no audio track. Rejecting mixed audio presence caused the first failure; two omitted video gaps (62.5us and 812.5us) exposed the edit-list failure next. Both paths were corrected without reencoding. The same four inputs merged successfully to MOV. Their 1,133 AVFoundation-readable compressed video samples matched in order; the reference composition and result had identical 1,111 decoded NV12 frames and presentation timestamps, including two renderer-generated gap frames. Presented audio passed the existing exact PCM validation. This verifies this retained recording on macOS, not Photos saving on the iPhone or general 4K acceptance.

`lastErrorStage` is optional backward-compatible manifest metadata (`validation`, `permission`, `preparing`, `gallery`). History displays the failed stage and provides an explicit, selectable error-details expansion, including for older records with only `lastError`. Never clear a failure by weakening preservation checks or deleting its sources.

Follow-up validation: the same combined Jest command above passed **10 suites / 97 tests**, `npm run typecheck` passed, the expanded `verify_passthrough.py` corpus passed, and the iOS Release simulator build passed. Changes are in the three iOS merger Swift files, the original store/recovery UI, both locales, their tests and the native corpus. The iPhone's source files were read/copied only, never modified or deleted; installing the updated native app and retrying Photos saving on the device remain necessary.

## Continuous startup failure follow-up (2026-09-29, 17:06 device session)

- The device manifest reports `mode=continuous`, `complete=false`, one unfinalized source and termination about 220ms after session creation. The source directory is empty. This is a startup failure, not evidence that an earlier gallery save blocks new recording. The persisted `[object Object]` erased the native cause; the specific device exception cannot be recovered from that manifest.
- `CameraSession+Video.swift` now activates audio on `CameraQueues.audioQueue` before reading its format and recommended encoder settings. Previously it rejected missing settings before activating the idle audio capture session. Keep this ordering, preserve typed `CameraError` cases, and stop audio on failed startup. This fixes an initialization defect; device acceptance is still required to attribute this particular failure to it.
- `features/wod/recordingErrorMessage.ts` preserves plain native callback dictionaries (`code`, `message`, nested cause) as well as normal errors. Capture failures remain incomplete and retain their files. Do not enqueue server merge or gallery finalization for an incomplete capture, or show a second gallery-wait alert for an already-reported capture failure.
- Regression tests exercise both synchronous native-start throws and asynchronous error callbacks, followed by a successful new session; they assert readable failure evidence, released recording/consumer state and no spurious gallery/server wait.

Validation: `npm test -- --runInBand app/workout/__tests__/visionTestPage.original.test.tsx features/wod/__tests__/recordingErrorMessage.test.ts` passed 2 suites / 14 tests; `npm run typecheck` and `git diff --check` passed. Regenerated the VisionCamera patch, checked clean application with `patch-package`, and verified all 18 patched files match the installed implementation. The Release simulator `xcodebuild` command above passed again. No device installation or physical camera/microphone test was performed in this follow-up; rebuilding the installed app is required.

## Microphone readiness follow-up (2026-09-29, 17:21 screenshot)

The rebuilt app exposed `capture/create-recorder-error: Audio is enabled but its capture format/settings are unavailable`. Activating the audio session before querying did not resolve the device failure. Do not equate `startRunning()` returning with a usable audio format/encoder recommendation.

- Continuous startup now waits for the first nonempty, ready microphone sample. `SegmentedAudioConfiguration` uses its actual `CMFormatDescription`; it no longer depends on `audioDeviceInput.device.activeFormat`. If MP4 recommendations are nil/empty, create initial AAC settings with the delivered PCM rate/channels and bitrate `min(320000, 128000 * channels)`. Do not silently disable microphone recording or substitute an arbitrary sample rate/channel count.
- `SegmentedAudioStartup` serializes its one-shot outcome on the camera queue and times out after 5 seconds. `pendingSegmentedRecording` reserves capture on that queue; `segmentedAudioStartup` is accessed only on the audio queue. Audio callbacks offer data without waiting on the camera queue. Stop/view detach cancels startup, late buffers cannot revive it, and retry cannot race prior audio shutdown. Missing microphone input/output and missing usable PCM have distinct error messages.
- Reproducible implementation remains in the VisionCamera patch (existing Swift files; no new Pod source entry). Added `scripts/native-video-tests/SegmentedAudioStartupSmoke.swift`; the existing continuous encoding fixture now uses the production AAC fallback. These tests cover startup and generated media only; physical microphone startup/Photos/long-run continuity still require device acceptance.

Validation: `scripts/native-video-tests/run-segmented-smoke.sh` passed (startup, lifecycle, JS callbacks, continuous A/V samples and legacy stop cases). Patch-package clean application matched all 18 changed files. The Release simulator `xcodebuild` command above and `git diff --check` passed. No device install or microphone capture was performed.
