import React from 'react';
import { fireEvent, render, waitFor } from '@testing-library/react-native';
import { EnvironmentHistoryCard } from '../EnvironmentCard';
import { listSessions, readRecords } from '../store';
import { reanalyzeObservation, reviewObservation } from '../review';
jest.mock('../store', () => ({ listSessions: jest.fn(async () => []), readRecords: jest.fn(async () => []), evidencePath:jest.fn(), deleteEnvironmentSession:jest.fn() }));
jest.mock('../review', () => ({ reanalyzeObservation:jest.fn(),reviewObservation:jest.fn(),summarizeHabits:jest.fn() }));
jest.mock('../../i18n', () => ({ t:(key:string) => key,useLocale:() => 'ko' }));
jest.mock('../../../store/useProfileStore', () => ({ useProfileId:() => 7 }));
jest.mock('expo-video', () => ({ VideoView:() => null,useVideoPlayer:jest.fn() }));
beforeEach(() => jest.clearAllMocks());
it('does not read historical files, mount evidence or invoke models merely by showing a history row', () => {
  const view=render(<EnvironmentHistoryCard sessionId="session" profileId={7} />);
  expect(listSessions).not.toHaveBeenCalled();expect(readRecords).not.toHaveBeenCalled();
  expect(reanalyzeObservation).not.toHaveBeenCalled();
  expect(view.queryByText('environment.raw')).toBeNull();
});
it('loads local data only on demand and labels the lack of evidence', async () => {
  const view=render(<EnvironmentHistoryCard sessionId="session" profileId={7} />);
  fireEvent.press(view.getByText('environment.history'));
  await waitFor(() => expect(view.getByText('environment.noData')).toBeTruthy());
  expect(listSessions).toHaveBeenCalledWith(7);expect(reanalyzeObservation).not.toHaveBeenCalled();
});

it('saves explicit target and behavior confirmation without modifying the AI response', async () => {
  const session = {id:'bundle',sessionId:'session',profileId:7};
  const r = {id:'record',sessionId:'session',kind:'behavior',source:'FoundationModels',outcome:'success',context:{appearanceHints:''},raw:'Sample 1: hand on bar.',evidence:[],startedAt:1,completedAt:2,promptVersion:2,responseFormat:'text',validation:'not_applicable'};
  jest.mocked(listSessions).mockResolvedValue([session as any]);
  jest.mocked(readRecords).mockResolvedValue([r as any]);
  const view=render(<EnvironmentHistoryCard sessionId="session" profileId={7} />);
  fireEvent.press(view.getByText('environment.history'));
  await waitFor(() => expect(view.getByText(/environment.kind.behavior/)).toBeTruthy());
  fireEvent.press(view.getByText(/environment.kind.behavior/));
  expect(view.getByText('environment.targetMissing')).toBeTruthy();
  fireEvent.press(view.getByText(/○ environment.confirmTarget/));
  fireEvent.press(view.getByText(/○ environment.behavior.grip_reset/));
  fireEvent.changeText(view.getByPlaceholderText('environment.correctedObservation'),'I adjusted my right hand in Sample 2.');
  fireEvent.press(view.getByText('environment.verdict.correct'));
  await waitFor(() => expect(reviewObservation).toHaveBeenCalledWith(session,r,'correct','',{targetConfirmed:true,confirmedBehaviors:['grip_reset'],correctedObservation:'I adjusted my right hand in Sample 2.'}));
  expect(r.raw).toBe('Sample 1: hand on bar.');
  expect(reanalyzeObservation).not.toHaveBeenCalled();
});
it('shows both original and reanalysis answers without starting another model request', async () => {
  const original = {id:'original',kind:'camera',source:'FoundationModels',outcome:'success',context:{appearanceHints:'black top'},raw:'Original answer',prompt:'Original question',evidence:[],startedAt:1,promptVersion:1};
  const child = {...original,id:'child',parentId:'original',startedAt:2,raw:'New answer',prompt:'New question',promptVersion:2,responseFormat:'text'};
  jest.mocked(listSessions).mockResolvedValue([{id:'bundle',sessionId:'session',profileId:7} as any]);
  jest.mocked(readRecords).mockResolvedValue([original as any,child as any]);
  const view=render(<EnvironmentHistoryCard sessionId="session" profileId={7} />);
  fireEvent.press(view.getByText('environment.history'));
  await waitFor(() => expect(view.getAllByText(/environment.kind.camera/)).toHaveLength(2));
  fireEvent.press(view.getAllByText(/environment.kind.camera/)[0]);
  expect(view.getByText('Original question')).toBeTruthy();
  expect(view.getByText('New question')).toBeTruthy();
  expect(view.getAllByText('Original answer').length).toBeGreaterThan(0);
  expect(view.getAllByText('New answer').length).toBeGreaterThan(0);
  expect(reanalyzeObservation).not.toHaveBeenCalled();
});
