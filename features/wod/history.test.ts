import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { fetchAnalysisHistory, type AnalysisResult } from "./history";

// The module reads env vars at import time, so the base URL defaults to:
const API_BASE_URL = "http://localhost:8088/api/v1";

const mockData: AnalysisResult[] = [
  {
    id: 1,
    session_id: "WOD-2026-03-22-14-05",
    status: "completed",
    output: "Good form on squats. Watch your depth.",
    created_at: "2026-03-22T14:05:00Z",
    updated_at: "2026-03-22T14:10:00Z",
  },
  {
    id: 2,
    session_id: "WOD-2026-03-20-09-30",
    status: "completed",
    output: "Mobility improving steadily.",
    created_at: "2026-03-20T09:30:00Z",
    updated_at: "2026-03-20T09:35:00Z",
  },
];

// ---- MSW server setup ----
const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("fetchAnalysisHistory", () => {
  it("should send GET request to /history with X-API-Key header", async () => {
    let capturedMethod: string | undefined;
    let capturedApiKey: string | null | undefined;

    server.use(
      http.get(`${API_BASE_URL}/history`, ({ request }) => {
        capturedMethod = request.method;
        capturedApiKey = request.headers.get("X-API-Key");
        return HttpResponse.json(mockData);
      })
    );

    await fetchAnalysisHistory(1);

    expect(capturedMethod).toBe("GET");
    // API_KEY defaults to "" when EXPO_PUBLIC_API_KEY is not set
    expect(capturedApiKey).toBe("");
  });

  it("should return the parsed JSON response body", async () => {
    server.use(
      http.get(`${API_BASE_URL}/history`, () => {
        return HttpResponse.json(mockData);
      })
    );

    const result = await fetchAnalysisHistory(1);

    expect(result).toHaveLength(2);
    expect(result[0]).toEqual(
      expect.objectContaining({
        id: 1,
        session_id: "WOD-2026-03-22-14-05",
        status: "completed",
      })
    );
    expect(result[1]).toEqual(
      expect.objectContaining({
        id: 2,
        session_id: "WOD-2026-03-20-09-30",
      })
    );
  });

  it("should throw when the server returns a non-OK status", async () => {
    server.use(
      http.get(`${API_BASE_URL}/history`, () => {
        return new HttpResponse("Service unavailable", { status: 503 });
      })
    );

    await expect(fetchAnalysisHistory(1)).rejects.toThrow(
      /Failed to fetch history/
    );
  });

  it("should throw when the server returns 404", async () => {
    server.use(
      http.get(`${API_BASE_URL}/history`, () => {
        return new HttpResponse("Not Found", { status: 404 });
      })
    );

    await expect(fetchAnalysisHistory(1)).rejects.toThrow(
      /Failed to fetch history/
    );
  });
});
