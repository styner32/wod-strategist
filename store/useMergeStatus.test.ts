import { useMergeStatus } from "./useMergeStatus";

jest.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: jest.fn(() => Promise.resolve(null)),
    setItem: jest.fn(() => Promise.resolve()),
    removeItem: jest.fn(() => Promise.resolve()),
  },
}));

const SID = "WOD-20261020-01SESSION00000000000000001";

describe("useMergeStatus.syncPendingSessions", () => {
  beforeEach(() => {
    useMergeStatus.getState().resetRemergeState();
  });

  const pendingIds = () => useMergeStatus.getState().pending.map((p) => p.sessionId);

  it("keeps a just-stopped recording's banner until the server lists the session", async () => {
    useMergeStatus.getState().addPending(SID);

    // Chunks are still uploading: the server has no PENDING row yet.
    await useMergeStatus.getState().syncPendingSessions([]);
    expect(pendingIds()).toEqual([SID]);

    await useMergeStatus.getState().syncPendingSessions([SID]);
    expect(pendingIds()).toEqual([SID]);

    // Once the listed session finishes, the banner clears.
    await useMergeStatus.getState().syncPendingSessions([]);
    expect(pendingIds()).toEqual([]);
  });

  it("prunes a re-merge entry as soon as its listed server row finishes", async () => {
    await useMergeStatus.getState().enqueueRemerge(
      { sessionId: SID, profileId: 1, workoutType: "wod" },
      async () => undefined,
    );
    expect(pendingIds()).toEqual([SID]);

    await useMergeStatus.getState().syncPendingSessions([]);
    expect(pendingIds()).toEqual([]);
  });
});
