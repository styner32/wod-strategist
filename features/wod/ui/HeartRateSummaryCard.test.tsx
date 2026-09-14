import React from 'react';
import { fireEvent, render } from '@testing-library/react-native';
import { HeartRateSummaryCard } from './HeartRateSummaryCard';
import type { HeartRateSummary } from '@/shared/heartRateSummary';
import ko from '@/features/i18n/locales/ko.json';
jest.mock('@/features/i18n', () => ({
 useLocale: () => 'ko',
 t: (key: string) => (require('@/features/i18n/locales/ko.json').heartRate as Record<string,string>)[key.split('.')[1]] ?? key,
}));
const summary: HeartRateSummary = { status: 'completed', processing_state: 'COMPLETED', quality_status: 'adequate', calculation_version: 2, avg_bpm: 130, peak_bpm: 155, min_bpm: 120, coverage: .8, valid_seconds: 80, excluded_seconds: 10, unknown_seconds: 10, applied: false, application_reason: 'video_insufficient' };
describe('Heart rate summary independent of video', () => {
 it('shows measured statistics with no fatigue or video output', () => {
  const view=render(<HeartRateSummaryCard summary={summary}/>);
  expect(view.getByText(/130 bpm/)).toBeTruthy();
  fireEvent.press(view.getByLabelText(ko.heartRate.details));
  expect(view.getByText('120 bpm')).toBeTruthy();expect(view.getByText('80%')).toBeTruthy();
  expect(view.getByText(ko.heartRate.video_insufficient)).toBeTruthy();expect(view.getByText(ko.heartRate.contactUnknown)).toBeTruthy();
 });
 it('distinguishes zero adjustment, no sensor and previous criteria', () => {
  const view=render(<HeartRateSummaryCard summary={{...summary, calculation_version:1, application_reason:'no_bonus'}}/>);
  expect(view.getByText(ko.heartRate.legacy)).toBeTruthy();
  fireEvent.press(view.getByLabelText(ko.heartRate.details));expect(view.getByText(ko.heartRate.no_bonus)).toBeTruthy();
  view.rerender(<HeartRateSummaryCard summary={{...summary,status:'none',avg_bpm:undefined,peak_bpm:undefined}}/>);
  expect(view.getByText(new RegExp(ko.heartRate.none))).toBeTruthy();expect(view.queryByText(/130 bpm/)).toBeNull();
 });
 it('keeps missing data as absent values',()=>{
  const view=render(<HeartRateSummaryCard summary={{...summary,status:'limited',avg_bpm:undefined,peak_bpm:undefined,min_bpm:undefined,application_reason:'quality_insufficient'}}/>);
  fireEvent.press(view.getByLabelText(ko.heartRate.details));
  expect(view.queryByText('0 bpm')).toBeNull();expect(view.getAllByText('—')).toHaveLength(3);
 });
});
