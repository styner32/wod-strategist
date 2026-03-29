export type WorkoutType = "wod";

export const DEFAULT_WORKOUT_TYPE: WorkoutType = "wod";

export function parseWorkoutType(value?: string | null): WorkoutType {
  return DEFAULT_WORKOUT_TYPE;
}

export function formatWorkoutTypeLabel(value: WorkoutType): string {
  return "WOD";
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

  return `WOD-${year}-${month}-${day}-${hours}-${minutes}`;
}
