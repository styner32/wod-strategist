const { getDefaultConfig } = require('expo/metro-config');
const config = getDefaultConfig(__dirname);

// tflite 확장자를 Asset으로 인식하도록 추가
config.resolver.assetExts.push('tflite');

module.exports = config;