import {
  parseWorkoutType,
  formatWorkoutTypeLabel,
  buildWorkoutSessionId,
  DEFAULT_WORKOUT_TYPE,
  extractCatalogMovementsFromText,
  parseRecentWorkoutSetup,
  RECENT_WORKOUT_TTL_MS,
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

  describe("parseRecentWorkoutSetup (24h pre-input persistence)", () => {
    it("retains pre-entered workout setup saved 8 hours earlier on the same day", () => {
      const now = 1775700000000;
      const saved = {
        savedAt: now - 8 * 60 * 60 * 1000,
        profileId: 3,
        workoutType: "wod",
        wodDescription: "AMRAP 15: 10 Pull-ups, 15 Thrusters",
        rawText: "",
        selectedMovements: ["Pull-up", "Thruster"],
        customMovements: [],
        sessionAppearance: "",
      };
      expect(RECENT_WORKOUT_TTL_MS).toBe(24 * 60 * 60 * 1000);
      expect(parseRecentWorkoutSetup(JSON.stringify(saved), now)).toEqual(saved);
    });
  });

  describe("extractCatalogMovementsFromText", () => {
    const groups = [
      { movements: ["Snatch", "Power Snatch", "Squat Snatch", "Thruster", "Front Rack Lunge", "Front Rack Walking Lunge"] },
      { movements: ["DB Lunge", "DB Overhead Lunge", "DB Walking Lunge", "DB Overhead Walking Lunge"] },
      { movements: ["Pull-up", "Chest-to-Bar Pull-up", "Push-up", "Burpee", "Burpee Broad Jump", "Lunge", "Walking Lunge", "Run", "Row"] },
    ];

    it("matches plural/hyphenated movements in order and prefers specific compound names", () => {
      const text = "For Time: 21-15-9 Power Snatches (43kg) + Pull ups, narrow grip";
      expect(extractCatalogMovementsFromText(text, groups)).toEqual([
        "Power Snatch",
        "Pull-up",
      ]);
    });

    it("handles Korean unit suffixes adjacent to English movement names", () => {
      const text = "AMRAP 12분: Thruster 15개 · Burpees 10개 · Row 250m";
      expect(extractCatalogMovementsFromText(text, groups)).toEqual([
        "Thruster",
        "Burpee",
        "Row",
      ]);
    });

    it("promotes distance-prescribed lunges to walking lunges while keeping AMRAP/rep lunges stationary", () => {
      expect(extractCatalogMovementsFromText("200ft dumbbell overhead lunge", groups)).toEqual([
        "DB Overhead Walking Lunge",
      ]);
      expect(extractCatalogMovementsFromText("25m front rack lunges + 10 burpee broad jumps", groups)).toEqual([
        "Front Rack Walking Lunge",
        "Burpee Broad Jump",
      ]);
      expect(extractCatalogMovementsFromText("AMRAP 10m: 20 dumbbell lunges", groups)).toEqual([
        "DB Lunge",
      ]);
      expect(
        extractCatalogMovementsFromText("400m Run / 20 DB Lunges (50/35lb)", groups),
      ).toEqual(["Run", "DB Lunge"]);
      expect(extractCatalogMovementsFromText("400m Run 20 DB Lunges", groups)).toEqual([
        "Run",
        "DB Lunge",
      ]);
      expect(
        extractCatalogMovementsFromText("400m Run / 200ft dumbbell overhead lunges", groups),
      ).toEqual(["Run", "DB Overhead Walking Lunge"]);
    });
  });
});
