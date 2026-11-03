import React from "react";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import { Share } from "react-native";
import { setLanguage } from "@/features/i18n";
import type { WorkoutShareContext } from "../../../shared/workoutShare";
import { buildWorkoutShareText } from "../../../shared/workoutShare";
import { getWorkoutShareContext } from "../api";
import { WorkoutShareSheet } from "./WorkoutShareSheet";

jest.mock("../api", () => ({ getWorkoutShareContext: jest.fn() }));
jest.mock("expo-localization", () => ({ getLocales: () => [{ languageCode: "en" }] }));
jest.mock("../../../shared/workoutShare", () => ({
  buildWorkoutShareText: jest.fn((context, options, locale) => [
    `${locale}: ${context.session.session_id}`,
    "confirmed records",
    ...(options.includeAIObservations ? ["AI observations"] : []),
    ...(options.includeSensorSummary ? ["sensor summary and effort"] : []),
  ].join("\n")),
}));

function fixture(sessionId = "one") {
  return { schema_version: 1, session: { session_id: sessionId } } as unknown as WorkoutShareContext;
}

function sheet(visible = true, sessionId = "one", profileId = 1) {
  return <WorkoutShareSheet visible={visible} sessionId={sessionId} profileId={profileId} onClose={jest.fn()} />;
}

beforeEach(() => {
  jest.clearAllMocks();
  setLanguage("en");
  (getWorkoutShareContext as jest.Mock).mockResolvedValue(fixture());
  jest.spyOn(Share, "share").mockResolvedValue({ action: Share.dismissedAction });
});

afterEach(() => jest.restoreAllMocks());

it("fetches only after opening and shares exactly the selectable preview", async () => {
  const view = render(sheet(false));
  expect(getWorkoutShareContext).not.toHaveBeenCalled();
  view.rerender(sheet());
  const preview = await view.findByLabelText("Text to share");
  expect(getWorkoutShareContext).toHaveBeenCalledWith("one", 1, expect.any(String), expect.any(AbortSignal));
  expect(preview.props.selectable).toBe(true);
  expect(view.getByLabelText("Include AI observations").props.value).toBe(true);
  expect(view.getByLabelText("Include sensor summary and effort").props.value).toBe(true);
  await act(async () => fireEvent.press(view.getByText("Choose an app to share with")));
  expect(Share.share).toHaveBeenCalledWith({ message: preview.props.children, title: "Ask about this workout" });
  expect(view.getByLabelText("Text to share").props.children).toBe(preview.props.children);
  expect(view.getByText(/select and copy this text/)).toBeTruthy();
});

it("updates the preview and actual shared message from both options", async () => {
  const view = render(sheet());
  await view.findByLabelText("Text to share");
  fireEvent(view.getByLabelText("Include AI observations"), "valueChange", false);
  fireEvent(view.getByLabelText("Include sensor summary and effort"), "valueChange", false);
  const preview = view.getByLabelText("Text to share").props.children;
  expect(preview).toBe("en: one\nconfirmed records");
  await act(async () => fireEvent.press(view.getByText("Choose an app to share with")));
  expect(Share.share).toHaveBeenLastCalledWith({ message: preview, title: "Ask about this workout" });
  expect(buildWorkoutShareText).toHaveBeenLastCalledWith(expect.anything(), { includeAIObservations: false, includeSensorSummary: false }, "en");
});

it("preserves the snapshot and options after a share failure and dismissal", async () => {
  (Share.share as jest.Mock).mockRejectedValueOnce(new Error("unavailable"));
  const view = render(sheet());
  await view.findByLabelText("Text to share");
  fireEvent(view.getByLabelText("Include AI observations"), "valueChange", false);
  const preview = view.getByLabelText("Text to share").props.children;
  await act(async () => fireEvent.press(view.getByText("Choose an app to share with")));
  expect(view.getByText(/Your text and choices are saved here/)).toBeTruthy();
  expect(view.getByLabelText("Text to share").props.children).toBe(preview);
  expect(view.getByLabelText("Include AI observations").props.value).toBe(false);
  await act(async () => fireEvent.press(view.getByText("Try sharing again")));
  expect(view.queryByText(/Your text and choices are saved here/)).toBeNull();
  expect(view.getByLabelText("Text to share").props.children).toBe(preview);
  expect(getWorkoutShareContext).toHaveBeenCalledTimes(1);
  expect(Share.share).toHaveBeenLastCalledWith({ message: preview, title: "Ask about this workout" });
});

it("resets and refetches after reopening, ignoring an old request", async () => {
  let finishOld!: (value: WorkoutShareContext) => void;
  (getWorkoutShareContext as jest.Mock).mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }));
  const view = render(sheet());
  view.rerender(sheet(false));
  (getWorkoutShareContext as jest.Mock).mockResolvedValue(fixture("fresh"));
  view.rerender(sheet());
  await view.findByLabelText("Text to share");
  await act(async () => finishOld(fixture("old")));
  expect(view.getByLabelText("Text to share").props.children).toContain("en: fresh");
  fireEvent(view.getByLabelText("Include AI observations"), "valueChange", false);
  view.rerender(sheet(false));
  view.rerender(sheet());
  await view.findByLabelText("Text to share");
  expect(view.getByLabelText("Include AI observations").props.value).toBe(true);
  expect(getWorkoutShareContext).toHaveBeenCalledTimes(3);
});

it("discards the previous profile's snapshot when the selected session changes", async () => {
  const view = render(sheet());
  await view.findByLabelText("Text to share");
  (getWorkoutShareContext as jest.Mock).mockResolvedValue(fixture("two"));
  view.rerender(sheet(true, "two", 2));
  await waitFor(() => expect(view.getByLabelText("Text to share").props.children).toContain("en: two"));
  expect(getWorkoutShareContext).toHaveBeenLastCalledWith("two", 2, expect.any(String), expect.any(AbortSignal));
});

it("offers a load retry without presenting an empty workout as a valid report", async () => {
  (getWorkoutShareContext as jest.Mock).mockRejectedValueOnce(new Error("offline"));
  const view = render(sheet());
  await view.findByText("Couldn't load workout data. Try again.");
  expect(view.queryByLabelText("Text to share")).toBeNull();
  expect(view.queryByText("Choose an app to share with")).toBeNull();
  fireEvent.press(view.getByText("Try again"));
  await view.findByLabelText("Text to share");
  expect(getWorkoutShareContext).toHaveBeenCalledTimes(2);
});

it("updates the labels and report language without refetching when locale changes", async () => {
  const view = render(sheet());
  await view.findByLabelText("Text to share");
  act(() => setLanguage("ko"));
  expect(view.getByText("이 운동을 AI에 물어보기")).toBeTruthy();
  expect(view.getByLabelText("AI 관찰 내용 포함").props.value).toBe(true);
  expect(view.getByLabelText("공유할 텍스트").props.children).toContain("ko: one");
  await act(async () => fireEvent.press(view.getByText("공유할 앱 선택")));
  expect(Share.share).toHaveBeenLastCalledWith({ message: view.getByLabelText("공유할 텍스트").props.children, title: "이 운동을 AI에 물어보기" });
  expect(getWorkoutShareContext).toHaveBeenCalledTimes(1);
});
