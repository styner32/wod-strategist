import {
  parseWorkoutType,
  formatWorkoutTypeLabel,
  buildWorkoutSessionId,
  DEFAULT_WORKOUT_TYPE,
} from "./workoutType";

describe("workoutType", () => {
  describe("parseWorkoutType", () => {
    it('should always return "wod"', () => {
      expect(parseWorkoutType("wod")).toBe("wod");
      expect(parseWorkoutType("rehab")).toBe("wod");
      expect(parseWorkoutType("anything")).toBe("wod");
      expect(parseWorkoutType(null)).toBe("wod");
      expect(parseWorkoutType(undefined)).toBe("wod");
    });
  });

  describe("DEFAULT_WORKOUT_TYPE", () => {
    it('should be "wod"', () => {
      expect(DEFAULT_WORKOUT_TYPE).toBe("wod");
    });
  });

  describe("formatWorkoutTypeLabel", () => {
    it('should return "WOD"', () => {
      expect(formatWorkoutTypeLabel("wod")).toBe("WOD");
    });
  });

  describe("buildWorkoutSessionId", () => {
    it("should produce WOD-YYYYMMDD-{ULID} format", () => {
      const date = new Date(2026, 0, 1, 9, 30); // Jan 1 2026
      const result = buildWorkoutSessionId("wod", date);

      // Should start with WOD-20260101-
      expect(result).toMatch(/^WOD-20260101-[0-9A-Z]{26}$/);
    });

    it("should NOT include profile ID", () => {
      const result = buildWorkoutSessionId("wod");
      expect(result).not.toMatch(/^P\d+-/);
    });

    it("should generate unique IDs for the same timestamp", () => {
      const date = new Date(2026, 3, 7, 12, 0);
      const id1 = buildWorkoutSessionId("wod", date);
      const id2 = buildWorkoutSessionId("wod", date);
      expect(id1).not.toBe(id2);
    });
  });
});
