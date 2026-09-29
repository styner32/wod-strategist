// Validate both the checked-in project and a fresh Expo prebuild:
// node scripts/check-ios-scene.cjs [project-root]
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');

const root = path.resolve(process.argv[2] || path.join(__dirname, '..'));
const projectRequire = createRequire(path.join(root, 'package.json'));
const config = JSON.parse(fs.readFileSync(path.join(root, 'app.json'))).expo;
const buildProperties = config.plugins.find(
  (plugin) => Array.isArray(plugin) && plugin[0] === 'expo-build-properties'
)[1].ios;
assert.equal(buildProperties.enableSceneSupport, true);
assert.equal(buildProperties.deploymentTarget, '16.4');
assert.equal(config.ios.infoPlist.UIApplicationSceneManifest, undefined,
  'Let expo-build-properties own the scene manifest');

const expoVersion = projectRequire('expo/package.json').version;
assert.match(expoVersion, /^57\.0\./, 'Revisit this SDK 57 compatibility check when upgrading SDKs');
assert.ok(Number(expoVersion.split('.')[2]) >= 23, 'Expo scene support requires 57.0.23+');

const ios = path.join(root, 'ios');
const projectName = fs.readdirSync(ios).find((name) => name.endsWith('.xcodeproj'));
assert.ok(projectName, 'An iOS project must exist');
const project = projectRequire('xcode').project(path.join(ios, projectName, 'project.pbxproj'));
project.parseSync();
const configurations = Object.values(project.pbxXCBuildConfigurationSection()).filter(
  (entry) => entry && typeof entry === 'object' &&
    String(entry.buildSettings?.PRODUCT_BUNDLE_IDENTIFIER).replaceAll('"', '') === config.ios.bundleIdentifier
);
assert.ok(configurations.length >= 2, 'Check both Debug and Release app configurations');
for (const { buildSettings } of configurations) {
  assert.equal(String(buildSettings.IPHONEOS_DEPLOYMENT_TARGET), '16.4');
  const plistPath = path.join(ios, buildSettings.INFOPLIST_FILE.replaceAll('"', ''));
  const plist = JSON.parse(execFileSync('plutil', ['-convert', 'json', '-o', '-', plistPath], { encoding: 'utf8' }));
  const manifest = plist.UIApplicationSceneManifest;
  assert.equal(manifest.UIApplicationSupportsMultipleScenes, false);
  assert.deepEqual(manifest.UISceneConfigurations.UIWindowSceneSessionRoleApplication, [{
    UISceneConfigurationName: 'Default Configuration',
    UISceneDelegateClassName: 'EXExpoAppSceneDelegate',
  }]);
  const appDirectory = path.dirname(plistPath);
  const delegate = fs.readFileSync(path.join(appDirectory, 'AppDelegate.swift'), 'utf8');
  assert.match(delegate, /AppDelegate: ExpoAppDelegate, ExpoReactNativeFactoryProvider/);
  assert.doesNotMatch(delegate, /factory\.startReactNative|UIWindow\(frame:|configurationForConnecting/);
  assert.equal(fs.existsSync(path.join(appDirectory, 'SceneDelegate.swift')), false);
}
console.log(`Scene lifecycle verified: Expo ${expoVersion}, single scene, iOS 16.4, ${configurations.length} configurations`);
