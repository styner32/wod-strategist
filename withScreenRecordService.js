const { withAndroidManifest } = require('@expo/config-plugins');

module.exports = function withScreenRecordService(config) {
  return withAndroidManifest(config, async (config) => {
    const androidManifest = config.modResults;
    const mainApplication = androidManifest.manifest.application[0];

    // 1. Android 14 필수 권한 추가
    const permissions = [
      'android.permission.FOREGROUND_SERVICE',
      'android.permission.FOREGROUND_SERVICE_MEDIA_PROJECTION'
    ];
    
    if (!androidManifest.manifest['uses-permission']) {
      androidManifest.manifest['uses-permission'] = [];
    }
    
    permissions.forEach(permission => {
      if (!androidManifest.manifest['uses-permission'].some(p => p.$['android:name'] === permission)) {
        androidManifest.manifest['uses-permission'].push({ $: { 'android:name': permission } });
      }
    });

    // 2. 서비스 선언 (foregroundServiceType 명시 필수)
    // Nitro 라이브러리가 사용하는 서비스 클래스명
    const serviceName = "com.margelo.nitro.nitroscreenrecorder.ScreenRecordingService";
    
    if (!mainApplication.service) mainApplication.service = [];
    
    const existingService = mainApplication.service.find(s => s.$['android:name'] === serviceName);
    
    if (existingService) {
      existingService.$['android:foregroundServiceType'] = "mediaProjection";
    } else {
      mainApplication.service.push({
        $: {
          'android:name': serviceName,
          'android:enabled': "true",
          'android:exported': "false",
          'android:foregroundServiceType': "mediaProjection"
        }
      });
    }

    return config;
  });
};