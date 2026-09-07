import {
  createGetMeasurementSettingsCommand,
  createStartMeasurementCommand,
  createStopMeasurementCommand,
  parseMeasurementSettingsResponse,
  parseStartMeasurementResponse,
  parseAccPacket,
  parseHeartRateMeasurement,
  parseBatteryLevel,
  toUint8Array,
  decodeDeltaFrames,
  decodeRawFrames,
  readSignedBits,
  PMD_OP_GET_SETTINGS,
  PMD_OP_START,
  PMD_OP_STOP,
  PMD_MEASUREMENT_ACC,
  PmdStreamContext,
} from "../polarPmdProtocol";

import settingsFixture from "../__fixtures__/h10-settings.json";
import startResponseFixture from "../__fixtures__/h10-start-response.json";
import accFramesFixture from "../__fixtures__/h10-acc-frames.json";
import hrRrFixture from "../__fixtures__/h10-hr-rr.json";
import batteryFixture from "../__fixtures__/h10-battery.json";

describe("polarPmdProtocol", () => {
  describe("bit reading helper", () => {
    it("correctly reads signed bits across byte boundaries", () => {
      // 0xFD = 11111101 (-3 in 8-bit)
      const data = new Uint8Array([0xfd, 0x05]);
      expect(readSignedBits(data, 0, 8)).toBe(-3);
      expect(readSignedBits(data, 8, 8)).toBe(5);

      // Read 6 bits from byte 0: 111101 (-3 in 6-bit)
      expect(readSignedBits(data, 0, 6)).toBe(-3);
    });
  });

  describe("PMD Settings Query & Response", () => {
    it("creates GET_MEASUREMENT_SETTINGS command", () => {
      const cmd = createGetMeasurementSettingsCommand();
      expect(Array.from(cmd)).toEqual([PMD_OP_GET_SETTINGS, PMD_MEASUREMENT_ACC]);
    });

    it("parses settings response fixture correctly", () => {
      const parsed = parseMeasurementSettingsResponse(settingsFixture.response.base64);
      expect(parsed.sampleRates).toEqual([25, 50, 100, 200]);
      expect(parsed.resolutions).toEqual([16]);
      expect(parsed.ranges).toEqual([2, 4, 8, 16]);
      expect(parsed.channels).toBe(3);
      expect(parsed.more).toBe(false);
    });

    it("skips the 'more' flag byte at index 4 (payload starts at index 5)", () => {
      // f0 01 02 00 | more | 00 01 32 00  -> single sample rate of 50 Hz
      const withMoreSet = new Uint8Array([
        0xf0, 0x01, 0x02, 0x00, 0x01, 0x00, 0x01, 0x32, 0x00,
      ]);
      const parsed = parseMeasurementSettingsResponse(withMoreSet);
      expect(parsed.sampleRates).toEqual([50]);
      expect(parsed.more).toBe(true);
    });

    it("parses settings response from an explicit hex string", () => {
      const parsed = parseMeasurementSettingsResponse(settingsFixture.response.hex, "hex");
      expect(parsed.sampleRates).toContain(50);
      expect(parsed.resolutions).toContain(16);
      expect(parsed.ranges).toContain(8);
    });

    it("throws on truncated or invalid response", () => {
      expect(() => parseMeasurementSettingsResponse(new Uint8Array([0xf0, 0x01]))).toThrow(
        /too short/,
      );
      expect(() =>
        parseMeasurementSettingsResponse(new Uint8Array([0x00, 0x01, 0x02, 0x00])),
      ).toThrow(/Invalid PMD settings response/);
      expect(() =>
        parseMeasurementSettingsResponse(new Uint8Array([0xf0, 0x01, 0x01, 0x00])),
      ).toThrow(/Unexpected measurement type/);
      expect(() =>
        parseMeasurementSettingsResponse(new Uint8Array([0xf0, 0x01, 0x02, 0x05])),
      ).toThrow(/error status/);
    });
  });

  describe("PMD Start & Stop Commands", () => {
    it("creates default START command with 50Hz, 16bit, 8G", () => {
      const cmd = createStartMeasurementCommand();
      expect(cmd[0]).toBe(PMD_OP_START);
      expect(cmd[1]).toBe(PMD_MEASUREMENT_ACC);
      // Sample rate 50 = 0x0032
      expect(cmd[2]).toBe(0x00);
      expect(cmd[3]).toBe(0x01);
      expect(cmd[4]).toBe(50);
      expect(cmd[5]).toBe(0);
      // Resolution 16 = 0x0010
      expect(cmd[6]).toBe(0x01);
      expect(cmd[7]).toBe(0x01);
      expect(cmd[8]).toBe(16);
      expect(cmd[9]).toBe(0);
      // Range 8 = 0x0008
      expect(cmd[10]).toBe(0x02);
      expect(cmd[11]).toBe(0x01);
      expect(cmd[12]).toBe(8);
      expect(cmd[13]).toBe(0);
    });

    it("creates customized START command with channels", () => {
      const cmd = createStartMeasurementCommand({
        sampleRate: 100,
        resolution: 16,
        range: 4,
        channels: 3,
      });
      expect(cmd[4]).toBe(100);
      expect(cmd[12]).toBe(4);
      expect(cmd[14]).toBe(0x04);
      expect(cmd[16]).toBe(3);
    });

    it("creates STOP command", () => {
      const cmd = createStopMeasurementCommand();
      expect(Array.from(cmd)).toEqual([PMD_OP_STOP, PMD_MEASUREMENT_ACC]);
    });

    it("parses start response success and errors", () => {
      const successRes = parseStartMeasurementResponse(startResponseFixture.success.base64);
      expect(successRes.success).toBe(true);

      const errorRes = parseStartMeasurementResponse(
        startResponseFixture.errorUnsupported.base64,
      );
      expect(errorRes.success).toBe(false);
      expect(errorRes.errorCode).toBe(3);

      const invalidParamRes = parseStartMeasurementResponse(
        startResponseFixture.errorInvalidParameter.base64,
      );
      expect(invalidParamRes.success).toBe(false);
      expect(invalidParamRes.errorCode).toBe(5);
    });
  });

  describe("ACC Frame Decoding & Clock Anchoring", () => {
    it("decodes all fixture frames (delta and raw) matching expectations", () => {
      const ctx: PmdStreamContext = {
        baseEpochMs: 1000,
        accHz: 50,
      };

      for (let i = 0; i < accFramesFixture.frames.length; i++) {
        const frameFixture = accFramesFixture.frames[i];
        const parsed = parseAccPacket(frameFixture.base64, ctx, 1200 + i * 200);

        expect(parsed.samples.length).toBe(frameFixture.sampleCount);
        expect(parsed.dt).toBe(20);

        // Verify values match expected within 0.001 G
        for (let s = 0; s < parsed.samples.length; s++) {
          const actual = parsed.samples[s];
          const expected = frameFixture.expectedSamplesG[s];
          expect(actual[0]).toBeCloseTo(expected[0], 2);
          expect(actual[1]).toBeCloseTo(expected[1], 2);
          expect(actual[2]).toBeCloseTo(expected[2], 2);
        }
      }
    });

    it("establishes clock anchor on first packet and calculates device delta on subsequent packets", () => {
      const ctx: PmdStreamContext = {
        baseEpochMs: 100000,
        accHz: 50,
      };

      const frame0 = accFramesFixture.frames[0];
      const parsed0 = parseAccPacket(frame0.base64, ctx, 102000); // 2000ms phone offset

      expect(ctx.anchor).toBeDefined();
      expect(ctx.anchor!.phoneOffsetMs).toBe(2000);
      expect(parsed0.packetOffsetMs).toBe(2000);
      // First sample offset = packetOffset - (N - 1) * 20
      expect(parsed0.firstSampleOffsetMs).toBe(2000 - (parsed0.samples.length - 1) * 20);

      // Packet 1 with jittered phone reception (e.g. 250ms later instead of 200ms)
      const frame1 = accFramesFixture.frames[1];
      const parsed1 = parseAccPacket(frame1.base64, ctx, 102250);

      // Device delta is exactly 200ms between packet 0 and 1
      expect(parsed1.packetOffsetMs).toBe(2200);
      expect(parsed1.firstSampleOffsetMs).toBe(2200 - (parsed1.samples.length - 1) * 20);
    });

    it("handles re-anchoring after stream disconnect", () => {
      const ctx: PmdStreamContext = {
        baseEpochMs: 100000,
        accHz: 50,
      };

      parseAccPacket(accFramesFixture.frames[0].base64, ctx, 102000);
      expect(ctx.anchor!.phoneOffsetMs).toBe(2000);

      // Reset anchor (simulating disconnect / reconnect gap)
      delete ctx.anchor;

      // New stream starts at 110000 (10000ms offset)
      const parsedAfterReconnect = parseAccPacket(accFramesFixture.frames[0].base64, ctx, 110000);
      expect(ctx.anchor!.phoneOffsetMs).toBe(10000);
      expect(parsedAfterReconnect.packetOffsetMs).toBe(10000);
    });

    it("calculates dt from device timestamps and preserves the last valid interval on loss", () => {
      const ctx: PmdStreamContext = { baseEpochMs: 0, accHz: 50 };

      const makePacket = (timestampNs: bigint, sampleCount: number) => {
        const bytes = new Uint8Array(10 + sampleCount * 3);
        bytes[0] = 0x02; // PMD_MEASUREMENT_ACC
        const view = new DataView(bytes.buffer);
        view.setBigUint64(1, timestampNs, true);
        bytes[9] = 0x00; // raw TYPE_0 (1 byte per channel = 3 bytes per sample)
        return bytes;
      };

      // Packet 1: First packet -> nominal fallback dt = 1000/50 = 20
      const pkt1 = makePacket(1_000_000_000n, 36);
      const parsed1 = parseAccPacket(pkt1, ctx, 1000);
      expect(parsed1.dt).toBe(20);

      // Packet 2: 703ms device delta with 36 samples -> 703 / 36 = 19.5277... -> 19.53
      const pkt2 = makePacket(1_703_000_000n, 36);
      const parsed2 = parseAccPacket(pkt2, ctx, 1703);
      expect(parsed2.dt).toBe(19.53);

      // A large loss gap keeps the previously measured interval.
      const pktPacketLoss = makePacket(5_203_000_000n, 36);
      const parsedPacketLoss = parseAccPacket(pktPacketLoss, ctx, 5203);
      expect(parsedPacketLoss.dt).toBe(19.53);

      // A duplicate timestamp also keeps the previously measured interval.
      const pktAnomaly = makePacket(5_203_000_000n, 36);
      const parsedAnomaly = parseAccPacket(pktAnomaly, ctx, 5203);
      expect(parsedAnomaly.dt).toBe(19.53);

      // Packet 5 after reconnect (anchor deleted) -> falls back to nominal dt = 20
      delete ctx.anchor;
      const pkt3 = makePacket(10_000_000_000n, 36);
      const parsed3 = parseAccPacket(pkt3, ctx, 10000);
      expect(parsed3.dt).toBe(20);
    });

    it.each([25, 50, 100, 200])("keeps packet loss as a gap at %iHz", (accHz) => {
      const ctx: PmdStreamContext = { baseEpochMs: 0, accHz };
      const interval = 1000 / accHz;
      const packet = (deviceMs: number, samples = 36) => {
        const bytes = new Uint8Array(10 + samples * 3);
        bytes[0] = PMD_MEASUREMENT_ACC;
        new DataView(bytes.buffer).setBigUint64(1, BigInt(deviceMs) * 1_000_000n, true);
        return bytes;
      };
      const packetDuration = 36 * interval;
      parseAccPacket(packet(1000), ctx, 1000);
      // The very next packet is lost, before any measured interval exists.
      const afterLoss = parseAccPacket(packet(1000 + 2 * packetDuration), ctx);
      expect(afterLoss.dt).toBe(interval);
      expect(afterLoss.firstSampleOffsetMs).toBe(1000 + packetDuration + interval);
      // Normal delivery resumes with a different packet size.
      const resumed = parseAccPacket(packet(1000 + 2 * packetDuration + 18 * interval, 18), ctx);
      expect(resumed.dt).toBe(interval);
      expect(resumed.firstSampleOffsetMs).toBe(afterLoss.packetOffsetMs + interval);
    });

    it("retains the measured 51.2Hz interval through single and repeated packet loss", () => {
      const ctx: PmdStreamContext = { baseEpochMs: 0, accHz: 50 };
      const packet = (deviceMs: number) => {
        const bytes = new Uint8Array(10 + 36 * 3);
        bytes[0] = PMD_MEASUREMENT_ACC;
        new DataView(bytes.buffer).setBigUint64(1, BigInt(deviceMs) * 1_000_000n, true);
        return bytes;
      };
      parseAccPacket(packet(1000), ctx, 1000);
      expect(parseAccPacket(packet(1703), ctx).dt).toBe(19.53);
      for (const end of [3109, 4515]) {
        const parsed = parseAccPacket(packet(end), ctx);
        expect(parsed.dt).toBe(19.53);
        expect(parsed.firstSampleOffsetMs).toBeCloseTo(end - 35 * 19.53, 2);
      }
      expect(parseAccPacket(packet(5218), ctx).dt).toBe(19.53);
      // A fresh stream must not inherit the old measured rate.
      delete ctx.anchor;
      ctx.accHz = 100;
      expect(parseAccPacket(packet(6000), ctx, 6000).dt).toBe(10);
    });

    it("decodes 8-bit TYPE_0 frames using 1 byte per channel", () => {
      const ctx: PmdStreamContext = { baseEpochMs: 0, accHz: 50 };
      // header (type, 8-byte timestamp, frame type 0x00 = raw TYPE_0) + 2 samples
      const packet = new Uint8Array([
        0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0x00,
        0x0a, 0xf6, 0x64, // 10, -10, 100 mG
        0x14, 0xec, 0x32, // 20, -20, 50 mG
      ]);
      const parsed = parseAccPacket(packet, ctx, 0);
      expect(parsed.frameType).toBe(0);
      expect(parsed.isDelta).toBe(false);
      expect(parsed.samples).toEqual([
        [0.01, -0.01, 0.1],
        [0.02, -0.02, 0.05],
      ]);
    });

    it("throws on unsupported frame types instead of guessing the sample width", () => {
      const ctx: PmdStreamContext = { baseEpochMs: 0, accHz: 50 };
      const rawType3 = new Uint8Array([0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0x03, 0, 0, 0]);
      expect(() => parseAccPacket(rawType3, ctx)).toThrow(/Unsupported ACC frame type/);

      // TYPE_2 (24-bit) has no compressed form in the PMD spec
      const compressedType2 = new Uint8Array([0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0x82, 0, 0, 0]);
      expect(() => parseAccPacket(compressedType2, ctx)).toThrow(
        /Unsupported compressed ACC frame type/,
      );
    });

    it("throws on invalid or short packet", () => {
      const ctx: PmdStreamContext = { baseEpochMs: 1000, accHz: 50 };
      expect(() => parseAccPacket(new Uint8Array([0x02, 0x01]), ctx)).toThrow(/too short/);
      expect(() =>
        parseAccPacket(new Uint8Array([0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]), ctx),
      ).toThrow(/Not an ACC packet/);
    });
  });

  describe("Heart Rate & RR Interval Parsing", () => {
    it("parses all HR/RR notification fixtures", () => {
      for (const pkt of hrRrFixture.packets) {
        const parsed = parseHeartRateMeasurement(pkt.base64);
        expect(parsed.bpm).toBe(pkt.expectedBpm);
        expect(parsed.rrIntervalsMs).toEqual(pkt.expectedRrMs);
      }
    });

    it("handles empty or malformed HR data gracefully", () => {
      expect(parseHeartRateMeasurement(new Uint8Array([]))).toEqual({ bpm: 0, rrIntervalsMs: [] });
      expect(parseHeartRateMeasurement(new Uint8Array([0x01]))).toEqual({
        bpm: 0,
        rrIntervalsMs: [],
      });
    });
  });

  describe("Binary string decoding", () => {
    it("decodes characteristic strings as base64 by default, never sniffing for hex", () => {
      // "abcd" is valid base64 AND consists purely of hex characters.
      // Sniffing would decode it as hex (2 bytes) instead of base64 (3 bytes).
      expect(Array.from(toUint8Array("abcd"))).toEqual([0x69, 0xb7, 0x1d]);
      expect(Array.from(toUint8Array("abcd", "hex"))).toEqual([0xab, 0xcd]);
    });
  });

  describe("Battery Level Parsing", () => {
    it("parses battery level fixtures", () => {
      for (const sample of batteryFixture.samples) {
        const percent = parseBatteryLevel(sample.base64);
        expect(percent).toBe(sample.expectedPercent);
      }
    });

    it("handles empty data gracefully", () => {
      expect(parseBatteryLevel(new Uint8Array([]))).toBe(0);
    });
  });
});
