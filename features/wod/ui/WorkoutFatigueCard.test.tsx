import { render } from "@testing-library/react-native";
import React from "react";
import type { SessionFatigue } from "../history";
import {
  WorkoutFatigueCard,
  getFatigueColor,
  getFatigueStateText,
} from "./WorkoutFatigueCard";

jest.mock("@/features/i18n", () => ({
  t: (key: string, params?: Record<string, any>) => {
    if (params?.muscles) {
      return `주요 부하: ${params.muscles}`;
    }
    const map: Record<string, string> = {
      "historyList.fatigueCardTitle": "신체 부위별 피로도 / 부하",
      "historyList.fresh": "신선",
      "historyList.moderate": "보통",
      "historyList.fatigued": "피로 주의",
      "historyList.exhausted": "극심한 피로",
      "historyList.insufficientEvidence": "분석 근거 부족",
      "historyList.insufficientEvidenceNotice":
        "유효한 동작 데이터가 부족하여 신체 부위별 부하를 산출할 수 없습니다.",
      "historyList.heartRateAdjusted": "실측 심박으로 보정한 추정값",
      "muscleGroups.shoulders_push": "어깨 / 상체 밀기",
      "muscleGroups.upper_pull_grip": "등·광배 / 당기기·악력",
      "muscleGroups.posterior_chain": "허리 / 후면사슬",
      "muscleGroups.quads_squat": "하체 / 스쿼트",
      "muscleGroups.core_midline": "코어 / 미드라인",
      "muscleGroups.cardio_metabolic": "심폐 / 전신 유산소",
    };
    return map[key] || key;
  },
}));

describe("WorkoutFatigueCard", () => {
  const mockFatigue: SessionFatigue = {
    overall_score: 68,
    state: "fatigued",
    state_ko: "피로 주의",
    muscles: {
      shoulders_push: 85,
      upper_pull_grip: 20,
      posterior_chain: 45,
      quads_squat: 78,
      core_midline: 60,
      cardio_metabolic: 70,
    },
  };

  it("returns null when fatigue is not provided", () => {
    const { toJSON } = render(<WorkoutFatigueCard fatigue={null} />);
    expect(toJSON()).toBeNull();
  });

  it("renders overall score, state badge, and muscle groups", () => {
    const { getByText } = render(<WorkoutFatigueCard fatigue={mockFatigue} />);

    expect(getByText("신체 부위별 피로도 / 부하")).toBeTruthy();
    expect(getByText("피로 주의 (68%)")).toBeTruthy();

    // Muscle names
    expect(getByText("어깨 / 상체 밀기")).toBeTruthy();
    expect(getByText("하체 / 스쿼트")).toBeTruthy();
    expect(getByText("심폐 / 전신 유산소")).toBeTruthy();

    // Muscle scores
    expect(getByText("85%")).toBeTruthy();
    expect(getByText("78%")).toBeTruthy();
    expect(getByText("70%")).toBeTruthy();
  });

  it("renders top loaded muscle summary when score > 30", () => {
    const { getByText } = render(<WorkoutFatigueCard fatigue={mockFatigue} />);
    expect(
      getByText(/주요 부하: 어깨 \/ 상체 밀기 \(85%\), 하체 \/ 스쿼트 \(78%\)/),
    ).toBeTruthy();
  });

  it("renders available status with /100 score format, top muscles >= 50, and HR adjusted note", () => {
    const availableFatigue: SessionFatigue = {
      status: "available",
      overall_score: 68,
      state: "fatigued",
      state_ko: "피로 주의",
      heart_rate_adjusted: true,
      muscles: {
        shoulders_push: 85,
        upper_pull_grip: 20,
        posterior_chain: 45,
        quads_squat: 78,
        core_midline: 49,
        cardio_metabolic: 70,
      },
      guidance: {
        state_code: "fatigued",
        advice_code: "high_load",
        text_en: "High workout load observed",
        text_ko: "이번 세션의 추정 운동 부하가 높습니다.",
      },
    };

    const { getByText, queryByText } = render(
      <WorkoutFatigueCard fatigue={availableFatigue} />,
    );

    // /100 format in header and bars
    expect(getByText("피로 주의 (68/100)")).toBeTruthy();
    expect(getByText("85/100")).toBeTruthy();
    expect(getByText("78/100")).toBeTruthy();

    // HR adjusted notice
    expect(getByText(/실측 심박으로 보정한 추정값/)).toBeTruthy();

    // Guidance text
    expect(getByText(/이번 세션의 추정 운동 부하가 높습니다./)).toBeTruthy();

    // Top muscles >= 50 (shoulders 85, quads 78, cardio 70 -> top 2 are shoulders and quads)
    expect(
      getByText(/주요 부하: 어깨 \/ 상체 밀기 \(85\/100\), 하체 \/ 스쿼트 \(78\/100\)/),
    ).toBeTruthy();
    expect(queryByText(/49\/100/)).toBeNull;
  });

  it("renders insufficient_evidence card without 0% or score", () => {
    const insufficientFatigue: SessionFatigue = {
      status: "insufficient_evidence",
      heart_rate_adjusted: false,
    };

    const { getByText, queryByText } = render(
      <WorkoutFatigueCard fatigue={insufficientFatigue} />,
    );

    expect(getByText("분석 근거 부족")).toBeTruthy();
    expect(
      getByText("유효한 동작 데이터가 부족하여 신체 부위별 부하를 산출할 수 없습니다."),
    ).toBeTruthy();
    expect(queryByText("0%")).toBeNull();
    expect(queryByText("0/100")).toBeNull();
  });

  describe("getFatigueColor", () => {
    it("returns correct color based on threshold", () => {
      expect(getFatigueColor(15)).toBe("#30D158"); // Green
      expect(getFatigueColor(40)).toBe("#64D2FF"); // Cyan
      expect(getFatigueColor(65)).toBe("#FF9F0A"); // Orange
      expect(getFatigueColor(90)).toBe("#FF453A"); // Red
    });
  });

  describe("getFatigueStateText", () => {
    it("returns localized state text based on state and score", () => {
      expect(getFatigueStateText("fresh", 10)).toBe("신선");
      expect(getFatigueStateText("moderate", 40)).toBe("보통");
      expect(getFatigueStateText("fatigued", 65)).toBe("피로 주의");
      expect(getFatigueStateText("exhausted", 90)).toBe("극심한 피로");
    });
  });
});
