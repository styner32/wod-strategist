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

## Internationalization (i18n)
Setup lives in `features/i18n/index.ts`. Locale resources are at `features/i18n/locales/{en,ko}.json`.

- Locale is resolved from device (`expo-localization`) at startup; only `en` and `ko` are supported. Unknown codes fall back to `en`.
- A Zustand store (`useLocaleStore`) drives re-renders when the user switches language. Components reading the active locale should call `useLocale()`.
- Use `t("key", { ...vars })` for every user-facing string — do not hardcode English.
- `setLanguage(code)` switches the locale at runtime.
- When adding a new string, add the key to **both** `en.json` and `ko.json` in the same change.