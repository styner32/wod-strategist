import { requireOptionalNativeModule } from 'expo';

export interface AppleAiFrame {
  path: string;
  capturedAt: number;
}

export interface AppleAiPreparedFrame extends AppleAiFrame {
  sourceIndex: number;
  width: number;
  height: number;
}

export interface AppleAiPreparation {
  version: 1;
  frames: AppleAiPreparedFrame[];
  crop: { x: number; y: number; width: number; height: number };
  cropMode: 'person_with_context' | 'full_frame';
  fallbackReason: string | null;
  selectionMethod: 'endpoints_plus_visual_change' | 'uniform_low_change' | 'uniform_unreliable_subject';
  candidateCount: number;
  personCounts: number[];
  changeScores: number[];
  elapsedMs: number;
}

export interface AppleAiRequest {
  requestId: string;
  frames: AppleAiFrame[];
  wodDescription: string;
  movements: string;
  appearanceHints: string;
  language: string;
}

export interface AppleAiAnswer {
  feedback: string;
  elapsedMs: number;
  error?: string;
}

interface AppleAiModule {
  readonly promptVersion: number;
  getAvailability(language: string): Promise<string>;
  getThermalState(): Promise<number>;
  prepareFrames(requestId: string, frames: AppleAiFrame[]): Promise<AppleAiPreparation>;
  analyzeFrames(request: AppleAiRequest): Promise<AppleAiAnswer>;
  cancel(requestId: string): Promise<void>;
}

// An optional native module keeps Android, older binaries and Expo Go usable.
const native = requireOptionalNativeModule<AppleAiModule>('AppleOnDeviceAi');

// Do not silently drop identity context when JS is refreshed on an older binary.
export const appleOnDeviceAi: AppleAiModule = native && native.promptVersion === 3 && typeof native.prepareFrames === 'function' ? native : {
  promptVersion: 3,
  getAvailability: async () => 'module_missing',
  getThermalState: async () => -1,
  prepareFrames: async () => { throw new Error('module_missing'); },
  analyzeFrames: async () => ({ feedback: '', elapsedMs: 0, error: 'module_missing' }),
  cancel: async () => {},
};

interface EnvironmentModule {
  environmentVersion: number;
  environmentSystemInstructions: string;
  cancelEnvironmentWork(): Promise<void>;
  startEnvironmentMotion(): Promise<void>;
  stopEnvironmentMotion(): Promise<void>;
  environmentSample(): Promise<Record<string, unknown>>;
  environmentFrames(path: string): Promise<{ frames: { path: string; mediaOffsetMs: number; width: number; height: number }[]; durationMs: number }>;
  environmentAudio(path: string): Promise<{ path?: string; durationMs?: number; mediaOffsetMs?: number; error?: string }>;
  environmentSound(path: string): Promise<Record<string, unknown>>;
  requestEnvironmentLocationPermission(): Promise<string>;
  environmentWeather(): Promise<Record<string, unknown>>;
  cancelEnvironmentWeather(): Promise<void>;
  observeEnvironment(input: AppleAiRequest, prompt: string): Promise<AppleAiAnswer>;
}
const environmentNative = native as unknown as EnvironmentModule | null;
export const environmentNativeAvailable = environmentNative?.environmentVersion === 1;
const missing = async (): Promise<never> => { throw new Error('module_missing'); };
export const appleEnvironment: EnvironmentModule = environmentNativeAvailable ? environmentNative! : {
  environmentVersion: 0, environmentSystemInstructions: '', cancelEnvironmentWork: async () => {}, startEnvironmentMotion: missing, stopEnvironmentMotion: async () => {},
  environmentSample: missing, environmentFrames: missing, environmentAudio: missing,
  environmentSound: missing, requestEnvironmentLocationPermission: missing, environmentWeather: missing, cancelEnvironmentWeather: async () => {}, observeEnvironment: missing,
};
