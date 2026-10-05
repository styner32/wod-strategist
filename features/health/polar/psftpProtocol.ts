/**
 * Minimal Polar PS-FTP (RFC60/RFC76) and protobuf subset for H10 internal HR recording.
 *
 * Ported from the Polar BLE SDK (BlePsFtpUtils.kt, BlePsFtpClient.kt, BDBleApiImpl.kt and
 * pftp_request.proto / pftp_response.proto / exercise_samples.proto / types.proto).
 * Copyright © Polar Electro Oy. Used under the Polar SDK License:
 * https://github.com/polarofficial/polar-ble-sdk/blob/master/Polar_SDK_License.txt
 *
 * Only the operations needed for H10 exercise recording are implemented:
 * start/stop/status queries and GET/REMOVE file operations.
 */
import { Buffer } from "buffer";

export const PSFTP_SERVICE_UUID = "0000feee-0000-1000-8000-00805f9b34fb";
export const PSFTP_MTU_CHARACTERISTIC_UUID = "fb005c51-02e7-f387-1cad-8acd2d8df0c8";

export const PFTP_QUERY_REQUEST_START_RECORDING = 14;
export const PFTP_QUERY_REQUEST_STOP_RECORDING = 15;
export const PFTP_QUERY_REQUEST_RECORDING_STATUS = 16;

export const PFTP_COMMAND_GET = 0;
export const PFTP_COMMAND_REMOVE = 3;

const PB_SAMPLE_TYPE_HEART_RATE = 1;

const RFC76_STATUS_ERROR_OR_RESPONSE = 0;
const RFC76_STATUS_LAST = 1;
const RFC76_STATUS_MORE = 3;

/** PbPFtpError names (pftp_error.proto), used only for readable error codes. */
const PFTP_ERROR_NAMES: Record<number, string> = {
  1: "REBOOTING",
  2: "TRY_AGAIN",
  100: "UNIDENTIFIED_HOST_ERROR",
  101: "INVALID_COMMAND",
  102: "INVALID_PARAMETER",
  103: "NO_SUCH_FILE_OR_DIRECTORY",
  104: "DIRECTORY_EXISTS",
  105: "FILE_EXISTS",
  106: "OPERATION_NOT_PERMITTED",
  107: "NO_SUCH_USER",
  108: "TIMEOUT",
  200: "UNIDENTIFIED_DEVICE_ERROR",
  201: "NOT_IMPLEMENTED",
  202: "SYSTEM_BUSY",
  203: "INVALID_CONTENT",
  204: "CHECKSUM_FAILURE",
  205: "DISK_FULL",
  206: "PREREQUISITE_NOT_MET",
  207: "INSUFFICIENT_BUFFER",
  208: "WAIT_FOR_IDLING",
  209: "BATTERY_TOO_LOW",
};

export class PftpError extends Error {
  readonly code: number;
  readonly codeName: string;
  constructor(code: number) {
    const name = PFTP_ERROR_NAMES[code] ?? "UNKNOWN";
    super(`PFTP error ${code} (${name})`);
    this.name = "PftpError";
    this.code = code;
    this.codeName = name;
  }
}

// ---------------------------------------------------------------------------
// Protobuf (proto2) subset
// ---------------------------------------------------------------------------

function encodeVarint(value: number): number[] {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error(`Cannot encode varint ${value}`);
  }
  const out: number[] = [];
  let v = value;
  while (v > 0x7f) {
    out.push((v % 0x80) | 0x80);
    v = Math.floor(v / 0x80);
  }
  out.push(v);
  return out;
}

function encodeKey(field: number, wireType: number): number[] {
  return encodeVarint(field * 8 + wireType);
}

function encodeVarintField(field: number, value: number): number[] {
  return [...encodeKey(field, 0), ...encodeVarint(value)];
}

function encodeBytesField(field: number, bytes: Uint8Array | number[]): number[] {
  return [...encodeKey(field, 2), ...encodeVarint(bytes.length), ...Array.from(bytes)];
}

function encodeStringField(field: number, value: string): number[] {
  return encodeBytesField(field, Buffer.from(value, "utf8"));
}

interface PbField {
  field: number;
  wireType: number;
  varint?: number;
  bytes?: Uint8Array;
}

function readVarint(data: Uint8Array, offset: number): { value: number; next: number } {
  let value = 0;
  let multiplier = 1;
  let i = offset;
  for (;;) {
    if (i >= data.length) throw new Error("Truncated protobuf varint");
    const byte = data[i++];
    value += (byte & 0x7f) * multiplier;
    if ((byte & 0x80) === 0) break;
    multiplier *= 0x80;
    if (multiplier > 2 ** 56) throw new Error("Protobuf varint too long");
  }
  return { value, next: i };
}

/** Parses one message level. Unknown fields are kept and ignored by callers. */
export function decodeFields(data: Uint8Array): PbField[] {
  const fields: PbField[] = [];
  let offset = 0;
  while (offset < data.length) {
    const key = readVarint(data, offset);
    offset = key.next;
    const field = Math.floor(key.value / 8);
    const wireType = key.value % 8;
    if (wireType === 0) {
      const v = readVarint(data, offset);
      offset = v.next;
      fields.push({ field, wireType, varint: v.value });
    } else if (wireType === 2) {
      const len = readVarint(data, offset);
      offset = len.next;
      if (offset + len.value > data.length) throw new Error("Truncated protobuf bytes");
      fields.push({ field, wireType, bytes: data.subarray(offset, offset + len.value) });
      offset += len.value;
    } else if (wireType === 5) {
      if (offset + 4 > data.length) throw new Error("Truncated protobuf fixed32");
      offset += 4;
      fields.push({ field, wireType });
    } else if (wireType === 1) {
      if (offset + 8 > data.length) throw new Error("Truncated protobuf fixed64");
      offset += 8;
      fields.push({ field, wireType });
    } else {
      throw new Error(`Unsupported protobuf wire type ${wireType}`);
    }
  }
  return fields;
}

function decodePackedVarints(bytes: Uint8Array): number[] {
  const values: number[] = [];
  let offset = 0;
  while (offset < bytes.length) {
    const v = readVarint(bytes, offset);
    values.push(v.value);
    offset = v.next;
  }
  return values;
}

function utf8(bytes: Uint8Array | undefined): string {
  return bytes ? Buffer.from(bytes).toString("utf8") : "";
}

/** PbPFtpRequestStartRecordingParams { sample_type=HR, recording_interval{seconds}, sample_data_identifier } */
export function encodeStartRecordingParams(identifier: string, intervalSeconds = 1): Uint8Array {
  const duration = encodeVarintField(3, intervalSeconds); // PbDuration.seconds = 3
  return Uint8Array.from([
    ...encodeVarintField(1, PB_SAMPLE_TYPE_HEART_RATE),
    ...encodeBytesField(2, duration),
    ...encodeStringField(3, identifier),
  ]);
}

/** PbPFtpOperation { command = 1 (required, encoded even when 0), path = 2 } */
export function encodeOperation(command: number, path: string): Uint8Array {
  return Uint8Array.from([...encodeVarintField(1, command), ...encodeStringField(2, path)]);
}

export interface RecordingStatus {
  recordingOn: boolean;
  identifier: string;
}

/** PbRequestRecordingStatusResult { recording_on = 1, sample_data_identifier = 2 } */
export function decodeRecordingStatus(data: Uint8Array): RecordingStatus {
  // The SDK treats an empty response as "not recording".
  if (data.length === 0) return { recordingOn: false, identifier: "" };
  let recordingOn = false;
  let identifier = "";
  for (const f of decodeFields(data)) {
    if (f.field === 1 && f.varint !== undefined) recordingOn = f.varint !== 0;
    if (f.field === 2) identifier = utf8(f.bytes);
  }
  return { recordingOn, identifier };
}

export interface DirectoryEntry {
  name: string;
  size: number;
}

/** PbPFtpDirectory { repeated PbPFtpEntry entries = 1 { name = 1, size = 2 } } */
export function decodeDirectory(data: Uint8Array): DirectoryEntry[] {
  const entries: DirectoryEntry[] = [];
  for (const f of decodeFields(data)) {
    if (f.field !== 1 || !f.bytes) continue;
    let name = "";
    let size = 0;
    for (const e of decodeFields(f.bytes)) {
      if (e.field === 1) name = utf8(e.bytes);
      if (e.field === 2 && e.varint !== undefined) size = e.varint;
    }
    entries.push({ name, size });
  }
  return entries;
}

function decodeDurationMs(bytes: Uint8Array): number {
  let hours = 0;
  let minutes = 0;
  let seconds = 0;
  let millis = 0;
  for (const f of decodeFields(bytes)) {
    if (f.varint === undefined) continue;
    if (f.field === 1) hours = f.varint;
    if (f.field === 2) minutes = f.varint;
    if (f.field === 3) seconds = f.varint;
    if (f.field === 4) millis = f.varint;
  }
  return ((hours * 60 + minutes) * 60 + seconds) * 1000 + millis;
}

export interface SensorOfflineRange {
  start_index: number;
  stop_index: number;
}

export interface ExerciseSamples {
  recordingIntervalMs: number;
  heartRateSamples: number[];
  /** SDK ignores this field; preserved here to learn how the H10 marks missing samples. */
  heartRateOffline: SensorOfflineRange[];
  /** Field numbers present in the file other than 1/2/3, for diagnostics. */
  otherFields: number[];
}

/**
 * PbExerciseSamples { recording_interval = 1, heart_rate_samples = 2 (packed),
 * heart_rate_offline = 3 { start_index = 1, stop_index = 2 } }
 */
export function decodeExerciseSamples(data: Uint8Array): ExerciseSamples {
  const result: ExerciseSamples = {
    recordingIntervalMs: 0,
    heartRateSamples: [],
    heartRateOffline: [],
    otherFields: [],
  };
  for (const f of decodeFields(data)) {
    if (f.field === 1 && f.bytes) {
      result.recordingIntervalMs = decodeDurationMs(f.bytes);
    } else if (f.field === 2) {
      if (f.bytes) result.heartRateSamples.push(...decodePackedVarints(f.bytes));
      else if (f.varint !== undefined) result.heartRateSamples.push(f.varint);
    } else if (f.field === 3 && f.bytes) {
      const range: SensorOfflineRange = { start_index: 0, stop_index: 0 };
      for (const r of decodeFields(f.bytes)) {
        if (r.field === 1 && r.varint !== undefined) range.start_index = r.varint;
        if (r.field === 2 && r.varint !== undefined) range.stop_index = r.varint;
      }
      result.heartRateOffline.push(range);
    } else if (!result.otherFields.includes(f.field)) {
      result.otherFields.push(f.field);
    }
  }
  return result;
}

// ---------------------------------------------------------------------------
// RFC60 message header and RFC76 air-packet framing
// ---------------------------------------------------------------------------

/** RFC60 request: 2-byte little-endian header length (15 bits) followed by the protobuf header. */
export function buildRequestMessage(header: Uint8Array): Uint8Array {
  if (header.length > 0x7fff) throw new Error("PFTP request header too large");
  return Uint8Array.from([header.length & 0xff, (header.length >> 8) & 0x7f, ...header]);
}

/** RFC60 query: 2-byte little-endian query id with bit 15 set, followed by optional parameters. */
export function buildQueryMessage(id: number, params?: Uint8Array): Uint8Array {
  return Uint8Array.from([id & 0xff, ((id >> 8) & 0x7f) | 0x80, ...(params ?? [])]);
}

/**
 * Splits a complete message into RFC76 air packets of at most `packetSize` bytes.
 * Header byte: bit0 next (0 first, 1 following), bits1-2 status (MORE=3, LAST=1), bits4-7 sequence.
 */
export function buildRfc76Frames(message: Uint8Array, packetSize: number): Uint8Array[] {
  if (packetSize < 2) throw new Error("PFTP packet size too small");
  const chunk = packetSize - 1;
  const frames: Uint8Array[] = [];
  let offset = 0;
  let seq = 0;
  let next = 0;
  do {
    const remaining = message.length - offset;
    const more = remaining > chunk;
    const length = more ? chunk : remaining;
    const status = more ? RFC76_STATUS_MORE : RFC76_STATUS_LAST;
    const frame = new Uint8Array(length + 1);
    frame[0] = next | (status << 1) | (seq << 4);
    frame.set(message.subarray(offset, offset + length), 1);
    frames.push(frame);
    offset += length;
    seq = (seq + 1) & 0x0f;
    next = 1;
  } while (offset < message.length);
  return frames;
}

/** Packet the SDK sends to cancel an in-flight multi-packet response. */
export const RFC76_CANCEL_PACKET = Uint8Array.from([0x00, 0x00, 0x00]);

export type ResponseProgress =
  | { done: false }
  | { done: true; payload: Uint8Array }
  | { done: true; error: Error; cancel: boolean };

/** Reassembles one RFC76 response from MTU-characteristic notifications. */
export class Rfc76ResponseAssembler {
  private expectedSeq = 0;
  private expectedNext = 0;
  private chunks: Uint8Array[] = [];

  push(packet: Uint8Array): ResponseProgress {
    if (packet.length < 1) {
      return { done: true, error: new Error("Empty PFTP packet"), cancel: false };
    }
    const header = packet[0];
    const next = header & 0x01;
    const status = (header >> 1) & 0x03;
    const seq = (header >> 4) & 0x0f;
    if (seq !== this.expectedSeq) {
      // SDK reports this as error 303 and cancels only an unfinished stream.
      return {
        done: true,
        error: new Error(`PFTP air packet lost (expected seq ${this.expectedSeq}, got ${seq})`),
        cancel: status === RFC76_STATUS_MORE,
      };
    }
    if (next !== this.expectedNext) {
      return { done: true, error: new Error("PFTP unexpected first/next bit"), cancel: status === RFC76_STATUS_MORE };
    }
    this.expectedSeq = (this.expectedSeq + 1) & 0x0f;
    this.expectedNext = 1;
    if (status === RFC76_STATUS_ERROR_OR_RESPONSE) {
      const code = packet.length >= 3 ? packet[1] | (packet[2] << 8) : packet.length === 2 ? packet[1] : 0;
      if (code === 0) return { done: true, payload: this.concat() };
      return { done: true, error: new PftpError(code), cancel: false };
    }
    this.chunks.push(packet.subarray(1));
    if (status === RFC76_STATUS_LAST) return { done: true, payload: this.concat() };
    if (status === RFC76_STATUS_MORE) return { done: false };
    return { done: true, error: new Error(`PFTP unknown status ${status}`), cancel: false };
  }

  private concat(): Uint8Array {
    const total = this.chunks.reduce((sum, c) => sum + c.length, 0);
    const out = new Uint8Array(total);
    let offset = 0;
    for (const c of this.chunks) {
      out.set(c, offset);
      offset += c.length;
    }
    return out;
  }
}
