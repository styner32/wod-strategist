import { Buffer } from "buffer";
import type { Device } from "react-native-ble-plx";

import {
  __failNextWrites,
  __getMockFileContent,
  __resetMockFileSystem,
} from "../../../../__mocks__/expo-file-system";
import { PolarSensorRecorder } from "../polarSensorRecorder";
import settingsFixture from "../__fixtures__/h10-settings.json";
import startFixture from "../__fixtures__/h10-start-response.json";
import accFramesFixture from "../__fixtures__/h10-acc-frames.json";

jest.mock("expo-constants", () => ({
  __esModule: true,
  default: {
    expoConfig: { version: "2.0.0-test" },
  },
}));

jest.mock("react-native", () => ({
  Platform: { OS: "android" },
}));

describe("PolarSensorRecorder", () => {
  beforeEach(() => {
    jest.useFakeTimers();
    __resetMockFileSystem();
    if (PolarSensorRecorder.isActive()) {
      PolarSensorRecorder.stop();
    }
  });

  afterEach(async () => {
    if (PolarSensorRecorder.isActive()) {
      await PolarSensorRecorder.stop();
    }
    jest.useRealTimers();
  });

  it("starts without a device, writes header line, and stops cleanly", async () => {
    const baseEpochMs = 1788673845000;
    PolarSensorRecorder.start({
      sessionId: "WOD-20260906-01JQXYZ",
      profileId: 10,
      baseEpochMs,
    });

    expect(PolarSensorRecorder.isActive()).toBe(true);

    const result = await PolarSensorRecorder.stop();
    expect(result).not.toBeNull();
    expect(result!.sessionId).toBe("WOD-20260906-01JQXYZ");
    expect(PolarSensorRecorder.isActive()).toBe(false);

    const content = __getMockFileContent(result!.filePath);
    expect(content).not.toBeNull();
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    // Session without device: exactly header and footer
    expect(lines.length).toBe(2);

    // Header validation (spec Section 3)
    const header = lines[0];
    expect(header.k).toBe("meta");
    expect(header.schema_version).toBe("2.0.0");
    expect(header.workout_session_id).toBe("WOD-20260906-01JQXYZ");
    expect(header.profile_id).toBe(10);
    expect(header.clock_source).toBe("capture_clock");
    expect(header.base_epoch_ms).toBe(baseEpochMs);
    expect(header.requested_sampling).toEqual({
      acc_hz: 50,
      acc_range_g: 8,
      acc_resolution_bits: 16,
    });
    expect(header.device).toBeNull();
    expect(header.app.version).toBe("2.0.0-test");
    expect(header.app.platform).toBe("android");

    // Footer validation
    const footer = lines[1];
    expect(footer.k).toBe("end");
    expect(footer.pause_intervals).toEqual([]);
    expect(footer.summary.hr_samples).toBe(0);
    expect(footer.summary.acc_samples).toBe(0);
    expect(footer.summary.dropped_packets).toBeNull();
    expect(footer.summary.gaps).toBe(0);
  });

  it("records pause and resume with inline events and aggregates pause_intervals in footer", async () => {
    const baseEpochMs = Date.now();
    PolarSensorRecorder.start({
      sessionId: "WOD-PAUSE-TEST",
      profileId: 1,
      baseEpochMs,
    });

    // Advance 5 seconds, then pause
    jest.advanceTimersByTime(5000);
    PolarSensorRecorder.pause();

    // Pause for 10 seconds, then resume
    jest.advanceTimersByTime(10000);
    PolarSensorRecorder.resume();

    // Run for another 5 seconds, then stop
    jest.advanceTimersByTime(5000);
    const result = await PolarSensorRecorder.stop();
    expect(result).not.toBeNull();

    const content = __getMockFileContent(result!.filePath);
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    // Header, pause, resume, footer
    expect(lines.length).toBe(4);
    expect(lines[1].k).toBe("pause");
    expect(lines[1].t).toBeGreaterThanOrEqual(5000);

    expect(lines[2].k).toBe("resume");
    expect(lines[2].t).toBeGreaterThanOrEqual(15000);

    const footer = lines[3];
    expect(footer.k).toBe("end");
    expect(footer.pause_intervals.length).toBe(1);
    expect(footer.pause_intervals[0].start_offset_ms).toBe(lines[1].t);
    expect(footer.pause_intervals[0].end_offset_ms).toBe(lines[2].t);
  });

  it("handles stop while currently paused by closing the open pause interval", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-STOP-WHILE-PAUSED",
      profileId: 2,
      baseEpochMs: Date.now(),
    });

    jest.advanceTimersByTime(3000);
    PolarSensorRecorder.pause();

    jest.advanceTimersByTime(4000);
    const result = await PolarSensorRecorder.stop();
    expect(result).not.toBeNull();

    const content = __getMockFileContent(result!.filePath);
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    const footer = lines[lines.length - 1];
    expect(footer.pause_intervals.length).toBe(1);
    expect(footer.pause_intervals[0].end_offset_ms).toBeGreaterThanOrEqual(7000);
  });

  it("records heart rate and RR intervals via onHeartRate", async () => {
    const baseEpochMs = 10000;
    PolarSensorRecorder.start({
      sessionId: "WOD-HR-TEST",
      profileId: 5,
      baseEpochMs,
    });

    // Push HR events
    PolarSensorRecorder.onHeartRate(140, [448, 446], 12000);
    PolarSensorRecorder.onHeartRate(142, [445], 13000);

    // Flush batch by advancing timers by 1s
    jest.advanceTimersByTime(1000);

    const result = await PolarSensorRecorder.stop();
    const content = __getMockFileContent(result!.filePath);
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    const hrLines = lines.filter((l) => l.k === "hr");
    expect(hrLines.length).toBe(2);
    expect(hrLines[0]).toEqual({
      k: "hr",
      t: 2000,
      bpm: 140,
      rr: [448, 446],
    });
    expect(hrLines[1]).toEqual({
      k: "hr",
      t: 3000,
      bpm: 142,
      rr: [445],
    });

    const footer = lines[lines.length - 1];
    expect(footer.summary.hr_samples).toBe(2);
  });

  it("records gap_start on onDeviceLost and gap_end on reconnect, incrementing stream_id", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-GAP-TEST",
      profileId: 3,
      baseEpochMs: Date.now(),
    });

    jest.advanceTimersByTime(2000);
    PolarSensorRecorder.onDeviceLost("link-loss");

    const status = PolarSensorRecorder.getLiveStatus();
    expect(status.dropped).toBeNull();

    // Reconnect device
    jest.advanceTimersByTime(1000);
    const mockDevice: any = {
      name: "Polar H10 Reconnected",
      requestMTU: jest.fn().mockResolvedValue(232),
      writeCharacteristicWithResponseForService: jest.fn().mockResolvedValue(undefined),
      monitorCharacteristicForService: jest.fn().mockReturnValue({ remove: jest.fn() }),
    };

    PolarSensorRecorder.onDeviceReady(mockDevice, false);

    const result = await PolarSensorRecorder.stop();
    const content = __getMockFileContent(result!.filePath);
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    const gapStart = lines.find((l) => l.k === "gap_start");
    expect(gapStart).toBeDefined();
    expect(gapStart.reason).toBe("link-loss");
    expect(gapStart.gap_id).toBe(1);
    expect(gapStart.t).toBeGreaterThanOrEqual(2000);

    const gapEnd = lines.find((l) => l.k === "gap_end");
    expect(gapEnd).toBeDefined();
    expect(gapEnd.gap_id).toBe(1);
    expect(gapEnd.t).toBeGreaterThanOrEqual(3000);

    const deviceReady = lines.find((l) => l.k === "device_ready" && l.stream_id === 2);
    expect(deviceReady).toBeDefined();
    expect(deviceReady.device.name).toBe("Polar H10 Reconnected");

    const footer = lines[lines.length - 1];
    expect(footer.summary.gaps).toBe(1);
  });

  it("leaves open gap unclosed when stopped during a gap", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-OPEN-GAP-TEST",
      profileId: 4,
      baseEpochMs: Date.now(),
    });

    jest.advanceTimersByTime(1500);
    PolarSensorRecorder.onDeviceLost("disconnected");

    const result = await PolarSensorRecorder.stop();
    const content = __getMockFileContent(result!.filePath);
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    const gapStart = lines.find((l) => l.k === "gap_start");
    expect(gapStart).toBeDefined();
    expect(gapStart.gap_id).toBe(1);

    const gapEnd = lines.find((l) => l.k === "gap_end");
    expect(gapEnd).toBeUndefined(); // Remains open per spec

    const footer = lines[lines.length - 1];
    expect(footer.summary.gaps).toBe(1);
  });

  it("handles device already connected before start()", async () => {
    const mockDevice: any = {
      name: "Polar H10 Pre-connected",
      requestMTU: jest.fn().mockResolvedValue(232),
      writeCharacteristicWithResponseForService: jest.fn().mockResolvedValue(undefined),
      monitorCharacteristicForService: jest.fn().mockReturnValue({ remove: jest.fn() }),
    };

    PolarSensorRecorder.setBattery(88);
    PolarSensorRecorder.onDeviceReady(mockDevice, true);

    // Now start workout recording
    PolarSensorRecorder.start({
      sessionId: "WOD-PRECONNECT-TEST",
      profileId: 7,
      baseEpochMs: Date.now(),
    });

    const result = await PolarSensorRecorder.stop();
    const content = __getMockFileContent(result!.filePath);
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    expect(lines[0].k).toBe("meta");
    expect(lines[0].device).toBeNull();
    expect(lines[1].k).toBe("device_ready");
    expect(lines[1].stream_id).toBe(1);
    expect(lines[1].device.name).toBe("Polar H10 Pre-connected");
    expect(lines[1].device.battery_percent_start).toBe(88);
  });

  it("handles onDeviceReady, PMD negotiation, stream_start with clock_anchor, and streaming", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-PMD-TEST",
      profileId: 1,
      baseEpochMs: 100000,
    });

    let cpCallback: ((err: any, char: any) => void) | null = null;
    let dataCallback: ((err: any, char: any) => void) | null = null;

    const mockDevice: any = {
      name: "Polar H10 12345678",
      requestMTU: jest.fn().mockResolvedValue(232),
      writeCharacteristicWithResponseForService: jest.fn().mockResolvedValue(undefined),
      monitorCharacteristicForService: jest.fn((service, char, cb) => {
        if (char.includes("5c81")) cpCallback = cb;
        if (char.includes("5c82")) dataCallback = cb;
        return { remove: jest.fn() };
      }),
    };

    PolarSensorRecorder.onDeviceReady(mockDevice, true);

    // Flushes microtasks for async setupPmdStreaming
    await Promise.resolve();

    expect(mockDevice.requestMTU).toHaveBeenCalledWith(232);
    expect(mockDevice.writeCharacteristicWithResponseForService).toHaveBeenCalled();

    // Simulate Control Point settings response
    expect(cpCallback).not.toBeNull();
    cpCallback!(null, { value: settingsFixture.response.base64 });

    // Simulate Control Point start success response
    cpCallback!(null, { value: startFixture.success.base64 });

    // Simulate 2 ACC packets from fixture
    expect(dataCallback).not.toBeNull();
    dataCallback!(null, { value: accFramesFixture.frames[0].base64 });
    dataCallback!(null, { value: accFramesFixture.frames[1].base64 });

    // Advance timers for 1s flush
    jest.advanceTimersByTime(1000);

    const liveStatus = PolarSensorRecorder.getLiveStatus();
    expect(liveStatus.accSamples).toBe(
      accFramesFixture.frames[0].sampleCount + accFramesFixture.frames[1].sampleCount,
    );

    const result = await PolarSensorRecorder.stop();
    const content = __getMockFileContent(result!.filePath);
    const lines = content!.trim().split("\n").map((l) => JSON.parse(l));

    // Check device_ready
    const deviceReadyLine = lines.find((l) => l.k === "device_ready");
    expect(deviceReadyLine).toBeDefined();
    expect(deviceReadyLine.stream_id).toBe(1);

    // Check stream_start
    const streamStartLine = lines.find((l) => l.k === "stream_start");
    expect(streamStartLine).toBeDefined();
    expect(streamStartLine.stream_id).toBe(1);
    expect(streamStartLine.sampling).toEqual({
      acc_hz: 50,
      acc_range_g: 8,
      acc_resolution_bits: 16,
      frame_type: 1,
      delta_compressed: true,
    });
    expect(streamStartLine.clock_anchor).toEqual({
      device_timestamp_ns: accFramesFixture.frames[0].timestampNs,
      capture_offset_ms: expect.any(Number),
      method: "first_packet_received",
    });

    // Check acc lines
    const accLines = lines.filter((l) => l.k === "acc");
    expect(accLines.length).toBe(2);
    expect(accLines[0].stream_id).toBe(1);
    expect(accLines[0].v.length).toBe(accFramesFixture.frames[0].sampleCount);
    expect(accLines[1].stream_id).toBe(1);
    expect(accLines[1].v.length).toBe(accFramesFixture.frames[1].sampleCount);

    const footer = lines[lines.length - 1];
    expect(footer.summary.acc_samples).toBe(liveStatus.accSamples);
  });

  it("timestamps samples with the negotiated sample rate, not the requested one", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-PMD-25HZ",
      profileId: 1,
      baseEpochMs: 100000,
    });

    let cpCallback: ((err: any, char: any) => void) | null = null;
    let dataCallback: ((err: any, char: any) => void) | null = null;

    const mockDevice: any = {
      name: "Polar H10 12345678",
      requestMTU: jest.fn().mockResolvedValue(232),
      writeCharacteristicWithResponseForService: jest.fn().mockResolvedValue(undefined),
      monitorCharacteristicForService: jest.fn((service, char, cb) => {
        if (char.includes("5c81")) cpCallback = cb;
        if (char.includes("5c82")) dataCallback = cb;
        return { remove: jest.fn() };
      }),
    };

    PolarSensorRecorder.onDeviceReady(mockDevice, true);
    await Promise.resolve();

    // Device offers 25Hz only: f0 01 02 00 | more | rate 25 | res 16 | range 8
    const only25Hz = Buffer.from("f001020000000119000101100002010800", "hex").toString(
      "base64",
    );
    cpCallback!(null, { value: only25Hz });

    // START must carry the rate the device actually offers
    const startCall = mockDevice.writeCharacteristicWithResponseForService.mock.calls.at(-1);
    expect(Array.from(Buffer.from(startCall[2], "base64"))[4]).toBe(25);

    dataCallback!(null, { value: accFramesFixture.frames[0].base64 });
    jest.advanceTimersByTime(1000);

    const result = await PolarSensorRecorder.stop();
    const lines = __getMockFileContent(result!.filePath)!
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l));

    expect(lines.find((l) => l.k === "stream_start").sampling.acc_hz).toBe(25);
    expect(lines.find((l) => l.k === "acc").dt).toBe(40);
  });

  it("snapshots the starting battery per session instead of freezing the first reading", async () => {
    const mockDevice: any = {
      name: "Polar H10 12345678",
      requestMTU: jest.fn().mockResolvedValue(232),
      writeCharacteristicWithResponseForService: jest.fn().mockResolvedValue(undefined),
      monitorCharacteristicForService: jest.fn().mockReturnValue({ remove: jest.fn() }),
    };
    PolarSensorRecorder.onDeviceReady(mockDevice, false);

    PolarSensorRecorder.onBattery(92);
    PolarSensorRecorder.start({
      sessionId: "WOD-BATT-1",
      profileId: 3,
      baseEpochMs: Date.now(),
    });
    PolarSensorRecorder.onBattery(85);
    const first = await PolarSensorRecorder.stop();
    const firstLines = __getMockFileContent(first!.filePath)!
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l));

    expect(firstLines.find((l) => l.k === "device_ready").device.battery_percent_start).toBe(92);
    expect(firstLines[firstLines.length - 1].device.battery_percent_end).toBe(85);

    PolarSensorRecorder.start({
      sessionId: "WOD-BATT-2",
      profileId: 3,
      baseEpochMs: Date.now(),
    });
    const second = await PolarSensorRecorder.stop();
    const secondLines = __getMockFileContent(second!.filePath)!
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l));

    // Session 2 starts from the latest reading, not session 1's snapshot
    expect(secondLines.find((l) => l.k === "device_ready").device.battery_percent_start).toBe(85);
  });

  it("records one gap when the same outage is reported twice", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-DOUBLE-LOST",
      profileId: 4,
      baseEpochMs: Date.now(),
    });

    // onDisconnected fires, then the reconnect path tears the link down again
    PolarSensorRecorder.onDeviceLost("disconnected");
    PolarSensorRecorder.onDeviceLost("reconnect-inactive");

    const result = await PolarSensorRecorder.stop();
    const lines = __getMockFileContent(result!.filePath)!
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l));

    expect(lines.filter((l) => l.k === "gap_start").length).toBe(1);
    expect(lines.find((l) => l.k === "gap_start").reason).toBe("disconnected");
    expect(lines[lines.length - 1].summary.gaps).toBe(1);
  });

  it("retries buffered lines after a failed write instead of dropping them", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-WRITE-RETRY",
      profileId: 2,
      baseEpochMs: Date.now(),
    });

    // The header write fails; its line must survive for the next flush.
    __failNextWrites(1);
    PolarSensorRecorder.onHeartRate(120, [500], Date.now());
    jest.advanceTimersByTime(1000);

    const result = await PolarSensorRecorder.stop();
    const lines = __getMockFileContent(result!.filePath)!
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l));

    // Nothing was lost: meta, hr and the footer all reached disk
    expect(lines.map((l) => l.k)).toEqual(["meta", "hr", "end"]);
    expect(result!.complete).toBe(true);
  });

  it("reports an incomplete file when lines never reach disk", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-WRITE-FAIL",
      profileId: 2,
      baseEpochMs: Date.now(),
    });

    // Every write from here on fails, including the footer.
    __failNextWrites(50);
    PolarSensorRecorder.onHeartRate(120, [500], Date.now());
    jest.advanceTimersByTime(1000);

    const result = await PolarSensorRecorder.stop();
    expect(result).not.toBeNull();
    expect(result!.complete).toBe(false);
  });

  it("does not resurrect subscriptions when the session ends mid-setup", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-STALE-SETUP",
      profileId: 5,
      baseEpochMs: Date.now(),
    });

    let releaseMtu: (() => void) | null = null;
    const mockDevice: any = {
      name: "Polar H10 12345678",
      requestMTU: jest.fn(
        () => new Promise((resolve) => {
          releaseMtu = () => resolve(232);
        }),
      ),
      writeCharacteristicWithResponseForService: jest.fn().mockResolvedValue(undefined),
      monitorCharacteristicForService: jest.fn().mockReturnValue({ remove: jest.fn() }),
    };

    // Setup parks on the MTU round-trip
    PolarSensorRecorder.onDeviceReady(mockDevice, true);
    await Promise.resolve();
    expect(mockDevice.requestMTU).toHaveBeenCalled();
    expect(mockDevice.monitorCharacteristicForService).not.toHaveBeenCalled();

    // The user stops recording while the MTU request is still outstanding
    await PolarSensorRecorder.stop();

    // ...and only then does the device answer
    releaseMtu!();
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();

    // No subscriptions, and no GET_SETTINGS for a session that already ended
    expect(mockDevice.monitorCharacteristicForService).not.toHaveBeenCalled();
    expect(mockDevice.writeCharacteristicWithResponseForService).not.toHaveBeenCalledWith(
      expect.anything(),
      expect.anything(),
      expect.stringMatching(/^AQI=$/), // [0x01, 0x02] GET_SETTINGS
    );
  });

  it("shares in-flight promise on concurrent stop() calls", async () => {
    PolarSensorRecorder.start({
      sessionId: "WOD-CONCURRENT-STOP",
      profileId: 9,
      baseEpochMs: Date.now(),
    });

    const p1 = PolarSensorRecorder.stop();
    const p2 = PolarSensorRecorder.stop();

    expect(p1).toBe(p2);

    const [res1, res2] = await Promise.all([p1, p2]);
    expect(res1).not.toBeNull();
    expect(res1).toBe(res2);
    expect(PolarSensorRecorder.isActive()).toBe(false);
  });
});
