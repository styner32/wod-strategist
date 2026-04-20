# WOD Strategist - Frontend Mobile Rules

You are a Senior React Native Engineer. These rules apply to the mobile application codebase.

## Android Recording Performance
* Android OOM crashes are prevented via configurable flags, NOT by checking the OS level.
* **Configurable Flags:** Read `showSkeleton`, `lowFps`, `force720p`, and `skipCompression` from route params in `visionTestPage.tsx`.
* The `usePoseDetection.ts` throttle uses `runAtTargetFps()`. This is essential for preventing crashes.

## BLE Heart Rate Monitor
* **Filtering:** The scan filter must explicitly exclude devices named `"Polar mobile"` (which are phone-app bridges, not actual hardware straps).
* **Memory Management:** The `BleManager` singleton must be created **outside** the React component to prevent memory leaks.
* **Dependency:** Must maintain `react-native-ble-plx` at v3.5.1+ to prevent Android null promise rejection crashes.

## 🚫 CRITICAL CONSTRAINTS (Never do these)
* **NEVER hardcode `Platform.OS === 'android'`** (or `IS_ANDROID`) to gate performance optimizations in `visionTestPage.tsx`. Always use the configurable param pattern.
* **NEVER remove the `"Polar mobile"` exclusion** from the BLE scan filter.
* **NEVER** move the `BleManager` instantiation inside a hook or component render cycle.