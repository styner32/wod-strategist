import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import {
  fetchRecommendedStretches,
  fetchRecommendedStretch,
  type RecommendedStretch,
} from "./api";
import { normalizeStretchKey } from "./normalize";

const API_BASE_URL = "http://localhost:8088/api/v1";

const mockStretches: RecommendedStretch[] = [
  {
    id: 1,
    name: "Couch Stretch",
    target_area: "Quadriceps & Hip Flexors",
    description: "Deep hip flexor and quad stretch against a wall",
    duration_hint: "60s per side",
    caution: "Keep torso upright",
    image_url: "https://example.com/couch.jpg",
    video_url: "https://example.com/couch.mp4",
    aliases: ["Wall Quad Stretch"],
    created_at: "2026-04-01T10:00:00Z",
    updated_at: "2026-04-01T10:00:00Z",
    normalized_key: "couch stretch",
    in_catalog: true,
    session_count: 2,
    last_recommended_at: "2026-04-02T15:00:00Z",
    sessions: [
      {
        session_id: "WOD-20260402-002",
        analysis_id: 2,
        analysis_type: "wod",
        target_area: "Quadriceps & Hip Flexors",
        reason: "Tight hip flexors from wall balls",
        duration_hint: "60s",
        provisional: false,
        created_at: "2026-04-02T15:00:00Z",
      },
      {
        session_id: "WOD-20260401-001",
        analysis_id: 1,
        analysis_type: "wod",
        target_area: "Quadriceps & Hip Flexors",
        reason: "Tight quads from front squats",
        duration_hint: "60s",
        provisional: true,
        created_at: "2026-04-01T10:00:00Z",
      },
    ],
  },
];

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("normalizeStretchKey", () => {
  it("should normalize empty or whitespace-only strings to empty string", () => {
    expect(normalizeStretchKey("")).toBe("");
    expect(normalizeStretchKey("   ")).toBe("");
  });

  it("should lowercase and trim input", () => {
    expect(normalizeStretchKey("  Pigeon Pose  ")).toBe("pigeon pose");
  });

  it("should convert hyphens and consecutive spaces to single space", () => {
    expect(normalizeStretchKey("Wall-Quadriceps--Stretch")).toBe("wall quadriceps stretch");
    expect(normalizeStretchKey("Doorway   Pec   Stretch")).toBe("doorway pec stretch");
    expect(normalizeStretchKey("  Wrist-Flexor / Extensor - Stretch  ")).toBe("wrist flexor / extensor stretch");
  });
});

describe("fetchRecommendedStretches", () => {
  it("should query /stretches/recommended with profile_id", async () => {
    let capturedUrl: string | undefined;

    server.use(
      http.get(`${API_BASE_URL}/stretches/recommended`, ({ request }) => {
        capturedUrl = request.url;
        return HttpResponse.json(mockStretches);
      })
    );

    const result = await fetchRecommendedStretches(1);

    expect(capturedUrl).toContain("profile_id=1");
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe("Couch Stretch");
    expect(result[0].session_count).toBe(2);
    expect(result[0].sessions).toHaveLength(2);
  });

  it("should include limit when provided", async () => {
    let capturedUrl: string | undefined;

    server.use(
      http.get(`${API_BASE_URL}/stretches/recommended`, ({ request }) => {
        capturedUrl = request.url;
        return HttpResponse.json(mockStretches);
      })
    );

    await fetchRecommendedStretches(1, 50);

    expect(capturedUrl).toContain("profile_id=1");
    expect(capturedUrl).toContain("limit=50");
  });

  it("should throw on API error", async () => {
    server.use(
      http.get(`${API_BASE_URL}/stretches/recommended`, () => {
        return new HttpResponse("Server error", { status: 500 });
      })
    );

    await expect(fetchRecommendedStretches(1)).rejects.toThrow(/API Error \[500\]/);
  });
});

describe("fetchRecommendedStretch", () => {
  it("should query /stretches/recommended with key and return single item", async () => {
    let capturedUrl: string | undefined;

    server.use(
      http.get(`${API_BASE_URL}/stretches/recommended`, ({ request }) => {
        capturedUrl = request.url;
        return HttpResponse.json(mockStretches);
      })
    );

    const result = await fetchRecommendedStretch(1, "couch stretch");

    expect(capturedUrl).toContain("profile_id=1");
    expect(capturedUrl).toContain("key=couch+stretch");
    expect(result).not.toBeNull();
    expect(result?.normalized_key).toBe("couch stretch");
  });

  it("should return null when no matches found", async () => {
    server.use(
      http.get(`${API_BASE_URL}/stretches/recommended`, () => {
        return HttpResponse.json([]);
      })
    );

    const result = await fetchRecommendedStretch(1, "unknown key");
    expect(result).toBeNull();
  });
});
