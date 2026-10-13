import React from "react";
import { fireEvent, render, waitFor } from "@testing-library/react-native";
import { WODTrainingCard } from "./WODTrainingCard";
import { getWODTraining, getWODTrainingLoad, updateWODEffort } from "../api";
jest.mock("../api", () => ({ getWODTraining: jest.fn(), getWODTrainingLoad: jest.fn(), updateWODEffort: jest.fn() }));
jest.mock("@/features/i18n", () => ({ useLocale: jest.fn(), t: (key: string, args?: Record<string, unknown>) => `${key}${args ? JSON.stringify(args) : ""}` }));
const score = { evaluation_version: 1, intensity: 80, wod_start: { seconds: 120, end_seconds: 1320, status: "confirmed" } };
const data = { eligible: true, effort: 8, session_load: 160, wod_start: score.wod_start, score };
const comparison = { seven: { daily_average: 10, missing_effort: 0, missing_duration: 0 }, twenty_eight: { daily_average: 8, missing_effort: 0, missing_duration: 0 }, status: "available", change_percent: 25 };
beforeEach(() => { jest.clearAllMocks(); (getWODTraining as jest.Mock).mockResolvedValue(data); (getWODTrainingLoad as jest.Mock).mockResolvedValue({ comparison, missing_workout_time: 0 }); });
it("does not fetch or offer effort for historical scores", () => {
  const view = render(<WODTrainingCard sessionId="legacy" profileId={1} scoreJSON='{"intensity":80}' />);
  expect(view.toJSON()).toBeNull(); expect(getWODTraining).not.toHaveBeenCalled();
});
it("saves edited effort and refreshes the period comparison", async () => {
  (updateWODEffort as jest.Mock).mockResolvedValue({ ...data, effort: 4, session_load: 80 });
  const view = render(<WODTrainingCard sessionId="new" profileId={1} scoreJSON={JSON.stringify(score)} />);
  fireEvent.press(view.getByText(/wodTraining.title/));
  await waitFor(() => expect(getWODTraining).toHaveBeenCalledWith("new"));
  fireEvent.press(view.getByLabelText('wodTraining.effortChoice{"score":4}'));
  fireEvent.press(view.getByText("wodTraining.save"));
  await waitFor(() => expect(updateWODEffort).toHaveBeenCalledWith("new", 4));
  await waitFor(() => expect(view.getByText('wodTraining.sessionLoad{"value":"80.0"}')).toBeTruthy());
  expect(getWODTrainingLoad).toHaveBeenCalledTimes(2);
});
it("shows a withheld comparison for missing records and retains input on save failure", async () => {
  (getWODTrainingLoad as jest.Mock).mockResolvedValue({ comparison: { ...comparison, status: "incomplete_records", change_percent: null } });
  (updateWODEffort as jest.Mock).mockRejectedValue(new Error("offline"));
  const view = render(<WODTrainingCard sessionId="new" profileId={1} scoreJSON={JSON.stringify(score)} />);
  fireEvent.press(view.getByText(/wodTraining.title/));
  await waitFor(() => expect(view.getByText("wodTraining.incomplete_records")).toBeTruthy());
  fireEvent.press(view.getByLabelText('wodTraining.effortChoice{"score":5}'));fireEvent.press(view.getByText("wodTraining.save"));
  await waitFor(() => expect(view.getByText("wodTraining.failed")).toBeTruthy());
});
