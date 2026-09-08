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
| Inference FPS throttle | `usePoseDetection.ts` | 5 fps | 15 fps |
| `android:largeHeap` | `AndroidManifest.xml` | `true` | N/A |

### Rules
- Do NOT hardcode `IS_ANDROID` for feature gating in `visionTestPage.tsx` — use the route-param configuration pattern.
- The `usePoseDetection.ts` throttle uses `runAtTargetFps()` from `react-native-vision-camera`. Do not remove it — it is the single most impactful fix for Android OOM.
- The recording dashboard shows **OPT FLAGS** during recording. Keep this in sync when adding new flags.

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
- Each 10-second chunk tracks the maximum (peak) heart rate received during that window via `chunkMaxBpmRef`.
- When a chunk finishes (`onRecordingFinished`), `chunkPeakBpm` is sent to `processWorkoutChunk` as `heartRateBpm`, ensuring peak cardiovascular stress during exercise bursts is captured instead of momentary recovery dips.
- 1Hz telemetry recording continues to record instantaneous samples via `bpmRef.current`.

### Polar ACC packet timing
- `parseAccPacket` accepts device-derived `dt` only within 10% of `1000 / accHz` (the negotiated rate), allowing the observed 19.53ms interval at 50Hz.
- A rejected delta uses `lastSampleIntervalMs`, or the nominal interval before a valid measurement exists. Do not stretch samples across packet loss using a fixed upper limit such as 60ms.
- Reset the measured interval with the clock anchor on a new stream; stop/disconnect also clears it. This heuristic cannot detect every small partial-packet loss without a sequence counter.

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
