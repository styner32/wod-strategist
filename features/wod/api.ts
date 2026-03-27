import {
  createUploadTask,
  FileSystemUploadType,
  UploadProgressData,
} from "expo-file-system/legacy";

import type { WorkoutType } from "./workoutType";
import type { components } from "./schema";

const API_BASE_URL =
  process.env.EXPO_PUBLIC_API_URL || "http://localhost:8088/api/v1";
const API_SECRET_KEY = process.env.EXPO_PUBLIC_API_KEY || "";

// ==========================================
// Core API Client
// ==========================================

export interface ApiRequestOptions extends RequestInit {
  bodyPayload?: any; // JSON body
}

/**
 * A tiny fetch wrapper that injects the base URL and API Key header,
 * and automatically parses JSON or throws on errors.
 */
export async function apiClient<T = any>(
  endpoint: string,
  options: ApiRequestOptions = {}
): Promise<T> {
  const { bodyPayload, headers: customHeaders, ...fetchOptions } = options;

  const url = `${API_BASE_URL}${endpoint.startsWith("/") ? endpoint : `/${endpoint}`}`;
  
  const headers = new Headers(customHeaders);
  headers.set("X-API-Key", API_SECRET_KEY);

  if (bodyPayload && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  const res = await fetch(url, {
    ...fetchOptions,
    headers,
    body: bodyPayload ? JSON.stringify(bodyPayload) : fetchOptions.body,
  });

  if (!res.ok) {
    let errorText = res.statusText;
    try {
      errorText = await res.text();
    } catch {}
    throw new Error(`API Error [${res.status}]: ${errorText || res.statusText}`);
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
  profileId?: number;
  startSecs?: number;
  endSecs?: number;
}

export type UploadUrlResponse = Required<components["schemas"]["controllers.CreateUploadURLResponse"]>;
export type UploadCompleteResponse = Required<components["schemas"]["controllers.CompleteUploadResponse"]>;

export interface ChunkAnalysisResult {
  id: number;
  session_id: string;
  status: string;
  output: string;
  start_secs?: number;
  end_secs?: number;
  created_at: string;
  updated_at: string;
}

export async function fetchChunkAnalysis(sessionId: string): Promise<ChunkAnalysisResult[]> {
  return apiClient<ChunkAnalysisResult[]>(`/chunk-analysis/${sessionId}`);
}

export async function fetchMovements(): Promise<string[]> {
  return apiClient<string[]>("/movements");
}

export async function fetchInjuries(): Promise<string[]> {
  return apiClient<string[]>("/injuries");
}

// ==========================================
// Profile API
// ==========================================

export interface ProfileResponse {
  id: number;
  birth_year: number;
  birth_month: number;
  birth_day: number;
  gender: string;
  height_cm: number;
  weight_kg: number;
}

export interface CreateProfileRequest {
  birth_year: number;
  birth_month: number;
  birth_day: number;
  gender: string;
  height_cm: number;
  weight_kg: number;
}

export async function createProfile(
  data: CreateProfileRequest
): Promise<ProfileResponse> {
  return apiClient<ProfileResponse>("/profiles", {
    method: "POST",
    bodyPayload: data,
  });
}

export async function getProfile(id: number): Promise<ProfileResponse> {
  return apiClient<ProfileResponse>(`/profiles/${id}`);
}

/** Step 1: Request a signed upload URL from our API */
export async function getUploadUrl(
  sessionId: string,
  filename: string
): Promise<UploadUrlResponse> {
  return apiClient<UploadUrlResponse>("/upload-url", {
    method: "POST",
    bodyPayload: { session_id: sessionId, filename },
  });
}

/** Step 2: Stream the binary payload directly into GCS using Expo FileSystem */
export async function uploadToGcs(
  uploadUrl: string,
  fileUri: string,
  mimeType: string,
  onProgress?: (progress: number) => void,
  onCancelReady?: (cancel: () => Promise<void>) => void
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
    }
  );

  // Expose the cancel function to the caller before starting
  onCancelReady?.(() => uploadTask.cancelAsync());

  const response = await uploadTask.uploadAsync();

  if (!response) {
    throw new Error("Failed to upload to GCS: No response from upload task.");
  }

  if (response.status < 200 || response.status >= 300) {
    throw new Error(
      `Failed to upload to GCS: HTTP ${response.status} ${response.body || ""}`
    );
  }
}

/** Step 3: Tell the backend the video is uploaded and ready for processing */
export async function notifyUploadComplete(
  sessionId: string,
  gcsUri: string,
  movements: string[],
  injuries: string[],
  workoutType: string,
  profileId?: number
): Promise<UploadCompleteResponse> {
  return apiClient<UploadCompleteResponse>("/upload-complete", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      gcs_uri: gcsUri,
      movements,
      injuries,
      workout_type: workoutType,
      ...(profileId ? { profile_id: profileId } : {}),
    },
  });
}

export async function notifyChunkUploadComplete(
  sessionId: string,
  gcsUri: string,
  movements: string[],
  injuries: string[],
  workoutType: string,
  profileId?: number,
  startSecs?: number,
  endSecs?: number
): Promise<UploadCompleteResponse> {
  return apiClient<UploadCompleteResponse>("/chunk-complete", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      gcs_uri: gcsUri,
      movements,
      injuries,
      workout_type: workoutType,
      ...(profileId ? { profile_id: profileId } : {}),
      ...(startSecs !== undefined ? { start_secs: startSecs } : {}),
      ...(endSecs !== undefined ? { end_secs: endSecs } : {}),
    },
  });
}

/**
 * Orchestrates the full 3-step workout upload flow.
 */
export async function processWorkoutVideo(
  fileUri: string,
  sessionId: string = "session_dev_001",
  options: ProcessWorkoutVideoOptions = {}
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

  console.log("🚀 Starting upload process for:", filename);

  const { upload_url, gcs_uri } = await getUploadUrl(sessionId, filename);
  console.log("✅ Got Signed URL");

  await uploadToGcs(upload_url, fileUri, mimeType, onProgress, onCancelReady);
  console.log("✅ Uploaded to GCS");

  const result = await notifyUploadComplete(
    sessionId,
    gcs_uri,
    movements,
    injuries,
    workoutType,
    profileId
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
  options: ProcessWorkoutVideoOptions = {}
): Promise<UploadResult> {
  const {
    movements = [],
    injuries = [],
    mimeType = "video/mp4",
    workoutType = "wod",
    profileId,
    startSecs,
    endSecs,
  } = options;
  const filename = fileUri.split("/").pop() || "chunk.mp4";

  const { upload_url, gcs_uri } = await getUploadUrl(sessionId, filename);
  await uploadToGcs(upload_url, fileUri, mimeType);

  const result = await notifyChunkUploadComplete(
    sessionId,
    gcs_uri,
    movements,
    injuries,
    workoutType,
    profileId,
    startSecs,
    endSecs
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
    profileId?: number;
  } = {}
): Promise<MergeChunksResult> {
  const {
    workoutType = "wod",
    movements = [],
    injuries = [],
    profileId,
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
      ...(profileId ? { profile_id: profileId } : {}),
    },
  });

  return {
    taskId: result.task_id,
    sessionId: result.session_id,
    message: result.message,
  };
}
