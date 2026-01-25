module.exports = function (api) {
  api.cache(true);
  return {
    presets: ["babel-preset-expo"],
    plugins: [
      // 🚨 1번: Worklets (Vision Camera 필수)
      "react-native-worklets-core/plugin",

      // 🚨 2번: Reanimated (항상 마지막!)
      "react-native-reanimated/plugin",
    ],
  };
};
