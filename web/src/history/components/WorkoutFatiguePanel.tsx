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
  if (!fatigue) {
    return null;
  }

  // Handle insufficient evidence
  if (fatigue.status === "insufficient_evidence") {
    return (
      <div className="bg-bg-elevated border border-border rounded-xl p-5 mb-6">
        <div className="flex items-center justify-between gap-3 mb-2">
          <div className="flex items-center gap-2">
            <span className="text-lg">⚡</span>
            <h2 className="text-base font-semibold text-text-primary">
              Muscle Fatigue & Strain (신체 부위별 피로도 / 부하)
            </h2>
          </div>
          <div className="px-3 py-1 rounded-full border text-xs font-semibold bg-bg-secondary text-text-muted border-border">
            분석 근거 부족
          </div>
        </div>
        <p className="text-xs text-text-muted mt-1">
          유효한 동작 데이터가 부족하여 신체 부위별 부하를 산출할 수 없습니다.
        </p>
      </div>
    );
  }

  if (!fatigue.muscles) {
    return null;
  }

  const isAvailable = fatigue.status === "available";
  const overallScore = fatigue.overall_score ?? 0;
  const overallColors = getFatigueColorClasses(overallScore);
  const stateLabel = fatigue.state_ko || fatigue.state;
  const scoreSuffix = isAvailable ? "/100" : "%";

  // Identify top-loaded muscles
  const threshold = isAvailable ? 50 : 30;
  const topMuscles = MUSCLE_GROUPS.map((g) => ({
    ...g,
    score: fatigue.muscles?.[g.key] ?? 0,
  }))
    .filter((m) => m.score >= threshold)
    .sort((a, b) => {
      if (b.score !== a.score) return b.score - a.score;
      return (
        MUSCLE_GROUPS.findIndex((mg) => mg.key === a.key) -
        MUSCLE_GROUPS.findIndex((mg) => mg.key === b.key)
      );
    });

  return (
    <div className="bg-bg-elevated border border-border rounded-xl p-5 mb-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3 mb-4">
        <div className="flex items-center gap-2">
          <span className="text-lg">⚡</span>
          <div>
            <div className="flex items-center gap-2">
              <h2 className="text-base font-semibold text-text-primary">
                Muscle Fatigue & Strain (신체 부위별 피로도 / 부하)
              </h2>
              {fatigue.heart_rate_adjusted && (
                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-amber-500/15 text-amber-400 border border-amber-500/30">
                  ⚡ 실측 심박 보정
                </span>
              )}
            </div>
            <p className="text-xs text-text-muted mt-0.5">
              Biomechanically computed muscle load across 6 functional movement patterns
            </p>
          </div>
        </div>
        <div
          className={`px-3 py-1 rounded-full border text-xs font-semibold ${overallColors.bg} ${overallColors.border} ${overallColors.text}`}
        >
          {stateLabel} ({overallScore}{scoreSuffix})
        </div>
      </div>

      {/* 6 Muscle Group Bars */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-3.5 my-2">
        {MUSCLE_GROUPS.map((g) => {
          const score = Math.min(100, Math.max(0, fatigue.muscles?.[g.key] ?? 0));
          const colors = getFatigueColorClasses(score);

          return (
            <div key={g.key} className="space-y-1.5">
              <div className="flex items-center justify-between text-xs">
                <span className="font-medium text-text-secondary">
                  {g.label}
                </span>
                <span className={`font-semibold ${colors.text}`}>{score}{scoreSuffix}</span>
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
              .map((m) => `${m.label.split(" (")[0]} (${m.score}${scoreSuffix})`)
              .join(", ")}
          </span>
        </div>
      )}

      {/* Guidance */}
      {fatigue.guidance && (
        <div className="mt-4 pt-3 border-t border-border/60 text-xs flex flex-col gap-1 bg-bg-secondary/40 rounded-lg p-3">
          <div className="flex items-center gap-1.5 font-medium text-text-primary">
            <span>💡</span>
            <span>추정 운동 부하 가이드: {fatigue.guidance.state_label}</span>
          </div>
          <p className="text-text-secondary pl-5">{fatigue.guidance.advice_label}</p>
        </div>
      )}
    </div>
  );
}

