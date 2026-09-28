import { useAuthStore } from "../auth/useAuthStore";
import { AppState, Platform } from 'react-native';
import Constants from 'expo-constants';
import * as FS from 'expo-file-system/legacy';
import { ulid } from 'ulid';
import { appleEnvironment, appleOnDeviceAi, environmentNativeAvailable } from '../../modules/apple-on-device-ai';
import { acquireAppleAiSlot } from '../ai-coach/appleAiExecution';
import { readAppleAiPower } from '../ai-coach/appleAiPower';
import { advanceAppleAiProtection, initialAppleAiProtection } from '../ai-coach/appleAiProtection';
import { NdjsonWriter } from '../health/polar/ndjsonWriter';
import { archiveEvidence, bundlePath, holdEnvironmentUploads, saveRecord, saveSession, flushEnvironmentUploads } from './store';
import { environmentPromptVersion, prepareObservation, finishObservation } from './observation';
import { observationKinds, type Context, type EnvironmentRecord, type EnvironmentSession, type SourceChunk } from './types';

export function newEnvironmentRecord(session: EnvironmentSession, kind: EnvironmentRecord['kind'], source: string): EnvironmentRecord {
  const now = Date.now();
  return { version: 1, id: ulid(), bundleId: session.id, sessionId: session.sessionId, profileId: session.profileId,
    kind, source, model: source === 'FoundationModels' ? 'SystemLanguageModel.default' : source === 'SoundAnalysis' ? 'SNClassifierIdentifier.version1' : null,
    modelVersion: null, tokens: null, executionUnit: null, promptVersion: environmentPromptVersion,
    prompt: null, context: session.context, scheduledAt: now, startedAt: now, completedAt: now,
    outcome: 'skipped', reason: null, preparationMs: 0, inferenceMs: 0, raw: null, parsed: null,
    validation: 'not_applicable', evidence: [], powerBefore: null, powerAfter: null };
}
export class EnvironmentRecorder {
  private session: EnvironmentSession;
  private writer: NdjsonWriter | null = null;
  private releaseUploads: () => void;
  private timer: ReturnType<typeof setInterval> | null = null;
  private warning: ReturnType<typeof AppState.addEventListener> | null = null;
  private chunk: SourceChunk | null = null;
  private nextAt = 0;
  private nextWeatherAt = 0;
  private step = 0;
  private active = true;
  private paused = false;
  private memoryHalted = false;
  private protection = initialAppleAiProtection;
  private pending: Promise<void> | null = null;
  private tickPending = false;
  private tickTask: Promise<void> | null = null;
  private weatherPending: Promise<void> | null = null;
  private requestId: string | null = null;
  private generation = 0;
  private stopPromise: Promise<void> | null = null;
  private ready: Promise<void>;
  private intervalMs: number;
  constructor(identity: { sessionId: string; profileId: number; startedAt: number }, context: Context,
    settings: Record<string, unknown>, private publish: (state: { status: string; record?: EnvironmentRecord }) => void) {
    this.intervalMs = settings.observationIntervalSeconds === 30 ? 30_000 : settings.observationIntervalSeconds === 120 ? 120_000 : 60_000;
    this.session = { version: 1, id: ulid(), profileId: identity.profileId, sessionId: identity.sessionId,
      ownerUserId: useAuthStore.getState().userId, startedAt: identity.startedAt, endedAt: null, complete: false, context, settings: { ...settings, observationIntervalSeconds: this.intervalMs / 1000 },
      appVersion: Constants.expoConfig?.version ?? 'unknown', os: `${Platform.OS} ${Platform.Version}`, device: 'unavailable' };
    this.releaseUploads = holdEnvironmentUploads();
    this.ready = this.initialize().catch(error => { this.publish({ status: 'save_error' }); this.active = false; console.warn('Environment initialization failed', error); });
  }
  private async initialize() {
    this.session.eventsFile = `environment_${this.session.id}_events.ndjson`;
    await saveSession(this.session);
    if (!this.active) return;
    const filename = `environment_${this.session.id}_events`;
    this.session.eventsFile = `${filename}.ndjson`;
    this.writer = new NdjsonWriter(filename, `environment/${this.session.id}`);
    this.writer.startAutoFlush(5000);
    this.event('start', { settings: this.session.settings, nativeAvailable: environmentNativeAvailable,
      unsupported: ['exposureDuration', 'ISO', 'focusDistance'], clock: 'capture_epoch_ms_not_merged_video_time' });
    await saveSession(this.session);
    if (environmentNativeAvailable && !this.paused && this.active) await appleEnvironment.startEnvironmentMotion();
    if (!this.active) { await appleEnvironment.stopEnvironmentMotion(); return; }
    this.nextAt = Date.now() + this.intervalMs;
    this.warning = AppState.addEventListener('memoryWarning', () => { this.memoryHalted = true; void this.suspend('memory_warning'); });
    this.timer = setInterval(() => { if (!this.tickPending) this.tickTask = this.tick(); }, 1000);
    this.publish({ status: environmentNativeAvailable ? 'waiting' : 'module_missing' });
  }
  event(event: string, data: Record<string, unknown> = {}) {
    this.writer?.write({ event, at: Date.now(), captureOffsetMs: Date.now()-this.session.startedAt, ...data });
  }
  offerChunk(chunk: SourceChunk) {
    if (!this.active) return;
    this.chunk = chunk;
    this.event('chunk', { ...chunk, path: undefined, sourceFile: chunk.path.split('/').pop() });
  }
  settings(settings: Record<string, unknown>) { this.session.settings = settings; this.event('camera_settings', settings); }
  private async tick() {
    if (this.tickPending || !this.active || this.paused || this.memoryHalted) return;
    this.tickPending = true;
    try {
      const power = await readAppleAiPower();
      if (!this.active || this.paused || this.memoryHalted) return;
      this.protection = advanceAppleAiProtection(this.protection, power, Date.now());
      this.event('power', { ...power, source: 'system' });
      if (Date.now() >= this.nextAt) {
        try { this.event('storage', { freeBytes: await FS.getFreeDiskStorageAsync(), source: 'system' }); } catch { this.event('storage', { status: 'unavailable' }); }
      }
      if (this.protection.reason) {
        await appleEnvironment.stopEnvironmentMotion();
        if (this.requestId) void appleOnDeviceAi.cancel(this.requestId).catch(() => {});
        void appleEnvironment.cancelEnvironmentWork().catch(() => {});
        this.publish({ status: this.protection.reason });
      } else if (environmentNativeAvailable) {
        await appleEnvironment.startEnvironmentMotion();
        const sample = await appleEnvironment.environmentSample();
        if (this.active && !this.paused) {
          this.event('motion', { source: 'CoreMotion_device_not_person', ...sample });
          if (typeof sample.device === 'string') this.session.device = sample.device;
        }
      }
      const now = Date.now();
      if (!this.active || this.paused || this.memoryHalted) return;
      if (now >= this.nextWeatherAt && !this.weatherPending && !this.protection.reason && environmentNativeAvailable) {
        this.nextWeatherAt = now + 300_000;
        this.weatherPending = this.weather().finally(() => { this.weatherPending = null; });
      }
      if (now < this.nextAt) return;
      const scheduled = this.nextAt;
      this.nextAt = now + this.intervalMs;
      const kind = observationKinds[this.step++ % observationKinds.length];
      const record = newEnvironmentRecord(this.session, kind, kind === 'sound' ? 'SoundAnalysis' : 'FoundationModels');
      record.scheduledAt = scheduled; record.powerBefore = power;
      prepareObservation(record, Math.floor((this.step - 1) / observationKinds.length));
      if (this.pending) { this.event('skipped', { kind, reason: 'busy', scheduledAt: scheduled }); return; }
      const chunk = this.chunk;
      const analysisEnabled = this.session.settings.environmentAnalysis !== false;
      const release = analysisEnabled && !this.protection.reason && environmentNativeAvailable && chunk && now-chunk.captureEnd <= this.intervalMs ? acquireAppleAiSlot() : null;
      record.reason = !analysisEnabled ? 'measurements_only' : this.protection.reason ?? (!environmentNativeAvailable ? 'module_missing' : !chunk || now-chunk.captureEnd > this.intervalMs ? 'no_recent_chunk' : !release ? 'busy' : null);
      this.pending = (async () => {
        try {
          if (release && chunk) await this.observe(record, chunk);
          record.completedAt = Date.now();
          await saveRecord(record);
          if (this.active && !this.paused) this.publish({ status: record.reason ?? 'waiting', record });
        } catch (error) { this.publish({ status: 'save_error' }); this.event('save_error', { detail: String(error) }); }
        finally { release?.(); this.pending = null; }
      })();
    } catch (error) { this.event('sample_error', { detail: String(error) }); }
    finally { this.tickPending = false; }
  }
  private async observe(record: EnvironmentRecord, chunk: SourceChunk) {
    const generation = this.generation;
    const cancelled = () => !this.active || this.paused || this.memoryHalted || generation !== this.generation || !!this.protection.reason;
    const temps: string[] = [];
    this.requestId = record.id;
    this.publish({ status: 'analyzing', record });
    record.chunk = { captureStart: chunk.captureStart, captureEnd: chunk.captureEnd, durationMs: chunk.durationMs,
      index: chunk.index, sourceFile: chunk.path.split('/').pop()!, clock: 'chunk_media_ms_and_capture_epoch_ms' };
    try {
      if (record.kind === 'sound') {
        const audio = await appleEnvironment.environmentAudio(chunk.path);
        if (audio.error || !audio.path) throw new Error(audio.error ?? 'no_audio_track');
        temps.push(audio.path);
        record.evidence = await archiveEvidence(this.session.id, record.id, [{ path: audio.path, mediaOffsetMs: 0 }], 'audio/mp4');
        record.data = { durationMs: audio.durationMs };
        record.preparationMs = Date.now()-record.startedAt;
        if (cancelled()) throw new Error('cancelled');
        const start = Date.now();
        const result = await appleEnvironment.environmentSound(audio.path);
        record.inferenceMs = Date.now()-start;
        record.raw = JSON.stringify(result); record.data = { ...record.data, ...result };
      } else {
        const availability = await appleOnDeviceAi.getAvailability(record.context.language);
        if (availability !== 'available') throw new Error(availability);
        if (cancelled()) throw new Error('cancelled');
        const prepared = await appleEnvironment.environmentFrames(chunk.path);
        temps.push(...prepared.frames.map(f => f.path));
        record.evidence = await archiveEvidence(this.session.id, record.id, prepared.frames, 'image/jpeg');
        record.preparationMs = Date.now()-record.startedAt;
        if (cancelled()) throw new Error('cancelled');
        record.data = { systemInstructions: appleEnvironment.environmentSystemInstructions, imageCaptionFormat: "Sample N, media offset M ms. Gaps are unobserved." };
        const answer = await appleEnvironment.observeEnvironment({ requestId: record.id, ...record.context,
          frames: prepared.frames.map(f => ({ path: f.path, capturedAt: f.mediaOffsetMs })) }, record.prompt!);
        record.raw = answer.feedback; record.inferenceMs = answer.elapsedMs;
        finishObservation(record);
        if (answer.error) throw new Error(answer.error);
      }
      if (cancelled()) throw new Error('cancelled');
      record.outcome = 'success'; record.reason = null;
    } catch (error) {
      record.reason = cancelled() ? 'cancelled' : error instanceof Error ? error.message : 'analysis_failed';
      record.outcome = record.reason === 'cancelled' ? 'cancelled' : 'error';
    } finally {
      record.powerAfter = await readAppleAiPower();
      this.requestId = null;
      await Promise.allSettled(temps.map(path => FS.deleteAsync(path.startsWith('file://') ? path : `file://${path}`, { idempotent: true })));
    }
  }
  private async weather() {
    const record = newEnvironmentRecord(this.session, 'weather', 'WeatherKit');
    try {
      const result = await appleEnvironment.environmentWeather();
      record.data = result; record.outcome = result.error ? 'error' : 'success';
      record.reason = typeof result.error === 'string' ? result.error : null;
      record.completedAt = Date.now();
      await saveRecord(record);
    } catch (error) { this.event('weather_error', { detail: String(error) }); }
  }
  async suspend(reason = 'paused') {
    this.paused = true; this.generation++;
    this.event(reason); this.publish({ status: reason });
    if (this.requestId) void appleOnDeviceAi.cancel(this.requestId).catch(() => {});
    void appleEnvironment.cancelEnvironmentWork().catch(() => {});
    await appleEnvironment.stopEnvironmentMotion().catch(() => {});
    await appleEnvironment.cancelEnvironmentWeather().catch(() => {});
    await this.pending;
  }
  resume() {
    if (!this.active || this.memoryHalted) return;
    this.paused = false; this.chunk = null; this.nextAt = Date.now()+this.intervalMs;
    this.event('resume'); this.publish({ status: 'waiting' });
  }
  stop(reason = 'stop') {
    if (this.stopPromise) return this.stopPromise;
    this.active = false;
    if (this.timer) clearInterval(this.timer);
    this.warning?.remove();
    this.stopPromise = (async () => {
      try {
        await this.ready;
        await this.suspend(reason);
        await this.tickTask;
        await appleEnvironment.stopEnvironmentMotion().catch(() => {});
        await this.weatherPending;
        this.event('end');
        const result = this.writer?.close();
        this.session.endedAt = Date.now(); this.session.complete = reason === 'stop' && !!result?.complete;
        this.session.writerComplete = result?.complete ?? false;
        await saveSession(this.session);
      } finally { this.releaseUploads(); void flushEnvironmentUploads().catch(() => {}); }
    })();
    return this.stopPromise;
  }
}
