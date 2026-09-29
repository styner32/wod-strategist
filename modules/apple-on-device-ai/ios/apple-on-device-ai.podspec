Pod::Spec.new do |s|
  s.name = 'apple-on-device-ai'
  s.version = '1.0.0'
  s.summary = 'On-device Apple workout feedback experiment'
  s.description = 'Image prompting through the on-device Apple Foundation Model.'
  s.author = 'WOD Strategist'
  s.homepage = 'https://docs.expo.dev/modules/'
  s.platforms = { :ios => '16.4' }
  s.source = { git: '' }
  s.static_framework = true
  s.dependency 'ExpoModulesCore'
  s.weak_frameworks = 'FoundationModels'
  s.frameworks = 'ImageIO', 'UIKit', 'Vision', 'UniformTypeIdentifiers', 'AVFoundation', 'SoundAnalysis', 'CoreMotion', 'CoreLocation', 'WeatherKit'
  s.pod_target_xcconfig = { 'DEFINES_MODULE' => 'YES' }
  s.source_files = '**/*.{swift,h,m,mm}'
end
