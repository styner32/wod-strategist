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
    it("should produce WOD-YYYY-MM-DD-HH-MM format", () => {
      const date = new Date(2026, 0, 1, 9, 30); // Jan 1 2026
      const result = buildWorkoutSessionId("wod", date);
      expect(result).toBe("WOD-2026-01-01-09-30");
    });
  });
});
