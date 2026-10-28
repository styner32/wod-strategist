import { fireEvent, render } from "@testing-library/react-native";
import React from "react";
import type { ProfileReadinessResponse } from "../api";
import { HomeReadinessCard } from "./HomeReadinessCard";

jest.mock("@/features/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) => {
    if (params) {
      return `${key}:${JSON.stringify(params)}`;
    }
    return key;
  },
  useLocale: () => "ko",
}));

describe("HomeReadinessCard", () => {
  const sampleReadiness: ProfileReadinessResponse = {
    profile_id: 1,
    overall_fatigue_score: 58,
    overall_state: "fatigued",
    overall_state_ko: "피로 주의",
    evidence_status: "complete",
    evidence: {
      total_sessions: 3,
      valid_sessions: 3,
      excluded_sessions: 0,
      unresolved_time_sessions: 0,
    },
    as_of: new Date().toISOString(),
    last_workout_at: new Date(Date.now() - 5 * 60 * 60 * 1000).toISOString(),
    muscle_readiness: [
      {
        group: "shoulders_push",
        name_ko: "어깨/푸쉬",
        fatigue_score: 72,
        state: "fatigued",
        state_ko: "피로 주의",
        note: "",
      },
      {
        group: "upper_pull_grip",
        name_ko: "상체 당기기/그립",
        fatigue_score: 54,
        state: "fatigued",
        state_ko: "피로 주의",
        note: "",
      },
      {
        group: "posterior_chain",
        name_ko: "후면 사슬",
        fatigue_score: 20,
        state: "fresh",
        state_ko: "신선",
        note: "",
      },
      {
        group: "quads_squat",
        name_ko: "하체/스쿼트",
        fatigue_score: 35,
        state: "moderate",
        state_ko: "보통",
        note: "",
      },
      {
        group: "core_midline",
        name_ko: "코어",
        fatigue_score: 25,
        state: "fresh",
        state_ko: "신선",
        note: "",
      },
      {
        group: "cardio_metabolic",
        name_ko: "심폐 지구력",
        fatigue_score: 48,
        state: "moderate",
        state_ko: "보통",
        note: "",
      },
    ],
    recent_workouts: [
      {
        session_id: "WOD-20261027-01TEST",
        workout_at: new Date(Date.now() - 5 * 60 * 60 * 1000).toISOString(),
        movements: ["Thruster", "Pull-up"],
        duration_seconds: 420,
        effort: 8,
      },
    ],
  };

  it("renders empty onboarding prompt when profile has no workout history", () => {
    const { getByTestId, getByText } = render(
      <HomeReadinessCard
        hasProfile={true}
        readiness={{
          ...sampleReadiness,
          overall_fatigue_score: null,
          evidence_status: "no_history",
          muscle_readiness: [],
        }}
      />,
    );

    expect(getByTestId("home-readiness-no-history")).toBeTruthy();
    expect(getByText("home.readinessNoHistory")).toBeTruthy();
  });

  it("shows summary by default and expands 6-muscle bars when tapped", () => {
    const onPressHistory = jest.fn();
    const { getByTestId, queryByTestId, getByText } = render(
      <HomeReadinessCard
        hasProfile={true}
        readiness={sampleReadiness}
        onPressHistory={onPressHistory}
      />,
    );

    expect(getByText("피로 주의 · 58/100")).toBeTruthy();
    expect(queryByTestId("home-readiness-muscles")).toBeNull();

    // Expand on tap
    fireEvent.press(getByTestId("home-readiness-card"));
    expect(getByTestId("home-readiness-muscles")).toBeTruthy();
    expect(getByText("72/100")).toBeTruthy();

    // Navigate to history from expanded view
    fireEvent.press(getByTestId("home-readiness-history-link"));
    expect(onPressHistory).toHaveBeenCalledTimes(1);
  });

  it("renders partial history badge on its own row without colliding with header title", () => {
    const { getByText } = render(
      <HomeReadinessCard
        hasProfile={true}
        readiness={{
          ...sampleReadiness,
          evidence_status: "partial",
        }}
      />,
    );

    expect(getByText("home.readinessTitle")).toBeTruthy();
    expect(getByText("preWod.partialHistoryBadge")).toBeTruthy();
    expect(getByText("피로 주의 · 58/100")).toBeTruthy();
  });
});
