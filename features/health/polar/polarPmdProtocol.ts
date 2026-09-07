import { Buffer } from "buffer";

export const PMD_SERVICE_UUID = "fb005c80-02e7-f387-1cad-8acd2d8df0c8";
export const PMD_CONTROL_POINT_UUID = "fb005c81-02e7-f387-1cad-8acd2d8df0c8";
export const PMD_DATA_UUID = "fb005c82-02e7-f387-1cad-8acd2d8df0c8";

export const HR_SERVICE_UUID = "180D";
export const HR_CHARACTERISTIC_UUID = "2A37";

export const BATTERY_SERVICE_UUID = "180F";
export const BATTERY_CHARACTERISTIC_UUID = "2A19";

export const PMD_OP_GET_SETTINGS = 0x01;
export const PMD_OP_START = 0x02;
export const PMD_OP_STOP = 0x03;
export const PMD_RESPONSE_CODE = 0xf0;

export const PMD_MEASUREMENT_ACC = 0x02;

/**
 * Bytes per channel value for each ACC frame type.
 * Polar BLE SDK (AccData.kt): TYPE_0 = 8-bit, TYPE_1 = 16-bit, TYPE_2 = 24-bit.
 * Compressed (delta) frames only exist for TYPE_0 and TYPE_1.
 */
const ACC_FRAME_SAMPLE_BYTES: Record<number, number> = { 0: 1, 1: 2, 2: 3 };
const ACC_CHANNELS = 3;

/** Encoding of a binary payload delivered as a string. */
export type BinaryStringEncoding = "base64" | "hex";

export interface PmdSettings {
  sampleRates: number[];
  resolutions: number[];
  ranges: number[];
  channels?: number;
  /** Device signalled that more settings follow in a subsequent response. */
  more: boolean;
}

export interface PmdStreamContext {
  baseEpochMs: number;
  accHz: number;
  anchor?: {
    deviceMs: number;
    phoneOffsetMs: number;
    deviceTimestampNs: string;
  };
  lastDeviceMs?: number;
}

export interface ParsedAccPacket {
  packetOffsetMs: number;
  firstSampleOffsetMs: number;
  dt: number;
  samples: [number, number, number][]; // in G
  deviceMs: number;
  deviceTimestampNs: string;
  isNewAnchor: boolean;
  /** Frame type id (data[9] & 0x7F) and whether the frame was delta-compressed. */
  frameType: number;
  isDelta: boolean;
}

/**
 * Creates the PMD GET_MEASUREMENT_SETTINGS command byte array.
 * Default is ACC (0x02).
 */
export function createGetMeasurementSettingsCommand(
  measurementType: number = PMD_MEASUREMENT_ACC,
): Uint8Array {
  return new Uint8Array([PMD_OP_GET_SETTINGS, measurementType]);
}

/**
 * Parses the PMD Control Point response for GET_MEASUREMENT_SETTINGS.
 */
export function parseMeasurementSettingsResponse(
  raw: Uint8Array | Buffer | string,
  encoding?: BinaryStringEncoding,
): PmdSettings {
  const data = toUint8Array(raw, encoding);
  if (data.length < 4) {
    throw new Error(`PMD settings response too short: ${data.length} bytes`);
  }
  if (data[0] !== PMD_RESPONSE_CODE || data[1] !== PMD_OP_GET_SETTINGS) {
    throw new Error(
      `Invalid PMD settings response header: 0x${data[0].toString(16)} 0x${data[1].toString(16)}`,
    );
  }
  if (data[2] !== PMD_MEASUREMENT_ACC) {
    throw new Error(`Unexpected measurement type in response: ${data[2]}`);
  }
  if (data[3] !== 0x00) {
    throw new Error(`PMD settings error status: 0x${data[3].toString(16)}`);
  }

  const result: PmdSettings = {
    sampleRates: [],
    resolutions: [],
    ranges: [],
    // data[4] is the "more" flag; the settings payload starts at index 5.
    // See Polar BLE SDK PmdControlPointResponse.kt.
    more: data.length > 4 && data[4] !== 0x00,
  };

  let offset = 5;
  while (offset + 2 <= data.length) {
    const settingType = data[offset++];
    const count = data[offset++];

    switch (settingType) {
      case 0x00: {
        // Sample rate (uint16 LE)
        for (let i = 0; i < count && offset + 2 <= data.length; i++) {
          const val = data[offset] | (data[offset + 1] << 8);
          offset += 2;
          result.sampleRates.push(val);
        }
        break;
      }
      case 0x01: {
        // Resolution (uint16 LE)
        for (let i = 0; i < count && offset + 2 <= data.length; i++) {
          const val = data[offset] | (data[offset + 1] << 8);
          offset += 2;
          result.resolutions.push(val);
        }
        break;
      }
      case 0x02: {
        // Range (uint16 LE)
        for (let i = 0; i < count && offset + 2 <= data.length; i++) {
          const val = data[offset] | (data[offset + 1] << 8);
          offset += 2;
          result.ranges.push(val);
        }
        break;
      }
      case 0x04: {
        // Channels (uint8 or uint16)
        if (offset < data.length) {
          result.channels = data[offset++];
          // Skip if extra bytes for channel entry
          if (count > 1) {
            offset += count - 1;
          }
        }
        break;
      }
      default: {
        // Skip unknown setting type (assuming 2 bytes per value if space allows)
        offset += count * 2;
        break;
      }
    }
  }

  return result;
}

/**
 * Creates the PMD START_MEASUREMENT command byte array based on available or preferred settings.
 */
export function createStartMeasurementCommand(options?: {
  sampleRate?: number;
  resolution?: number;
  range?: number;
  channels?: number;
}): Uint8Array {
  const sampleRate = options?.sampleRate ?? 50;
  const resolution = options?.resolution ?? 16;
  const range = options?.range ?? 8;

  const bytes = [
    PMD_OP_START,
    PMD_MEASUREMENT_ACC,
    0x00,
    0x01,
    sampleRate & 0xff,
    (sampleRate >> 8) & 0xff,
    0x01,
    0x01,
    resolution & 0xff,
    (resolution >> 8) & 0xff,
    0x02,
    0x01,
    range & 0xff,
    (range >> 8) & 0xff,
  ];

  if (options?.channels !== undefined) {
    bytes.push(0x04, 0x01, options.channels & 0xff, (options.channels >> 8) & 0xff);
  }

  return new Uint8Array(bytes);
}

/**
 * Creates the PMD STOP_MEASUREMENT command byte array.
 */
export function createStopMeasurementCommand(
  measurementType: number = PMD_MEASUREMENT_ACC,
): Uint8Array {
  return new Uint8Array([PMD_OP_STOP, measurementType]);
}

/**
 * Parses the response from START_MEASUREMENT.
 */
export function parseStartMeasurementResponse(
  raw: Uint8Array | Buffer | string,
  encoding?: BinaryStringEncoding,
): { success: boolean; errorCode?: number } {
  const data = toUint8Array(raw, encoding);
  if (data.length < 4) {
    return { success: false, errorCode: -1 };
  }
  if (data[0] !== PMD_RESPONSE_CODE || data[1] !== PMD_OP_START) {
    return { success: false, errorCode: -2 };
  }
  if (data[3] === 0x00) {
    return { success: true };
  }
  return { success: false, errorCode: data[3] };
}

/**
 * Reads signed bitWidth bits from data at bitOffset (LSB first per byte).
 */
export function readSignedBits(
  data: Uint8Array,
  bitOffset: number,
  bitWidth: number,
): number {
  if (bitWidth <= 0 || bitWidth > 32) return 0;
  let val = 0;
  for (let i = 0; i < bitWidth; i++) {
    const totalBit = bitOffset + i;
    const byteIndex = Math.floor(totalBit / 8);
    if (byteIndex >= data.length) break;
    const bitInByte = totalBit % 8;
    const bit = (data[byteIndex] >> bitInByte) & 1;
    val |= bit << i;
  }
  // Sign extension in 32-bit arithmetic
  return (val << (32 - bitWidth)) >> (32 - bitWidth);
}

/**
 * Reads a little-endian signed integer of `byteLength` bytes.
 */
export function readSignedIntLE(
  data: Uint8Array,
  offset: number,
  byteLength: number,
): number {
  let value = 0;
  for (let i = 0; i < byteLength; i++) {
    value |= data[offset + i] << (8 * i);
  }
  const bits = byteLength * 8;
  return bits >= 32 ? value : (value << (32 - bits)) >> (32 - bits);
}

/**
 * Decodes delta frames into 3D samples in milliG.
 *
 * `refSampleBytes` is dictated by the frame type (TYPE_0 = 1, TYPE_1 = 2) and
 * sizes the uncompressed reference sample the deltas accumulate onto.
 */
export function decodeDeltaFrames(
  data: Uint8Array,
  channels: number = ACC_CHANNELS,
  refSampleBytes: number = 2,
): [number, number, number][] {
  const refBytes = channels * refSampleBytes;
  if (data.length < refBytes) {
    return [];
  }
  const refX = readSignedIntLE(data, 0, refSampleBytes);
  const refY = readSignedIntLE(data, refSampleBytes, refSampleBytes);
  const refZ = readSignedIntLE(data, refSampleBytes * 2, refSampleBytes);

  const samples: [number, number, number][] = [[refX, refY, refZ]];

  let offset = refBytes;
  while (offset + 2 <= data.length) {
    const deltaSize = data[offset++];
    const sampleCount = data[offset++];

    if (deltaSize === 0) continue;

    const totalBits = sampleCount * deltaSize * channels;
    const byteLength = Math.ceil(totalBits / 8);
    if (offset + byteLength > data.length) {
      break;
    }

    const payload = data.subarray(offset, offset + byteLength);
    let bitOffset = 0;
    for (let s = 0; s < sampleCount; s++) {
      const dx = readSignedBits(payload, bitOffset, deltaSize);
      bitOffset += deltaSize;
      const dy = readSignedBits(payload, bitOffset, deltaSize);
      bitOffset += deltaSize;
      const dz = readSignedBits(payload, bitOffset, deltaSize);
      bitOffset += deltaSize;

      const last = samples[samples.length - 1];
      samples.push([last[0] + dx, last[1] + dy, last[2] + dz]);
    }
    offset += byteLength;
  }

  return samples;
}

/**
 * Decodes uncompressed ACC frames into 3D samples in milliG.
 *
 * `sampleBytes` is dictated by the frame type (TYPE_0 = 1, TYPE_1 = 2, TYPE_2 = 3).
 */
export function decodeRawFrames(
  data: Uint8Array,
  channels: number = ACC_CHANNELS,
  sampleBytes: number = 2,
): [number, number, number][] {
  const bytesPerSample = channels * sampleBytes;
  const sampleCount = Math.floor(data.length / bytesPerSample);
  const samples: [number, number, number][] = [];

  for (let i = 0; i < sampleCount; i++) {
    const off = i * bytesPerSample;
    samples.push([
      readSignedIntLE(data, off, sampleBytes),
      readSignedIntLE(data, off + sampleBytes, sampleBytes),
      readSignedIntLE(data, off + sampleBytes * 2, sampleBytes),
    ]);
  }

  return samples;
}

/**
 * Parses a single PMD Data characteristic notification packet (ACC).
 *
 * Implements device clock anchoring:
 * - On first packet, establishes anchor (deviceMs -> reception phone offset).
 * - Subsequent packets calculate offset from device clock delta.
 * - Sample offsets are 20ms monotonic: offset[i] = packetOffset - (N - 1 - i) * dt.
 */
export function parseAccPacket(
  raw: Uint8Array | Buffer | string,
  context: PmdStreamContext,
  receivedAtMs?: number,
  encoding?: BinaryStringEncoding,
): ParsedAccPacket {
  const data = toUint8Array(raw, encoding);
  if (data.length < 10) {
    throw new Error(`PMD ACC packet too short: ${data.length} bytes`);
  }

  if (data[0] !== PMD_MEASUREMENT_ACC) {
    throw new Error(`Not an ACC packet: type=${data[0]}`);
  }

  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  let timestampNs: bigint;
  if (typeof view.getBigUint64 === "function") {
    timestampNs = view.getBigUint64(1, true);
  } else {
    const low = BigInt(view.getUint32(1, true));
    const high = BigInt(view.getUint32(5, true));
    timestampNs = (high << 32n) | low;
  }
  const deviceTimestampNs = timestampNs.toString();
  const deviceMs = Number(timestampNs / 1000000n);

  const frameTypeByte = data[9];
  const isDelta = (frameTypeByte & 0x80) !== 0;
  const frameType = frameTypeByte & 0x7f;
  const sampleBytes = ACC_FRAME_SAMPLE_BYTES[frameType];
  if (sampleBytes === undefined) {
    throw new Error(`Unsupported ACC frame type: ${frameType}`);
  }
  if (isDelta && frameType > 1) {
    throw new Error(`Unsupported compressed ACC frame type: ${frameType}`);
  }

  const payload = data.subarray(10);
  const rawSamples = isDelta
    ? decodeDeltaFrames(payload, ACC_CHANNELS, sampleBytes)
    : decodeRawFrames(payload, ACC_CHANNELS, sampleBytes);

  // Convert raw milliG to G rounded to 3 decimal places
  const samples: [number, number, number][] = rawSamples.map(([x, y, z]) => [
    Math.round((x / 1000) * 1000) / 1000,
    Math.round((y / 1000) * 1000) / 1000,
    Math.round((z / 1000) * 1000) / 1000,
  ]);

  const nowMs = receivedAtMs ?? Date.now();
  let isNewAnchor = false;
  if (!context.anchor || deviceMs < context.anchor.deviceMs) {
    context.anchor = {
      deviceMs,
      phoneOffsetMs: Math.max(0, nowMs - context.baseEpochMs),
      deviceTimestampNs,
    };
    isNewAnchor = true;
    delete context.lastDeviceMs;
  }

  const packetOffsetMs = context.anchor.phoneOffsetMs + (deviceMs - context.anchor.deviceMs);
  const nominalDt = Math.round(1000 / context.accHz);
  const N = samples.length;

  let dt = nominalDt;
  if (!isNewAnchor && context.lastDeviceMs !== undefined && deviceMs > context.lastDeviceMs && N > 0) {
    const deltaMs = deviceMs - context.lastDeviceMs;
    const computedDt = Math.round((deltaMs / N) * 100) / 100;
    // Polar H10 ACC supports 25Hz (40ms), 50Hz (20ms), 100Hz (10ms), 200Hz (5ms).
    // An interval > 60ms indicates packet loss or abnormal delay; fall back to nominalDt.
    if (computedDt >= 1 && computedDt <= 60) {
      dt = computedDt;
    }
  }
  context.lastDeviceMs = deviceMs;

  const firstSampleOffsetMs = N > 0 ? packetOffsetMs - (N - 1) * dt : packetOffsetMs;

  return {
    packetOffsetMs,
    firstSampleOffsetMs,
    dt,
    samples,
    deviceMs,
    deviceTimestampNs,
    isNewAnchor,
    frameType,
    isDelta,
  };
}

/**
 * Parses Bluetooth GATT Heart Rate Measurement characteristic (0x2A37).
 * Extracts BPM and RR intervals in milliseconds.
 */
export function parseHeartRateMeasurement(
  raw: Uint8Array | Buffer | string,
  encoding?: BinaryStringEncoding,
): {
  bpm: number;
  rrIntervalsMs: number[];
} {
  const data = toUint8Array(raw, encoding);
  if (data.length < 2) {
    return { bpm: 0, rrIntervalsMs: [] };
  }

  const flags = data[0];
  const is16Bit = (flags & 0x01) !== 0;
  const hasEnergy = (flags & 0x08) !== 0;
  const hasRr = (flags & 0x10) !== 0;

  let bpm = 0;
  let offset = 1;

  if (is16Bit) {
    if (offset + 2 > data.length) return { bpm: 0, rrIntervalsMs: [] };
    bpm = data[offset] | (data[offset + 1] << 8);
    offset += 2;
  } else {
    bpm = data[offset];
    offset += 1;
  }

  if (hasEnergy) {
    offset += 2;
  }

  const rrIntervalsMs: number[] = [];
  if (hasRr) {
    while (offset + 2 <= data.length) {
      const rrRaw = data[offset] | (data[offset + 1] << 8);
      offset += 2;
      // 1/1024 s resolution -> ms
      const rrMs = Math.round((rrRaw / 1024) * 1000);
      rrIntervalsMs.push(rrMs);
    }
  }

  return { bpm, rrIntervalsMs };
}

/**
 * Parses Bluetooth GATT Battery Level characteristic (0x2A19).
 * Returns percentage (0-100).
 */
export function parseBatteryLevel(
  raw: Uint8Array | Buffer | string,
  encoding?: BinaryStringEncoding,
): number {
  const data = toUint8Array(raw, encoding);
  if (data.length === 0) return 0;
  return data[0];
}

/**
 * Normalizes a Buffer, Uint8Array, or encoded string into a Uint8Array.
 *
 * Strings are decoded as base64 by default because that is what
 * react-native-ble-plx hands back for every characteristic value. Content
 * sniffing is deliberately not attempted: a base64 payload can consist purely
 * of hex characters, and guessing wrong yields silently corrupt samples.
 * Callers holding hex (tests, captured dumps) must say so explicitly.
 */
export function toUint8Array(
  raw: Uint8Array | Buffer | string,
  encoding: BinaryStringEncoding = "base64",
): Uint8Array {
  if (typeof raw === "string") {
    return new Uint8Array(Buffer.from(raw, encoding));
  }
  if (raw instanceof Uint8Array) {
    return raw;
  }
  return new Uint8Array(Buffer.from(raw));
}
