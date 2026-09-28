import { AppState } from 'react-native';
import { reanalyzeObservation, reviewObservation } from '../review';
import { appleEnvironment, appleOnDeviceAi } from '../../../modules/apple-on-device-ai';
import { readAppleAiPower } from '../../ai-coach/appleAiPower';
import { acquireAppleAiSlot } from '../../ai-coach/appleAiExecution';
import { archiveEvidence, environmentRecordingActive, saveRecord } from '../store';
import type { EnvironmentRecord, EnvironmentSession } from '../types';
jest.mock('../../../modules/apple-on-device-ai', () => ({
  appleOnDeviceAi:{cancel:jest.fn(async () => {})},
  appleEnvironment:{environmentSystemInstructions:'use evidence',cancelEnvironmentWork:jest.fn(async () => {}),
    observeEnvironment:jest.fn(async () => ({feedback:'Sample 1: a post hides the left foot.',elapsedMs:50})),environmentSound:jest.fn(),environmentFrames:jest.fn()},
}));
jest.mock('../recorder', () => ({newEnvironmentRecord:(s:any,kind:string,source:string) => ({version:1,id:'new-record',bundleId:s.id,sessionId:s.sessionId,profileId:s.profileId,
  context:s.context,kind,source,promptVersion:2,raw:null,parsed:null,validation:'not_applicable',evidence:[],startedAt:Date.now(),preparationMs:0,inferenceMs:0})}));
jest.mock('../store', () => ({saveRecord:jest.fn(async () => {}),environmentRecordingActive:jest.fn(() => false),flushEnvironmentUploads:jest.fn(async () => {}),
  evidencePath:(bundle:string,name:string) => `${bundle}/${name}`,
  archiveEvidence:jest.fn(async (_bundle:string,id:string,inputs:any[]) => inputs.map((input,i) => ({...input,filename:`${id}_${i}.jpg`,mime:'image/jpeg'})))}));
jest.mock('../../ai-coach/appleAiPower', () => ({readAppleAiPower:jest.fn(async () => ({battery:0.8,lowPower:false,thermal:0}))}));
const original: EnvironmentRecord = {version:1,id:'original',bundleId:'original-bundle',sessionId:'s',profileId:1,kind:'camera',
  context:{wodDescription:'original WOD',movements:'squat',appearanceHints:'blue top',language:'ko'},questionId:'camera.lighting',
  raw:'old answer',prompt:'old prompt',promptVersion:1,startedAt:1,
  evidence:[0,5000,10000].map((ms,i) => ({filename:`old-${i}.jpg`,mime:'image/jpeg',bytes:10,mediaOffsetMs:ms,width:640,height:480})),
  chunk:{captureStart:100,captureEnd:10100,durationMs:10000,index:1,sourceFile:'chunk.mp4',clock:'chunk_media_ms_and_capture_epoch_ms'},
} as EnvironmentRecord;
const session = {id:'session-bundle',sessionId:'s',profileId:1,context:{...original.context,wodDescription:'CHANGED',appearanceHints:'red top'}} as EnvironmentSession;
beforeEach(() => {
  AppState.currentState = 'active';
  jest.clearAllMocks(); jest.mocked(environmentRecordingActive).mockReturnValue(false);
  jest.mocked(readAppleAiPower).mockResolvedValue({battery:0.8,lowPower:false,thermal:0});
});
it('reuses exact evidence order, media times, original context and question without touching the original', async () => {
  const before=JSON.stringify(original);
  const r=await reanalyzeObservation(session,original);
  expect(r).toMatchObject({parentId:'original',context:original.context,questionId:'camera.lighting',responseFormat:'text',validation:'not_applicable',parsed:null,outcome:'success'});
  expect(archiveEvidence).toHaveBeenCalledWith('session-bundle','new-record',original.evidence.map(e => ({...e,path:`original-bundle/${e.filename}`})),'image/jpeg');
  expect(appleEnvironment.observeEnvironment).toHaveBeenCalledTimes(1);
  const [input,prompt]=jest.mocked(appleEnvironment.observeEnvironment).mock.calls[0];
  expect(input).toMatchObject({...original.context,frames:[0,5000,10000].map((capturedAt,i) => ({path:`session-bundle/new-record_${i}.jpg`,capturedAt}))});
  expect(prompt).toContain('Which visible areas are too dark'); expect(prompt).not.toContain('CHANGED');
  expect(appleEnvironment.environmentFrames).not.toHaveBeenCalled();
  expect(JSON.stringify(original)).toBe(before);
  expect(saveRecord).toHaveBeenCalledWith(r);
});
it('only starts on an explicit call and refuses recording or an occupied shared slot', async () => {
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
  jest.mocked(environmentRecordingActive).mockReturnValue(true);
  await expect(reanalyzeObservation(session,original)).rejects.toThrow('recording_active');
  jest.mocked(environmentRecordingActive).mockReturnValue(false);
  const release=acquireAppleAiSlot()!;
  try { await expect(reanalyzeObservation(session,original)).rejects.toThrow('busy'); } finally { release(); }
  expect(archiveEvidence).not.toHaveBeenCalled();
});
it.each([{battery:0.1,lowPower:false,thermal:0},{battery:0.8,lowPower:true,thermal:0},{battery:0.8,lowPower:false,thermal:2}])('preserves a power rejection without inference (%j)',async power => {
  jest.mocked(readAppleAiPower).mockResolvedValue(power);
  expect(await reanalyzeObservation(session,original)).toMatchObject({outcome:'error',reason:'power'});
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
  expect(archiveEvidence).not.toHaveBeenCalled();
});
it('retains error response text and releases the slot after failed persistence', async () => {
  jest.mocked(appleEnvironment.observeEnvironment).mockResolvedValueOnce({feedback:'partial text',elapsedMs:10,error:'timeout'});
  expect(await reanalyzeObservation(session,original)).toMatchObject({outcome:'error',reason:'timeout',raw:'partial text'});
  jest.mocked(saveRecord).mockRejectedValueOnce(new Error('disk full'));
  await expect(reanalyzeObservation(session,original)).rejects.toThrow('disk full');
  const release=acquireAppleAiSlot(); expect(release).not.toBeNull(); release?.();
});
it('cancels on a memory warning while preserving the completed native answer as interrupted evidence', async () => {
  let finish!: (result:any) => void;
  jest.mocked(appleEnvironment.observeEnvironment).mockImplementationOnce(() => new Promise(resolve => {finish=resolve;}));
  const spy=jest.spyOn(AppState,'addEventListener');
  const pending=reanalyzeObservation(session,original);
  for(let i=0;i<20;i++) await Promise.resolve();
  const listener=spy.mock.calls.find(([event]) => event==='memoryWarning')![1];
  listener('memoryWarning' as any);
  finish({feedback:'late answer',elapsedMs:50});
  expect(await pending).toMatchObject({outcome:'cancelled',reason:'cancelled',raw:'late answer'});
  expect(appleOnDeviceAi.cancel).toHaveBeenCalledWith('new-record'); spy.mockRestore();
});
it('saves reviewer identity, selected behaviors and corrected text separately from the original', async () => {
  await reviewObservation(session,original,'correct',' note ',{targetConfirmed:true,confirmedBehaviors:['foot_reset','foot_reset'],correctedObservation:' corrected '});
  expect(saveRecord).toHaveBeenCalledWith(expect.objectContaining({context:original.context,review:{targetId:'original',verdict:'correct',note:'note',targetConfirmed:true,confirmedBehaviors:['foot_reset'],correctedObservation:'corrected'}}));
  expect(original.raw).toBe('old answer'); expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
});

it('does not start explicit inference when already backgrounded', async () => {
  AppState.currentState = 'background';
  await expect(reanalyzeObservation(session,original)).rejects.toThrow('background');
  expect(archiveEvidence).not.toHaveBeenCalled();
});
