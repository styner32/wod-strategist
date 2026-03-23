import {
  parseWorkoutType,
  formatWorkoutTypeLabel,
  buildWorkoutSessionId,
  DEFAULT_WORKOUT_TYPE,
  WORKOUT_TYPE_OPTIONS,
} from "./workoutType";

describe("workoutType", () => {
  describe("DEFAULT_WORKOUT_TYPE", () => {
    it('should be "wod"', () => {
      expect(DEFAULT_WORKOUT_TYPE).toBe("wod");
    });
  });

  describe("WORKOUT_TYPE_OPTIONS", () => {
    it("should contain wod and rehab options", () => {
      const values = WORKOUT_TYPE_OPTIONS.map((o) => o.value);
      expect(values).toContain("wod");
      expect(values).toContain("rehab");
    });

    it("should have label and description for each option", () => {
      for (const option of WORKOUT_TYPE_OPTIONS) {
        expect(option.label).toBeTruthy();
        expect(option.description).toBeTruthy();
      }
    });
  });

  describe("parseWorkoutType", () => {
    it('should return "rehab" when value is "rehab"', () => {
      expect(parseWorkoutType("rehab")).toBe("rehab");
    });

    it('should default to "wod" when value is undefined', () => {
      expect(parseWorkoutType(undefined)).toBe("wod");
    });

    it('should default to "wod" when value is null', () => {
      expect(parseWorkoutType(null)).toBe("wod");
    });

    it('should default to "wod" for any other string', () => {
      expect(parseWorkoutType("something_else")).toBe("wod");
      expect(parseWorkoutType("")).toBe("wod");
      expect(parseWorkoutType("WOD")).toBe("wod");
    });
  });

  describe("formatWorkoutTypeLabel", () => {
    it('should return "WOD" for wod type', () => {
      expect(formatWorkoutTypeLabel("wod")).toBe("WOD");
    });

    it('should return "Rehab" for rehab type', () => {
      expect(formatWorkoutTypeLabel("rehab")).toBe("Rehab");
    });
  });

  describe("buildWorkoutSessionId", () => {
    it("should produce WOD-YYYY-MM-DD-HH-MM format for wod type", () => {
      const date = new Date(2026, 2, 22, 14, 5); // March 22, 2026 14:05
      const result = buildWorkoutSessionId("wod", date);
      expect(result).toBe("WOD-2026-03-22-14-05");
    });

    it("should produce REHAB-YYYY-MM-DD-HH-MM format for rehab type", () => {
      const date = new Date(2026, 0, 1, 9, 30); // Jan 1, 2026 09:30
      const result = buildWorkoutSessionId("rehab", date);
      expect(result).toBe("REHAB-2026-01-01-09-30");
    });

    it("should zero-pad single-digit months, days, hours, and minutes", () => {
      const date = new Date(2026, 0, 5, 3, 7); // Jan 5, 2026 03:07
      const result = buildWorkoutSessionId("wod", date);
      expect(result).toBe("WOD-2026-01-05-03-07");
    });

    it("should use current date when no date is provided", () => {
      const result = buildWorkoutSessionId("wod");
      // Just verify the format matches PREFIX-YYYY-MM-DD-HH-MM
      expect(result).toMatch(/^WOD-\d{4}-\d{2}-\d{2}-\d{2}-\d{2}$/);
    });
  });
});
