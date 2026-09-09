import {
  createUploadTask,
  FileSystemUploadType,
  getInfoAsync,
  uploadAsync,
  UploadProgressData,
} from "expo-file-system/legacy";

import { getToken } from "@/features/auth/storage";
import type { components } from "./schema";
import type { WorkoutType } from "./workoutType";

const API_BASE_URL =
  process.env.EXPO_PUBLIC_API_URL || "http://localhost:8088/api/v1";

// ==========================================
// Core API Client
// ==========================================

export interface ApiRequestOptions extends RequestInit {
  bodyPayload?: any; // JSON body
}

/**
 * A tiny fetch wrapper that injects the base URL and Authorization header,
 * and automatically parses JSON or throws on errors.
 */
export async function apiClient<T = any>(
  endpoint: string,
  options: ApiRequestOptions = {},
): Promise<T> {
  const { bodyPayload, headers: customHeaders, ...fetchOptions } = options;

  const url = `${API_BASE_URL}${endpoint.startsWith("/") ? endpoint : `/${endpoint}`}`;

  const headers = new Headers(customHeaders);

  // Inject auth token if available
  const token = await getToken();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }

  if (bodyPayload && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  const res = await fetch(url, {
    ...fetchOptions,
    headers,
    body: bodyPayload ? JSON.stringify(bodyPayload) : fetchOptions.body,
  });

  if (res.status === 401) {
    // Lazy import to avoid circular dependency
    const { useAuthStore } = await import("@/features/auth/useAuthStore");
    useAuthStore.getState().handleUnauthorized();
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    let errorText = res.statusText;
    try {
      errorText = await res.text();
    } catch {}
    throw new Error(
      `API Error [${res.status}]: ${errorText || res.statusText}`,
    );
  }

  // Not all responses have JSON bodies (e.g. 204 No Content)
  const contentType = res.headers.get("Content-Type") || "";
  if (contentType.includes("application/json")) {
    return res.json() as Promise<T>;
  }

  return res.text() as Promise<any>;
}

// ==========================================
// API Endpoints
// ==========================================

export interface UploadResult {
  taskId: string;
  sessionId: string;
}

export interface ProcessWorkoutVideoOptions {
  onProgress?: (progress: number) => void;
  onCancelReady?: (cancel: () => Promise<void>) => void;
  movements?: string[];
  injuries?: string[];
  mimeType?: string;
  workoutType?: WorkoutType;
  profileId: number;
  startSecs?: number;
  endSecs?: number;
  heartRateBpm?: number;
  workoutConfidence?: number;
  appearanceHints?: string;
}

export type UploadUrlResponse = Required<
  components["schemas"]["controllers.CreateUploadURLResponse"]
>;
export type UploadCompleteResponse = Required<
  components["schemas"]["controllers.CompleteUploadResponse"]
>;

export interface ChunkAnalysisResult {
  id: number;
  session_id: string;
  status: string;
  output: string;
  exercise_type?: string;
  start_secs?: number;
  end_secs?: number;
  created_at: string;
  updated_at: string;
}

export async function fetchChunkAnalysis(
  sessionId: string,
): Promise<ChunkAnalysisResult[]> {
  return apiClient<ChunkAnalysisResult[]>(`/chunk-analysis/${sessionId}`);
}

export async function fetchMovements(): Promise<string[]> {
  return apiClient<string[]>("/movements");
}

export interface MovementGroup {
  category: string;
  movements: string[];
}

export async function fetchMovementGroups(): Promise<MovementGroup[]> {
  return apiClient<MovementGroup[]>("/movement-groups");
}

export async function fetchInjuries(): Promise<string[]> {
  return apiClient<string[]>("/injuries");
}

// ==========================================
// Profile API
// ==========================================

export interface AppearanceInput {
  top?: string;
  bottom?: string;
  shoes?: string;
  hair?: string;
  build?: string;
  gear?: string[];
  notes?: string;
}

export interface ProfileResponse {
  id: number;
  name: string;
  birth_year: number;
  birth_month: number;
  birth_day: number;
  gender: string;
  height_cm: number;
  weight_kg: number;
  fitness_level: string;
  injuries: string[];
  appearance?: string | AppearanceInput;
  archived_at?: string;
}

export interface CreateProfileRequest {
  name?: string;
  birth_year?: number;
  birth_month?: number;
  birth_day?: number;
  gender?: string;
  height_cm?: number;
  weight_kg?: number;
  fitness_level?: string;
  injuries?: string[];
  appearance?: string | AppearanceInput;
}

export interface UpdateProfileRequest {
  name?: string;
  birth_year?: number;
  birth_month?: number;
  birth_day?: number;
  gender?: string;
  height_cm?: number;
  weight_kg?: number;
  fitness_level?: string;
  injuries?: string[];
  appearance?: string | AppearanceInput;
}

export async function createProfile(
  data: CreateProfileRequest,
): Promise<ProfileResponse> {
  return apiClient<ProfileResponse>("/profiles", {
    method: "POST",
    bodyPayload: data,
  });
}

export async function getProfile(id: number): Promise<ProfileResponse> {
  return apiClient<ProfileResponse>(`/profiles/${id}`);
}

export async function listProfiles(
  includeArchived = false,
): Promise<ProfileResponse[]> {
  const params = includeArchived ? "?include_archived=true" : "";
  return apiClient<ProfileResponse[]>(`/profiles${params}`);
}

export async function updateProfile(
  id: number,
  data: UpdateProfileRequest,
): Promise<ProfileResponse> {
  return apiClient<ProfileResponse>(`/profiles/${id}`, {
    method: "PUT",
    bodyPayload: data,
  });
}

export async function archiveProfile(id: number): Promise<void> {
  return apiClient(`/profiles/${id}/archive`, {
    method: "POST",
  });
}

export async function unarchiveProfile(id: number): Promise<void> {
  return apiClient(`/profiles/${id}/unarchive`, {
    method: "POST",
  });
}

/** Step 1: Request a signed upload URL from our API */
export async function getUploadUrl(
  sessionId: string,
  filename: string,
  profileId: number,
): Promise<UploadUrlResponse> {
  return apiClient<UploadUrlResponse>("/upload-url", {
    method: "POST",
    bodyPayload: { session_id: sessionId, filename, profile_id: profileId },
  });
}

export interface PrepareSensorUploadRequest {
  calculation_version?: 1 | 2;
  profile_id: number;
  request_id: string;
  expected_version: string;
  size_bytes: number;
  sha256: string;
}

export interface PrepareSensorUploadResponse {
  request_id: string;
  version: string;
  state: string;
  object_name: string;
  upload_url?: string;
  required_headers?: Record<string, string>;
  expires_at?: string;
}

export interface CompleteSensorUploadRequest {
  profile_id: number;
  request_id: string;
  version: string;
}

export interface CompleteSensorUploadResponse {
  accepted: boolean;
  request_id?: string;
  version?: string;
  state: string;
  error_code?: string;
  retryable: boolean;
}

export interface SensorStatusResponse {
  request_id: string;
  version: string;
  state: string;
  last_error_code?: string | null;
  retryable: boolean;
}

export async function prepareSensorUpload(
  sessionId: string,
  req: PrepareSensorUploadRequest,
): Promise<PrepareSensorUploadResponse> {
  return apiClient<PrepareSensorUploadResponse>(
    `/sessions/${sessionId}/sensor-upload`,
    {
      method: "POST",
      bodyPayload: req,
    },
  );
}

export async function completeSensorUpload(
  sessionId: string,
  req: CompleteSensorUploadRequest,
): Promise<CompleteSensorUploadResponse> {
  return apiClient<CompleteSensorUploadResponse>(
    `/sessions/${sessionId}/sensor-complete`,
    {
      method: "POST",
      bodyPayload: req,
    },
  );
}

export async function getSensorStatus(
  sessionId: string,
  profileId: number,
): Promise<SensorStatusResponse> {
  const params = new URLSearchParams({ profile_id: String(profileId) });
  return apiClient<SensorStatusResponse>(
    `/sessions/${sessionId}/sensor-status?${params.toString()}`,
  );
}

export async function uploadSensorToGcs(
  uploadUrl: string,
  fileUri: string,
  requiredHeaders?: Record<string, string>,
): Promise<void> {
  const info = await getInfoAsync(fileUri);
  if (!info.exists || info.isDirectory) {
    throw new Error("Sensor upload file is missing or is not a regular file");
  }

  const headers = {
    "Content-Type": "application/x-ndjson",
    ...(requiredHeaders || {}),
  };

  // Unlike uploadTaskStartAsync, this native entry point checks file existence
  // before creating an iOS background task (which can throw an NSException).
  const response = await uploadAsync(
    uploadUrl,
    fileUri,
    {
      httpMethod: "PUT",
      headers,
      uploadType: FileSystemUploadType.BINARY_CONTENT,
    },
  );

  if (!response) {
    throw new Error(
      "Failed to upload sensor telemetry to GCS: No response from upload task.",
    );
  }

  if (response.status < 200 || response.status >= 300) {
    const err: any = new Error(
      `Failed to upload sensor telemetry to GCS: HTTP ${response.status} ${response.body || ""}`,
    );
    err.status = response.status;
    err.body = response.body;
    throw err;
  }
}

/** Step 2: Stream the binary payload directly into GCS using Expo FileSystem */
export async function uploadToGcs(
  uploadUrl: string,
  fileUri: string,
  mimeType: string,
  onProgress?: (progress: number) => void,
  onCancelReady?: (cancel: () => Promise<void>) => void,
): Promise<void> {
  const uploadTask = createUploadTask(
    uploadUrl,
    fileUri,
    {
      httpMethod: "PUT",
      headers: { "Content-Type": mimeType },
      uploadType: FileSystemUploadType.BINARY_CONTENT,
    },
    (data: UploadProgressData) => {
      if (onProgress && data.totalBytesExpectedToSend > 0) {
        onProgress(data.totalBytesSent / data.totalBytesExpectedToSend);
      }
    },
  );

  // Expose the cancel function to the caller before starting
  onCancelReady?.(() => uploadTask.cancelAsync());

  const response = await uploadTask.uploadAsync();

  if (!response) {
    throw new Error("Failed to upload to GCS: No response from upload task.");
  }

  if (response.status < 200 || response.status >= 300) {
    throw new Error(
      `Failed to upload to GCS: HTTP ${response.status} ${response.body || ""}`,
    );
  }
}

export async function notifyUploadComplete(
  sessionId: string,
  gcsUri: string,
  movements: string[],
  injuries: string[],
  workoutType: string,
  profileId: number,
  appearanceHints?: string,
): Promise<UploadCompleteResponse> {
  return apiClient<UploadCompleteResponse>("/upload-complete", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      gcs_uri: gcsUri,
      movements,
      injuries,
      workout_type: workoutType,
      profile_id: profileId,
      ...(appearanceHints ? { appearance_hints: appearanceHints } : {}),
    },
  });
}

export async function notifyChunkUploadComplete(
  sessionId: string,
  gcsUri: string,
  movements: string[],
  injuries: string[],
  workoutType: string,
  profileId: number,
  startSecs?: number,
  endSecs?: number,
  heartRateBpm?: number,
  workoutConfidence?: number,
  appearanceHints?: string,
): Promise<UploadCompleteResponse> {
  return apiClient<UploadCompleteResponse>("/chunk-complete", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      gcs_uri: gcsUri,
      movements,
      injuries,
      workout_type: workoutType,
      profile_id: profileId,
      ...(startSecs !== undefined ? { start_secs: startSecs } : {}),
      ...(endSecs !== undefined ? { end_secs: endSecs } : {}),
      ...(heartRateBpm !== undefined && heartRateBpm > 0
        ? { heart_rate_bpm: heartRateBpm }
        : {}),
      ...(workoutConfidence !== undefined
        ? { workout_confidence: workoutConfidence }
        : {}),
      ...(appearanceHints ? { appearance_hints: appearanceHints } : {}),
    },
  });
}

/**
 * Orchestrates the full 3-step workout upload flow.
 */
export async function processWorkoutVideo(
  fileUri: string,
  sessionId: string = "session_dev_001",
  options: ProcessWorkoutVideoOptions,
): Promise<UploadResult> {
  const {
    onProgress,
    onCancelReady,
    movements = [],
    injuries = [],
    mimeType = "video/mp4",
    workoutType = "wod",
    profileId,
  } = options;
  const filename = fileUri.split("/").pop() || "workout.mp4";

  if (!sessionId) {
    throw new Error(
      "session_id is required but was empty. The recording may not have started properly.",
    );
  }

  console.log(
    "🚀 Starting upload process for:",
    filename,
    "sessionId:",
    sessionId,
  );

  const { upload_url, gcs_uri } = await getUploadUrl(
    sessionId,
    filename,
    profileId,
  );
  console.log("✅ Got Signed URL");

  await uploadToGcs(upload_url, fileUri, mimeType, onProgress, onCancelReady);
  console.log("✅ Uploaded to GCS");

  const result = await notifyUploadComplete(
    sessionId,
    gcs_uri,
    movements,
    injuries,
    workoutType,
    profileId,
  );
  console.log("✅ Analysis Started:", result);

  return {
    taskId: result.task_id,
    sessionId: result.session_id,
  };
}

export async function processWorkoutChunk(
  fileUri: string,
  sessionId: string,
  options: ProcessWorkoutVideoOptions,
): Promise<UploadResult> {
  const {
    movements = [],
    injuries = [],
    mimeType = "video/mp4",
    workoutType = "wod",
    profileId,
    startSecs,
    endSecs,
    heartRateBpm,
    workoutConfidence,
    appearanceHints,
  } = options;
  const filename = fileUri.split("/").pop() || "chunk.mp4";

  // DEBUG: Simulate slow upload. Set to 0 for normal behavior.
  // e.g. 15000 = each upload takes 15s extra, causing pile-up with 10s chunks.
  const DEBUG_SLOW_UPLOAD_MS = 0;
  if (DEBUG_SLOW_UPLOAD_MS > 0) {
    console.warn(
      `⏳ DEBUG: Simulating slow upload (${DEBUG_SLOW_UPLOAD_MS}ms delay)`,
    );
    await new Promise((resolve) => setTimeout(resolve, DEBUG_SLOW_UPLOAD_MS));
  }

  const { upload_url, gcs_uri } = await getUploadUrl(
    sessionId,
    filename,
    profileId,
  );
  await uploadToGcs(upload_url, fileUri, mimeType);

  const result = await notifyChunkUploadComplete(
    sessionId,
    gcs_uri,
    movements,
    injuries,
    workoutType,
    profileId,
    startSecs,
    endSecs,
    heartRateBpm,
    workoutConfidence,
    appearanceHints,
  );

  return {
    taskId: result.task_id,
    sessionId: result.session_id,
  };
}

export interface MergeChunksResult {
  taskId: string;
  sessionId: string;
  message: string;
}

/**
 * Triggers server-side merging of all uploaded chunks for a session.
 * The backend downloads chunks from GCS, merges with FFmpeg, then
 * enqueues a full video analysis task on the merged video.
 */
export async function mergeChunks(
  sessionId: string,
  options: {
    workoutType?: WorkoutType;
    movements?: string[];
    injuries?: string[];
    profileId: number;
    enableTts?: boolean;
    wodDescription?: string;
    appearanceHints?: string;
  },
): Promise<MergeChunksResult> {
  const {
    workoutType = "wod",
    movements = [],
    injuries = [],
    profileId,
    enableTts = false,
    wodDescription,
    appearanceHints,
  } = options;

  const result = await apiClient<{
    task_id: string;
    session_id: string;
    message: string;
  }>("/merge-chunks", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      workout_type: workoutType,
      movements,
      injuries,
      profile_id: profileId,
      enable_tts: enableTts,
      ...(wodDescription ? { wod_description: wodDescription } : {}),
      ...(appearanceHints ? { appearance_hints: appearanceHints } : {}),
    },
  });

  return {
    taskId: result.task_id,
    sessionId: result.session_id,
    message: result.message,
  };
}

// ==========================================
// Video Download
// ==========================================

export interface VideoDownloadURLResponse {
  session_id: string;
  kind: string;
  download_url: string;
  filename: string;
  expires_at: string;
}

/**
 * Fetches a time-limited signed URL for downloading a session's video.
 * @param sessionId - The session ID
 * @param profileId - The profile ID (used for GCS path resolution)
 * @param kind - "merged" (video only) or "hardsubbed" (with guidance overlay)
 */
export async function fetchVideoDownloadURL(
  sessionId: string,
  profileId: number,
  kind: "merged" | "hardsubbed" | "encoded" = "merged",
): Promise<VideoDownloadURLResponse> {
  return apiClient<VideoDownloadURLResponse>(
    `/video-download/${sessionId}?kind=${kind}&profile_id=${profileId}`,
  );
}

// ==========================================
// Highlights
// ==========================================

import type { HighlightResult } from "./history";

export interface GenerateHighlightResult {
  task_id: string;
  session_id: string;
  message: string;
}

/**
 * Triggers server-side highlight video generation for a session.
 */
export async function generateHighlight(
  sessionId: string,
  profileId: number,
  maxDuration: number = 60,
): Promise<GenerateHighlightResult> {
  return apiClient<GenerateHighlightResult>("/generate-highlight", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      profile_id: profileId,
      max_duration: maxDuration,
    },
  });
}

/**
 * Fetches highlight results for a session. Returns all variants and their status.
 */
export async function fetchHighlightResults(
  sessionId: string,
): Promise<HighlightResult[]> {
  return apiClient<HighlightResult[]>(`/highlight/${sessionId}`);
}

/**
 * Fetches a signed download URL for a specific highlight result.
 */
export async function fetchHighlightDownloadURL(
  highlightId: number,
): Promise<VideoDownloadURLResponse> {
  return apiClient<VideoDownloadURLResponse>(
    `/highlight-download/${highlightId}`,
  );
}

// ==========================================
// Retry Analysis
// ==========================================

export interface RetryAnalysisResponse {
  message: string;
  task_id: string;
  session_id: string;
}

/**
 * Re-triggers video analysis for a failed session using existing GCS files.
 */
export async function retryAnalysis(
  sessionId: string,
  profileId: number,
): Promise<RetryAnalysisResponse> {
  return apiClient<RetryAnalysisResponse>("/retry-analysis", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      profile_id: profileId,
    },
  });
}

// ==========================================
// Archive History
// ==========================================

/**
 * Archives a history record (soft-delete). The record won't appear in the history list.
 */
export async function archiveHistory(id: number): Promise<void> {
  return apiClient(`/history/${id}/archive`, {
    method: "POST",
  });
}

// ==========================================
// Generate Hardsubbed Video
// ==========================================

export interface GenerateHardSubResponse {
  message: string;
  task_id: string;
  session_id: string;
}

/**
 * Triggers generation of a hardsubbed video with burned-in subtitles.
 */
export async function generateHardSub(
  sessionId: string,
  profileId: number,
  enableTts: boolean = false,
): Promise<GenerateHardSubResponse> {
  return apiClient<GenerateHardSubResponse>("/generate-hardsub", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      profile_id: profileId,
      enable_tts: enableTts,
    },
  });
}

// ==========================================
// Parse Workout Image (Whiteboard OCR)
// ==========================================

export interface ParseWorkoutImageResponse {
  wod_description: string;
  movements: string[];
  raw_text: string;
}

/**
 * Sends a whiteboard photo to the backend for Gemini-based OCR + typo correction.
 * Returns structured WOD data including description, movements, and raw text.
 *
 * @param imageUri - Local file URI of the image (camera or gallery)
 */
export async function parseWorkoutImage(
  imageUri: string,
): Promise<ParseWorkoutImageResponse> {
  const url = `${API_BASE_URL}/parse-workout-image`;

  const formData = new FormData();
  const filename = imageUri.split("/").pop() || "whiteboard.jpg";
  const ext = filename.split(".").pop()?.toLowerCase();
  const mimeType = ext === "png" ? "image/png" : "image/jpeg";

  formData.append("image", {
    uri: imageUri,
    name: filename,
    type: mimeType,
  } as any);

  const headers: Record<string, string> = {};

  const token = await getToken();
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(url, {
    method: "POST",
    headers,
    body: formData,
  });

  if (res.status === 401) {
    const { useAuthStore } = await import("@/features/auth/useAuthStore");
    useAuthStore.getState().handleUnauthorized();
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    let errorText = res.statusText;
    try {
      errorText = await res.text();
    } catch {}
    throw new Error(
      `API Error [${res.status}]: ${errorText || res.statusText}`,
    );
  }

  return res.json() as Promise<ParseWorkoutImageResponse>;
}

// ==========================================
// Parse Appearance Image
// ==========================================

/**
 * Sends a person photo to the backend for Gemini-based appearance parsing.
 * Returns structured appearance cues (persistent, session, removable).
 *
 * @param imageUri - Local file URI of the image (camera or gallery)
 */
export async function parseAppearanceImage(
  imageUri: string,
): Promise<{ appearance: string }> {
  const url = `${API_BASE_URL}/appearance-from-image`;

  const formData = new FormData();
  const filename = imageUri.split("/").pop() || "person.jpg";
  const ext = filename.split(".").pop()?.toLowerCase();
  const mimeType = ext === "png" ? "image/png" : "image/jpeg";

  formData.append("image", {
    uri: imageUri,
    name: filename,
    type: mimeType,
  } as any);

  const headers: Record<string, string> = {};

  const token = await getToken();
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(url, {
    method: "POST",
    headers,
    body: formData,
  });

  if (res.status === 401) {
    const { useAuthStore } = await import("@/features/auth/useAuthStore");
    useAuthStore.getState().handleUnauthorized();
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    let errorText = res.statusText;
    try {
      errorText = await res.text();
    } catch {}
    throw new Error(
      `API Error [${res.status}]: ${errorText || res.statusText}`,
    );
  }

  return res.json() as Promise<{ appearance: string }>;
}

// ==========================================
// Debug Telemetry
// ==========================================

import type { TelemetrySession } from "../debug/types";

/**
 * Uploads a debug telemetry session JSON to the backend for GCS storage.
 * The backend writes it to gs://{bucket}/debug/telemetry/{profileId}/{sessionId}.json.
 */
export async function uploadDebugTelemetry(
  session: TelemetrySession,
): Promise<void> {
  await apiClient<{ ok: boolean }>("/debug/telemetry", {
    method: "POST",
    bodyPayload: session,
  });
}

// ==========================================
// Related WODs
// ==========================================

export type RelatedWODsResponse =
  components["schemas"]["controllers.RelatedWODsResponse"];

export interface FetchRelatedWodsParams {
  profileId: number;
  movement?: string;
  weightKg?: number;
  sessionId?: string;
  limit?: number;
}

export async function fetchRelatedWods(
  params: FetchRelatedWodsParams,
): Promise<RelatedWODsResponse> {
  const query = new URLSearchParams();
  query.append("profile_id", params.profileId.toString());
  if (params.movement) query.append("movement", params.movement);
  if (params.weightKg !== undefined && !isNaN(params.weightKg)) {
    query.append("weight_kg", params.weightKg.toString());
  }
  if (params.sessionId) query.append("session_id", params.sessionId);
  if (params.limit !== undefined)
    query.append("limit", params.limit.toString());

  return apiClient<RelatedWODsResponse>(`/related-wods?${query.toString()}`);
}

// ==========================================
// Pre-WOD Strategy & Scaling Advice
// ==========================================

export interface MuscleReadinessItem {
  group: string;
  name_ko: string;
  fatigue_score: number;
  state: "fresh" | "moderate" | "fatigued" | "exhausted";
  state_ko: string;
  note: string;
}

export interface TargetRPEInfo {
  score: number;
  label: string;
  pacing_strategy: string;
}

export interface ScalingAdviceItem {
  movement: string;
  recommendation: string;
  detail: string;
}

export interface MobilityWarmupItem {
  title: string;
  target_area: string;
  duration: string;
  reason: string;
}

export interface EvidenceCounts {
  total_sessions: number;
  valid_sessions: number;
  excluded_sessions: number;
  unresolved_time_sessions: number;
}

export interface PreWodAdviceResponse {
  profile_id: number;
  overall_fatigue_score?: number | null;
  overall_state: string;
  overall_state_ko: string;
  muscle_readiness?: MuscleReadinessItem[];
  target_rpe?: TargetRPEInfo | null;
  scaling_advice: ScalingAdviceItem[];
  mobility_warmup: MobilityWarmupItem[];
  overall_summary: string;
  advice_code?: string;
  evidence_status?: "no_history" | "insufficient" | "partial" | "complete" | string;
  evidence?: EvidenceCounts;
  as_of?: string;
  last_workout_at?: string | null;
}

export interface PreWodAdviceRequest {
  profile_id: number;
  wod_description?: string;
  movements?: string[];
}

export async function fetchPreWodAdvice(
  req: PreWodAdviceRequest,
): Promise<PreWodAdviceResponse> {
  return apiClient<PreWodAdviceResponse>("/strategies/pre-wod-advice", {
    method: "POST",
    bodyPayload: req,
  });
}
