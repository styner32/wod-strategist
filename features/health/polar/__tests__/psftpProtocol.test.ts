import {
  PFTP_COMMAND_GET,
  PftpError,
  RFC76_CANCEL_PACKET,
  Rfc76ResponseAssembler,
  buildQueryMessage,
  buildRequestMessage,
  buildRfc76Frames,
  decodeDirectory,
  decodeExerciseSamples,
  decodeRecordingStatus,
  encodeOperation,
  encodeStartRecordingParams,
} from "../psftpProtocol";

const bytes = (...values: number[]) => Uint8Array.from(values);
const ascii = (value: string) => Array.from(Buffer.from(value, "utf8"));

describe("psftpProtocol", () => {
  it("encodes H10 HR start params as sample_type, PbDuration.seconds and identifier", () => {
    expect(Array.from(encodeStartRecordingParams("AB", 1))).toEqual([
      0x08, 0x01, // sample_type = HEART_RATE
      0x12, 0x02, 0x18, 0x01, // recording_interval { seconds = 1 }
      0x1a, 0x02, ...ascii("AB"), // sample_data_identifier
    ]);
  });

  it("encodes GET operations with an explicit zero command", () => {
    expect(Array.from(encodeOperation(PFTP_COMMAND_GET, "/"))).toEqual([0x08, 0x00, 0x12, 0x01, 0x2f]);
  });

  it("builds RFC60 query and request headers", () => {
    expect(Array.from(buildQueryMessage(14, bytes(0xaa)))).toEqual([14, 0x80, 0xaa]);
    expect(Array.from(buildQueryMessage(16))).toEqual([16, 0x80]);
    expect(Array.from(buildRequestMessage(bytes(1, 2, 3)))).toEqual([3, 0, 1, 2, 3]);
  });

  it("splits RFC76 frames with first/next bit, MORE/LAST status and wrapping sequence", () => {
    const message = Uint8Array.from({ length: 40 }, (_, i) => i);
    const frames = buildRfc76Frames(message, 3); // 2 payload bytes per frame
    expect(frames).toHaveLength(20);
    expect(frames[0][0]).toBe(0x06); // next 0, MORE, seq 0
    expect(frames[1][0]).toBe(0x17); // next 1, MORE, seq 1
    expect(frames[16][0]).toBe(0x07); // sequence wrapped to 0
    expect(frames[19][0]).toBe(0x33); // next 1, LAST, seq 3
    expect(Array.from(frames[19].subarray(1))).toEqual([38, 39]);
    expect(buildRfc76Frames(bytes(9), 20).map((f) => Array.from(f))).toEqual([[0x02, 9]]);
  });

  it("reassembles multi-packet responses and accepts a zero-code response", () => {
    const assembler = new Rfc76ResponseAssembler();
    expect(assembler.push(bytes(0x06, 1, 2))).toEqual({ done: false });
    const done = assembler.push(bytes(0x13, 3));
    expect(done.done && "payload" in done && Array.from(done.payload)).toEqual([1, 2, 3]);
    const empty = new Rfc76ResponseAssembler().push(bytes(0x00, 0x00, 0x00));
    expect(empty.done && "payload" in empty && empty.payload.length).toBe(0);
  });

  it("reports PFTP error codes and lost packets", () => {
    const error = new Rfc76ResponseAssembler().push(bytes(0x00, 103, 0));
    expect(error.done && "error" in error && error.error).toBeInstanceOf(PftpError);
    expect((error as unknown as { error: PftpError }).error.codeName).toBe("NO_SUCH_FILE_OR_DIRECTORY");
    const lost = new Rfc76ResponseAssembler();
    lost.push(bytes(0x06, 1));
    const gap = lost.push(bytes(0x37, 2)); // seq 3 instead of 1
    expect(gap).toMatchObject({ done: true, cancel: true });
    expect(Array.from(RFC76_CANCEL_PACKET)).toEqual([0, 0, 0]);
  });

  it("decodes recording status and directories", () => {
    expect(decodeRecordingStatus(new Uint8Array())).toEqual({ recordingOn: false, identifier: "" });
    expect(decodeRecordingStatus(bytes(0x08, 0x01, 0x12, 0x02, ...ascii("ID")))).toEqual({ recordingOn: true, identifier: "ID" });
    const entry = (name: string, size: number) => {
      const body = [0x0a, name.length, ...ascii(name), 0x10, size];
      return [0x0a, body.length, ...body];
    };
    expect(decodeDirectory(bytes(...entry("WOD/", 0), ...entry("SAMPLES.BPB", 42)))).toEqual([
      { name: "WOD/", size: 0 },
      { name: "SAMPLES.BPB", size: 42 },
    ]);
  });

  it("decodes packed HR samples (multi-byte varints), interval and offline ranges", () => {
    const decoded = decodeExerciseSamples(bytes(
      0x0a, 0x02, 0x18, 0x01, // recording_interval { seconds = 1 }
      0x12, 0x04, 90, 0xc8, 0x01, 0, // packed [90, 200, 0]
      0x1a, 0x04, 0x08, 0x05, 0x10, 0x09, // offline { start 5, stop 9 }
      0x20, 0x01, // unknown field 4
    ));
    expect(decoded).toEqual({
      recordingIntervalMs: 1000,
      heartRateSamples: [90, 200, 0],
      heartRateOffline: [{ start_index: 5, stop_index: 9 }],
      otherFields: [4],
    });
  });
});
