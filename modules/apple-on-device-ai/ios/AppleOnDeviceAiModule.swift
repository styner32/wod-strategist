import ExpoModulesCore
import Foundation

struct AppleAiFrameRecord: Record {
  @Field var path: String = ""
  @Field var capturedAt: Double = 0
}

struct AppleAiRequestRecord: Record {
  @Field var requestId: String = ""
  @Field var frames: [AppleAiFrameRecord] = []
  @Field var wodDescription: String = ""
  @Field var movements: String = ""
  @Field var appearanceHints: String = ""
  @Field var language: String = "en"
}

public class AppleOnDeviceAiModule: Module {
  private let engine = AppleAiEngine()
  private let probe = EnvironmentProbe()
  private let environmentWork = EnvironmentWorkRunner()
  private var weather: EnvironmentWeather?
  private var thermalObserver: NSObjectProtocol?

  public func definition() -> ModuleDefinition {
    Name("AppleOnDeviceAi")
    Constant("promptVersion") { 3 }
    Constant("environmentVersion") { 1 }
    Constant("environmentSystemInstructions") { AppleAiEngine.environmentInstructions }
    AsyncFunction("cancelEnvironmentWork") { () async in await self.environmentWork.cancel() }
    AsyncFunction("startEnvironmentMotion") { self.probe.startMotion() }
    AsyncFunction("stopEnvironmentMotion") { self.probe.stopMotion() }
    AsyncFunction("environmentSample") { self.probe.sample() }
    AsyncFunction("environmentFrames") { (path: String) async throws -> [String: Any] in
      try await self.environmentWork.run("frames", path: path)
    }
    AsyncFunction("environmentAudio") { (path: String) async throws -> [String: Any] in
      try await self.environmentWork.run("audio", path: path)
    }
    AsyncFunction("environmentSound") { (path: String) async throws -> [String: Any] in
      try await self.environmentWork.run("sound", path: path)
    }
    AsyncFunction("requestEnvironmentLocationPermission") { () async -> String in
      let service = await MainActor.run { () -> EnvironmentWeather in
        if self.weather == nil { self.weather = EnvironmentWeather() }
        return self.weather!
      }
      return await service.requestPermission()
    }
    AsyncFunction("environmentWeather") { () async -> [String: Any] in
      let service = await MainActor.run { () -> EnvironmentWeather in
        if self.weather == nil { self.weather = EnvironmentWeather() }
        return self.weather!
      }
      return await service.fetch()
    }
    AsyncFunction("cancelEnvironmentWeather") { () async in
      await self.weather?.cancel()
    }
    AsyncFunction("observeEnvironment") { (input: AppleAiRequestRecord, prompt: String) async -> [String: Any] in
      var request = AppleAiRequest(requestId: input.requestId,
        frames: input.frames.map { AppleAiFrame(path: $0.path, capturedAt: $0.capturedAt) },
        wodDescription: input.wodDescription, movements: input.movements,
        appearanceHints: input.appearanceHints, language: input.language)
      request.observationPrompt = prompt
      let started = ProcessInfo.processInfo.systemUptime
      let result = await self.engine.analyze(request)
      var value: [String: Any] = ["feedback": result.feedback, "elapsedMs": (ProcessInfo.processInfo.systemUptime - started) * 1000]
      if let error = result.error { value["error"] = error }
      return value
    }

    OnCreate {
      let engine = self.engine
      self.thermalObserver = NotificationCenter.default.addObserver(
        forName: ProcessInfo.thermalStateDidChangeNotification, object: nil, queue: nil
      ) { _ in
        guard ProcessInfo.processInfo.thermalState.rawValue >= 2 else { return }
        Task { await engine.cancelAll(reason: "thermal") }
      }
    }

    OnDestroy {
      self.probe.stopMotion()
      if let observer = self.thermalObserver { NotificationCenter.default.removeObserver(observer) }
      let engine = self.engine
      Task { await engine.cancelAll() }
    }

    AsyncFunction("getAvailability") { (language: String) -> String in
      AppleAiEngine.availability(language: language)
    }

    AsyncFunction("getThermalState") { () -> Int in
      ProcessInfo.processInfo.thermalState.rawValue
    }

    AsyncFunction("prepareFrames") { (requestId: String, frames: [AppleAiFrameRecord]) async throws -> [String: Any] in
      let result = try await self.engine.prepare(requestId: requestId,
        frames: frames.map { AppleAiFrame(path: $0.path, capturedAt: $0.capturedAt) })
      return result.dictionary
    }

    AsyncFunction("analyzeFrames") { (input: AppleAiRequestRecord) async -> [String: Any] in
      let result = await self.engine.analyze(AppleAiRequest(
        requestId: input.requestId,
        frames: input.frames.map { AppleAiFrame(path: $0.path, capturedAt: $0.capturedAt) },
        wodDescription: input.wodDescription, movements: input.movements,
        appearanceHints: input.appearanceHints, language: input.language
      ))
      var value: [String: Any] = ["feedback": result.feedback, "elapsedMs": result.elapsedMs]
      if let error = result.error { value["error"] = error }
      return value
    }

    AsyncFunction("cancel") { (requestId: String) async in
      await self.engine.cancel(requestId)
    }
  }
}
