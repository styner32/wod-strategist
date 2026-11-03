import { extractCatalogMovementsFromText } from "../features/wod/workoutType";
import { extractSuggestedMovements } from "./workoutProgress";
import { normalizeDistanceLungeClauses } from "./lungePrescription";

const groups = [{ movements: ["DB Lunge", "DB Walking Lunge", "DB Overhead Lunge", "DB Overhead Walking Lunge", "Front Rack Lunge", "Front Rack Walking Lunge", "Reverse Lunge", "Jumping Lunge", "Walking Lunge", "Lunge"] }];

describe("distance prescription ownership in setup and history", () => {
  it.each([
    ["400m Run / 20 DB Lunges (50/35lb)", "DB Lunge"],
    ["400m Run/20 DB Lunges (50/35lb)", "DB Lunge"],
    ["400m Run 20 DB Lunges", "DB Lunge"],
    ["20 DB Lunges / 400m Run", "DB Lunge"],
    ["Run (400m) 20 DB Lunges", "DB Lunge"],
    ["400m (Run) 20 DB Lunges", "DB Lunge"],
    ["Run 400m DB Lunge", "DB Lunge"],
    ["DB Lunge 400m Run", "DB Lunge"],
    ["20 DB Lunge 20m", "DB Lunge"],
    ["20m DB Lunge 20 reps", "DB Lunge"],
    ["20 reps DB Lunge (20m)", "DB Lunge"],
    ["20m DB Lunge 50lb", "DB Walking Lunge"],
    ["400m Run / 200ft DB Overhead Lunges", "DB Overhead Walking Lunge"],
    ["200/150ft DB Overhead Lunge (50/35lb)", "DB Overhead Walking Lunge"],
    ["DB Lunge:20m", "DB Walking Lunge"],
    ["DB Lunge (20m)", "DB Walking Lunge"],
    ["(20m) DB Lunge", "DB Walking Lunge"],
    ["For Time: 20m DB Lunge", "DB Walking Lunge"],
    ["For Time 20m DB Lunge", "DB Walking Lunge"],
    ["AMRAP 10m: 20 DB Lunges", "DB Lunge"],
    ["AMRAP 10m DB Lunges", "DB Lunge"],
    ["EMOM 12m DB Lunges", "DB Lunge"],
    ["Every 2m DB Lunges", "DB Lunge"],
    ["200ft dumbbell overhead lunge", "DB Overhead Walking Lunge"],
    ["25m front rack lunge", "Front Rack Walking Lunge"],
    ["25m front-rack lunge", "Front Rack Walking Lunge"],
    ["200ft DB-Lunge", "DB Walking Lunge"],
    ["20미터 덤벨 오버헤드 런지", "DB Overhead Walking Lunge"],
    ["덤벨 오버헤드 런지 (20미터)", "DB Overhead Walking Lunge"],
    ["20 Reverse Lunges", "Reverse Lunge"],
    ["20 Jumping Lunges", "Jumping Lunge"],
    ["20m Walking Lunge", "Walking Lunge"],
  ])("associates only the lunge's own distance: %s", (text, expected) => {
    expect(extractSuggestedMovements(text)).toEqual([expected]);
    // The setup catalog used here omits cardio so both routes can be compared directly.
    if (!/[가-힣]/.test(text)) expect(extractCatalogMovementsFromText(text, groups)).toEqual([expected]);
  });

  it("keeps distance and count prescriptions for the same movement separate", () => {
    const text = "200ft DB Lunge / 20 DB Lunges";
    expect(extractCatalogMovementsFromText(text, groups)).toEqual(["DB Walking Lunge", "DB Lunge"]);
    expect(extractSuggestedMovements(text)).toEqual(["DB Walking Lunge", "DB Lunge"]);
  });

  it("preserves numeric ratios and ordinary WOD text", () => {
    expect(normalizeDistanceLungeClauses("200/150ft DB Lunge (50/35lb) / 20 DB Lunges")).toBe("200/150ft DB walking Lunge (50/35lb) / 20 DB Lunges");
    expect(normalizeDistanceLungeClauses("20m Samson (Hip Flexor Lunge) Stretch / 10 Burpees")).toBe("20m Samson (Hip Flexor Lunge) Stretch / 10 Burpees");
  });
});
