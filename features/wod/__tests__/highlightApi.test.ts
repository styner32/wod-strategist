import { fetchHighlightResults, generateHighlight } from "../api";

jest.mock("@/features/auth/storage", () => ({
  getToken: jest.fn().mockResolvedValue("highlight-test-token"),
}));
jest.mock("expo-file-system/legacy", () => ({
  FileSystemUploadType: { BINARY_CONTENT: 0, MULTIPART: 1 },
  createUploadTask: jest.fn(),
  getInfoAsync: jest.fn(),
  uploadAsync: jest.fn(),
}));

const SESSION_ID = "WOD-20261028-highlight-test";

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers({ "Content-Type": "application/json" }),
    json: async () => body,
  } as Response;
}

describe("highlight API cancellation contract", () => {
  let fetchSpy: jest.SpyInstance;

  beforeEach(() => {
    fetchSpy = jest.spyOn(globalThis, "fetch");
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  it("forwards the results signal through the authenticated API client", async () => {
    const controller = new AbortController();
    fetchSpy.mockResolvedValue(jsonResponse([]));

    await expect(fetchHighlightResults(SESSION_ID, controller.signal)).resolves.toEqual([]);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [url, options] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(url).toMatch(new RegExp(`/highlight/${SESSION_ID}$`));
    expect(options.signal).toBe(controller.signal);
    expect(new Headers(options.headers).get("Authorization")).toBe("Bearer highlight-test-token");
  });

  it.each([
    { duration: undefined, expected: 60 },
    { duration: 90, expected: 90 },
  ])("preserves the generation body with duration $duration and a signal", async ({ duration, expected }) => {
    const controller = new AbortController();
    const result = { task_id: "highlight-task", session_id: SESSION_ID, message: "queued" };
    fetchSpy.mockResolvedValue(jsonResponse(result));

    await expect(generateHighlight(SESSION_ID, 7, duration, controller.signal)).resolves.toEqual(result);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [url, options] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(url).toMatch(/\/generate-highlight$/);
    expect(options.method).toBe("POST");
    expect(options.signal).toBe(controller.signal);
    expect(JSON.parse(options.body as string)).toEqual({
      session_id: SESSION_ID,
      profile_id: 7,
      max_duration: expected,
    });
    expect(new Headers(options.headers).get("Authorization")).toBe("Bearer highlight-test-token");
  });

  it.each([
    { name: "results GET", request: (signal: AbortSignal) => fetchHighlightResults(SESSION_ID, signal) },
    { name: "generation POST", request: (signal: AbortSignal) => generateHighlight(SESSION_ID, 7, 60, signal) },
  ])("propagates cancellation of an in-flight $name without retrying", async ({ request }) => {
    const controller = new AbortController();
    const abortError = Object.assign(new Error("Request cancelled"), { name: "AbortError" });
    let markRequested!: () => void;
    const requested = new Promise<void>(resolve => { markRequested = resolve; });
    fetchSpy.mockImplementation((_url: string, options: RequestInit) => {
      markRequested();
      return new Promise<Response>((_resolve, reject) => {
        const signal = options.signal;
        if (!signal) {
          reject(new Error("Cancellation signal was not forwarded"));
        } else if (signal.aborted) {
          reject(abortError);
        } else {
          signal.addEventListener("abort", () => reject(abortError), { once: true });
        }
      });
    });

    const pending = request(controller.signal);
    const rejected = expect(pending).rejects.toBe(abortError);
    await requested;
    controller.abort();
    await rejected;

    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });
});
