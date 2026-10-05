import { useCallback, useEffect, useRef, useState } from 'react';
import { readAppleAiPower } from './appleAiPower';
import { acquireAppleAiSlot } from './appleAiExecution';
import { deleteAsync } from 'expo-file-system/legacy';
import { appleOnDeviceAi, type AppleAiFrame } from '../../modules/apple-on-device-ai';
import { advanceAppleAiProtection, initialAppleAiProtection } from './appleAiProtection';
import type { AppleAiObservation } from './appleAiObservation';

export interface AppleAiFeedbackOptions {
  enabled: boolean;
  running: boolean;
  foreground: boolean;
  sessionId: string;
  wodDescription: string;
  movements: string;
  appearanceHints?: string;
  language: string;
  capture: () => Promise<{ path: string }>;
  onObservation?: (observation: AppleAiObservation) => Promise<void>;
}

export interface AppleAiFeedbackResult {
  feedback: string;
  capturedAt: number;
  lastCapturedAt: number;
  elapsedMs: number;
}

function wait(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise(resolve => {
    if (signal.aborted) { resolve(); return; }
    const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve(); };
    const timer = setTimeout(finish, ms);
    signal.addEventListener('abort', finish, { once: true });
  });
}

export function useAppleAiFeedback(options: AppleAiFeedbackOptions) {
  const { enabled, running, foreground, sessionId, wodDescription, movements, language, capture, onObservation } = options;
  const appearanceHints = (options.appearanceHints ?? '').trim().slice(0, 300);
  const [availability, setAvailability] = useState('checking');
  const [protection, setProtection] = useState<string | null>('checking');
  const protectionRef = useRef(initialAppleAiProtection);
  const [phase, setPhase] = useState('idle');
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<AppleAiFeedbackResult | null>(null);
  const [archiveError, setArchiveError] = useState(false);
  const controllerRef = useRef<AbortController | null>(null);
  // Shared across effect restarts: even a cancelled native call must settle
  // before a resumed/new session can capture or analyze again.
  const inFlight = useRef<Promise<void>>(Promise.resolve());
  const sequence = useRef(0);

  const stop = useCallback(() => {
    controllerRef.current?.abort();
    return inFlight.current;
  }, []);

  useEffect(() => {
    setResult(null);
    setError(null);
    setArchiveError(false);
  }, [sessionId, enabled, appearanceHints, movements, wodDescription, language]);

  useEffect(() => {
    if (!enabled || !foreground) return;
    const controller = new AbortController();
    setAvailability('checking');
    setProtection('checking');
    const poll = async () => {
      while (!controller.signal.aborted) {
        const [model, power] = await Promise.allSettled([
          appleOnDeviceAi.getAvailability(language), readAppleAiPower(),
        ]);
        if (controller.signal.aborted) return;
        const next = advanceAppleAiProtection(protectionRef.current,
          power.status === 'fulfilled' ? power.value : { battery: -1, lowPower: null, thermal: -1 }, Date.now());
        protectionRef.current = next;
        // Abort immediately, before the React effect cleanup follows the state update.
        const available = model.status === 'fulfilled' ? model.value : 'unavailable';
        if (next.reason || available !== 'available') stop();
        setProtection(next.reason);
        setAvailability(available);
        await wait(5000, controller.signal);
      }
    };
    void poll();
    return () => { controller.abort(); stop(); };
  }, [enabled, foreground, language, stop]);

  useEffect(() => {
    if (!enabled || !running || !foreground || availability !== 'available' || protection) return;
    const controller = new AbortController();
    const { signal } = controller;
    controllerRef.current = controller;
    const run = async () => {
      while (!signal.aborted) {
        const release = acquireAppleAiSlot();
        if (!release) { await wait(1000, signal); continue; }
        const requestId = `${sessionId}:apple:${Date.now()}:${++sequence.current}`;
        const frames: AppleAiFrame[] = [];
        const observation: AppleAiObservation = {
          sessionId, request: { requestId, frames, wodDescription, movements, appearanceHints, language },
          startedAt: Date.now(), completedAt: 0, outcome: 'error', error: 'analysis_failed', answer: null,
        };
        let timedOut = false;
        let deadline: ReturnType<typeof setTimeout> | undefined;
        const cancel = () => { void appleOnDeviceAi.cancel(requestId).catch(() => {}); };
        signal.addEventListener('abort', cancel, { once: true });
        try {
          setError(null);
          setPhase('collecting');
          for (let i = 0; i < 6; i++) {
            if (signal.aborted) break;
            const photo = await capture();
            // Register paths even if cancellation arrived while capture was pending.
            frames.push({ path: photo.path, capturedAt: Date.now() });
            if (i < 5) await wait(800, signal);
          }
          if (signal.aborted) break;
          // Retain every candidate and the exact encoded model inputs for review.
          observation.preparation = await appleOnDeviceAi.prepareFrames(requestId, frames);
          if (signal.aborted) break;
          setPhase('analyzing');
          deadline = setTimeout(() => {
            if (signal.aborted) return;
            timedOut = true;
            setError('timeout');
            setPhase('error');
            cancel();
          }, 30_000);
          const selected = observation.preparation.frames;
          observation.inferenceStartedAt = Date.now();
          const answer = await appleOnDeviceAi.analyzeFrames({ requestId, frames: selected, wodDescription, movements, appearanceHints, language });
          if (!signal.aborted && !timedOut) {
            observation.answer = answer;
            observation.outcome = answer.error ? 'error' : 'success';
            observation.error = answer.error ?? null;
          }
          if (signal.aborted) break;
          if (timedOut) {
            // Retain the timeout and the slot until native cancellation settles.
          } else if (answer.error) {
            setError(answer.error);
            setPhase('error');
          } else {
            setResult({ feedback: answer.feedback, elapsedMs: answer.elapsedMs,
              capturedAt: selected[0].capturedAt, lastCapturedAt: selected[selected.length - 1].capturedAt });
            setPhase('waiting');
          }
        } catch {
          if (!signal.aborted && !timedOut) { setError('analysis_failed'); setPhase('error'); }
        } finally {
          try {
            if (deadline !== undefined) clearTimeout(deadline);
            signal.removeEventListener('abort', cancel);
            observation.completedAt = Date.now();
            if (timedOut || signal.aborted) {
              observation.outcome = timedOut ? 'timeout' : 'cancelled';
              observation.error = observation.outcome;
              observation.answer = null;
            }
            let archived = !onObservation;
            if (onObservation) {
              try { await onObservation(observation); archived = true; }
              catch (archiveFailure) {
                setArchiveError(true);
                console.warn('Apple AI observation could not be saved; retaining source snapshots:', archiveFailure);
              }
            }
            // Copy into the durable bundle before removing temporary camera files.
            // A failed save must not destroy the only remaining input evidence.
            if (archived) await Promise.allSettled([...frames, ...(observation.preparation?.frames ?? [])].map(frame => deleteAsync(
              frame.path.startsWith('file://') ? frame.path : `file://${frame.path}`, { idempotent: true },
            )));
          } finally { release(); }
        }
        await wait(10_000, signal);
      }
    };
    const previous = inFlight.current;
    inFlight.current = previous.catch(() => {}).then(run);
    return () => { controller.abort(); };
  }, [enabled, running, foreground, sessionId, wodDescription, movements, appearanceHints, language, capture, onObservation, availability, protection]);

  const status = !enabled ? 'off'
    : availability !== 'available' ? availability
    : protection ?? (!running || !foreground ? 'paused' : phase);
  return { status, error, result, archiveError, stop };
}
