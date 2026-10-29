import { AppState } from 'react-native';
import { reanalyzeObservation, reviewObservation, summarizeHabits } from '../review';
import { appleEnvironment, appleOnDeviceAi } from '../../../modules/apple-on-device-ai';
import { readAppleAiPower } from '../../ai-coach/appleAiPower';
import { acquireAppleAiSlot } from '../../ai-coach/appleAiExecution';
import { archiveEvidence, environmentRecordingActive, saveRecord } from '../store';
import { fetchArchiveStates } from '../../wod/archiveStates';
import { readEligibleEnvironmentSnapshot } from '../eligibleHistory';
import type { EnvironmentRecord, EnvironmentSession } from '../types';
jest.mock('../../wod/archiveStates', () => ({ fetchArchiveStates: jest.fn(), sourceCanInfluenceWorkouts: (source: any, state: any) => source.ownerUserId === 7 && ['active','not_registered'].includes(state?.state) }));
let mockOwner: number | null = 7;
let mockProfile: number | null = 1;
const mockProfileListeners = new Set<() => void>();
const mockAuthListeners = new Set<() => void>();
let mockArchived = false;
let mockRevision = "1";
jest.mock('../../auth/useAuthStore', () => ({ useAuthStore: {
  getState: () => ({ userId:mockOwner,isLoggedIn:mockOwner!==null }),
  subscribe: (listener: () => void) => { mockAuthListeners.add(listener); return () => mockAuthListeners.delete(listener); },
} }));
jest.mock('../../../store/useProfileStore', () => ({ useProfileStore: {
  getState: () => ({ activeProfileId:mockProfile }),
  subscribe: (listener: () => void) => { mockProfileListeners.add(listener); return () => mockProfileListeners.delete(listener); },
} }));
jest.mock('../eligibleHistory', () => ({ ...jest.requireActual('../eligibleHistory'), readEligibleEnvironmentSnapshot: jest.fn() }));
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
const session = {id:'session-bundle',sessionId:'s',profileId:1,ownerUserId:7,context:{...original.context,wodDescription:'CHANGED',appearanceHints:'red top'}} as EnvironmentSession;
beforeEach(() => {
  AppState.currentState = 'active';
  jest.spyOn(AppState,'addEventListener').mockImplementation(()=>({remove:jest.fn()}));
  jest.clearAllMocks(); mockOwner=7; mockProfile=1; mockArchived=false; mockRevision="1";
  jest.mocked(fetchArchiveStates).mockReset().mockImplementation(async (_pid,ids)=>({checked_at:'now',history_revision:mockRevision,states:ids.map(id=>({session_id:id,state:mockArchived?'archived':'active'}))}));
  jest.mocked(readEligibleEnvironmentSnapshot).mockResolvedValue({ownerUserId:7,profileId:1,sourceSessionIds:['s'],historyRevision:'1',records:[original]});
  jest.mocked(environmentRecordingActive).mockReturnValue(false);
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
  for(let i=0;i<100 && !finish;i++) await Promise.resolve();
  expect(finish).toBeDefined();
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

it('does not feed an archived or unverified source into a cross-session summary', async () => {
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({checked_at:'now',history_revision:'1',states:[{session_id:'s',state:'archived'}]});
  await expect(summarizeHabits({...session,ownerUserId:7}, [original])).rejects.toThrow('archived_source');
  jest.mocked(fetchArchiveStates).mockRejectedValueOnce(new Error('offline'));
  await expect(summarizeHabits({...session,ownerUserId:7}, [original])).rejects.toThrow('offline');
  expect(readEligibleEnvironmentSnapshot).not.toHaveBeenCalled();
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
});

it('refuses explicit local reanalysis of an archived session before invoking native AI', async () => {
  jest.mocked(fetchArchiveStates).mockResolvedValueOnce({checked_at:'now',history_revision:'2',states:[{session_id:'s',state:'archived'}]});
  await expect(reanalyzeObservation(session,original)).rejects.toThrow('archived_source');
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
  expect(archiveEvidence).not.toHaveBeenCalled();
});

it('rechecks after power sampling so a newly archived source never reaches native inference', async () => {
  jest.mocked(readAppleAiPower).mockImplementationOnce(async()=>{mockArchived=true;mockRevision="2";return {battery:0.8,lowPower:false,thermal:0};});
  const result=await reanalyzeObservation(session,original);
  expect(result).toMatchObject({outcome:'cancelled',reason:'source_unverified',raw:null});
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
  expect(original.raw).toBe('old answer');
});
it('rechecks owner after evidence preparation and discards a logged-out run', async () => {
  jest.mocked(archiveEvidence).mockImplementationOnce(async()=>{mockOwner=null;return original.evidence;});
  const result=await reanalyzeObservation(session,original);
  expect(result).toMatchObject({outcome:'cancelled',reason:'source_unverified',raw:null});
  expect(appleEnvironment.observeEnvironment).not.toHaveBeenCalled();
});
it('discards generated text if the source is archived while native inference is running', async () => {
  jest.mocked(appleEnvironment.observeEnvironment).mockImplementationOnce(async()=>{mockArchived=true;mockRevision="2";return {feedback:'MUST DISCARD',elapsedMs:50};});
  const result=await reanalyzeObservation(session,original);
  expect(result).toMatchObject({outcome:'cancelled',reason:'source_unverified',raw:null});
  expect(original.raw).toBe('old answer');
  expect(saveRecord).toHaveBeenCalledWith(expect.objectContaining({raw:null,outcome:'cancelled'}));
});
it.each(['reanalyze', 'summarize'] as const)('cancels %s on a profile switch and discards its late answer even after switching back', async operation => {
  let finish!: (answer: any) => void;
  jest.mocked(appleEnvironment.observeEnvironment).mockImplementationOnce(() => new Promise(resolve => { finish=resolve; }));
  const pending = operation === 'reanalyze' ? reanalyzeObservation(session, original) : summarizeHabits(session, [original]);
  for (let i=0; i<100 && !finish; i++) await Promise.resolve();
  expect(finish).toBeDefined();
  mockProfile=2; mockProfileListeners.forEach(listener => listener());
  expect(appleOnDeviceAi.cancel).toHaveBeenCalledWith('new-record');
  mockProfile=1; mockProfileListeners.forEach(listener => listener());
  finish({feedback:'OLD PROFILE ANSWER',elapsedMs:50});
  expect(await pending).toMatchObject({outcome:'cancelled',reason:'source_unverified',raw:null,parsed:null});
  expect(saveRecord).toHaveBeenCalledWith(expect.objectContaining({outcome:'cancelled',raw:null}));
  expect(original.raw).toBe('old answer');
  expect(mockProfileListeners.size).toBe(0); expect(mockAuthListeners.size).toBe(0);
  const release=acquireAppleAiSlot(); expect(release).not.toBeNull(); release?.();
});
it('verifies the full summary source set at publication instead of checking only the target session', async () => {
  jest.mocked(readEligibleEnvironmentSnapshot).mockResolvedValueOnce({ownerUserId:7,profileId:1,sourceSessionIds:['s','PAST-A'],historyRevision:'1',records:[original]});
  jest.mocked(appleEnvironment.observeEnvironment).mockImplementationOnce(async()=>{mockRevision="2";return {feedback:'STALE SUMMARY',elapsedMs:50};});
  const result=await summarizeHabits(session,[original]);
  expect(result).toMatchObject({outcome:'cancelled',reason:'source_unverified',raw:null});
  expect(jest.mocked(fetchArchiveStates).mock.calls.some(([,ids])=>ids.includes('PAST-A')&&ids.includes('s'))).toBe(true);
});
it('uses one archive probe at a time and cancels an in-flight native run when a probe invalidates its source', async () => {
  jest.useFakeTimers();
  let finishNative!: (value:any)=>void;
  let finishProbe!: (value:any)=>void;
  jest.mocked(appleEnvironment.observeEnvironment).mockImplementationOnce(()=>new Promise(resolve=>{finishNative=resolve;}));
  jest.mocked(fetchArchiveStates).mockImplementationOnce(async()=>({checked_at:'now',history_revision:'1',states:[{session_id:'s',state:'active'}]}))
    .mockImplementationOnce(async()=>({checked_at:'now',history_revision:'1',states:[{session_id:'s',state:'active'}]}))
    .mockImplementationOnce(()=>new Promise(resolve=>{finishProbe=resolve;}));
  const pending=reanalyzeObservation(session,original);
  try {
    for(let i=0;i<40;i++) await Promise.resolve();
    expect(appleEnvironment.observeEnvironment).toHaveBeenCalledTimes(1);
    await jest.advanceTimersByTimeAsync(1000);
    expect(fetchArchiveStates).toHaveBeenCalledTimes(3);
    await jest.advanceTimersByTimeAsync(3000);
    expect(fetchArchiveStates).toHaveBeenCalledTimes(3);
    mockArchived=true;mockRevision='2';
    finishProbe({checked_at:'now',history_revision:'2',states:[{session_id:'s',state:'archived'}]});
    for(let i=0;i<20;i++) await Promise.resolve();
    expect(appleOnDeviceAi.cancel).toHaveBeenCalledWith('new-record');
    finishNative({feedback:'LATE ANSWER',elapsedMs:5000});
    expect(await pending).toMatchObject({outcome:'cancelled',raw:null,reason:'source_unverified'});
  } finally { jest.useRealTimers(); }
});

it('releases the shared native slot and timers even if subscription cleanup throws', async () => {
  jest.useFakeTimers();
  try {
    jest.spyOn(AppState,'addEventListener').mockImplementationOnce(()=>({remove:()=>{throw new Error('listener cleanup failed');}}));
    await expect(reanalyzeObservation(session,original)).rejects.toThrow('listener cleanup failed');
    const release=acquireAppleAiSlot();
    expect(release).not.toBeNull(); release?.();
    expect(jest.getTimerCount()).toBe(0);
  } finally { jest.useRealTimers(); }
});
