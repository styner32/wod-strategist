import React from "react";
import { render } from "@testing-library/react-native";

import { WorkoutVideoPlayer } from "./WorkoutVideoPlayer";

const mockPlayer = {
  addListener: jest.fn(() => ({ remove: jest.fn() })),
  play: jest.fn(),
  currentTime: 0,
  loop: false,
  timeUpdateEventInterval: 0,
};

jest.mock("expo-video", () => {
  const ReactActual = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    useVideoPlayer: (_url: string, configure: (player: typeof mockPlayer) => void) => {
      configure(mockPlayer);
      return mockPlayer;
    },
    VideoView: () => ReactActual.createElement(View, { testID: "video-view" }),
  };
});

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 0, left: 0 }),
}));

jest.mock("@/features/i18n", () => ({
  t: (key: string, values?: { value?: number }) =>
    values?.value == null ? key : `${key}:${values.value}`,
  useLocale: () => "en",
}));

describe("WorkoutVideoPlayer highlights", () => {
  beforeEach(() => {
    mockPlayer.addListener.mockClear();
    mockPlayer.play.mockClear();
  });

  it("renders one parent card with its tag and exact observation rows", () => {
    const { getAllByText, getByText } = render(
      <WorkoutVideoPlayer
        videoUrl="https://example.test/merged.mp4"
        sessionLabel="Test session"
        chunks={[]}
        onClose={jest.fn()}
        highlightSegments={[{
          version: 2,
          start: "0:08",
          end: "0:13",
          type: "mixed_form",
          movement: "Snatch",
          reason: "Mixed evidence",
          tags: ["key_moment"],
          observations: [
            {
              start: "0:10",
              end: "0:10.2",
              type: "positive_form",
              reason: "Stable pull",
              confidence: 0.9,
            },
            {
              start: "0:11",
              end: "0:11.2",
              type: "form_issue",
              reason: "Early arm bend",
            },
          ],
        }]}
      />
    );

    expect(getAllByText("Snatch")).toHaveLength(1);
    expect(getByText("player.mixedForm")).toBeTruthy();
    expect(getByText("⭐ player.keyMoment")).toBeTruthy();
    expect(getByText("Stable pull")).toBeTruthy();
    expect(getByText("Early arm bend")).toBeTruthy();
    expect(getByText("0:10 – 0:10.2")).toBeTruthy();
  });

  it("does not repeat the key-moment tag on a standalone key card", () => {
    const { queryByText } = render(
      <WorkoutVideoPlayer
        videoUrl="https://example.test/merged.mp4"
        sessionLabel="Test session"
        chunks={[]}
        onClose={jest.fn()}
        highlightSegments={[{
          version: 2,
          start: "0:20",
          end: "0:25",
          type: "key_moment",
          movement: "Clean",
          tags: ["key_moment"],
          observations: [{
            start: "0:21",
            end: "0:22",
            type: "technique_event",
            reason: "Bar transition",
          }],
        }]}
      />
    );

    expect(queryByText("⭐ player.keyMoment")).toBeNull();
    expect(queryByText("Bar transition")).toBeTruthy();
  });
});
