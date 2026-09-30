import type { OriginalSessionRef } from "./originalVideoStore";

let mockUserId: number | null = 7;
let mockLoggedIn = true;
let mockDocuments = "file:///container-one/Documents/";
const mockFiles = new Map<string, string>();
const mockDirectories = new Set<string>();
const mockSave = jest.fn();
const mockPermission = jest.fn();
const mockMerge = jest.fn();
let mockFailSavedCommit = false;

const mockDirectory = (uri: string) => uri.replace(/\/$/, "");
jest.mock("../auth/useAuthStore", () => ({
  useAuthStore: { getState: () => ({ userId: mockUserId, isLoggedIn: mockLoggedIn }) },
}));
jest.mock("expo-media-library/legacy", () => ({
  requestPermissionsAsync: (...args: unknown[]) => mockPermission(...args),
  saveToLibraryAsync: (...args: unknown[]) => mockSave(...args),
}));
jest.mock("../wod/mergeChunksLocal", () => ({
  mergeChunksLocal: (...args: unknown[]) => mockMerge(...args),
}));
jest.mock("expo-file-system/legacy", () => ({
  get documentDirectory() { return mockDocuments; },
  getInfoAsync: jest.fn(async (uri: string) => {
    const isDirectory = mockDirectories.has(mockDirectory(uri));
    const value = mockFiles.get(uri);
    return value !== undefined || isDirectory
      ? { exists: true, isDirectory, size: value?.length ?? 0 }
      : { exists: false };
  }),
  makeDirectoryAsync: jest.fn(async (uri: string) => {
    let current = mockDirectory(uri);
    while (current.startsWith("file://") && current.length > 7) {
      mockDirectories.add(current);
      current = current.slice(0, current.lastIndexOf("/"));
    }
  }),
  readDirectoryAsync: jest.fn(async (uri: string) => {
    const prefix = mockDirectory(uri) + "/";
    if (!mockDirectories.has(mockDirectory(uri))) throw new Error("directory missing");
    return [...new Set([...mockFiles.keys(), ...mockDirectories].filter(p => p.startsWith(prefix))
      .map(p => p.slice(prefix.length).split("/")[0]).filter(Boolean))];
  }),
  writeAsStringAsync: jest.fn(async (uri: string, value: string) => { mockFiles.set(uri, value); }),
  readAsStringAsync: jest.fn(async (uri: string) => {
    const value = mockFiles.get(uri);
    if (value === undefined) throw new Error("file missing");
    return value;
  }),
  moveAsync: jest.fn(async ({ from, to }: { from: string; to: string }) => {
    const value = mockFiles.get(from);
    if (value === undefined || mockFiles.has(to)) throw new Error("invalid rename");
    if (mockFailSavedCommit && value.includes('"status":"saved"')) throw new Error("disk full at saved commit");
    mockFiles.set(to, value);
    mockFiles.delete(from);
  }),
  deleteAsync: jest.fn(async (uri: string) => {
    const path = mockDirectory(uri);
    for (const value of [...mockFiles.keys()]) if (value === path || value.startsWith(path + "/")) mockFiles.delete(value);
    for (const value of [...mockDirectories]) if (value === path || value.startsWith(path + "/")) mockDirectories.delete(value);
  }),
}));

let store: typeof import("./originalVideoStore");
let now: jest.SpyInstance;
beforeEach(() => {
  jest.resetModules();
  jest.clearAllMocks();
  mockFiles.clear(); mockDirectories.clear();
  mockUserId = 7; mockLoggedIn = true; mockFailSavedCommit = false;
  mockDocuments = "file:///container-one/Documents/";
  now = jest.spyOn(Date, "now").mockReturnValue(new Date(2026, 8, 30, 23, 59, 59).getTime());
  mockPermission.mockResolvedValue({ granted: true });
  mockSave.mockResolvedValue(undefined);
  mockMerge.mockImplementation(async (_sources: string[], output: string) => {
    const actual = output.replace(/\.mp4$/, ".mov");
    mockFiles.set(actual, "merged-camera-samples");
    return actual;
  });
  store = require("./originalVideoStore");
});
afterEach(() => now.mockRestore());

async function prepared(mode: "chunks" | "continuous" = "chunks") {
  return store.prepareOriginalSession({ profileId: 3, sessionId: "WOD-20260929-ORIGINAL", mode });
}
async function part(ref: OriginalSessionRef, order: number, extension = "mp4") {
  const directory = await store.beginOriginalChunk(ref, order);
  const path = directory + `camera-${order}.${extension}`;
  mockFiles.set(path, `original-${order}-video`);
  await store.addOriginalChunk(ref, { order, path, durationSecs: 10 });
  return path;
}
async function stopped(ref: OriginalSessionRef) {
  await store.markOriginalRecordingStopped(ref, { complete: true });
}
async function waitUntil(condition: () => boolean) {
  for (let i = 0; i < 100; i++) {
    if (condition()) return;
    await new Promise(resolve => setTimeout(resolve, 1));
  }
  throw new Error("condition did not settle");
}
function restart() {
  jest.resetModules();
  store = require("./originalVideoStore");
}

it("validates a single camera MOV through passthrough and saves with add-only permission and a durable marker", async () => {
  const ref = await prepared("continuous");
  const path = await part(ref, 0, "mov");
  await stopped(ref);
  mockSave.mockImplementationOnce(async () => {
    expect(mockFiles.has(path)).toBe(true);
    const manifests = [...mockFiles.entries()].filter(([name]) => /manifest-\d+\.json$/.test(name)).sort();
    expect(JSON.parse(manifests[manifests.length - 1][1]).status).toBe("saving");
  });
  const result = await store.finalizeAndSaveOriginal(ref);
  expect(mockPermission).toHaveBeenCalledWith(true);
  expect(mockSave).toHaveBeenCalledWith(expect.stringContaining("output/WOD-20260930-1.mov"));
  expect(mockMerge).toHaveBeenCalledWith([path], expect.stringContaining("output/WOD-20260930-1.mp4"));
  expect(result.status).toBe("saved");
  expect(mockFiles.has(path)).toBe(false);
  expect((await store.listOriginalSessions(3))[0]).toMatchObject({ status: "saved", cleanedAt: expect.any(Number) });
});

it("merges finalized runs in order and saves the actual MOV path returned by the merger", async () => {
  const ref = await prepared("continuous");
  const first = await part(ref, 1);
  const second = await part(ref, 2);
  await stopped(ref);
  const result = await store.finalizeAndSaveOriginal(ref);
  expect(mockMerge).toHaveBeenCalledWith([first, second], expect.stringContaining("output/WOD-20260930-1.mp4"));
  expect(mockSave).toHaveBeenCalledWith(expect.stringContaining("output/WOD-20260930-1.mov"));
  expect(result.status).toBe("saved");
});

it("retains originals on permission rejection and retries without a server history row", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  await stopped(ref);
  mockPermission.mockResolvedValueOnce({ granted: false });
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("permission_denied");
  expect(mockFiles.has(path)).toBe(true);
  expect(mockSave).not.toHaveBeenCalled();
  restart();
  expect((await store.listOriginalSessions(3))[0].status).toBe("permission_denied");
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("saved");
});

it("keeps all source files when passthrough merge or gallery save fails", async () => {
  const ref = await prepared();
  const first = await part(ref, 0);
  const second = await part(ref, 1);
  await stopped(ref);
  mockMerge.mockRejectedValueOnce(new Error("incompatible tracks"));
  expect(await store.finalizeAndSaveOriginal(ref)).toMatchObject({
    status: "failed", lastErrorStage: "preparing", lastError: "incompatible tracks",
  });
  expect((await store.listOriginalSessions(3))[0].lastErrorStage).toBe("preparing");
  expect(mockFiles.has(first) && mockFiles.has(second)).toBe(true);
  expect(mockSave).not.toHaveBeenCalled();
  mockSave.mockRejectedValueOnce(new Error("Photos save rejected"));
  expect(await store.finalizeAndSaveOriginal(ref)).toMatchObject({
    status: "failed", lastErrorStage: "gallery", lastError: "Photos save rejected",
  });
  expect(mockFiles.has(first) && mockFiles.has(second)).toBe(true);
});

it("rejects a merger result that aliases an original source", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  await stopped(ref);
  mockMerge.mockResolvedValueOnce(path);
  expect(await store.finalizeAndSaveOriginal(ref)).toMatchObject({
    status: "failed", lastError: "original_output_overlaps_source",
  });
  expect(mockFiles.has(path)).toBe(true);
  expect(mockSave).not.toHaveBeenCalled();
});

it.each(["missing", "empty", "unknown", "unfinalized"])("blocks a false full export for %s source evidence", async (problem) => {
  const ref = await prepared();
  const path = await part(ref, 0);
  if (problem === "missing") mockFiles.delete(path);
  if (problem === "empty") mockFiles.set(path, "");
  if (problem === "unknown") mockFiles.set(path.replace("camera-0", "untracked"), "unknown-camera-video");
  if (problem === "unfinalized") await store.beginOriginalChunk(ref, 1);
  await stopped(ref);
  const result = await store.finalizeAndSaveOriginal(ref);
  expect(result.status).toBe("needs_attention");
  expect(mockSave).not.toHaveBeenCalled();
  expect(mockMerge).not.toHaveBeenCalled();
});

it("keeps capture errors incomplete even if stop later reports success", async () => {
  const ref = await prepared();
  await part(ref, 0);
  await expect(store.addOriginalChunk(ref, { order: 0, path: "file:///outside.mp4", durationSecs: 10 })).rejects.toThrow();
  await stopped(ref);
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("needs_attention");
  expect(mockSave).not.toHaveBeenCalled();
});

it("recovers an interrupted recording as incomplete without exporting or deleting its files", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  await store.beginOriginalChunk(ref, 1);
  restart();
  const result = (await store.listOriginalSessions(3))[0];
  expect(result).toMatchObject({ status: "needs_attention", complete: false, captureIssue: "recording_interrupted" });
  expect(mockFiles.has(path)).toBe(true);
  expect(mockSave).not.toHaveBeenCalled();
});

it("records a late finalized file after an incomplete stop without upgrading to a full video", async () => {
  const ref = await prepared();
  const directory = await store.beginOriginalChunk(ref, 0);
  await store.markOriginalRecordingStopped(ref, { complete: false, reason: "finalization_timeout" });
  const path = directory + "late.mp4";
  mockFiles.set(path, "late-original");
  await store.addOriginalChunk(ref, { order: 0, path, durationSecs: 10 });
  const result = await store.finalizeAndSaveOriginal(ref);
  expect(result).toMatchObject({ complete: false, status: "needs_attention", captureIssue: "finalization_timeout" });
  expect(result.chunks[0]).toMatchObject({ finalized: true });
  expect(mockSave).not.toHaveBeenCalled();
  expect(mockFiles.has(path)).toBe(true);
});

it("never automatically repeats an interrupted Photos save; explicit already-saved confirmation cleans up", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  await stopped(ref);
  mockSave.mockImplementationOnce(() => new Promise(() => {}));
  void store.finalizeAndSaveOriginal(ref);
  await waitUntil(() => mockSave.mock.calls.length === 1);
  restart();
  expect((await store.listOriginalSessions(3))[0].status).toBe("uncertain");
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("uncertain");
  expect(mockSave).toHaveBeenCalledTimes(1);
  expect(mockFiles.has(path)).toBe(true);
  await store.confirmOriginalAlreadySaved(ref);
  expect(mockFiles.has(path)).toBe(false);
  expect(mockSave).toHaveBeenCalledTimes(1);
});

it("allows a second Photos save only when uncertain retry is explicit", async () => {
  const ref = await prepared();
  await part(ref, 0);
  await stopped(ref);
  mockSave.mockImplementationOnce(() => new Promise(() => {}));
  void store.finalizeAndSaveOriginal(ref);
  await waitUntil(() => mockSave.mock.calls.length === 1);
  restart();
  await store.listOriginalSessions(3);
  expect((await store.finalizeAndSaveOriginal(ref, { retryUncertain: true })).status).toBe("saved");
  expect(mockSave).toHaveBeenCalledTimes(2);
});

it("coalesces auto/manual saving and waits for live source consumers before cleanup", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  const release = store.holdOriginalFiles(ref);
  await stopped(ref);
  const automatic = store.finalizeAndSaveOriginal(ref);
  expect(store.finalizeAndSaveOriginal(ref)).toBe(automatic);
  expect((await automatic).status).toBe("saved");
  expect(mockSave).toHaveBeenCalledTimes(1);
  expect(mockFiles.has(path)).toBe(true);
  release(); release();
  await waitUntil(() => !mockFiles.has(path));
});

it("retains files and recovers as uncertain if the saved-state commit fails after Photos succeeds", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  await stopped(ref);
  mockFailSavedCommit = true;
  await expect(store.finalizeAndSaveOriginal(ref)).rejects.toThrow("disk full");
  expect(mockFiles.has(path)).toBe(true);
  restart();
  mockFailSavedCommit = false;
  expect((await store.listOriginalSessions(3))[0].status).toBe("uncertain");
  expect(mockSave).toHaveBeenCalledTimes(1);
});

it("rebases relative paths after an iOS container change", async () => {
  const ref = await prepared();
  const path = await part(ref, 0, "mov");
  await stopped(ref);
  const oldRoot = mockDocuments;
  mockDocuments = "file:///container-two/Documents/";
  for (const [uri, value] of [...mockFiles]) { mockFiles.delete(uri); mockFiles.set(uri.replace(oldRoot, mockDocuments), value); }
  for (const uri of [...mockDirectories]) { mockDirectories.delete(uri); mockDirectories.add(uri.replace(oldRoot.replace(/\/$/, ""), mockDocuments.replace(/\/$/, ""))); }
  restart();
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("saved");
  expect(mockMerge).toHaveBeenCalledWith([path.replace(oldRoot, mockDocuments)], expect.stringContaining(mockDocuments));
  expect(mockSave).toHaveBeenCalledWith(expect.stringContaining(mockDocuments));
});

it("isolates accounts and profiles, including a captured ref after account changes", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  await stopped(ref);
  expect(await store.listOriginalSessions(4)).toEqual([]);
  mockUserId = 8;
  expect(await store.listOriginalSessions(3)).toEqual([]);
  expect(() => store.finalizeAndSaveOriginal(ref)).toThrow("account_changed");
  expect(mockFiles.has(path)).toBe(true);
  mockLoggedIn = false;
  expect(await store.listOriginalSessions(3)).toEqual([]);
});

it("lists only owned profiles when offline hydration has no selected profile", async () => {
  await prepared();
  await store.prepareOriginalSession({ profileId: 4, sessionId: "WOD-20260929-ANOTHER" });
  mockUserId = 8;
  await store.prepareOriginalSession({ profileId: 5, sessionId: "WOD-20260929-OTHEROWNER" });
  mockUserId = 7;
  expect((await store.listOriginalSessions()).map(session => session.profileId).sort()).toEqual([3, 4]);
  expect((await store.listOriginalSessions(3)).map(session => session.profileId)).toEqual([3]);
});

it("ignores continuous analysis derivatives while validating the full source", async () => {
  const ref = await prepared("continuous");
  const path = await part(ref, 0);
  const analysis = path.slice(0, path.lastIndexOf("/")) + "/analysis";
  mockDirectories.add(analysis);
  mockFiles.set(analysis + "/part-1.mp4", "analysis-copy");
  mockFiles.set(path + ".segments.json", "native-segment-index");
  await stopped(ref);
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("saved");
  expect(mockFiles.has(analysis + "/part-1.mp4")).toBe(false);
  expect(mockFiles.has(path + ".segments.json")).toBe(false);
});

it("does not overwrite an existing session or trust an invalid newer manifest", async () => {
  const ref = await prepared();
  const path = await part(ref, 0);
  await stopped(ref);
  await expect(prepared()).rejects.toThrow("already_exists");
  mockFiles.set(store.getOriginalDirectory(ref) + "manifest-99999999.json", "invalid-json");
  restart();
  expect((await store.listOriginalSessions(3))[0]).toMatchObject({ status: "needs_attention", complete: false });
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("needs_attention");
  expect(mockFiles.has(path)).toBe(true);
  expect(mockSave).not.toHaveBeenCalled();
});

it("reserves one daily sequence across concurrent workout types and profiles", async () => {
  const types = ["WARMUP", "WOD", "ACCESSORY", "COOLDOWN"];
  const refs = await Promise.all(types.map((type, index) => store.prepareOriginalSession({
    profileId: index + 1, sessionId: `${type}-20260930-CONCURRENT`,
  })));
  const sessions = await store.listOriginalSessions();
  expect(sessions).toHaveLength(4);
  for (const [index, ref] of refs.entries()) {
    expect(sessions.find(session => session.sessionId === ref.sessionId)?.galleryFileStem)
      .toBe(`${types[index]}-20260930-${index + 1}`);
  }
});

it("retains the capture-date name across midnight, restart and a rejected gallery save", async () => {
  const ref = await prepared();
  await part(ref, 0);
  await stopped(ref);
  mockSave.mockRejectedValueOnce(new Error("Photos unavailable"));
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("failed");
  now.mockReturnValue(new Date(2026, 9, 1, 0, 0, 1).getTime());
  restart();
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("saved");
  expect(mockSave.mock.calls.map(call => call[0])).toEqual([
    expect.stringContaining("/WOD-20260930-1.mov"), expect.stringContaining("/WOD-20260930-1.mov"),
  ]);
  expect(mockMerge).toHaveBeenCalledTimes(1);
  const next = await store.prepareOriginalSession({ profileId: 4, sessionId: "WARMUP-20261001-NEXT" });
  expect((await store.listOriginalSessions(4)).find(session => session.sessionId === next.sessionId)?.galleryFileStem)
    .toBe("WARMUP-20261001-1");
});

it("does not reuse a number after save cleanup, restart, account change or failed capture", async () => {
  const ref = await prepared();
  await part(ref, 0);
  await stopped(ref);
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("saved");
  restart();
  mockUserId = 9;
  const failed = await store.prepareOriginalSession({ profileId: 4, sessionId: "WARMUP-20260930-FAILED" });
  await store.markOriginalRecordingStopped(failed, { complete: false, reason: "native_start_failed" });
  const next = await store.prepareOriginalSession({ profileId: 4, sessionId: "WOD-20260930-NEXT" });
  expect((await store.listOriginalSessions(4)).find(session => session.sessionId === next.sessionId)?.galleryFileStem)
    .toBe("WOD-20260930-3");
});

it("keeps a reservation if session metadata fails to commit and skips an interrupted temp reservation", async () => {
  const fs = require("expo-file-system/legacy");
  fs.moveAsync.mockImplementationOnce(async ({ from, to }: { from: string; to: string }) => {
    mockFiles.set(to, mockFiles.get(from)!);
    mockFiles.delete(from);
  }).mockRejectedValueOnce(new Error("owner metadata write failed"));
  await expect(prepared()).rejects.toThrow("owner metadata write failed");
  const ledger = `${mockDocuments}originals/filename-sequences/20260930/`;
  expect(mockFiles.has(`${ledger}1.json`)).toBe(true);
  mockFiles.set(`${ledger}2.json.tmp`, "partial");
  restart();
  const next = await store.prepareOriginalSession({ profileId: 4, sessionId: "WOD-20260930-NEXT" });
  expect((await store.listOriginalSessions(4)).find(session => session.sessionId === next.sessionId)?.galleryFileStem)
    .toBe("WOD-20260930-3");
});

it.each(["mp4", "mov"])("passes the reserved %s filename unchanged to Photos", async (extension) => {
  const ref = await prepared();
  await part(ref, 0);
  await stopped(ref);
  mockMerge.mockImplementationOnce(async (_sources: string[], requested: string) => {
    const output = requested.replace(/\.mp4$/, `.${extension}`);
    mockFiles.set(output, "encoded-camera-data");
    return output;
  });
  const result = await store.finalizeAndSaveOriginal(ref);
  expect(result).toMatchObject({ status: "saved", galleryFileStem: "WOD-20260930-1",
    outputPath: `output/WOD-20260930-1.${extension}` });
  expect(mockSave).toHaveBeenCalledWith(`${store.getOriginalDirectory(ref)}output/WOD-20260930-1.${extension}`);
});

it.each([false, true])("keeps older pending original filenames (existing output=%s)", async (existingOutput) => {
  const ref = await prepared();
  await part(ref, 0);
  await stopped(ref);
  // Fixture representing an existing pre-feature manifest, with no assigned name.
  const directory = store.getOriginalDirectory(ref);
  for (const [path, value] of mockFiles) {
    if (!path.startsWith(directory) || !/manifest-\d+\.json$/.test(path)) continue;
    const session = JSON.parse(value);
    delete session.galleryFileStem;
    if (existingOutput) session.outputPath = "output/original.mov";
    mockFiles.set(path, JSON.stringify(session));
  }
  if (existingOutput) mockFiles.set(`${directory}output/original.mov`, "existing-original");
  restart();
  expect((await store.finalizeAndSaveOriginal(ref)).status).toBe("saved");
  expect(mockSave).toHaveBeenCalledWith(`${directory}output/original.mov`);
  expect(mockMerge).toHaveBeenCalledTimes(existingOutput ? 0 : 1);
});
