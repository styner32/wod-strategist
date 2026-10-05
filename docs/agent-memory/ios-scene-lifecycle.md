# iOS scene lifecycle (Expo SDK 57)

- Xcode 27 / iOS 27 SDK builds require UIKit scenes. Lowering `IPHONEOS_DEPLOYMENT_TARGET` does not bypass that requirement.
- Use Expo `57.0.23` or newer within SDK 57, and `expo-build-properties` `57.0.19` or newer within SDK 57. Configure `ios.enableSceneSupport: true` and `ios.deploymentTarget: "16.4"` in that plugin in `app.json`. SDK 57 requires iOS 16.4; the former 16.0 target is no longer supported.
- The maintained runtime is `EXExpoAppSceneDelegate`. The app delegate conforms to `ExpoReactNativeFactoryProvider`, retains the factory, and initializes it in `didFinishLaunchingWithOptions`. It must not create the window or start React Native itself. No app-owned `SceneDelegate.swift` or scene-configuration override is needed.
- Let the build-properties plugin generate `UIApplicationSceneManifest`. Do not add a competing manifest to `app.json`. Keep `UIApplicationSupportsMultipleScenes: false` until the recording and BLE code supports multiple scenes.
- Expo's scene runtime reconstructs cold-start link launch options and forwards scene events through its subscriber pipeline. Validate cold and warm custom links, foreground/background behavior, camera recording, BLE and upload recovery on hardware after native upgrades. A hosted Universal Link requires a valid associated-domain/AASA configuration.
- The maintained SDK 57 Podfile defaults to prebuilt React Native and precompiled Expo modules. Keep its explicit `0`/`1` handling if opting back into `ios.buildReactNativeFromSource`; the old conditional-only SDK 55 assignments no longer disable prebuilt React Native.
- This repository tracks native projects and a customized BroadcastExtension. Use `npx expo prebuild --platform ios --no-clean --no-install` to synchronize the existing project. SDK 57 prebuild cleans by default. Test clean regeneration in a disposable copy before replacing the tracked native project; preserve signing, extension resources and native customizations.
- Run `node scripts/check-ios-scene.cjs` after native changes. Pass another project directory as its argument to verify a fresh prebuild. It checks the plugin configuration, minimum Expo version, Debug/Release deployment targets, manifest and factory ownership.
- SDK 58 includes scene support natively. When upgrading, follow its migration guidance, remove the SDK 57 opt-in and update this check; do not carry the SDK 57 workaround forward blindly.

## Related SDK 55 to 57 migrations

- Application navigation imports now come from `expo-router/react-navigation` and `expo-router/js-tabs`, not `@react-navigation/*`.
- Existing media-library operations use `expo-media-library/legacy`. Migrate their API separately rather than silently changing gallery behavior during the native upgrade.
- `File.copy()` and `File.move()` are asynchronous. Await them before publishing a destination URI or marking an encoded video ready. Queue tests cover delayed completion and rejected operations for both paths.
- Keep TypeScript 5.9 with `expo.install.exclude: ["typescript"]` until `openapi-typescript` supports TypeScript 6. Do not force an incompatible peer dependency as part of `expo install --fix`.
- SDK 55 patches for the Expo CLI, Router subtitle availability and image-picker React imports are retired. Keep the separate Nitro screen-recorder patch.
- `react-native-fast-tflite@2.0.0` needs the checked-in iOS patch: install its bindings via `RCTTurboModuleWithJSIBindings.installJSIBindingsWithRuntime:callInvoker:`. Its original `RCTBridge.currentBridge.runtime` lookup returns no runtime under bridgeless React Native 0.86. Keep the existing inference API; a future TFLite v3 migration also changes tensor buffers, VisionCamera v4 model boxing and Nitro compatibility. Generate this patch with `--include 'ios/Tflite\.mm$'` to avoid capturing downloaded Android build artifacts.

References: [Expo scene migration](https://github.com/expo/fyi/blob/main/ios-scene-lifecycle.md), [SDK 57 release notes](https://expo.dev/changelog/sdk-57), [Router migration](https://docs.expo.dev/router/migrate/sdk-55-to-56/).

## Validation on 2026-09-16

- Xcode 27.0 (`27A266a`), physical iPhone 14 Pro Max, iOS 27.0 (`24A437`): Debug build and signed installation passed. Cold launch displayed Home; patched TensorFlow bindings initialized without the original error.
- Cold URL `wodstrategist://queue` displayed Video Queue. Warm URL `wodstrategist://profiles` displayed Profiles in the same process. Screenshots and device launch results confirmed the destinations; this does not prove every link is delivered exactly once.
- `npm run typecheck`, mobile Jest (`npm test -- --runInBand --watchman=false --testPathIgnorePatterns=/web/`: 24 suites, 242 tests), `npm ls --depth=0`, and `git diff --check` passed. The queue suite includes four new async-copy/move regression cases.
- `node scripts/check-ios-scene.cjs` passed for the tracked project and a clean prebuild in a disposable directory using the published SDK 57.0.25 native template.
- `npx expo run:ios --scheme wodstrategist --device <UDID> --no-bundler` built successfully but remained at `Connecting`. Final installation and cold/warm launch validation used `xcrun devicectl`; do not describe the Expo CLI device-launch path as fully verified.
- Expo Doctor passed 19/21 checks. Remaining findings concern manually tracked native configuration and missing/untested React Native Directory metadata for TFLite/local modules. They were not suppressed.
- Release builds, the oldest supported iOS version, Android, full workout/camera inference, BLE recording, background/resume, upload recovery and hosted Universal Links remain unverified for this upgrade. No commit or deployment was performed.
