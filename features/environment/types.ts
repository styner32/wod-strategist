import type { AppleAiPowerSample } from '../ai-coach/appleAiProtection';
export const observationKinds = ['camera', 'behavior', 'sound', 'space'] as const;
export type ObservationKind = typeof observationKinds[number];
export const behaviors = ['grip_reset', 'foot_reset', 'rest_support', 'rest_walk', 'position_shift', 'transition', 'path_overlap', 'give_space'] as const;
export type Behavior = typeof behaviors[number];
export type Verdict = 'correct' | 'incorrect' | 'abstract' | 'uncertain';
export interface Context { wodDescription: string; movements: string; appearanceHints: string; language: string }
export interface SourceChunk {
  path: string; captureStart: number; captureEnd: number; durationMs: number; index: number;
}
export interface Evidence {
  filename: string; mime: string; bytes: number; mediaOffsetMs?: number; width?: number; height?: number;
}
export interface ParsedObservation {
  target: 'identified' | 'uncertain' | 'not_applicable';
  facts: string[]; interpretation: string; limitations: string[]; behaviors: Behavior[];
}
export interface QualityCheck {
  version: 1; status: 'flagged' | 'unchecked'; flags: ('empty_response' | 'example_copy')[];
}
export interface ReviewConfirmation {
  targetConfirmed?: boolean; confirmedBehaviors?: Behavior[]; correctedObservation?: string;
}
export interface EnvironmentRecord {
  version: 1; id: string; bundleId: string; sessionId: string; profileId: number;
  kind: ObservationKind | 'weather' | 'review' | 'summary';
  source: string; model: string | null; modelVersion: null; tokens: null; executionUnit: null;
  promptVersion: number; prompt: string | null; context: Context;
  questionId?: string; responseFormat?: 'text' | 'json'; quality?: QualityCheck;
  targetContext?: 'provided' | 'missing';
  scheduledAt: number; startedAt: number; completedAt: number;
  outcome: 'success' | 'error' | 'skipped' | 'cancelled'; reason: string | null;
  preparationMs: number; inferenceMs: number;
  raw: string | null; parsed: ParsedObservation | null; validation: 'valid' | 'invalid' | 'not_applicable';
  data?: Record<string, unknown>;
  evidence: Evidence[];
  chunk?: Omit<SourceChunk, 'path'> & { sourceFile: string; clock: 'chunk_media_ms_and_capture_epoch_ms' };
  powerBefore: AppleAiPowerSample | null; powerAfter: AppleAiPowerSample | null;
  parentId?: string;
  review?: { targetId: string; verdict: Verdict; note: string } & ReviewConfirmation;
}
export interface EnvironmentSession {
  version: 1; id: string; profileId: number; ownerUserId: number | null; sessionId: string; startedAt: number;
  endedAt: number | null; complete: boolean; context: Context;
  settings: Record<string, unknown>; appVersion: string; os: string; device: string;
  deleting?: boolean;
  eventsFile?: string; writerComplete?: boolean;
}
