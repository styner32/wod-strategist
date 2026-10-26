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
 it('displays v3 hybrid activity intensity and missing resting HR guidance without fabricating scores', () => {
  const view = render(
   <HeartRateSummaryCard
    summary={{
     ...summary,
     calculation_version: 3,
     hybrid: {
      heart_intensity: 0.75,
      motion_intensity: 0.80,
      hybrid_intensity: 76.25,
      hybrid_load_au: 12.8,
      valid_hybrid_seconds: 1200,
      data_coverage: 0.98,
      quality_flags: ['peak_hr_exceeded_max'],
      calibration_version: 'personal_chest_v1',
      algorithm_version: 'hybrid-0.75-0.25-gamma2',
     },
    }}
   />,
  );
  expect(view.getByText(/76\/100/)).toBeTruthy();
  fireEvent.press(view.getByLabelText(ko.heartRate.details));
  expect(view.getByText('75/100')).toBeTruthy();
  expect(view.getByText('80/100')).toBeTruthy();
  expect(view.getByText('12.8 AU')).toBeTruthy();
  expect(view.getByText('98%')).toBeTruthy();
  expect(view.getByText(ko.heartRate.calibrationPersonal)).toBeTruthy();
  expect(view.getByText(ko.heartRate.peak_hr_exceeded_max)).toBeTruthy();

  view.rerender(
   <HeartRateSummaryCard
    summary={{
     ...summary,
     calculation_version: 3,
     hybrid: {
      heart_intensity: null,
      motion_intensity: 0.80,
      hybrid_intensity: null,
      hybrid_load_au: null,
      valid_hybrid_seconds: 0,
      data_coverage: 0,
      quality_flags: ['missing_resting_hr', 'bootstrap_motion_calibration'],
      calibration_version: 'bootstrap_chest_v1',
      algorithm_version: 'hybrid-0.75-0.25-gamma2',
     },
    }}
   />,
  );
  expect(view.getByText(ko.heartRate.missing_resting_hr)).toBeTruthy();
  expect(view.getByText(ko.heartRate.calibrationBootstrap)).toBeTruthy();
 });
});
