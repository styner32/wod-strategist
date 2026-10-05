import type { AppleAiAnswer, AppleAiRequest, AppleAiPreparation } from '../../modules/apple-on-device-ai';

/** Capture-clock timestamps include pauses; they are not merged-video positions. */
export interface AppleAiObservation {
  sessionId: string;
  request: AppleAiRequest;
  startedAt: number;
  completedAt: number;
  outcome: 'success' | 'error' | 'cancelled' | 'timeout';
  error: string | null;
  answer: AppleAiAnswer | null;
  preparation?: AppleAiPreparation;
  inferenceStartedAt?: number;
}
