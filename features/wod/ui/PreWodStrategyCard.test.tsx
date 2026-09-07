import { fireEvent, render } from "@testing-library/react-native";
import React from "react";
import type { PreWodAdviceResponse } from "../api";
import { PreWodStrategyCard } from "./PreWodStrategyCard";

jest.mock("@/components/ui/icon-symbol", () => ({
  IconSymbol: () => null,
}));

jest.mock("@/features/i18n", () => ({
  t: (key: string) => key,
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
});
