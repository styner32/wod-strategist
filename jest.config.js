/** @type {import('jest').Config} */
module.exports = {
  preset: "jest-expo",
  moduleNameMapper: {
    "^@/(.*)$": "<rootDir>/$1",
    // MSW's package.json exports set `react-native: null`, which makes
    // the RN Jest env unable to resolve msw. Map to CJS entry directly.
    "^msw/node$": "<rootDir>/node_modules/msw/lib/node/index.js",
    "^msw$": "<rootDir>/node_modules/msw/lib/core/index.js",
  },
  testMatch: ["**/*.test.ts", "**/*.test.tsx"],
  transformIgnorePatterns: [
    "node_modules/(?!(jest-)?react-native|@react-native|expo(nent)?|@expo(nent)?/.*|react-navigation|@react-navigation/.*|@shopify/react-native-skia|react-native-reanimated|react-native-worklets-core|react-native-safe-area-context|react-native-screens|react-native-gesture-handler|msw|until-async|@bundled-es-modules|@mswjs|@open-draft)",
  ],
  collectCoverageFrom: [
    "features/**/*.{ts,tsx}",
    "components/**/*.{ts,tsx}",
    "constants/**/*.{ts,tsx}",
    "hooks/**/*.{ts,tsx}",
    "!**/*.d.ts",
  ],
};
