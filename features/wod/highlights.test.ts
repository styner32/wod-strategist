import {
  formatHighlightTimestamp,
  getHighlightSeekTime,
  parseHighlightSegments,
  parseHighlightTimestamp,
} from "./highlights";

describe("mobile highlight parsing", () => {
  it("preserves the legacy flat contract", () => {
    expect(parseHighlightSegments(JSON.stringify([
      {
        start: "04:52.125",
        end: "5:03",
        type: "best_form",
        movement: "Power Snatch",
        reason: "Stable receiving position",
      },
      { start_time: 10, end_time: 12, description: "Good rep" },
    ]))).toEqual([
      {
        start: "4:52.125",
        end: "5:03",
        startSeconds: 292.125,
        endSeconds: 303,
        type: "best_form",
        movement: "Power Snatch",
        reason: "Stable receiving position",
      },
      {
        start: "0:10",
        end: "0:12",
        startSeconds: 10,
        endSeconds: 12,
        type: "highlight",
        movement: undefined,
        reason: "Good rep",
      },
    ]);
  });

  it("keeps one mixed-form v2 parent and its exact, ordered evidence", () => {
    const result = parseHighlightSegments([
      {
        version: 2,
        start: "12:24.6",
        end: "12:29.6",
        type: "mixed_form",
        movement: "Deadlift",
        reason: "Strong setup followed by an early hip rise.",
        tags: ["key_moment", "ignored_tag"],
        observations: [
          {
            start: "12:27",
            end: "12:27.2",
            type: "form_issue",
            reason: "Hips rise before the shoulders.",
            confidence: 0.86,
            verified: true,
          },
          {
            start: "12:25",
            end: "12:26.4",
            type: "positive_form",
            reason: "Balanced setup.",
            confidence: 0.91,
          },
        ],
      },
    ]);

    expect(result).toHaveLength(1);
    expect(result[0]).toEqual(expect.objectContaining({
      version: 2,
      startSeconds: 744.6,
      endSeconds: 749.6,
      type: "mixed_form",
      tags: ["key_moment"],
    }));
    expect(result[0].observations).toEqual([
      expect.objectContaining({
        startSeconds: 745,
        endSeconds: 746.4,
        type: "positive_form",
      }),
      expect.objectContaining({
        start: "12:27",
        end: "12:27.2",
        startSeconds: 747,
        endSeconds: 747.2,
        type: "form_issue",
        confidence: 0.86,
        verified: true,
      }),
    ]);
  });

  it("filters malformed parents and observations while keeping valid parents", () => {
    const result = parseHighlightSegments([
      { start: 5, end: 5, type: "best_form" },
      {
        version: 2,
        start: 100,
        end: 106,
        type: "best_form",
        observations: [
          { start: 99, end: 101, type: "positive_form" },
          { start: 100, end: 101, type: "positive_form" },
          { start: 101, end: 102, type: "form_issue" },
          { start: 102, end: 103, type: "fatigue_onset" },
          { start: 103, end: 104, type: "positive_form" },
          { start: 104, end: 103, type: "form_issue" },
          { start: 104, end: 105, type: "unknown" },
        ],
      },
    ]);

    expect(result).toHaveLength(1);
    expect(result[0].observations).toEqual([
      expect.objectContaining({ startSeconds: 100, endSeconds: 101 }),
      expect.objectContaining({ startSeconds: 101, endSeconds: 102 }),
      expect.objectContaining({ startSeconds: 102, endSeconds: 103 }),
    ]);
  });

  it("keeps category-representative evidence when a parent has more than three observations", () => {
    const [segment] = parseHighlightSegments([{
      version: 2,
      start: 0,
      end: 10,
      type: "mixed_form",
      observations: [
        { start: 1, end: 2, type: "positive_form", reason: "positive one" },
        { start: 2, end: 3, type: "positive_form", reason: "positive two" },
        { start: 3, end: 4, type: "positive_form", reason: "positive three" },
        { start: 4, end: 5, type: "form_issue", reason: "only issue" },
      ],
    }]);

    expect(segment.observations).toHaveLength(3);
    expect(segment.observations?.map((observation) => observation.type)).toContain("form_issue");
    expect(segment.observations?.map((observation) => observation.reason)).toContain("only issue");
  });

  it("parses fractional and long clocks without losing precision", () => {
    expect(parseHighlightTimestamp("1:02:03.5")).toBe(3723.5);
    expect(formatHighlightTimestamp(3723.5)).toBe("1:02:03.5");
  });

  it("uses legacy preroll but starts v2 events at their padded parent boundary", () => {
    expect(getHighlightSeekTime(12.5)).toBe(7.5);
    expect(getHighlightSeekTime(3)).toBe(0);
    expect(getHighlightSeekTime(30, 20)).toBe(20);
    expect(getHighlightSeekTime(12.5, undefined, 2)).toBe(12.5);
  });
});
