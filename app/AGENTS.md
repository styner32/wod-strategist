# WOD Strategist - Frontend Mobile Rules

You are a Senior React Native Engineer. These rules apply to the mobile application codebase.

For detailed patterns see:
- [../docs/agent-memory/mobile-runtime.md](../docs/agent-memory/mobile-runtime.md)
- [../docs/agent-memory/storage-and-session-format.md](../docs/agent-memory/storage-and-session-format.md)

## Android Recording Performance
* Android OOM crashes are prevented via configurable flags, NOT by checking the OS level.
* **Configurable Flags:** Read `showSkeleton`, `lowFps`, `force720p`, and `skipCompression` from route params in `visionTestPage.tsx`.
* The `usePoseDetection.ts` throttle uses `runAtTargetFps()`. This is essential for preventing crashes.

## BLE Heart Rate Monitor
* **Filtering:** The scan filter must explicitly exclude devices named `"Polar mobile"` (which are phone-app bridges, not actual hardware straps).
* **Memory Management:** The `BleManager` singleton must be created **outside** the React component to prevent memory leaks.
* **Dependency:** Must maintain `react-native-ble-plx` at v3.5.1+ to prevent Android null promise rejection crashes.

## Session ID Generation
* Generated client-side in `features/wod/workoutType.ts` via `buildWorkoutSessionId(type)` using the `ulid` npm package.
* Format: `WOD-YYYYMMDD-{ULID}`. Never embed `profile_id` in the session ID — it goes in the GCS path prefix only.

## Internationalization (i18n)
* Setup lives in `features/i18n/` with locale files at `features/i18n/locales/{en,ko}.json`.
* Use `t("key", { ...interpolation })` from `features/i18n/index.ts` for all user-facing strings.
* Subscribe to `useLocale()` in components that must re-render on language switch. Use `setLanguage(code)` to change locale at runtime.
* When adding a user-facing string, add keys to **both** `en.json` and `ko.json` — do not hardcode English in components.

## 🚫 CRITICAL CONSTRAINTS (Never do these)
* **NEVER hardcode `Platform.OS === 'android'`** (or `IS_ANDROID`) to gate performance optimizations in `visionTestPage.tsx`. Always use the configurable param pattern.
* **NEVER remove the `"Polar mobile"` exclusion** from the BLE scan filter.
* **NEVER** move the `BleManager` instantiation inside a hook or component render cycle.
* **NEVER** embed `profile_id` in the session ID string.
* **NEVER** hardcode user-facing strings — always go through `t(key)`.
