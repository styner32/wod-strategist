# Mobile Runtime Memory

## Android recording performance
Android performance protections must be controlled by user-configurable flags, not hardcoded platform checks in `visionTestPage.tsx`.

### Configurable flags (setup.tsx → visionTestPage.tsx via route params)

| Flag | Param | Android Default | iOS Default |
|---|---|---|---|
| Skeleton Overlay | `showSkeleton` | OFF | ON |
| Low FPS (24fps) | `lowFps` | ON | OFF |
| Force 720p | `force720p` | ON | OFF |
| Skip Chunk Compression | `skipCompression` | ON | OFF |

### Non-configurable (hardcoded per platform)

| Optimization | File | Android | iOS |
|---|---|---|---|
| MoveNet inference FPS throttle | `usePoseDetection.ts` | 2 fps recording / 1 fps preview | 2 fps recording / 1 fps preview |
| `android:largeHeap` | `AndroidManifest.xml` | `true` | N/A |

### Rules
- Do NOT hardcode `IS_ANDROID` for feature gating in `visionTestPage.tsx` — use the route-param configuration pattern.
- The `usePoseDetection.ts` throttle uses `runAtTargetFps()` from `react-native-vision-camera`. Do not remove it — it is the single most impactful fix for Android OOM.
- The recording dashboard shows **OPT FLAGS** during recording. Keep this in sync when adding new flags.
- `onDeviceAi` defaults OFF and controls the Apple Foundation Models feedback experiment independently of MoveNet. See [apple-on-device-ai.md](apple-on-device-ai.md) for the iOS 27 native build requirement, image sampling, cancellation, and power protection contract.
- `continuousRecording` defaults OFF and is an experimental iOS recorder capability, separate from Android performance flags. See [original-video.md](original-video.md) for durable originals, automatic Photos saving, native source-versus-analysis completion and device acceptance requirements.

## BLE heart rate monitor
BLE HR integration uses `react-native-ble-plx`.

The scan filter matches devices by name or HR service UUID (`180D`) and explicitly excludes `"Polar mobile"` devices. The Polar Beat/Flow phone app re-broadcasts HR data as a BLE peripheral — connecting to it instead of the actual strap causes failed connections and GATT errors.

### Connection behavior

| Setting | Value | Purpose |
|---|---|---|
| Connection timeout | 10s (`CONNECTION_TIMEOUT_MS`) | Fail fast instead of hanging ~60s |
| Inactivity timeout | 15s (`INACTIVITY_TIMEOUT_MS`) | Reconnect if no HR data received |
| Reconnect backoff | 1s → 10s exponential | Prevents rapid reconnect storms |

### Rules
- Never remove the `"Polar mobile"` exclusion from scan filtering.
- Keep `BleManager` as a singleton outside React components to prevent memory leaks.
- `react-native-ble-plx` v3.5.1+ is required — earlier versions crash on Android (RN 0.76+) when `Promise.reject` receives a `null` error code.

### Chunk heart rate sampling (Peak BPM)
- Accepted readings are retained in `CaptureWindow` with their original `receivedAt` timestamps. Query each video's capture interval to send its peak as `heartRateBpm` to `processWorkoutChunk`.
- Delayed segment/export callbacks must not reset a mutable current-chunk maximum: they may arrive during the next capture interval. Continuous iOS events carry native epoch start/end times; MoveNet observations use the same native clock anchor.
- 1Hz telemetry recording continues to record instantaneous samples via `bpmRef.current`.

### Polar ACC packet timing
- `parseAccPacket` accepts device-derived `dt` only within 10% of `1000 / accHz` (the negotiated rate), allowing the observed 19.53ms interval at 50Hz.
- A rejected delta uses `lastSampleIntervalMs`, or the nominal interval before a valid measurement exists. Do not stretch samples across packet loss using a fixed upper limit such as 60ms.
- Reset the measured interval with the clock anchor on a new stream; stop/disconnect also clears it. This heuristic cannot detect every small partial-packet loss without a sequence counter.

## iOS scene lifecycle

- See [ios-scene-lifecycle.md](ios-scene-lifecycle.md) for Expo SDK 57 / Xcode 27 scene ownership, the iOS 16.4 minimum, native regeneration checks and dependency compatibility patches.

## Recording screen orientation

- `RootLayout` sets `OrientationLock.PORTRAIT_UP`. On iOS, `visionTestPage.tsx` must override it on every focus: use `OrientationLock.DEFAULT` for device-driven rotation, or `LANDSCAPE` when `landscapeMode=true`. Omitting the default override leaves ordinary recordings locked in portrait. Restore `PORTRAIT_UP` on blur and report rejected orientation requests.
- Use current window dimensions for the iOS layout. Size the viewfinder within its measured flex area after safe-area insets and recording controls; rotation must not remount the camera or restart recording.
- In a physically landscape window (`width > height`), put header/status in one row and the camera beside a controls/telemetry sidebar. Expanded telemetry must not share the camera's vertical space or impose the portrait thumbnail height cap. Keep the same camera subtree across orientation/toggle changes.
- Android retains the existing portrait Activity restriction for CameraX; `landscapeMode` uses the sideways-mount layout there. JavaScript tests do not establish physical-device rotation or capture continuity.

## iOS sensor upload startup safety
- A reinstall can change the absolute `file:///var/mobile/Containers/Data/Application/{UUID}/Documents/` prefix while retaining the files. `loadQueue()` rebases saved `Documents/sensor/*.ndjson` paths against the current `documentDirectory`, including backup recovery, without changing request IDs or upload stages.
- Check `getInfoAsync(filePath)` before `PREPARE_PENDING` / `PUT_PENDING`. Missing files or directories become `NEEDS_ATTENTION`; retain the queue entry. `COMPLETE_PENDING` must still reconcile with the server even if the local file is absent.
- Sensor PUTs use legacy `uploadAsync`, whose iOS implementation checks file existence before constructing a background upload. `createUploadTask().uploadAsync()` uses `uploadTaskStartAsync`, which lacks that guard in the installed Expo SDK and can raise an uncaught `NSInvalidArgumentException` for a stale path. JavaScript `.catch()` cannot catch that native exception.

## Internationalization (i18n)
Setup lives in `features/i18n/index.ts`. Locale resources are at `features/i18n/locales/{en,ko}.json`.

- Locale is resolved from device (`expo-localization`) at startup; only `en` and `ko` are supported. Unknown codes fall back to `en`.
- A Zustand store (`useLocaleStore`) drives re-renders when the user switches language. Components reading the active locale should call `useLocale()`.
- Use `t("key", { ...vars })` for every user-facing string — do not hardcode English.
- `setLanguage(code)` switches the locale at runtime.
- When adding a new string, add the key to **both** `en.json` and `ko.json` in the same change.

## Contact quality and sensor summaries

- See [heart-rate-quality.md](heart-rate-quality.md) for version 2 contact/drop filtering, raw versus accepted BPM, request version pinning, response-only summaries, and device acceptance status.
- Chunk peaks now update synchronously from accepted readings; stale/invalid fallback BPM is omitted. A stable BPM below 55 is warned about, not discarded solely for being low.

## Camera capability detection and 4K recording

- **Capability-Driven**: Do NOT hardcode device models for high-resolution features. Check `device.formats` at runtime via `supports4K30Fps()` (`features/video/cameraCapability.ts`).
- **30fps Invariant**: Recording remains at 30fps (or 24fps when `lowFps` is enabled) even when 4K (2160p) is selected.
- **Graceful Degradation**: If 2160p is persisted from a previous device session but the current device lacks 4K 30fps support, auto-fallback to 1080p (`resolveSafeResolution`).
- **Frame Processor Compatibility**: Recording and pose-test cameras explicitly use `pixelFormat="yuv"`, `videoHdr={false}`, and `enableBufferCompression={false}`. The installed `vision-camera-resize-plugin` accepts uncompressed 8-bit YUV/BGRA only; HDR selects 10-bit YUV and throws `Invalid PixelFormat`, even if `supportsVideoHdr` is true. Do not prefer HDR in format selection or enable it while using this processor. This preserves 4K resolution with SDR; buffer compression is unrelated to the saved-video `skipCompression` option.
- **Stabilization**: Cinematic stabilization (`cinematic-extended` or `cinematic`) is preferred when available in `videoStabilizationModes`.
- **Inference Throttle Safety**: Production MoveNet frame processing uses `runAtTargetFps()` at 2 fps during recording and 1 fps in preview, regardless of resolution. The pose test page can explicitly request a higher rate. The Apple AI experiment has a separate image-sampling schedule and does not change MoveNet's throttle.

### Environment observation journal
- Optional `environmentObservation` / `environmentAnalysis` / `observationIntervalSeconds` (30/60/120) are passed from setup. Defaults: off / true / 60. Existing saved 30/60/120 choices are preserved.
- `OPT FLAGS` includes `environment:<status>:<questionId|->:<review_needed|->`. Uses completed video chunks; never add another live frame processor for this journal.
- See [environment-observation.md](environment-observation.md) for source separation, shared AI slot, evidence uploads, review, and device validation boundaries.
