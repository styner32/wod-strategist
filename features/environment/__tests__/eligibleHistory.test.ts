import { fetchArchiveStates } from "../../wod/archiveStates";
import { readEligibleEnvironmentHistory } from "../eligibleHistory";
import { listSessions, readRecords } from "../store";
let mockUser = 1;
jest.mock("../../auth/useAuthStore", () => ({ useAuthStore: { getState: () => ({ userId: mockUser, isLoggedIn: true }) } }));
jest.mock("../../wod/api", () => ({ apiClient: jest.fn() }));
jest.mock("../../wod/archiveStates", () => ({ ...jest.requireActual("../../wod/archiveStates"), fetchArchiveStates: jest.fn() }));
jest.mock("../store", () => ({ listSessions: jest.fn(), readRecords: jest.fn() }));
beforeEach(() => {
  jest.clearAllMocks(); mockUser = 1;
  jest.mocked(readRecords).mockImplementation(async session => [{ id: session.sessionId, sessionId: session.sessionId }] as any);
});
it("scans exact IDs in batches of 100 until thirty eligible sessions remain, without reading archived evidence", async () => {
  const sessions = Array.from({ length: 140 }, (_, i) => ({ sessionId: `s${i}`, ownerUserId: 1, profileId: 7 }));
  jest.mocked(listSessions).mockResolvedValue(sessions as any);
  jest.mocked(fetchArchiveStates).mockImplementation(async (_pid, ids) => ({ checked_at: "now", history_revision: "1", states: ids.map(id => ({ session_id: id, state: Number(id.slice(1)) < 105 ? "archived" : "active" })) }));
  const records = await readEligibleEnvironmentHistory(7);
  expect(fetchArchiveStates).toHaveBeenCalledTimes(3);
  expect(jest.mocked(fetchArchiveStates).mock.calls[0][1]).toHaveLength(100);
  expect(records).toHaveLength(30); expect(records[0].sessionId).toBe("s105");
  expect(readRecords).toHaveBeenCalledTimes(30);
});
it("allows verified unregistered local sources but rejects foreign ownership and unavailable states", async () => {
  jest.mocked(listSessions).mockResolvedValue([
    { sessionId: "local", ownerUserId: 1, profileId: 7 }, { sessionId: "foreign", ownerUserId: 2, profileId: 7 },
    { sessionId: "blocked", ownerUserId: 1, profileId: 7 },
  ] as any);
  jest.mocked(fetchArchiveStates).mockResolvedValue({ checked_at: "now", history_revision: "1", states: [{ session_id: "local", state: "not_registered" }, { session_id: "blocked", state: "unavailable" }] });
  expect((await readEligibleEnvironmentHistory(7)).map(r => r.sessionId)).toEqual(["local"]);
});
it("fails closed offline without opening historical evidence", async () => {
  jest.mocked(listSessions).mockResolvedValue([{ sessionId: "local", ownerUserId: 1, profileId: 7 }] as any);
  jest.mocked(fetchArchiveStates).mockRejectedValue(new Error("offline"));
  await expect(readEligibleEnvironmentHistory(7)).rejects.toThrow("offline");
  expect(readRecords).not.toHaveBeenCalled();
});

it("rejects a mixed-revision multi-batch snapshot before loading any historical records", async () => {
  jest.mocked(listSessions).mockResolvedValue(Array.from({length:130},(_,i)=>({sessionId:`s${i}`,ownerUserId:1,profileId:7})) as any);
  jest.mocked(fetchArchiveStates).mockImplementationOnce(async (_pid,ids)=>({checked_at:"now",history_revision:"1",states:ids.map(id=>({session_id:id,state:Number(id.slice(1))<99?"archived":"active"}))}))
    .mockImplementationOnce(async (_pid,ids)=>({checked_at:"now",history_revision:"2",states:ids.map(id=>({session_id:id,state:"active"}))}));
  await expect(readEligibleEnvironmentHistory(7)).rejects.toThrow("history_changed_during_archive_check");
  expect(readRecords).not.toHaveBeenCalled();
});
it("revalidates all selected IDs after local evidence reads and discards a changed snapshot", async () => {
  jest.mocked(listSessions).mockResolvedValue([{sessionId:"A",ownerUserId:1,profileId:7}] as any);
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({checked_at:"now",history_revision:"1",states:[{session_id:"A",state:"active"}]})
    .mockResolvedValueOnce({checked_at:"now",history_revision:"2",states:[{session_id:"A",state:"archived"}]});
  await expect(readEligibleEnvironmentHistory(7)).rejects.toThrow("history_changed_during_archive_check");
  expect(readRecords).toHaveBeenCalledTimes(1);
  expect(jest.mocked(fetchArchiveStates).mock.calls[1][1]).toEqual(["A"]);
});
