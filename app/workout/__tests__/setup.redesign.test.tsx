import { createSetupSnapshot, recentSetupStorageKey, validateRecordingSetup } from "@/features/wod/recentWorkoutSetup";
import * as ImagePicker from "expo-image-picker";
import { parseWorkoutImage } from "@/features/wod/api";
import { fetchArchiveStates, notifyHistoryChanged } from "@/features/wod/archiveStates";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import React from "react";
import { Alert } from "react-native";
import WorkoutSetup from "../setup";

const mockRouterBack = jest.fn();
const mockRouterPush = jest.fn();
let mockLocalSearchParams: { mode?: string } = {};
let mockProfileId = 7;
let mockOwnerUserId = 1;
let mockSourceArchived = false;
const mockFetchMovementGroups = jest.fn();
const mockFetchPreWodAdvice = jest.fn();
const mockFetchRelatedWods = jest.fn();

const mockStorageMap = new Map<string, string>();

jest.mock("@react-native-async-storage/async-storage", () => ({
  getItem: jest.fn((key: string) =>
    Promise.resolve(mockStorageMap.get(key) ?? null),
  ),
  setItem: jest.fn((key: string, val: string) => {
    mockStorageMap.set(key, val);
    return Promise.resolve();
  }),
  removeItem: jest.fn((key: string) => {
    mockStorageMap.delete(key);
    return Promise.resolve();
  }),
}));

jest.mock("expo-router", () => ({
  router: {
    back: () => mockRouterBack(),
    push: (...args: unknown[]) => mockRouterPush(...args),
  },
  useLocalSearchParams: () => mockLocalSearchParams,
  useFocusEffect: (callback: () => unknown) => require("react").useEffect(callback, [callback]),
}));

jest.mock("expo-image-picker", () => ({
  launchCameraAsync: jest.fn(),
  launchImageLibraryAsync: jest.fn(),
}));

jest.mock("react-native-vision-camera", () => ({
  useCameraDevice: () => undefined,
}));

jest.mock("react-native-safe-area-context", () => {
  const { View } = jest.requireActual<typeof import("react-native")>(
    "react-native",
  );
  return {
    SafeAreaView: ({ children, ...props }: any) => (
      <View {...props}>{children}</View>
    ),
  };
});

jest.mock("@/components/ui/icon-symbol", () => ({
  IconSymbol: () => null,
}));

jest.mock("@/features/auth/jwt", () => ({ validateStoredToken: jest.fn(async () => ({valid:true,expiringSoon:false})) }));
jest.mock("@/features/wod/archiveStates", () => ({
  ...jest.requireActual("@/features/wod/archiveStates"),
  fetchArchiveStates: jest.fn(async (_profileId: number, ids: string[]) => ({checked_at:"now",history_revision:mockSourceArchived?"2":"1",states:ids.map(id=>({session_id:id,state:mockSourceArchived?"archived":"active"}))})),
}));
jest.mock("@/features/auth/useAuthStore", () => ({
  useAuthStore: Object.assign((selector: (s: any) => unknown) => selector({ userId: mockOwnerUserId }), { getState: () => ({ userId: mockOwnerUserId, isLoggedIn: true, handleUnauthorized: jest.fn() }) }),
}));
jest.mock("@/features/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
  useLocale: () => "ko",
}));

jest.mock("@/features/health/useBleHeartRate", () => ({
  getRegisteredBleDevice: jest.fn().mockResolvedValue(null),
  setRegisteredBleDevice: jest.fn().mockResolvedValue(undefined),
}));

jest.mock("@/features/wod/api", () => ({
  fetchMovementGroups: () => mockFetchMovementGroups(),
  fetchPreWodAdvice: (...args: unknown[]) => mockFetchPreWodAdvice(...args),
  fetchRelatedWods: (...args: unknown[]) => mockFetchRelatedWods(...args),
  parseWorkoutImage: jest.fn(),
}));

jest.mock("@/features/wod/ui/PreWodStrategyCard", () => ({
  PreWodStrategyCard: () => null,
}));

jest.mock("@/features/wod/ui/RelatedWodsCard", () => ({
  RelatedWodsCard: () => null,
}));

jest.mock("@/store/useProfileStore", () => ({
  useActiveProfile: () => ({
    id: mockProfileId,
    name: "Sunjin",
    fitnessLevel: "rx",
    injuries: [],
  }),
}));

describe("WorkoutSetup redesign", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockStorageMap.clear();
    mockProfileId = 7; mockOwnerUserId = 1; mockSourceArchived = false;
    mockLocalSearchParams = {};
    mockFetchMovementGroups.mockResolvedValue([
      { category: "Barbell", movements: ["Thruster", "Power Snatch", "Snatch"] },
      { category: "Gymnastics", movements: ["Pull-up", "Chest-to-Bar Pull-up"] },
    ]);
    mockFetchPreWodAdvice.mockResolvedValue(null);
    mockFetchRelatedWods.mockResolvedValue({ related: [] });
  });

  it("defaults to Photo scan mode and keeps manual movement catalog behind a secondary optional action", async () => {
    const { getByText, queryByPlaceholderText } = render(<WorkoutSetup />);

    await waitFor(() => expect(mockFetchMovementGroups).toHaveBeenCalled());

    // Photo mode is active by default
    expect(getByText("setup.photoPrimaryTitle")).toBeTruthy();
    expect(getByText("setup.takePhoto")).toBeTruthy();
    expect(getByText("setup.chooseFromGallery")).toBeTruthy();
    expect(queryByPlaceholderText("setup.searchMovements")).toBeNull();

    // Open secondary movements picker
    fireEvent.press(getByText("setup.secondaryMovementsBtn"));
    expect(getByText("setup.backToWodInput")).toBeTruthy();

    // Return back to primary Photo input
    fireEvent.press(getByText("setup.backToWodInput"));
    expect(getByText("setup.photoPrimaryTitle")).toBeTruthy();
  });

  it("auto-extracts movements from pre-entered WOD text, saves for later (24h), and restores in 1 tap", async () => {
    const alertSpy = jest.spyOn(Alert, "alert");
    const { getByText, getByPlaceholderText, getByTestId, unmount } = render(
      <WorkoutSetup />,
    );

    await waitFor(() => expect(mockFetchMovementGroups).toHaveBeenCalled());

    // Switch to Text tab and pre-enter today's WOD
    fireEvent.press(getByText("setup.inputMethodText"));
    fireEvent.changeText(
      getByPlaceholderText("setup.wodTextPlaceholder"),
      "For Time: 21-15-9 Thrusters (43kg) and Pull-ups",
    );

    // Save for later
    await act(async () => {
      fireEvent.press(getByTestId("save-workout-for-later-text-btn"));
    });

    expect(AsyncStorage.setItem).toHaveBeenCalledWith(
      recentSetupStorageKey({ownerUserId:1,profileId:7}),
      expect.any(String),
    );
    const savedJson = JSON.parse(mockStorageMap.get(recentSetupStorageKey({ownerUserId:1,profileId:7}))!).draft;
    expect(savedJson.profileId).toBe(7);
    expect(savedJson.selectedMovements).toEqual(["Thruster", "Pull-up"]);

    // Confirm alert navigates back to Home
    const buttons = alertSpy.mock.calls[0][2] as any[];
    buttons.find((b) => b.text === "common.ok").onPress();
    expect(mockRouterBack).toHaveBeenCalledTimes(1);

    unmount();

    // Re-open WorkoutSetup later the same day -> saved WOD card appears and restores to Confirm step
    const reopened = render(<WorkoutSetup />);
    await waitFor(() =>
      expect(reopened.getByTestId("reuse-recent-workout-card")).toBeTruthy(),
    );

    await act(async () => { fireEvent.press(reopened.getByTestId("reuse-recent-workout-card")); });
    expect(reopened.getByText("setup.confirmTitle")).toBeTruthy();
    expect(reopened.getByText("Thruster")).toBeTruthy();
    expect(reopened.getByText("Pull-up")).toBeTruthy();

    // Verify weightKg was preserved for fetchRelatedWods even when selectedMovements is populated
    await waitFor(() =>
      expect(mockFetchRelatedWods).toHaveBeenCalledWith(
        expect.objectContaining({
          profileId: 7,
          movement: "Thruster",
          weightKg: 43,
        }),
      ),
    );

    alertSpy.mockRestore();
  });

  it("opens Verify Workout as a confirmation popup and keeps header back button as 1-tap return to Home", async () => {
    mockLocalSearchParams = { mode: "text" };
    const {
      getByPlaceholderText,
      getByTestId,
      getByText,
      queryByTestId,
    } = render(<WorkoutSetup />);

    await waitFor(() => expect(mockFetchMovementGroups).toHaveBeenCalled());

    expect(getByPlaceholderText("setup.wodTextPlaceholder")).toBeTruthy();
    expect(queryByTestId("verify-workout-popup")).toBeNull();

    // Toggle quick-pick movement chip directly and open Verify Workout popup
    fireEvent.press(getByTestId("quick-pick-Thruster"));
    fireEvent.press(getByTestId("setup-next-btn"));

    expect(getByTestId("verify-workout-popup")).toBeTruthy();
    expect(getByText("setup.confirmTitle")).toBeTruthy();
    expect(getByText("Thruster")).toBeTruthy();

    // Close confirmation popup via X button -> stays on single-step Workout Setup
    fireEvent.press(getByTestId("verify-popup-close-btn"));
    expect(queryByTestId("verify-workout-popup")).toBeNull();
    expect(mockRouterBack).not.toHaveBeenCalled();

    // Header back button returns straight to Home in 1 tap
    fireEvent.press(getByTestId("setup-back-btn"));
    expect(mockRouterBack).toHaveBeenCalledTimes(1);
  });
  const seedRecorded = () => {
    const snapshot = { ...createSetupSnapshot({workoutType:"wod",wodDescription:"Fran",rawText:"Fran",selectedMovements:["Thruster"],customMovements:[],sessionAppearance:"blue top"},{ownerUserId:1,profileId:7}), origin:"recording",sourceSessionId:"WOD-20261029-SOURCE" };
    mockStorageMap.set(recentSetupStorageKey({ownerUserId:1,profileId:7}),JSON.stringify({version:2,recentRecording:snapshot}));
    return snapshot;
  };
  it("does not start after a reused source is archived while its confirmation dialog remains open", async () => {
    seedRecorded();
    const alert=jest.spyOn(Alert,"alert").mockImplementation(()=>{});
    const view=render(<WorkoutSetup />);
    await waitFor(()=>expect(view.getByTestId("reuse-recent-workout-card")).toBeTruthy());
    await act(async()=>{fireEvent.press(view.getByTestId("reuse-recent-workout-card"));});
    mockSourceArchived=true;
    await act(async()=>{fireEvent.press(view.getByTestId("setup-start-workout-btn"));});
    expect(mockRouterPush).not.toHaveBeenCalled();
    expect(alert).toHaveBeenCalledWith("common.error","setup.archiveSourceUnavailable");
    alert.mockRestore();
  });
  it("keeps recording provenance across save-later and the delayed camera route", async () => {
    seedRecorded();
    const view=render(<WorkoutSetup />);
    await waitFor(()=>expect(view.getByTestId("reuse-recent-workout-card")).toBeTruthy());
    await act(async()=>{fireEvent.press(view.getByTestId("reuse-recent-workout-card"));});
    await act(async()=>{fireEvent.press(view.getByTestId("save-workout-for-later-confirm-btn"));});
    const slots=JSON.parse(mockStorageMap.get(recentSetupStorageKey({ownerUserId:1,profileId:7}))!);
    expect(slots.draft).toBeUndefined();
    expect(slots.recentRecording).toMatchObject({origin:"recording",sourceSessionId:"WOD-20261029-SOURCE"});
    await act(async()=>{fireEvent.press(view.getByTestId("setup-start-workout-btn"));});
    const route=JSON.parse(mockRouterPush.mock.calls[0][0].params.recordingSetup);
    expect(route).toMatchObject({origin:"recording",sourceSessionId:"WOD-20261029-SOURCE"});
  });
  it("ignores a whiteboard parse delivered after the active profile changed", async () => {
    jest.mocked(ImagePicker.launchImageLibraryAsync).mockResolvedValueOnce({canceled:false,assets:[{uri:"file:///board.jpg"}]} as any);
    let resolve!: (value:any)=>void;
    jest.mocked(parseWorkoutImage).mockImplementationOnce(()=>new Promise(done=>{resolve=done;}));
    const view=render(<WorkoutSetup />);
    await act(async()=>{fireEvent.press(view.getByText("setup.chooseFromGallery"));});
    await waitFor(()=>expect(parseWorkoutImage).toHaveBeenCalled());
    mockProfileId=8; view.rerender(<WorkoutSetup />);
    await act(async()=>{resolve({wod_description:"OLD PROFILE PLAN",raw_text:"old",movements:["Thruster"]});});
    expect(view.queryByText("OLD PROFILE PLAN")).toBeNull();
    expect(view.queryByTestId("verify-workout-popup")).toBeNull();
    expect(mockStorageMap.has(recentSetupStorageKey({ownerUserId:1,profileId:8}))).toBe(false);
  });
  it("immediately restores photo controls when fresh inputs invalidate a pending scan", async () => {
    seedRecorded();
    jest.mocked(ImagePicker.launchImageLibraryAsync).mockResolvedValueOnce({
      canceled: false, assets: [{ uri: "file:///old-board.jpg" }],
    } as any);
    let resolveScan!: (value: any) => void;
    jest.mocked(parseWorkoutImage).mockImplementationOnce(() => new Promise(resolve => { resolveScan = resolve; }));
    const view = render(<WorkoutSetup />);
    await waitFor(() => expect(view.getByTestId("reuse-recent-workout-card")).toBeTruthy());
    await act(async () => { fireEvent.press(view.getByTestId("reuse-recent-workout-card")); });
    fireEvent.press(view.getByTestId("verify-popup-close-btn"));
    await act(async () => { fireEvent.press(view.getByText("setup.chooseFromGallery")); });
    expect(view.getByText("setup.scanningImage")).toBeTruthy();

    fireEvent.press(view.getByTestId("setup-fresh-inputs-btn"));

    expect(view.queryByText("setup.scanningImage")).toBeNull();
    expect(view.getByText("setup.chooseFromGallery")).toBeTruthy();
    await act(async () => { resolveScan({ wod_description: "IGNORED PHOTO", raw_text: "old", movements: ["Pull-up"] }); });
    expect(view.queryByText("IGNORED PHOTO")).toBeNull();
    expect(view.queryByTestId("verify-workout-popup")).toBeNull();
    expect(view.getByText("setup.chooseFromGallery")).toBeTruthy();
    const slots = JSON.parse(mockStorageMap.get(recentSetupStorageKey({ ownerUserId: 1, profileId: 7 }))!);
    expect(slots.draft).toBeUndefined();
    expect(slots.recentRecording.wodDescription).toBe("Fran");
  });
  it.each(["profile", "account"])("clears a pending scan on %s changes and keeps an older response from clearing the next scan", async scopeChange => {
    jest.mocked(ImagePicker.launchImageLibraryAsync)
      .mockResolvedValueOnce({ canceled: false, assets: [{ uri: "file:///old-board.jpg" }] } as any)
      .mockResolvedValueOnce({ canceled: false, assets: [{ uri: "file:///new-board.jpg" }] } as any);
    let resolveOld!: (value: any) => void;
    let resolveNew!: (value: any) => void;
    jest.mocked(parseWorkoutImage)
      .mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve; }))
      .mockImplementationOnce(() => new Promise(resolve => { resolveNew = resolve; }));
    const view = render(<WorkoutSetup />);
    await act(async () => { fireEvent.press(view.getByText("setup.chooseFromGallery")); });
    expect(view.getByText("setup.scanningImage")).toBeTruthy();

    if (scopeChange === "profile") mockProfileId = 8;
    else mockOwnerUserId = 2;
    view.rerender(<WorkoutSetup />);

    expect(view.queryByText("setup.scanningImage")).toBeNull();
    await act(async () => { fireEvent.press(view.getByText("setup.chooseFromGallery")); });
    expect(view.getByText("setup.scanningImage")).toBeTruthy();
    await act(async () => { resolveOld({ wod_description: "OLD SCOPE PLAN", raw_text: "old", movements: ["Thruster"] }); });
    expect(view.queryByText("OLD SCOPE PLAN")).toBeNull();
    expect(view.queryByTestId("verify-workout-popup")).toBeNull();
    expect(view.getByText("setup.scanningImage")).toBeTruthy();

    await act(async () => { resolveNew({ wod_description: "CURRENT SCOPE PLAN", raw_text: "new", movements: ["Pull-up"] }); });
    expect(view.queryByText("setup.scanningImage")).toBeNull();
    expect(view.getByDisplayValue("CURRENT SCOPE PLAN")).toBeTruthy();
    expect(view.getByTestId("verify-workout-popup")).toBeTruthy();
    const slots = JSON.parse(mockStorageMap.get(recentSetupStorageKey({ ownerUserId: mockOwnerUserId, profileId: mockProfileId }))!);
    expect(slots.draft.wodDescription).toBe("CURRENT SCOPE PLAN");
    expect(mockStorageMap.has(recentSetupStorageKey({ ownerUserId: 1, profileId: 7 }))).toBe(false);
  });
  it("clears scanning when applying recent settings and ignores the superseded photo", async () => {
    seedRecorded();
    jest.mocked(ImagePicker.launchImageLibraryAsync).mockResolvedValueOnce({
      canceled: false, assets: [{ uri: "file:///old-board.jpg" }],
    } as any);
    let resolveScan!: (value: any) => void;
    jest.mocked(parseWorkoutImage).mockImplementationOnce(() => new Promise(resolve => { resolveScan = resolve; }));
    const view = render(<WorkoutSetup />);
    await waitFor(() => expect(view.getByTestId("reuse-recent-workout-card")).toBeTruthy());
    await act(async () => { fireEvent.press(view.getByText("setup.chooseFromGallery")); });
    expect(view.getByText("setup.scanningImage")).toBeTruthy();

    await act(async () => { fireEvent.press(view.getByTestId("reuse-recent-workout-card")); });
    fireEvent.press(view.getByTestId("verify-popup-close-btn"));

    expect(view.queryByText("setup.scanningImage")).toBeNull();
    expect(view.getByText("setup.chooseFromGallery")).toBeTruthy();
    await act(async () => { resolveScan({ wod_description: "IGNORED PHOTO", raw_text: "old", movements: ["Pull-up"] }); });
    expect(view.queryByText("IGNORED PHOTO")).toBeNull();
    expect(view.queryByTestId("verify-workout-popup")).toBeNull();
    expect(view.getByText("setup.chooseFromGallery")).toBeTruthy();
  });
  it("immediately cancels inherited OCR when a history refresh finds its source archived", async () => {
    seedRecorded();
    jest.mocked(ImagePicker.launchImageLibraryAsync)
      .mockResolvedValueOnce({ canceled: false, assets: [{ uri: "file:///old-board.jpg" }] } as any)
      .mockResolvedValueOnce({ canceled: false, assets: [{ uri: "file:///new-board.jpg" }] } as any);
    let resolveOld!: (value: any) => void;
    let resolveNew!: (value: any) => void;
    jest.mocked(parseWorkoutImage)
      .mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve; }))
      .mockImplementationOnce(() => new Promise(resolve => { resolveNew = resolve; }));
    const view = render(<WorkoutSetup />);
    await waitFor(() => expect(view.getByTestId("reuse-recent-workout-card")).toBeTruthy());
    await act(async () => { fireEvent.press(view.getByTestId("reuse-recent-workout-card")); });
    fireEvent.press(view.getByTestId("verify-popup-close-btn"));
    await act(async () => { fireEvent.press(view.getByText("setup.chooseFromGallery")); });
    expect(view.getByText("setup.scanningImage")).toBeTruthy();

    mockSourceArchived = true;
    await act(async () => { notifyHistoryChanged(7); });

    expect(view.queryByText("setup.scanningImage")).toBeNull();
    expect(view.queryByTestId("setup-fresh-inputs-btn")).toBeNull();
    await act(async () => { fireEvent.press(view.getByText("setup.chooseFromGallery")); });
    await act(async () => { resolveOld({ wod_description: "ARCHIVED PHOTO", raw_text: "old", movements: ["Thruster"] }); });
    expect(view.getByText("setup.scanningImage")).toBeTruthy();
    expect(view.queryByTestId("verify-workout-popup")).toBeNull();
    await act(async () => { resolveNew({ wod_description: "FRESH PHOTO", raw_text: "new", movements: ["Pull-up"] }); });
    expect(view.getByDisplayValue("FRESH PHOTO")).toBeTruthy();
    const slots = JSON.parse(mockStorageMap.get(recentSetupStorageKey({ ownerUserId: 1, profileId: 7 }))!);
    expect(slots.draft.wodDescription).toBe("FRESH PHOTO");
    expect(slots.recentRecording.wodDescription).toBe("Fran");
  });
  it("keeps a new independent scan active when an older history refresh rejects its replaced source", async () => {
    seedRecorded();
    const view = render(<WorkoutSetup />);
    await waitFor(() => expect(view.getByTestId("reuse-recent-workout-card")).toBeTruthy());
    await act(async () => { fireEvent.press(view.getByTestId("reuse-recent-workout-card")); });
    fireEvent.press(view.getByTestId("verify-popup-close-btn"));
    let resolveRefresh!: (value: any) => void;
    jest.mocked(fetchArchiveStates).mockImplementationOnce(() => new Promise(resolve => { resolveRefresh = resolve; }));
    act(() => { notifyHistoryChanged(7); });

    fireEvent.press(view.getByTestId("setup-fresh-inputs-btn"));
    jest.mocked(ImagePicker.launchImageLibraryAsync).mockResolvedValueOnce({
      canceled: false, assets: [{ uri: "file:///new-board.jpg" }],
    } as any);
    let resolveScan!: (value: any) => void;
    jest.mocked(parseWorkoutImage).mockImplementationOnce(() => new Promise(resolve => { resolveScan = resolve; }));
    await act(async () => { fireEvent.press(view.getByText("setup.chooseFromGallery")); });
    mockSourceArchived = true;
    await act(async () => { resolveRefresh({ checked_at: "now", history_revision: "2", states: [{ session_id: "WOD-20261029-SOURCE", state: "archived" }] }); });

    expect(view.getByText("setup.scanningImage")).toBeTruthy();
    await act(async () => { resolveScan({ wod_description: "INDEPENDENT PHOTO", raw_text: "new", movements: ["Pull-up"] }); });
    expect(view.getByDisplayValue("INDEPENDENT PHOTO")).toBeTruthy();
    const slots = JSON.parse(mockStorageMap.get(recentSetupStorageKey({ ownerUserId: 1, profileId: 7 }))!);
    expect(slots.draft.wodDescription).toBe("INDEPENDENT PHOTO");
    expect(slots.recentRecording.wodDescription).toBe("Fran");
  });
  it("does not persist or apply a whiteboard merge if its inherited source was archived during parsing", async () => {
    seedRecorded();
    jest.mocked(ImagePicker.launchImageLibraryAsync).mockResolvedValueOnce({canceled:false,assets:[{uri:"file:///board.jpg"}]} as any);
    let resolve!: (value:any)=>void;
    jest.mocked(parseWorkoutImage).mockImplementationOnce(()=>new Promise(done=>{resolve=done;}));
    const view=render(<WorkoutSetup />);
    await waitFor(()=>expect(view.getByTestId("reuse-recent-workout-card")).toBeTruthy());
    await act(async()=>{fireEvent.press(view.getByTestId("reuse-recent-workout-card"));});
    fireEvent.press(view.getByTestId("verify-popup-close-btn"));
    await act(async()=>{fireEvent.press(view.getByText("setup.chooseFromGallery"));});
    mockSourceArchived=true;
    await act(async()=>{resolve({wod_description:"NEW PHOTO",raw_text:"photo",movements:["Pull-up"]});});
    expect(view.queryByText("NEW PHOTO")).toBeNull();
    expect(view.queryByTestId("verify-workout-popup")).toBeNull();
    const slots=JSON.parse(mockStorageMap.get(recentSetupStorageKey({ownerUserId:1,profileId:7}))!);
    expect(slots.draft).toBeUndefined();
    expect(slots.recentRecording.wodDescription).toBe("Fran");
  });

  it("produces a valid recordingSetup param when starting without WOD text or selected movements", async () => {
    const view = render(<WorkoutSetup />);
    fireEvent.press(view.getByTestId("setup-next-btn"));
    await waitFor(() => expect(view.getByTestId("verify-workout-popup")).toBeTruthy());
    await act(async () => {
      fireEvent.press(view.getByTestId("setup-start-workout-btn"));
    });
    expect(mockRouterPush).toHaveBeenCalledTimes(1);
    const rawParam = mockRouterPush.mock.calls[0][0].params.recordingSetup as string;
    await expect(
      validateRecordingSetup(decodeURIComponent(rawParam), { ownerUserId: 1, profileId: 7 }),
    ).resolves.toMatchObject({
      origin: "draft",
      wodDescription: "",
      selectedMovements: [],
    });
  });

});
