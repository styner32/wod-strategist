import { describe, expect, it } from '@jest/globals';
import { assetName, recordView, latestRecordReview } from '../onDeviceRecord';

describe('uploaded on-device archive display', () => {
  it('shows original frames for legacy archives and exact selected inputs for new archives', () => {
    const legacy = { schemaVersion: 1, frames: [{ filename: 'apple_ai_a_frame_1.jpg' }], answer: { feedback: 'original response', elapsedMs: 1500 } };
    expect(recordView(legacy)).toMatchObject({ apple: true, response: 'original response', elapsedMs: 1500, frames: legacy.frames });
    expect(recordView({ ...legacy, schemaVersion: 2, preparation: { frames: [{ filename: 'apple_ai_a_input_1.jpg' }] } }).frames).toEqual([{ filename: 'apple_ai_a_input_1.jpg' }]);
  });
  it('preserves invalid, failed and skipped observations instead of implying success', () => {
    const record = { version: 1, source: 'FoundationModels', outcome: 'error', validation: 'invalid', raw: 'caption only', reason: 'timeout', evidence: [{ filename: 'environment_a_0.jpg' }] };
    expect(recordView(record)).toMatchObject({ apple: false, response: 'caption only', reason: 'timeout', record, elapsedMs: null });
    expect(recordView({ outcome: 'skipped', reason: 'low_power' })).toMatchObject({ response: '', reason: 'low_power' });
  });
  it('separates metadata, sound and review records from AI responses', () => {
    expect(recordView({ complete: true, device: 'iPhone' }).session).toBe(true);
    expect(recordView({ source: 'SoundAnalysis', data: { confidence: 0.7 } })).toMatchObject({ source: 'SoundAnalysis', response: '' });
    expect(recordView({ source: 'user', review: { verdict: 'abstract' } }).record.review).toEqual({ verdict: 'abstract' });
  });
  it('tolerates malformed values and only opens session-local evidence filenames', () => {
    for (const value of [null, [], 'invalid']) expect(recordView(value).frames).toEqual([]);
    expect(recordView({ evidence: [null, { filename: 'https://other/private.jpg' }, { filename: '../environment_a.jpg' }] }).frames).toEqual([]);
    expect(assetName('environment_a_0.m4a')).toBe('environment_a_0.m4a');
    expect(assetName('environment_a/secret.jpg')).toBeNull();
  });
});

it('distinguishes natural language from invalid legacy JSON and never treats a rule pass as accuracy', () => {
  expect(recordView({responseFormat:'text',questionId:'camera.occlusion',raw:'Sample 1: a post hides the foot.'})).toMatchObject({responseFormat:'text',questionId:'camera.occlusion',quality:{status:'unchecked'}});
  expect(recordView({validation:'valid',raw:'visible evidence'}).quality).toMatchObject({status:'flagged',flags:['example_copy']});
});
it('uses the latest review and validates the parent filename for direct comparison', () => {
  const id='01ARZ3NDEKTSV4RRFFQ69G5FAV';
  expect(recordView({parentId:id}).parentFilename).toBe(`environment_${id}.json`);
  expect(recordView({parentId:'../private'}).parentFilename).toBeNull();
  const reviews=[{id:'a',outcome:'success',startedAt:1,review:{targetId:id,verdict:'correct'}},{id:'b',outcome:'success',startedAt:2,review:{targetId:id,verdict:'incorrect',correctedObservation:'not me'}}];
  expect(latestRecordReview(reviews.reverse(),id)).toMatchObject({verdict:'incorrect',correctedObservation:'not me'});
  expect(latestRecordReview([],id)).toBeNull();
});
