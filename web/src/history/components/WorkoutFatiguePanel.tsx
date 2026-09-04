import type { SessionFatigue } from "../../api/history";

interface WorkoutFatiguePanelProps {
  fatigue?: SessionFatigue | null;
}

const MUSCLE_GROUPS = [
  { key: "shoulders_push", label: "Shoulders & Push (어깨 / 상체 밀기)" },
  { key: "upper_pull_grip", label: "Back, Pull & Grip (등·광배 / 악력)" },
  { key: "posterior_chain", label: "Posterior Chain (허리 / 후면사슬)" },
  { key: "quads_squat", label: "Quads & Squat (하체 / 스쿼트)" },
  { key: "core_midline", label: "Core & Midline (코어 / 미드라인)" },
  { key: "cardio_metabolic", label: "Cardio & Full Body (심폐 / 전신 유산소)" },
] as const;

export function getFatigueColorClasses(score: number) {
  if (score <= 25) {
    return {
      text: "text-success",
      bg: "bg-success/15",
      border: "border-success/30",
      bar: "bg-success",
    };
  }
  if (score <= 50) {
    return {
      text: "text-sky-400",
      bg: "bg-sky-400/15",
      border: "border-sky-400/30",
      bar: "bg-sky-400",
    };
  }
  if (score <= 75) {
    return {
      text: "text-warning",
      bg: "bg-warning/15",
      border: "border-warning/30",
      bar: "bg-warning",
    };
  }
  return {
    text: "text-error",
    bg: "bg-error/15",
    border: "border-error/30",
    bar: "bg-error",
  };
}

export function WorkoutFatiguePanel({ fatigue }: WorkoutFatiguePanelProps) {
  if (!fatigue || !fatigue.muscles) {
    return null;
  }

  const overallColors = getFatigueColorClasses(fatigue.overall_score);
  const stateLabel = fatigue.state_ko || fatigue.state;

  // Identify top-loaded muscles
  const topMuscles = MUSCLE_GROUPS.map((g) => ({
    ...g,
    score: fatigue.muscles[g.key] ?? 0,
  }))
    .filter((m) => m.score > 30)
    .sort((a, b) => b.score - a.score);

  return (
    <div className="bg-bg-elevated border border-border rounded-xl p-5 mb-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3 mb-4">
        <div className="flex items-center gap-2">
          <span className="text-lg">⚡</span>
          <div>
            <h2 className="text-base font-semibold text-text-primary">
              Muscle Fatigue & Strain (신체 부위별 피로도 / 부하)
            </h2>
            <p className="text-xs text-text-muted mt-0.5">
              Biomechanically computed muscle load across 6 functional movement
              patterns
            </p>
          </div>
        </div>
        <div
          className={`px-3 py-1 rounded-full border text-xs font-semibold ${overallColors.bg} ${overallColors.border} ${overallColors.text}`}
        >
          {stateLabel} ({fatigue.overall_score}%)
        </div>
      </div>

      {/* 6 Muscle Group Bars */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-3.5 my-2">
        {MUSCLE_GROUPS.map((g) => {
          const score = Math.min(100, Math.max(0, fatigue.muscles[g.key] ?? 0));
          const colors = getFatigueColorClasses(score);

          return (
            <div key={g.key} className="space-y-1.5">
              <div className="flex items-center justify-between text-xs">
                <span className="font-medium text-text-secondary">
                  {g.label}
                </span>
                <span className={`font-semibold ${colors.text}`}>{score}%</span>
              </div>
              <div className="h-2 w-full bg-bg-tertiary rounded-full overflow-hidden">
                <div
                  className={`h-full rounded-full transition-all duration-300 ${colors.bar}`}
                  style={{ width: `${score}%` }}
                />
              </div>
            </div>
          );
        })}
      </div>

      {/* Top loaded muscles notice */}
      {topMuscles.length > 0 && (
        <div className="mt-4 pt-3 border-t border-border/60 text-xs text-text-muted flex items-center gap-1.5">
          <span className="font-medium text-text-secondary">
            주요 부하 (Main Strain):
          </span>
          <span>
            {topMuscles
              .slice(0, 2)
              .map((m) => `${m.label.split(" (")[0]} (${m.score}%)`)
              .join(", ")}
          </span>
        </div>
      )}
    </div>
  );
}
