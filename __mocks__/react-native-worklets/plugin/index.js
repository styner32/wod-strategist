/**
 * No-op stub for react-native-worklets/plugin.
 *
 * react-native-reanimated/plugin re-exports from react-native-worklets/plugin,
 * which is not installed (the project uses react-native-worklets-core instead).
 * This stub allows the babel-preset-expo pipeline to resolve the module
 * without crashing in the Jest/Node environment.
 */
module.exports = function () {
  return {};
};
