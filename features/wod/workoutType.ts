export type WorkoutType = "wod" | "rehab";

export const DEFAULT_WORKOUT_TYPE: WorkoutType = "wod";

export const WORKOUT_TYPE_OPTIONS: Array<{
  value: WorkoutType;
  label: string;
  description: string;
}> = [
  {
    value: "wod",
    label: "WOD",
    description: "Performance and movement coaching for regular training.",
  },
  {
    value: "rehab",
    label: "Rehab",
    description: "Safer return-to-training feedback for recovery work.",
  },
];

export function parseWorkoutType(value?: string | null): WorkoutType {
  return value === "rehab" ? "rehab" : DEFAULT_WORKOUT_TYPE;
}

export function formatWorkoutTypeLabel(value: WorkoutType): string {
  return value === "rehab" ? "Rehab" : "WOD";
}

export function buildWorkoutSessionId(
  workoutType: WorkoutType,
  now: Date = new Date()
): string {
  const year = now.getFullYear();
  const month = String(now.getMonth() + 1).padStart(2, "0");
  const day = String(now.getDate()).padStart(2, "0");
  const hours = String(now.getHours()).padStart(2, "0");
  const minutes = String(now.getMinutes()).padStart(2, "0");
  const prefix = workoutType === "rehab" ? "REHAB" : "WOD";

  return `${prefix}-${year}-${month}-${day}-${hours}-${minutes}`;
}
