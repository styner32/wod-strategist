jest.mock('expo-constants', () => ({ __esModule:true, default: { expoConfig:{version:'test'} } }));
import { AppState } from 'react-native';
import { EnvironmentRecorder } from '../recorder';
import { appleEnvironment } from '../../../modules/apple-on-device-ai';
import { readAppleAiPower } from '../../ai-coach/appleAiPower';
import { acquireAppleAiSlot } from '../../ai-coach/appleAiExecution';
import { saveRecord, saveSession } from '../store';
jest.mock('../../../modules/apple-on-device-ai', () => ({
  environmentNativeAvailable: true,
  appleOnDeviceAi: { getAvailability: jest.fn(async () => 'available'), cancel: jest.fn(async () => {}) },
  appleEnvironment: { cancelEnvironmentWork: jest.fn(async () => {}), startEnvironmentMotion: jest.fn(async () => {}), stopEnvironmentMotion: jest.fn(async () => {}),
    environmentSample: jest.fn(async () => ({ device:'test' })), environmentWeather: jest.fn(async () => ({ error:'denied' })),
    cancelEnvironmentWeather: jest.fn(async () => {}), environmentFrames: jest.fn(async () => ({ frames:[{ path:'/frame.jpg',mediaOffsetMs:0 }] })),
    environmentAudio: jest.fn(async () => ({ error: 'no_audio_track' })), environmentSound: jest.fn(),
    observeEnvironment: jest.fn(async () => ({ feedback:'caption only', elapsedMs:5 })) },
}));
jest.mock('../store', () => ({ saveRecord: jest.fn(async () => {}), saveSession: jest.fn(async () => {}),
  holdEnvironmentUploads: () => jest.fn(), flushEnvironmentUploads: jest.fn(async () => {}),
  archiveEvidence: jest.fn(async () => [{ filename:'e.jpg',mime:'image/jpeg',bytes:20 }]) }));
jest.mock('../../ai-coach/appleAiPower', () => ({ readAppleAiPower: jest.fn(async () => ({ battery:0.8,lowPower:false,thermal:0 })) }));
jest.mock('../../auth/useAuthStore', () => ({ useAuthStore: { getState: () => ({ userId: 1 }) } }));
jest.mock('../../health/polar/ndjsonWriter', () => ({ NdjsonWriter: class { write() {} startAutoFlush() {} close() { return { complete:true }; } } }));
jest.mock('expo-file-system/legacy', () => ({ deleteAsync: jest.fn(async () => {}), getFreeDiskStorageAsync: jest.fn(async () => 123456) }));
const context = { wodDescription:'WOD',movements:'Squat',appearanceHints:'black shirt',language:'ko' };
let recorder: EnvironmentRecorder;
const flush = async () => { for (let i=0;i<25;i++) await Promise.resolve(); };
const advance = async (ms:number) => { await jest.advanceTimersByTimeAsync(ms); await flush(); };
const offer = () => recorder.offerChunk({ path:'/chunk.mp4',captureStart:Date.now()-10000,captureEnd:Date.now(),durationMs:10000,index:1 });
beforeEach(async () => {
  jest.useFakeTimers(); jest.setSystemTime(1_000_000); jest.clearAllMocks();
  jest.mocked(readAppleAiPower).mockResolvedValue({ battery:0.8,lowPower:false,thermal:0 });
  recorder = new EnvironmentRecorder({ sessionId:'s',profileId:1,startedAt:Date.now() },context,{observationIntervalSeconds:30},jest.fn()); await flush();
});
afterEach(async () => { await recorder.stop(); jest.useRealTimers(); });
it('waits 30 seconds, skips a busy posture slot without extracting frames, and advances the category', async () => {
  offer(); const release = acquireAppleAiSlot()!;
  await advance(30_000);
  expect(appleEnvironment.environmentFrames).not.toHaveBeenCalled();
  expect(jest.mocked(saveRecord).mock.calls.some(([r]) => r.kind==='camera' && r.reason==='busy')).toBe(true);
  release(); offer(); await advance(30_000);
  expect(appleEnvironment.observeEnvironment).toHaveBeenCalledTimes(1);
  expect(jest.mocked(saveRecord).mock.calls.find(([r]) => r.kind==='behavior')?.[0]).toMatchObject({ raw:'caption only',validation:'not_applicable',responseFormat:'text',questionId:'behavior.posture',quality:{status:'unchecked'},outcome:'success' });
});
it('does not catch up after pause; sound without a track is not classified as quiet', async () => {
  offer(); await advance(30_000); await recorder.suspend(); await advance(120_000);
  expect(appleEnvironment.observeEnvironment).toHaveBeenCalledTimes(1);
  recorder.resume(); offer(); await advance(30_000); offer(); await advance(30_000);
  expect(jest.mocked(saveRecord).mock.calls.find(([r]) => r.kind==='sound')?.[0]).toMatchObject({ reason:'no_audio_track',outcome:'error' });
  expect(appleEnvironment.environmentSound).not.toHaveBeenCalled();
});
it('drains pending input extraction before stop resolves, then avoids inference', async () => {
  let finish!: (value: any) => void;
  jest.mocked(appleEnvironment.environmentFrames).mockImplementationOnce(() => new Promise(resolve => { finish=resolve; }));
  offer(); await advance(30_000);
  let stopped = false; const stopping=recorder.stop().then(() => { stopped=true; }); await flush();
  expect(stopped).toBe(false);
  finish({ frames:[{ path:'/frame.jpg',mediaOffsetMs:0 }] }); await stopping;
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
  expect(jest.mocked(saveRecord).mock.calls.some(([r]) => r.kind==='camera' && r.outcome==='cancelled')).toBe(true);
});
it('does not perform visual analysis under low power', async () => {
  jest.mocked(readAppleAiPower).mockResolvedValue({ battery:0.1,lowPower:false,thermal:0 });
  offer(); await advance(30_000);
  expect(appleEnvironment.environmentFrames).not.toHaveBeenCalled();
  expect(jest.mocked(saveRecord).mock.calls.some(([r]) => r.reason==='power')).toBe(true);
});
it('latches memory warnings for the rest of the session', async () => {
  // Capture the native AppState subscription used by the recorder.
  await recorder.stop();
  const spy = jest.spyOn(AppState,'addEventListener');
  recorder = new EnvironmentRecorder({ sessionId:'s2',profileId:1,startedAt:Date.now() },context,{observationIntervalSeconds:30},jest.fn()); await flush();
  const listener = spy.mock.calls.filter(([event]) => event==='memoryWarning').slice(-1)[0][1];
  listener('memoryWarning' as any); await flush(); recorder.resume(); offer(); await advance(60_000);
  expect(appleEnvironment.environmentFrames).not.toHaveBeenCalled();
  spy.mockRestore();
});
it('keeps measurements without image or sound inference when in measurements-only mode', async () => {
  await recorder.stop();
  recorder = new EnvironmentRecorder({ sessionId:'measure',profileId:1,startedAt:Date.now() },context,{environmentAnalysis:false,observationIntervalSeconds:30},jest.fn());
  await flush(); offer(); await advance(30_000);
  expect(appleEnvironment.environmentSample).toHaveBeenCalled();
  expect(appleEnvironment.environmentFrames).not.toHaveBeenCalled();
  expect(jest.mocked(saveRecord).mock.calls.some(([r]) => r.reason==='measurements_only')).toBe(true);
});

it('defaults to 60 seconds and cycles one question per visual turn without extra calls', async () => {
  await recorder.stop();
  recorder = new EnvironmentRecorder({ sessionId:'default',profileId:1,startedAt:Date.now() },context,{},jest.fn());
  await flush(); offer(); await advance(30_000);
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
  offer(); await advance(30_000);
  expect(appleEnvironment.observeEnvironment).toHaveBeenCalledTimes(1);
  for (let i=0;i<4;i++) { offer(); await advance(60_000); }
  const questions = jest.mocked(saveRecord).mock.calls.map(([r]) => r.questionId).filter(Boolean);
  expect(questions).toEqual(['camera.occlusion','behavior.posture','space.relative_position','camera.framing']);
  expect(appleEnvironment.observeEnvironment).toHaveBeenCalledTimes(4);
});
it('preserves a saved 120 second interval', async () => {
  await recorder.stop();
  recorder = new EnvironmentRecorder({ sessionId:'slow',profileId:1,startedAt:Date.now() },context,{observationIntervalSeconds:120},jest.fn());
  await flush(); offer(); await advance(119_000);
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
  offer(); await advance(1000);
  expect(appleEnvironment.observeEnvironment).toHaveBeenCalledTimes(1);
});
it('never extracts visual evidence under serious thermal pressure', async () => {
  jest.mocked(readAppleAiPower).mockResolvedValue({battery:0.8,lowPower:false,thermal:2});
  offer(); await advance(30_000);
  expect(appleEnvironment.environmentFrames).not.toHaveBeenCalled();
  expect(jest.mocked(saveRecord).mock.calls.some(([r]) => r.reason === 'thermal')).toBe(true);
});
