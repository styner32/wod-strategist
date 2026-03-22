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
  movements?: string[];
  injuries?: string[];
  mimeType?: string;
  workoutType?: WorkoutType;
}

export type UploadUrlResponse = Required<components["schemas"]["controllers.CreateUploadURLResponse"]>;
export type UploadCompleteResponse = Required<components["schemas"]["controllers.CompleteUploadResponse"]>;

export async function fetchMovements(): Promise<string[]> {
  return apiClient<string[]>("/movements");
}

export async function fetchInjuries(): Promise<string[]> {
  return apiClient<string[]>("/injuries");
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
  onProgress?: (progress: number) => void
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
  workoutType: string
): Promise<UploadCompleteResponse> {
  return apiClient<UploadCompleteResponse>("/upload-complete", {
    method: "POST",
    bodyPayload: {
      session_id: sessionId,
      gcs_uri: gcsUri,
      movements,
      injuries,
      workout_type: workoutType,
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
    movements = [],
    injuries = [],
    mimeType = "video/mp4",
    workoutType = "wod",
  } = options;
  const filename = fileUri.split("/").pop() || "workout.mp4";

  console.log("🚀 Starting upload process for:", filename);

  const { upload_url, gcs_uri } = await getUploadUrl(sessionId, filename);
  console.log("✅ Got Signed URL");

  await uploadToGcs(upload_url, fileUri, mimeType, onProgress);
  console.log("✅ Uploaded to GCS");

  const result = await notifyUploadComplete(
    sessionId,
    gcs_uri,
    movements,
    injuries,
    workoutType
  );
  console.log("✅ Analysis Started:", result);

  return {
    taskId: result.task_id,
    sessionId: result.session_id,
  };
}
