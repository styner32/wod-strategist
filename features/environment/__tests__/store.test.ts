import { apiClient, getUploadUrl, uploadSessionAssetToGcs } from '../../wod/api';
import { deleteEnvironmentSession, archiveEvidence, bundlePath, flushEnvironmentUploads, holdEnvironmentUploads, listSessions, readRecords, saveRecord, saveSession } from '../store';
import type { EnvironmentSession, EnvironmentRecord } from '../types';
let mockUserId = 7;
let mockRecording = false;
jest.mock('../../auth/useAuthStore', () => ({ useAuthStore: { getState: () => ({ userId:mockUserId,isLoggedIn:true,isRecordingActive:mockRecording }) } }));
const mockFiles = new Map<string, string>();
let mockDocumentDirectory = 'file:///docs/';
let mockSequence = 0;
jest.mock('ulid', () => ({ ulid: () => `01ARZ3NDEKTSV4RRFFQ69G5F${String(++mockSequence).padStart(2, '0')}` }));
jest.mock('../../wod/api', () => ({ apiClient: jest.fn(), getUploadUrl: jest.fn(), uploadSessionAssetToGcs: jest.fn() }));
jest.mock('expo-file-system/legacy', () => ({
  get documentDirectory() { return mockDocumentDirectory; },
  makeDirectoryAsync: jest.fn(async () => {}),
  getInfoAsync: jest.fn(async (path: string) => ({ exists: mockFiles.has(path) || [...mockFiles.keys()].some(key => key.startsWith(path)) })),
  readDirectoryAsync: jest.fn(async (path: string) => [...new Set([...mockFiles.keys()].filter(key => key.startsWith(path)).map(key => key.slice(path.length).split('/')[0]))]),
  writeAsStringAsync: jest.fn(async (path: string, value: string) => { mockFiles.set(path, value); }),
  readAsStringAsync: jest.fn(async (path: string) => {
    if (!mockFiles.has(path)) throw new Error('Missing file');
    return mockFiles.get(path);
  }),
  copyAsync: jest.fn(async ({ from, to }: { from: string; to: string }) => {
    if (!mockFiles.has(from)) throw new Error('Missing image');
    mockFiles.set(to, mockFiles.get(from)!);
  }),
  moveAsync: jest.fn(async ({ from, to }: { from: string; to: string }) => {
    if (!mockFiles.has(from)) throw new Error('Missing file');
    mockFiles.set(to, mockFiles.get(from)!); mockFiles.delete(from);
  }),
  deleteAsync: jest.fn(async (path: string) => {
    for (const key of mockFiles.keys()) if (key === path || key.startsWith(path + (path.endsWith('/') ? '' : '/'))) mockFiles.delete(key);
  }),
}));


const session: EnvironmentSession = { version:1,id:'01ARZ3NDEKTSV4RRFFQ69G5FAV',profileId:3,ownerUserId:7,sessionId:'session',startedAt:100,endedAt:200,complete:true,context:{wodDescription:'WOD',movements:'Squat',appearanceHints:'black',language:'ko'},settings:{},appVersion:'1',os:'iOS',device:'test' };
const record = (): EnvironmentRecord => ({ version:1,id:'01ARZ3NDEKTSV4RRFFQ69G5FAA',bundleId:session.id,sessionId:session.sessionId,profileId:3,kind:'behavior',source:'FoundationModels',outcome:'success',evidence:[],raw:'original',startedAt:120, model:'SystemLanguageModel.default',modelVersion:null,tokens:null,executionUnit:null,promptVersion:1,prompt:null,context:session.context,scheduledAt:120,completedAt:140,reason:null,preparationMs:1,inferenceMs:19,parsed:null,validation:'invalid',powerBefore:null,powerAfter:null });
beforeEach(() => {
  jest.clearAllMocks(); mockFiles.clear();mockUserId=7;mockRecording=false;mockSequence=0;
  jest.mocked(getUploadUrl).mockImplementation(async (_s,f) => ({upload_url:'https://upload/'+f,gcs_uri:'gs://x'}));
  jest.mocked(uploadSessionAssetToGcs).mockResolvedValue(undefined);
});
it('defers uploads during recording, publishes evidence before JSON, and retains local originals', async () => {
  const release = holdEnvironmentUploads();
  await saveSession(session); mockFiles.set('file:///tmp/image.jpg','jpeg');
  const r = record();r.evidence=await archiveEvidence(session.id,r.id,[{path:'/tmp/image.jpg',mediaOffsetMs:100}],'image/jpeg'); await saveRecord(r);
  await flushEnvironmentUploads(); expect(getUploadUrl).not.toHaveBeenCalled();
  release();await flushEnvironmentUploads();
  expect(jest.mocked(uploadSessionAssetToGcs).mock.calls.map(c => c[2])).toEqual(['image/jpeg','application/json','application/json']);
  expect(await readRecords(session)).toEqual([r]);
  expect(mockFiles.get(bundlePath(session.id)+r.evidence[0].filename)).toBe('jpeg');
  jest.mocked(uploadSessionAssetToGcs).mockClear();await flushEnvironmentUploads();expect(uploadSessionAssetToGcs).not.toHaveBeenCalled();
});
it('retries failures without changing object identities or dropping responses', async () => {
  await saveSession(session);await saveRecord(record());
  jest.mocked(uploadSessionAssetToGcs).mockRejectedValueOnce(new Error('offline'));
  const spy=jest.spyOn(console,'warn').mockImplementation(() => {});
  await flushEnvironmentUploads();await flushEnvironmentUploads();
  expect(jest.mocked(getUploadUrl).mock.calls[0]).toEqual(jest.mocked(getUploadUrl).mock.calls[1]);
  expect((await readRecords(session))[0].raw).toBe('original');spy.mockRestore();
});
it('recovers interrupted sessions as incomplete, never as successfully stopped', async () => {
  await saveSession({...session,endedAt:null,complete:false});await saveRecord(record());await flushEnvironmentUploads();
  expect((await listSessions(3))[0]).toMatchObject({complete:false,endedAt:expect.any(Number)});
});
it('isolates account data and suppresses uploads during any camera recording', async () => {
  await saveSession(session);await saveRecord(record());mockUserId=8;
  expect(await listSessions(3)).toEqual([]);await flushEnvironmentUploads();expect(getUploadUrl).not.toHaveBeenCalled();
  mockUserId=7;mockRecording=true;await flushEnvironmentUploads();expect(getUploadUrl).not.toHaveBeenCalled();
});

it('keeps a durable deletion request after server failure and never uploads those files again', async () => {
  await saveSession(session);await saveRecord(record());
  jest.mocked(apiClient).mockRejectedValueOnce(new Error('offline'));
  const spy=jest.spyOn(console,'warn').mockImplementation(() => {});
  await expect(deleteEnvironmentSession(3,'session')).rejects.toThrow('deletion_pending');
  expect((await listSessions(3))[0].deleting).toBe(true);
  expect(getUploadUrl).not.toHaveBeenCalled();
  jest.mocked(apiClient).mockResolvedValueOnce(undefined);await flushEnvironmentUploads();
  expect(await listSessions(3)).toEqual([]);expect(getUploadUrl).not.toHaveBeenCalled();spy.mockRestore();
});
