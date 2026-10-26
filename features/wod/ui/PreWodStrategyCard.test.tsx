import { fireEvent, render } from "@testing-library/react-native";
import React from "react";
import type { PreWodAdviceResponse } from "../api";
import { PreWodStrategyCard } from "./PreWodStrategyCard";

jest.mock("@/components/ui/icon-symbol", () => ({
  IconSymbol: () => null,
}));

jest.mock("@/features/i18n", () => ({
  t: (key: string, opts?: Record<string, unknown>) =>
    opts ? `${key}:${JSON.stringify(opts)}` : key,
  useLocale: () => "ko",
}));

const mockAdvice: PreWodAdviceResponse = {
  profile_id: 1,
  overall_fatigue_score: 65,
  overall_state: "fatigued",
  overall_state_ko: "피로 주의",
  overall_summary:
    "어깨 피로도가 높으므로 오늘 오버헤드 동작은 템포를 조절하세요.",
  target_rpe: {
    score: 7,
    label: "RPE 7 (조절된 페이스)",
    pacing_strategy: "초반 2라운드는 80% 페이스로 호흡을 유지하세요.",
  },
  muscle_readiness: [
    {
      group: "shoulders_push",
      name_ko: "어깨 / 상체 밀기",
      fatigue_score: 75,
      state: "fatigued",
      state_ko: "피로 주의",
      note: "어제 스내치로 인해 어깨 피로 누적",
    },
    {
      group: "quads_squat",
      name_ko: "하체 / 스쿼트",
      fatigue_score: 20,
      state: "fresh",
      state_ko: "신선",
      note: "충분히 회복된 상태입니다.",
    },
  ],
  scaling_advice: [
    {
      movement: "Handstand Push-up",
      recommendation: "스케일링 추천",
      detail: "박스 핸드스탠드 푸시업으로 변경 권장",
    },
  ],
  mobility_warmup: [
    {
      title: "Thoracic Extension",
      target_area: "Upper Back",
      duration: "2분",
      reason: "어깨 가동성 확보",
    },
  ],
};

describe("PreWodStrategyCard", () => {
  it("renders null when advice is null and loading is false", () => {
    const { toJSON } = render(
      <PreWodStrategyCard advice={null} loading={false} />,
    );
    expect(toJSON()).toBeNull();
  });

  it("renders loading indicator when loading is true", () => {
    const { getByText } = render(
      <PreWodStrategyCard advice={null} loading={true} />,
    );
    expect(getByText("preWod.analyzingFatigue")).toBeTruthy();
  });

  it("renders advice summary, RPE, muscles, scaling, and mobility", () => {
    const { getByText } = render(<PreWodStrategyCard advice={mockAdvice} />);

    expect(
      getByText(
        "어깨 피로도가 높으므로 오늘 오버헤드 동작은 템포를 조절하세요.",
      ),
    ).toBeTruthy();
    expect(getByText("RPE 7 (조절된 페이스)")).toBeTruthy();
    expect(
      getByText("초반 2라운드는 80% 페이스로 호흡을 유지하세요."),
    ).toBeTruthy();
    expect(getByText("어깨 / 상체 밀기")).toBeTruthy();
    expect(getByText("하체 / 스쿼트")).toBeTruthy();
    expect(getByText("Handstand Push-up")).toBeTruthy();
    expect(getByText("박스 핸드스탠드 푸시업으로 변경 권장")).toBeTruthy();
    expect(getByText("Thoracic Extension")).toBeTruthy();
  });

  it("toggles muscle detail view on press", () => {
    const { getByText, queryByText } = render(
      <PreWodStrategyCard advice={mockAdvice} />,
    );

    // In compact mode, detailed note is not rendered
    expect(queryByText("어제 스내치로 인해 어깨 피로 누적")).toBeNull();

    // Toggle expand
    fireEvent.press(getByText("▼ 상세보기"));

    // Detailed note is now visible
    expect(getByText("어제 스내치로 인해 어깨 피로 누적")).toBeTruthy();
  });

  it("renders correctly for no_history / insufficient with null score and null target_rpe", () => {
    const noHistoryAdvice: PreWodAdviceResponse = {
      profile_id: 1,
      overall_fatigue_score: null,
      overall_state: "moderate",
      overall_state_ko: "보통",
      overall_summary: "최근 운동 근거만으로 강도를 판단할 수 없습니다. 실제 컨디션을 확인하세요.",
      target_rpe: null,
      evidence_status: "no_history",
      evidence: {
        total_sessions: 0,
        valid_sessions: 0,
        excluded_sessions: 0,
        unresolved_time_sessions: 0,
      },
      scaling_advice: [],
      mobility_warmup: [],
    };

    const { getByText, queryByText } = render(
      <PreWodStrategyCard advice={noHistoryAdvice} />,
    );

    expect(getByText("보통")).toBeTruthy();
    expect(queryByText(/null/)).toBeNull();
    expect(queryByText(/%/)).toBeNull();
    expect(queryByText(/RPE/)).toBeNull();
    expect(
      getByText("최근 운동 근거만으로 강도를 판단할 수 없습니다. 실제 컨디션을 확인하세요."),
    ).toBeTruthy();
  });

  it("renders partial history badge and /100 score for partial evidence status", () => {
    const partialAdvice: PreWodAdviceResponse = {
      profile_id: 1,
      overall_fatigue_score: 55,
      overall_state: "moderate",
      overall_state_ko: "보통",
      overall_summary: "최근 7일 중 일부 기록만 확인되었습니다.",
      target_rpe: null,
      evidence_status: "partial",
      evidence: {
        total_sessions: 3,
        valid_sessions: 2,
        excluded_sessions: 1,
        unresolved_time_sessions: 0,
      },
      muscle_readiness: [
        {
          group: "shoulders_push",
          name_ko: "어깨 / 상체 밀기",
          fatigue_score: 60,
          state: "moderate",
          state_ko: "보통",
          note: "일부 기록 기반",
        },
      ],
      scaling_advice: [],
      mobility_warmup: [],
    };

    const { getByText } = render(<PreWodStrategyCard advice={partialAdvice} />);

    expect(getByText("preWod.partialHistoryBadge")).toBeTruthy();
    expect(getByText("보통 (55/100)")).toBeTruthy();
    expect(getByText("60/100")).toBeTruthy();
  });

  it("renders recent 3 WOD effort reflection reason, movements, duration, manual Effort, and separate hybrid sensor badge", () => {
    const adviceWithReflection: PreWodAdviceResponse = {
      ...mockAdvice,
      evidence_status: "complete",
      effort_reflection: {
        applied: true,
        rated_count: 1,
        sensor_count: 1,
        total_recent: 2,
        reason:
          "직전 Thruster·Pull-up WOD(8분 15초)에서 높은 체감 강도(Effort 9/10)가 기록되어 초반 페이스를 조정했습니다.",
        recent_workouts: [
          {
            session_id: "WOD-20261024-01A",
            workout_at: "2026-10-24T09:30:00Z",
            movements: ["Thruster", "Pull-up"],
            duration_seconds: 495,
            effort: 9,
            hybrid_sensor: {
              heart_intensity: 0.82,
              motion_intensity: 0.82,
              hybrid_intensity: 82.2,
              data_coverage: 0.96,
            },
          },
          {
            session_id: "WOD-20261022-01B",
            workout_at: "2026-10-22T10:00:00Z",
            movements: ["Deadlift"],
            duration_seconds: null,
            effort: null,
          },
        ],
      },
    };

    const { getByText } = render(
      <PreWodStrategyCard advice={adviceWithReflection} />,
    );

    expect(getByText("preWod.recentFeedbackTitle")).toBeTruthy();
    expect(
      getByText(
        "직전 Thruster·Pull-up WOD(8분 15초)에서 높은 체감 강도(Effort 9/10)가 기록되어 초반 페이스를 조정했습니다.",
      ),
    ).toBeTruthy();
    expect(getByText("Thruster · Pull-up · 8:15")).toBeTruthy();
    expect(getByText('preWod.effortBadge:{"effort":9}')).toBeTruthy();
    expect(getByText('preWod.hybridSensorBadge:{"score":82}')).toBeTruthy();
    expect(
      getByText("Deadlift · preWod.durationUnconfirmed"),
    ).toBeTruthy();
    expect(getByText("preWod.effortUnrated")).toBeTruthy();
  });
});
