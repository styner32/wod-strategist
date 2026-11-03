import AsyncStorage from "@react-native-async-storage/async-storage";
import { fetchArchiveStates } from "../archiveStates";
import { RECENT_WORKOUT_SETUP_KEY } from "../workoutType";
import { clearRecentSetup, createSetupSnapshot, loadRecentSetup, recentSetupStorageKey, saveDraftSetup, saveRecordingSetup, saveSetupForLater, serializeSetupSnapshot, validateRecordingSetup, verifyRecentSetup } from "../recentWorkoutSetup";
let mockUser = 1;
const mockStorage = new Map<string, string>();
jest.mock("@react-native-async-storage/async-storage", () => ({
  getItem: jest.fn(async (key: string) => mockStorage.get(key) ?? null),
  setItem: jest.fn(async (key: string, value: string) => { mockStorage.set(key, value); }),
}));
jest.mock("../../auth/useAuthStore", () => ({ useAuthStore: { getState: () => ({ userId: mockUser, isLoggedIn: true }) } }));
jest.mock("../api", () => ({ apiClient: jest.fn() }));
jest.mock("../archiveStates", () => ({ ...jest.requireActual("../archiveStates"), fetchArchiveStates: jest.fn() }));
const scope = { ownerUserId: 1, profileId: 7 };
const fields = { workoutType: "wod" as const, wodDescription: "Fran", rawText: "21-15-9", selectedMovements: ["Thruster"], customMovements: [], sessionAppearance: "blue top" };
beforeEach(() => { jest.clearAllMocks(); mockStorage.clear(); mockUser = 1; });
it("preserves a genuine draft when an actual recording binds its exact session", async () => {
  const draft = await saveDraftSetup(fields, scope);
  const launch = createSetupSnapshot({ ...fields, wodDescription: "Grace" }, scope);
  await saveRecordingSetup(JSON.stringify(launch), scope, "WOD-20261029-REAL");
  const saved = JSON.parse(mockStorage.get(recentSetupStorageKey(scope))!);
  expect(saved.draft.snapshotId).toBe(draft.snapshotId);
  expect(saved.recentRecording).toMatchObject({ origin: "recording", sourceSessionId: "WOD-20261029-REAL", wodDescription: "Grace" });
  jest.mocked(fetchArchiveStates).mockResolvedValue({ checked_at: "now", history_revision: "1", states: [{ session_id: "WOD-20261029-REAL", state: "archived" }] });
  expect((await loadRecentSetup(scope))?.snapshotId).toBe(draft.snapshotId);
});
it("requires a new successful check for every recorded setup reuse and permits drafts offline", async () => {
  const launch = createSetupSnapshot(fields, scope);
  await saveRecordingSetup(JSON.stringify(launch), scope, "WOD-20261029-REAL");
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({ checked_at: "now", history_revision: "1", states: [{ session_id: "WOD-20261029-REAL", state: "not_registered" }] });
  const recorded = await loadRecentSetup(scope);
  expect(recorded?.sourceSessionId).toBe("WOD-20261029-REAL");
  jest.mocked(fetchArchiveStates).mockRejectedValue(new Error("offline"));
  await expect(verifyRecentSetup(recorded!, scope)).rejects.toThrow("offline");
  expect(await loadRecentSetup(scope)).toBeUndefined();
  const draft = await saveDraftSetup(fields, scope);
  expect(await verifyRecentSetup(draft, scope)).toBe(true);
});
it("keeps unidentified legacy data without offering it, and never crosses account or profile", async () => {
  mockStorage.set(RECENT_WORKOUT_SETUP_KEY, JSON.stringify({ ...fields, savedAt: Date.now(), profileId: 7 }));
  expect(await loadRecentSetup(scope)).toBeUndefined();
  expect(mockStorage.has(RECENT_WORKOUT_SETUP_KEY)).toBe(true);
  await saveDraftSetup(fields, scope);
  expect(await loadRecentSetup({ ...scope, profileId: 8 })).toBeUndefined();
  mockUser = 2;
  await expect(loadRecentSetup(scope)).rejects.toThrow("setup_scope_changed");
});
it("does not persist an unstarted launch and clears only the selected slot", async () => {
  const draft = await saveDraftSetup(fields, scope);
  createSetupSnapshot(fields, scope);
  expect(JSON.parse(mockStorage.get(recentSetupStorageKey(scope))!).recentRecording).toBeUndefined();
  await clearRecentSetup(scope, draft.snapshotId);
  expect(await loadRecentSetup(scope)).toBeUndefined();
  expect(AsyncStorage.setItem).toHaveBeenCalled();
});

it("rechecks a restored recording rather than persisting a permanent local exclusion", async () => {
  const launch = createSetupSnapshot(fields, scope);
  await saveRecordingSetup(JSON.stringify(launch), scope, "WOD-20261029-REAL");
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({ checked_at: "now", history_revision: "2", states: [{ session_id: "WOD-20261029-REAL", state: "archived" }] })
    .mockResolvedValueOnce({ checked_at: "now", history_revision: "3", states: [{ session_id: "WOD-20261029-REAL", state: "active" }] });
  expect(await loadRecentSetup(scope)).toBeUndefined();
  expect((await loadRecentSetup(scope))?.sourceSessionId).toBe("WOD-20261029-REAL");
});

it("preserves recorded provenance through editing and save-later so archive or offline cannot launder it into a draft", async () => {
  const launch = createSetupSnapshot(fields, scope);
  await saveRecordingSetup(JSON.stringify(launch), scope, "WOD-20261029-SOURCE");
  jest.mocked(fetchArchiveStates).mockResolvedValue({ checked_at: "now", history_revision: "1", states: [{ session_id: "WOD-20261029-SOURCE", state: "active" }] });
  const reused = (await loadRecentSetup(scope))!;
  const saved = await saveSetupForLater({ ...fields, wodDescription: "Edited inherited plan", selectedMovements: ["Thruster", "Pull-up"] }, scope, reused);
  expect(saved).toMatchObject({ origin: "recording", sourceSessionId: "WOD-20261029-SOURCE" });
  expect(JSON.parse(mockStorage.get(recentSetupStorageKey(scope))!).draft).toBeUndefined();
  const route = createSetupSnapshot(fields, scope, Date.now(), saved);
  expect(route).toMatchObject({ origin: "recording", sourceSessionId: "WOD-20261029-SOURCE" });
  jest.mocked(fetchArchiveStates).mockResolvedValue({ checked_at: "now", history_revision: "2", states: [{ session_id: "WOD-20261029-SOURCE", state: "archived" }] });
  expect(await loadRecentSetup(scope)).toBeUndefined();
  await expect(validateRecordingSetup(JSON.stringify(route), scope)).rejects.toThrow("archived_source");
  jest.mocked(fetchArchiveStates).mockRejectedValue(new Error("offline"));
  expect(await loadRecentSetup(scope)).toBeUndefined();
  await expect(saveSetupForLater(fields, scope, saved)).rejects.toThrow("offline");
});

it("allows launching a fresh recording without WOD text or selected movements and does not clobber recentRecording", async () => {
  const existingLaunch = createSetupSnapshot(fields, scope);
  await saveRecordingSetup(serializeSetupSnapshot(existingLaunch), scope, "WOD-20261029-PRIOR");

  const emptyLaunch = createSetupSnapshot(
    { workoutType: "wod", wodDescription: "", rawText: "", selectedMovements: [], customMovements: [], sessionAppearance: "" },
    scope,
  );
  jest.mocked(fetchArchiveStates).mockRejectedValue(new Error("offline"));
  await expect(validateRecordingSetup(serializeSetupSnapshot(emptyLaunch), scope)).resolves.toMatchObject({
    origin: "draft",
    wodDescription: "",
    selectedMovements: [],
  });
  expect(fetchArchiveStates).not.toHaveBeenCalled();

  await saveRecordingSetup(serializeSetupSnapshot(emptyLaunch), scope, "WOD-20261030-EMPTY");
  const saved = JSON.parse(mockStorage.get(recentSetupStorageKey(scope))!);
  expect(saved.recentRecording).toMatchObject({
    origin: "recording",
    sourceSessionId: "WOD-20261029-PRIOR",
    wodDescription: "Fran",
  });
});

it("still rechecks archived provenance when an inherited recording setup has cleared WOD text and movements", async () => {
  const launch = createSetupSnapshot(fields, scope);
  await saveRecordingSetup(serializeSetupSnapshot(launch), scope, "WOD-20261029-SOURCE");
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({
    checked_at: "now",
    history_revision: "1",
    states: [{ session_id: "WOD-20261029-SOURCE", state: "active" }],
  });
  const reused = (await loadRecentSetup(scope))!;
  const clearedInherited = createSetupSnapshot(
    { workoutType: "wod", wodDescription: "", rawText: "", selectedMovements: [], customMovements: [], sessionAppearance: "blue top" },
    scope,
    Date.now(),
    reused,
  );
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({
    checked_at: "now",
    history_revision: "2",
    states: [{ session_id: "WOD-20261029-SOURCE", state: "archived" }],
  });
  await expect(validateRecordingSetup(serializeSetupSnapshot(clearedInherited), scope)).rejects.toThrow("archived_source");
});

it("allows an empty inherited launch only after confirming its original recording is active", async () => {
  const recorded = {
    ...createSetupSnapshot(fields, scope),
    origin: "recording" as const,
    sourceSessionId: "WOD-20261029-SOURCE",
  };
  const launch = createSetupSnapshot(
    { ...fields, wodDescription: "", rawText: "", selectedMovements: [] },
    scope,
    Date.now(),
    recorded,
  );
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({
    checked_at: "now", history_revision: "1",
    states: [{ session_id: recorded.sourceSessionId, state: "active" }],
  });

  await expect(validateRecordingSetup(serializeSetupSnapshot(launch), scope)).resolves.toMatchObject({
    origin: "recording", sourceSessionId: recorded.sourceSessionId, wodDescription: "",
  });
  expect(fetchArchiveStates).toHaveBeenCalledTimes(1);
  expect(fetchArchiveStates).toHaveBeenCalledWith(scope.profileId, [recorded.sourceSessionId], undefined);
});

it("preserves percent sequences in serializeSetupSnapshot across extra decodeURIComponent in route params", async () => {
  const launch = createSetupSnapshot(
    { ...fields, wodDescription: "Back Squat 5x5 @ 80%20 & 50%22 effort" },
    scope,
  );
  const serialized = serializeSetupSnapshot(launch);
  expect(serialized).not.toContain("%");
  const afterExtraDecode = decodeURIComponent(serialized);
  const validated = await validateRecordingSetup(afterExtraDecode, scope);
  expect(validated?.wodDescription).toBe("Back Squat 5x5 @ 80%20 & 50%22 effort");
});
