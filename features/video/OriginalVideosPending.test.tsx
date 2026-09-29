import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";

import { OriginalVideosPending } from "./OriginalVideosPending";
import type { OriginalVideoSession } from "./originalVideoStore";

let mockUserId = 7;
let mockProfileId: number | null = 3;
const mockList = jest.fn();
const mockSave = jest.fn();
const mockConfirm = jest.fn();
jest.mock("../auth/useAuthStore", () => ({
  useAuthStore: (selector: (value: unknown) => unknown) => selector({ userId: mockUserId, isLoggedIn: true }),
}));
jest.mock("../../store/useProfileStore", () => ({ useProfileId: () => mockProfileId }));
jest.mock("../../hooks/use-color-scheme", () => ({ useColorScheme: () => "light" }));
jest.mock("../i18n", () => ({ t: (key: string) => key, useLocale: () => "en" }));
jest.mock("../wod/sessionLabel", () => ({ formatSessionLabel: (id: string) => id }));
jest.mock("./originalVideoStore", () => ({
  listOriginalSessions: (...args: unknown[]) => mockList(...args),
  finalizeAndSaveOriginal: (...args: unknown[]) => mockSave(...args),
  confirmOriginalAlreadySaved: (...args: unknown[]) => mockConfirm(...args),
  subscribeOriginalVideos: () => () => {},
}));

const pending: OriginalVideoSession = {
  ownerUserId: 7, profileId: 3, sessionId: "WOD-20260929-LOCALONLY", version: 1,
  revision: 4, mode: "continuous", createdAt: 1, stoppedAt: 10, complete: true,
  status: "permission_denied", chunks: [],
};

beforeEach(() => {
  jest.clearAllMocks();
  mockUserId = 7; mockProfileId = 3;
  mockList.mockResolvedValue([pending]);
  mockSave.mockResolvedValue({ ...pending, status: "saved" });
  mockConfirm.mockResolvedValue(undefined);
});

it("shows and retries device originals without receiving any server history row", async () => {
  const view = render(<OriginalVideosPending />);
  await waitFor(() => expect(view.getByText(pending.sessionId)).toBeTruthy());
  expect(view.getByText("originalVideos.description")).toBeTruthy();
  expect(mockSave).not.toHaveBeenCalled();
  fireEvent.press(view.getByText("originalVideos.retry"));
  await waitFor(() => expect(mockSave).toHaveBeenCalledWith(pending, { retryUncertain: false }));
});

it("requires explicit confirmation before retrying an uncertain Photos save", async () => {
  const uncertain = { ...pending, status: "uncertain" as const };
  mockList.mockResolvedValue([uncertain]);
  const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
  const view = render(<OriginalVideosPending />);
  await waitFor(() => expect(view.getByText("originalVideos.status.uncertain")).toBeTruthy());
  expect(mockSave).not.toHaveBeenCalled();
  fireEvent.press(view.getByText("originalVideos.retry"));
  expect(mockSave).not.toHaveBeenCalled();
  expect(alert).toHaveBeenCalledWith("originalVideos.retryUncertainTitle", "originalVideos.retryUncertainBody", expect.any(Array));
  await act(async () => { alert.mock.calls[0][2]?.[1].onPress?.(); });
  await waitFor(() => expect(mockSave).toHaveBeenCalledWith(uncertain, { retryUncertain: true }));
  alert.mockRestore();
});

it("hides another account or profile's rows immediately on selection changes", async () => {
  const view = render(<OriginalVideosPending />);
  await waitFor(() => expect(view.getByText(pending.sessionId)).toBeTruthy());
  mockUserId = 8;
  await act(async () => { view.rerender(<OriginalVideosPending />); });
  expect(view.queryByText(pending.sessionId)).toBeNull();
  mockUserId = 7; mockProfileId = 4;
  await act(async () => { view.rerender(<OriginalVideosPending />); });
  expect(view.queryByText(pending.sessionId)).toBeNull();
});

it("keeps incomplete recordings visible without offering a full-video save", async () => {
  mockList.mockResolvedValue([{ ...pending, status: "needs_attention", complete: false, captureIssue: "recording_interrupted" }]);
  const view = render(<OriginalVideosPending />);
  await waitFor(() => expect(view.getByText("originalVideos.status.needs_attention")).toBeTruthy());
  expect(view.queryByText("originalVideos.retry")).toBeNull();
  expect(mockSave).not.toHaveBeenCalled();
});

it("shows owned recovery records when offline profile hydration has no active selection", async () => {
  mockProfileId = null;
  const view = render(<OriginalVideosPending />);
  await waitFor(() => expect(view.getByText(pending.sessionId)).toBeTruthy());
  expect(mockList).toHaveBeenCalledWith(null);
  expect(view.getByText("originalVideos.profile")).toBeTruthy();
});

it("distinguishes preparation failure and reveals the persisted native error on request", async () => {
  const lastError = "VIDEO_MERGE_ERROR: Chunk 1 has incompatible format";
  mockList.mockResolvedValue([{ ...pending, status: "failed", lastErrorStage: "preparing", lastError }]);
  const view = render(<OriginalVideosPending />);
  await waitFor(() => expect(view.getByText("originalVideos.failureStage.preparing")).toBeTruthy());
  expect(view.queryByText(lastError)).toBeNull();
  fireEvent.press(view.getByText("originalVideos.showError"));
  expect(view.getByText(lastError)).toBeTruthy();
  expect(mockSave).not.toHaveBeenCalled();
});

it("can reveal an existing failure without newer stage metadata", async () => {
  mockList.mockResolvedValue([{ ...pending, status: "failed", lastError: "old native error" }]);
  const view = render(<OriginalVideosPending />);
  await waitFor(() => expect(view.getByText("originalVideos.showError")).toBeTruthy());
  fireEvent.press(view.getByText("originalVideos.showError"));
  expect(view.getByText("old native error")).toBeTruthy();
});
