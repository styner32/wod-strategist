import Darwin
import AVFoundation
import CoreMotion
import CoreLocation
import SoundAnalysis
import UIKit
import WeatherKit

/// Auxiliary capture only: never opens a camera or microphone session.
final class EnvironmentProbe {
  private let motion = CMMotionManager()
  private let hardware: String = {
    var size = 0
    guard sysctlbyname("hw.machine", nil, &size, nil, 0) == 0, size > 0 else { return "unavailable" }
    var bytes = [CChar](repeating: 0, count: size)
    guard sysctlbyname("hw.machine", &bytes, &size, nil, 0) == 0 else { return "unavailable" }
    return String(cString: bytes)
  }()
  private func memoryBytes() -> UInt64? {
    var info = task_vm_info_data_t()
    var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<integer_t>.size)
    let result = withUnsafeMutablePointer(to: &info) { pointer in
      pointer.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
        task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
      }
    }
    return result == KERN_SUCCESS ? info.phys_footprint : nil
  }

  func startMotion() { motion.deviceMotionUpdateInterval = 1; if motion.isDeviceMotionAvailable && !motion.isDeviceMotionActive { motion.startDeviceMotionUpdates() } }
  func stopMotion() { motion.stopDeviceMotionUpdates() }
  func sample() -> [String: Any] {
    var result: [String: Any] = ["at": Date().timeIntervalSince1970 * 1000,
      "motionStatus": motion.isDeviceMotionAvailable ? (motion.deviceMotion == nil ? "no_sample" : "available") : "unsupported",
      "device": hardware, "os": UIDevice.current.systemVersion]
    if let bytes = memoryBytes() { result["processPhysicalFootprintBytes"] = bytes }
    if let m = motion.deviceMotion {
      result["motion"] = ["timestampUptimeSeconds": m.timestamp,
        "attitudeRadians": [m.attitude.roll, m.attitude.pitch, m.attitude.yaw],
        "gravityG": [m.gravity.x, m.gravity.y, m.gravity.z],
        "accelerationG": [m.userAcceleration.x, m.userAcceleration.y, m.userAcceleration.z],
        "rotationRadiansPerSecond": [m.rotationRate.x, m.rotationRate.y, m.rotationRate.z]]
    }
    return result
  }

  static func localURL(_ path: String) -> URL {
    path.hasPrefix("file://") ? URL(string: path)! : URL(fileURLWithPath: path)
  }
  static func temporary(_ ext: String) -> URL {
    FileManager.default.temporaryDirectory.appendingPathComponent("environment_\(UUID().uuidString).\(ext)")
  }

  static func frames(_ path: String) async throws -> [String: Any] {
    let asset = AVURLAsset(url: localURL(path))
    let duration = try await asset.load(.duration).seconds
    guard duration.isFinite, duration >= 0.2 else { throw ProbeError.invalidVideo }
    let generator = AVAssetImageGenerator(asset: asset)
    generator.appliesPreferredTrackTransform = true
    generator.maximumSize = CGSize(width: 640, height: 640)
    generator.requestedTimeToleranceBefore = .zero
    generator.requestedTimeToleranceAfter = .zero
    return try await withTaskCancellationHandler {
    var output: [[String: Any]] = []
    do {
      for seconds in [0.0, duration / 2, max(0, duration - 0.1)] {
        try Task.checkCancellation()
        let (image, actual) = try await generator.image(at: CMTime(seconds: seconds, preferredTimescale: 600))
        let url = temporary("jpg")
        guard let jpeg = UIImage(cgImage: image).jpegData(compressionQuality: 0.85) else { throw ProbeError.invalidVideo }
        try jpeg.write(to: url, options: .atomic)
        output.append(["path": url.path, "mediaOffsetMs": actual.seconds * 1000, "width": image.width, "height": image.height])
      }
      return ["frames": output, "durationMs": duration * 1000]
    } catch {
      for item in output { if let p = item["path"] as? String { try? FileManager.default.removeItem(atPath: p) } }
      throw error
    }
    } onCancel: { generator.cancelAllCGImageGeneration() }
  }

  static func audio(_ path: String) async throws -> [String: Any] {
    let asset = AVURLAsset(url: localURL(path))
    guard !(try await asset.loadTracks(withMediaType: .audio)).isEmpty else { return ["error": "no_audio_track"] }
    let duration = try await asset.load(.duration).seconds
    guard duration.isFinite, duration > 0 else { throw ProbeError.invalidVideo }
    let seconds = min(5, duration)
    let url = temporary("m4a")
    guard let exporter = AVAssetExportSession(asset: asset, presetName: AVAssetExportPresetAppleM4A) else { throw ProbeError.invalidVideo }
    exporter.outputURL = url; exporter.outputFileType = .m4a
    exporter.timeRange = CMTimeRange(start: .zero, duration: CMTime(seconds: seconds, preferredTimescale: 600))
    await withTaskCancellationHandler { await exporter.export() } onCancel: { exporter.cancelExport() }
    guard exporter.status == .completed else { try? FileManager.default.removeItem(at: url); throw exporter.error ?? ProbeError.invalidVideo }
    return ["path": url.path, "mediaOffsetMs": 0, "durationMs": seconds * 1000]
  }

  static func classify(_ path: String) async throws -> [String: Any] {
    let url = localURL(path)
    let observer = SoundObserver()
    let analyzer = try SNAudioFileAnalyzer(url: url)
    let request = try SNClassifySoundRequest(classifierIdentifier: .version1)
    try analyzer.add(request, withObserver: observer)
    await withTaskCancellationHandler { analyzer.analyze() } onCancel: { analyzer.cancelAnalysis() }
    try Task.checkCancellation()
    if let error = observer.error { throw error }
    let file = try AVAudioFile(forReading: url, commonFormat: .pcmFormatFloat32, interleaved: false)
    guard let buffer = AVAudioPCMBuffer(pcmFormat: file.processingFormat, frameCapacity: 4096) else { throw ProbeError.invalidAudio }
    var energy = 0.0; var count = 0; var peak = 0.0
    while file.framePosition < file.length {
      try Task.checkCancellation()
      try Task.checkCancellation()
      try file.read(into: buffer, frameCount: 4096)
      guard buffer.frameLength > 0, let channels = buffer.floatChannelData else { break }
      for c in 0..<Int(buffer.format.channelCount) {
        for i in 0..<Int(buffer.frameLength) { let v = Double(channels[c][i]); energy += v*v; peak = max(peak, abs(v)); count += 1 }
      }
    }
    var output: [String: Any] = ["classifications": observer.results, "unit": "dBFS", "notCalibratedSPL": true,
      "signalStatus": count == 0 ? "no_samples" : (peak == 0 ? "digital_silence" : "present")]
    if count > 0, energy > 0 { output["rmsDbfs"] = 10 * log10(energy / Double(count)) }
    if peak > 0 { output["peakDbfs"] = 20 * log10(peak) }
    return output
  }
}
private enum ProbeError: Error { case invalidVideo, invalidAudio, locationUnavailable }
private final class SoundObserver: NSObject, SNResultsObserving {
  var results: [[String: Any]] = []; var error: Error?
  func request(_ request: SNRequest, didProduce result: SNResult) {
    guard let result = result as? SNClassificationResult else { return }
    results.append(["startMs": result.timeRange.start.seconds * 1000, "durationMs": result.timeRange.duration.seconds * 1000,
      "labels": result.classifications.prefix(3).map { ["label": $0.identifier, "confidence": $0.confidence] as [String: Any] }])
  }
  func request(_ request: SNRequest, didFailWithError error: Error) { self.error = error }
  func requestDidComplete(_ request: SNRequest) {}
}

@MainActor
final class EnvironmentWeather: NSObject, @preconcurrency CLLocationManagerDelegate {
  private let manager = CLLocationManager()
  private var pending: CheckedContinuation<CLLocation, Error>?
  private var permissionPending: CheckedContinuation<String, Never>?
  private var timeout: Task<Void, Never>?
  private var fetchTask: Task<[String: Any], Never>?
  override init() { super.init(); manager.delegate = self; manager.desiredAccuracy = kCLLocationAccuracyKilometer }
  func requestPermission() async -> String {
    if manager.authorizationStatus != .notDetermined { return String(manager.authorizationStatus.rawValue) }
    guard permissionPending == nil else { return "busy" }
    return await withCheckedContinuation { continuation in
      permissionPending = continuation
      manager.requestWhenInUseAuthorization()
    }
  }
  private func location() async throws -> CLLocation {
    guard pending == nil else { throw ProbeError.locationUnavailable }
    return try await withCheckedThrowingContinuation { continuation in
      pending = continuation
      timeout = Task { try? await Task.sleep(nanoseconds: 15_000_000_000); if !Task.isCancelled { finish(.failure(ProbeError.locationUnavailable)) } }
      switch manager.authorizationStatus {
      case .notDetermined: finish(.failure(ProbeError.locationUnavailable))
      case .authorizedAlways, .authorizedWhenInUse: manager.requestLocation()
      default: finish(.failure(ProbeError.locationUnavailable))
      }
    }
  }
  func cancel() { fetchTask?.cancel(); finish(.failure(CancellationError())) }
  private func finish(_ result: Result<CLLocation, Error>) {
    timeout?.cancel(); timeout = nil
    let continuation = pending; pending = nil
    manager.stopUpdatingLocation(); continuation?.resume(with: result)
  }
  func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
    if manager.authorizationStatus != .notDetermined, let continuation = permissionPending {
      permissionPending = nil; continuation.resume(returning: String(manager.authorizationStatus.rawValue))
    }
    guard pending != nil else { return }
    switch manager.authorizationStatus {
    case .authorizedAlways, .authorizedWhenInUse: manager.requestLocation()
    case .denied, .restricted: finish(.failure(ProbeError.locationUnavailable))
    default: break
    }
  }
  func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
    guard let location = locations.last, location.horizontalAccuracy >= 0, abs(location.timestamp.timeIntervalSinceNow) < 300 else { finish(.failure(ProbeError.locationUnavailable)); return }
    finish(.success(location))
  }
  func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) { finish(.failure(error)) }
  func fetch() async -> [String: Any] {
    guard fetchTask == nil else { return ["error": "busy"] }
    let task = Task { await self.performFetch() }
    fetchTask = task
    let deadline = Task { try? await Task.sleep(nanoseconds: 25_000_000_000); if !Task.isCancelled { self.cancel() } }
    let result = await task.value
    deadline.cancel(); fetchTask = nil
    return result
  }
  private func performFetch() async -> [String: Any] {
    do {
      let location = try await location()
      try Task.checkCancellation()
      let weather = try await WeatherService.shared.weather(for: location, including: .current)
      let attribution = try await WeatherService.shared.attribution
      return ["source": "WeatherKit", "scope": "outdoor_region_not_indoor", "observedAt": weather.date.timeIntervalSince1970 * 1000,
        "temperatureC": weather.temperature.converted(to: .celsius).value, "humidityFraction": weather.humidity,
        "condition": String(describing: weather.condition), "locationAccuracyM": location.horizontalAccuracy,
        "regionLatitude": (location.coordinate.latitude * 10).rounded() / 10,
        "regionLongitude": (location.coordinate.longitude * 10).rounded() / 10,
        "attribution": ["name": attribution.serviceName, "markURL": attribution.combinedMarkLightURL.absoluteString, "legalURL": attribution.legalPageURL.absoluteString]]
    } catch { return ["error": "weather_unavailable", "detail": String(describing: error)] }
  }
}

/// One bounded auxiliary extraction/classification task. Cancellation drains native work.
actor EnvironmentWorkRunner {
  private var current: Task<[String: Any], Error>?
  func run(_ kind: String, path: String) async throws -> [String: Any] {
    guard current == nil else { throw CocoaError(.userCancelled) }
    let task = Task.detached(priority: .utility) {
      switch kind {
      case "frames": return try await EnvironmentProbe.frames(path)
      case "audio": return try await EnvironmentProbe.audio(path)
      default: return try await EnvironmentProbe.classify(path)
      }
    }
    current = task
    let deadline = Task { try? await Task.sleep(nanoseconds: 15_000_000_000); if !Task.isCancelled { task.cancel() } }
    defer { deadline.cancel(); current = nil }
    return try await task.value
  }
  func cancel() { current?.cancel() }
}
