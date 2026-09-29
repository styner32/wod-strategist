const mockMergeVideos = jest.fn();
const mockGetInfoAsync = jest.fn();
const mockDeleteAsync = jest.fn();

jest.mock("expo-file-system/legacy", () => ({
  documentDirectory: "/mock/documents/",
  getInfoAsync: (...args: unknown[]) => mockGetInfoAsync(...args),
  deleteAsync: (...args: unknown[]) => mockDeleteAsync(...args),
}));
jest.mock("@/modules/video-merger", () => ({
  VideoMergerModule: { mergeVideos: (...args: unknown[]) => mockMergeVideos(...args) },
}));

import { mergeChunksLocal, mergedOutputPath } from "../mergeChunksLocal";

beforeEach(() => {
  jest.resetAllMocks();
  mockGetInfoAsync.mockResolvedValue({ exists: true, size: 1024, isDirectory: false });
  mockMergeVideos.mockImplementation(async (inputs: string[], outputPath: string) => ({
    success: true, outputPath, inputCount: inputs.length, durationSeconds: 10,
  }));
});

describe("mergeChunksLocal", () => {
  it("rejects empty input without calling native code", async () => {
    await expect(mergeChunksLocal([], "/out.mp4")).rejects.toThrow("no chunk paths");
    expect(mockMergeVideos).not.toHaveBeenCalled();
  });

  it.each([
    [{ exists: false }, "chunk not found"],
    [{ exists: true, size: 0 }, "empty or unreadable"],
    [{ exists: true, size: 1024, isDirectory: true }, "empty or unreadable"],
  ])("fails the whole merge for an invalid input %p without omitting it", async (invalid, reason) => {
    mockGetInfoAsync.mockResolvedValueOnce({ exists: true, size: 1024 }).mockResolvedValueOnce(invalid);
    await expect(mergeChunksLocal(["/valid.mov", "/bad.mov"], "/out.mp4")).rejects.toThrow(reason);
    expect(mockMergeVideos).not.toHaveBeenCalled();
    expect(mockDeleteAsync).not.toHaveBeenCalled();
  });

  it("keeps all sources in order, including a small valid final chunk", async () => {
    mockGetInfoAsync.mockImplementation(async (path: string) => ({ exists: true, size: path === "/tail.mov" ? 16 : 1024 }));
    await expect(mergeChunksLocal(["/first.mov", "/tail.mov"], "/out.mp4")).resolves.toBe("/out.mp4");
    expect(mockMergeVideos).toHaveBeenCalledWith(["/first.mov", "/tail.mov"], "/out.mp4");
    expect(mockDeleteAsync).not.toHaveBeenCalled();
  });

  it("preserves the existing output and sources through a failed merge and retry", async () => {
    const files = new Map([["/chunk.mov", "source"], ["/out.mp4", "previous result"]]);
    mockGetInfoAsync.mockImplementation(async (path: string) => ({ exists: files.has(path), size: files.get(path)?.length }));
    mockDeleteAsync.mockImplementation(async (path: string) => files.delete(path));
    mockMergeVideos.mockRejectedValueOnce(new Error("passthrough failed"));
    await expect(mergeChunksLocal(["/chunk.mov"], "/out.mp4")).rejects.toThrow("passthrough failed");
    expect(files.get("/out.mp4")).toBe("previous result");
    expect(files.get("/chunk.mov")).toBe("source");
    await expect(mergeChunksLocal(["/chunk.mov"], "/out.mp4")).resolves.toBe("/out.mp4");
    expect(mockDeleteAsync).not.toHaveBeenCalled();
  });

  it("checks and returns the actual MOV fallback URI instead of the requested MP4", async () => {
    mockMergeVideos.mockResolvedValue({ success: true, outputPath: "file:///out.mov", inputCount: 1 });
    await expect(mergeChunksLocal(["/chunk.mov"], "/out.mp4")).resolves.toBe("file:///out.mov");
    expect(mockGetInfoAsync).toHaveBeenLastCalledWith("file:///out.mov");
  });

  it.each([
    { success: false, outputPath: "/out.mp4", inputCount: 1 },
    { success: true, outputPath: "", inputCount: 1 },
    { success: true, outputPath: "/out.mp4", inputCount: 0 },
    { success: true, outputPath: "/out.mp4" },
  ])("rejects an incomplete native result %p", async (result) => {
    mockMergeVideos.mockResolvedValue(result);
    await expect(mergeChunksLocal(["/chunk.mov"], "/out.mp4")).rejects.toThrow("preserve every input");
    expect(mockDeleteAsync).not.toHaveBeenCalled();
  });

  it.each([{ exists: false }, { exists: true, size: 0 }])("rejects a missing or empty native output %p", async (output) => {
    mockGetInfoAsync.mockResolvedValueOnce({ exists: true, size: 1024 }).mockResolvedValueOnce(output);
    await expect(mergeChunksLocal(["/chunk.mov"], "/out.mp4")).rejects.toThrow("output is missing or empty");
    expect(mockDeleteAsync).not.toHaveBeenCalled();
  });
});

describe("mergedOutputPath", () => {
  it("uses the persistent document directory", () => {
    expect(mergedOutputPath("WOD-20260428-ABC123")).toBe("/mock/documents/merged_WOD-20260428-ABC123.mp4");
  });
});
