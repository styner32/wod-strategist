import { apiClient } from "../api";
import { ARCHIVE_STATE_TIMEOUT_MS, fetchArchiveStates, sourceCanInfluenceWorkouts, subscribeHistoryChanges } from "../archiveStates";
let mockUser = 1;
jest.mock("../../auth/useAuthStore", () => ({ useAuthStore: { getState: () => ({ userId: mockUser, isLoggedIn: true }) } }));
jest.mock("../api", () => ({ apiClient: jest.fn() }));
const response = (state = "active", revision = "1") => ({ checked_at: "2026-10-29T01:00:00Z", history_revision: revision, states: [{ session_id: "WOD-20261029-EXACT", state }] });
beforeEach(() => { jest.clearAllMocks(); mockUser = 1; });
it("checks exact IDs including old or unregistered recordings, rather than a history page", async () => {
  jest.mocked(apiClient).mockResolvedValue(response("not_registered"));
  const result = await fetchArchiveStates(7, ["WOD-20261029-EXACT", "WOD-20261029-EXACT"]);
  expect(apiClient).toHaveBeenCalledWith("/sessions/archive-states", expect.objectContaining({ method: "POST", bodyPayload: { profile_id: 7, session_ids: ["WOD-20261029-EXACT"] } }));
  expect(sourceCanInfluenceWorkouts({ ownerUserId: 1, profileId: 7, sessionId: "WOD-20261029-EXACT" }, result.states[0])).toBe(true);
  expect(sourceCanInfluenceWorkouts({ ownerUserId: 2, profileId: 7, sessionId: "WOD-20261029-EXACT" }, result.states[0])).toBe(false);
});
it.each(["archived", "unavailable"])("excludes a %s source", async state => {
  jest.mocked(apiClient).mockResolvedValue(response(state));
  const result = await fetchArchiveStates(7, ["WOD-20261029-EXACT"]);
  expect(sourceCanInfluenceWorkouts({ ownerUserId: 1, profileId: 7, sessionId: "WOD-20261029-EXACT" }, result.states[0])).toBe(false);
});
it("does not reuse an earlier active result when offline or accept a missing state", async () => {
  jest.mocked(apiClient).mockResolvedValueOnce(response()).mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce({ ...response(), states: [] });
  await fetchArchiveStates(7, ["WOD-20261029-EXACT"]);
  await expect(fetchArchiveStates(7, ["WOD-20261029-EXACT"])).rejects.toThrow("offline");
  await expect(fetchArchiveStates(7, ["WOD-20261029-EXACT"])).rejects.toThrow("invalid_archive_state_response");
});
it("ignores an answer delivered after the account changed", async () => {
  jest.mocked(apiClient).mockImplementationOnce(async () => { mockUser = 2; return response(); });
  await expect(fetchArchiveStates(7, ["WOD-20261029-EXACT"])).rejects.toThrow("archive_scope_changed");
});
it("notifies profile consumers when a fresh read sees a new history revision", async () => {
  const changed = jest.fn(); const off = subscribeHistoryChanges(changed);
  jest.mocked(apiClient).mockResolvedValueOnce(response("active", "100")).mockResolvedValueOnce(response("archived", "101"));
  await fetchArchiveStates(88, ["WOD-20261029-EXACT"]); changed.mockClear();
  await fetchArchiveStates(88, ["WOD-20261029-EXACT"]);
  expect(changed).toHaveBeenCalledWith(88); off();
});

it("rejects a late older response after a newer archive revision was already observed", async () => {
  jest.mocked(apiClient).mockResolvedValueOnce(response("archived", "5")).mockResolvedValueOnce(response("active", "4"));
  await fetchArchiveStates(99, ["WOD-20261029-EXACT"]);
  await expect(fetchArchiveStates(99, ["WOD-20261029-EXACT"])).rejects.toThrow("stale_archive_state_response");
});

it("bounds a stalled entire archive lookup, aborts it, and fails closed", async () => {
  jest.useFakeTimers();
  jest.mocked(apiClient).mockImplementationOnce(()=>new Promise(()=>{}));
  const pending=fetchArchiveStates(77,["WOD-20261029-EXACT"]);
  const rejected=expect(pending).rejects.toThrow("archive_state_timeout");
  try {
    await jest.advanceTimersByTimeAsync(ARCHIVE_STATE_TIMEOUT_MS);
    await rejected;
    expect(jest.mocked(apiClient).mock.calls[0][1]?.signal?.aborted).toBe(true);
    expect(jest.getTimerCount()).toBe(0);
  } finally { jest.useRealTimers(); }
});
it("ends promptly when its caller aborts and removes its deadline", async () => {
  jest.useFakeTimers();
  const caller=new AbortController();
  const remove=jest.spyOn(caller.signal,"removeEventListener");
  jest.mocked(apiClient).mockImplementationOnce(()=>new Promise(()=>{}));
  const pending=fetchArchiveStates(78,["WOD-20261029-EXACT"],caller.signal);
  const rejected=expect(pending).rejects.toThrow("archive_check_cancelled");
  caller.abort();
  try { await rejected; expect(jest.getTimerCount()).toBe(0); expect(remove).toHaveBeenCalledWith("abort",expect.any(Function)); } finally { remove.mockRestore(); jest.useRealTimers(); }
});
