import { aggregateHabits, observationPrompt, observationQuestion, parseObservation, checkObservation, prepareObservation, finishObservation } from '../observation';
import type { EnvironmentRecord, ParsedObservation } from '../types';
const context = { appearanceHints: 'black shirt', wodDescription: 'AMRAP', movements: 'Pull-up', language: 'ko' };
const valid: ParsedObservation = { target: 'identified', facts: ['Sample 1: the right hand is on the bar.'], interpretation: '', limitations: ['sample gaps'], behaviors: ['grip_reset'] };
const record = (sessionId: string, id = sessionId): EnvironmentRecord => ({ id, sessionId, startedAt: 1, context, kind: 'behavior', outcome: 'success', raw: JSON.stringify(valid), parsed: valid, evidence:[{filename:'e.jpg'}] } as EnvironmentRecord);
const reviewed = (r: EnvironmentRecord, at = 2): EnvironmentRecord => ({...r,id:`review-${r.id}-${at}`,kind:'review',startedAt:at,
  review:{targetId:r.id, verdict:'correct',note:'',targetConfirmed:true,confirmedBehaviors:['grip_reset']}});
it('still reads legacy JSON without attributing uncertain or camera behavior', () => {
  expect(parseObservation(JSON.stringify(valid), 'behavior')?.behaviors).toEqual(['grip_reset']);
  expect(parseObservation(JSON.stringify({ ...valid, target: 'uncertain' }), 'behavior')?.behaviors).toEqual([]);
  expect(parseObservation(JSON.stringify(valid), 'camera')?.behaviors).toEqual([]);
  expect(parseObservation('Caption', 'behavior')).toBeNull();
  expect(parseObservation(JSON.stringify({...valid,behaviors:['impolite']}), 'behavior')).toBeNull();
});
it('keeps raw text separate from JSON validation and flags copies without claiming correctness', () => {
  const r = record('s'); r.context = {...context,appearanceHints:''};
  prepareObservation(r); r.raw = 'Sample 1: shoes are hidden by a post.'; finishObservation(r);
  expect(r).toMatchObject({promptVersion:2,questionId:'behavior.posture',targetContext:'missing',responseFormat:'text',parsed:null,validation:'not_applicable',quality:{status:'unchecked'}});
  expect(r.raw).toBe('Sample 1: shoes are hidden by a post.');
  expect(checkObservation(' \n')).toMatchObject({status:'flagged',flags:['empty_response']});
  for (const text of ['visible evidence','Tentative meaning or empty','sampling or occlusion limit']) expect(checkObservation(text).flags).toContain('example_copy');
});
it('asks one specific question, passes exact context and does not supply an example answer', () => {
  const prompt = observationPrompt('space', context);
  expect(prompt).toContain(JSON.stringify(context));
  expect(prompt).toContain('If missing or ambiguous');
  expect(prompt).toContain('Never treat an unseen action as not performed');
  expect(prompt).not.toContain('visible evidence');
  expect(prompt).not.toContain('"facts"');
  expect(observationQuestion('camera',0).id).toBe('camera.occlusion');
  expect(observationQuestion('camera',1).id).toBe('camera.framing');
  expect(observationQuestion('camera',2).id).toBe('camera.lighting');
  expect(observationQuestion('camera',3).id).toBe('camera.occlusion');
  expect(observationQuestion('behavior',0,'behavior.position').id).toBe('behavior.position');
});
it('requires a specific target and behavior confirmation, even for old valid JSON and old correct reviews', () => {
  const r = record('s'); const review = reviewed(r);
  expect(aggregateHabits([r])).toEqual([]);
  expect(aggregateHabits([r,{...review,review:{targetId:r.id,verdict:'correct',note:''}}])).toEqual([]);
  expect(aggregateHabits([r,{...review,review:{...review.review!,targetConfirmed:false}}])).toEqual([]);
  expect(aggregateHabits([r,{...review,review:{...review.review!,confirmedBehaviors:[]}}])).toEqual([]);
  expect(aggregateHabits([r,review])[0].evidence).toHaveLength(1);
});
it('does not count copied answers unless a user supplies an independently reviewed correction', () => {
  const r = {...record('s'),raw:'visible evidence',parsed:null}; const review=reviewed(r);
  expect(aggregateHabits([r,review])).toEqual([]);
  expect(aggregateHabits([r,{...review,review:{...review.review!,correctedObservation:'Sample 2: I moved my right hand along the bar.'}}])[0].evidence[0].observation).toContain('right hand');
  expect(r.raw).toBe('visible evidence');
});
it('counts sessions once across samples and reanalyses, and applies the latest review verdict', () => {
  const samples = [record('s1'), record('s1','duplicate'), record('s2'), record('s3'), {...record('s1','again'),parentId:'s1'}];
  const records = [...samples, ...samples.map(r => reviewed(r))];
  expect(aggregateHabits(records)[0]).toMatchObject({repeated:true});
  expect(aggregateHabits(records)[0].evidence).toHaveLength(3);
  const change = {...reviewed(samples[3],3),review:{...reviewed(samples[3]).review!,verdict:'incorrect' as const}};
  expect(aggregateHabits([...records,change])[0].repeated).toBe(false);
  expect(aggregateHabits([...records,change,reviewed(samples[3],4)])[0].repeated).toBe(true);
});
it('uses the latest 30 sessions and does not let a late reanalysis bring back an old session', () => {
  const samples = Array.from({length:31},(_,i) => ({...record(`s${i}`),startedAt:i+1}));
  const history = [...samples,...samples.map(r => reviewed(r,100))];
  const again = {...samples[0],id:'again',parentId:samples[0].id,startedAt:1000};
  const evidence = aggregateHabits([...history,again,reviewed(again,1001)])[0].evidence;
  expect(evidence).toHaveLength(30);
  expect(evidence.some(e => e.sessionId==='s0')).toBe(false);
});
