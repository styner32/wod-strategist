import {
  createUploadTask,
  FileSystemUploadType,
  UploadProgressData,
} from "expo-file-system/legacy";

const API_BASE_URL =
  process.env.EXPO_PUBLIC_API_URL || "http://localhost:8088/api/v1";
const API_SECRET_KEY = process.env.EXPO_PUBLIC_API_KEY || "";

export interface UploadResult {
  taskId: string;
  sessionId: string;
}

export async function fetchMovements(): Promise<string[]> {
  const res = await fetch(`${API_BASE_URL}/movements`, {
    headers: { "X-API-Key": API_SECRET_KEY },
  });
  if (!res.ok) throw new Error("Failed to fetch movements");
  return res.json();
}

export async function processWorkoutVideo(
  fileUri: string,
  sessionId: string = "session_dev_001",
  onProgress?: (progress: number) => void,
  movements?: string[]
): Promise<UploadResult> {
  const filename = fileUri.split("/").pop() || "workout.mp4";

  console.log("🚀 Starting upload process for:", filename);

  // 1. Get Signed URL
  const uploadUrlRes = await fetch(`${API_BASE_URL}/upload-url`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": API_SECRET_KEY,
    },
    body: JSON.stringify({
      session_id: sessionId,
      filename: filename,
    }),
  });

  if (!uploadUrlRes.ok) {
    const err = await uploadUrlRes.text();
    throw new Error(`Failed to get upload URL: ${err}`);
  }

  const { upload_url, gcs_uri } = await uploadUrlRes.json();
  console.log("✅ Got Signed URL");

  // 2. Upload to GCS (Directly) using FileSystem for streaming upload
  // This avoids loading the entire file into memory as a Blob
  const uploadTask = createUploadTask(
    upload_url,
    fileUri,
    {
      httpMethod: "PUT",
      headers: {
        "Content-Type": "video/mp4",
      },
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
      `Failed to upload to GCS: ${response.status} ${response.body || ""}`
    );
  }

  console.log("✅ Uploaded to GCS");

  // 3. Notify Complete
  const completeRes = await fetch(`${API_BASE_URL}/upload-complete`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": API_SECRET_KEY,
    },
    body: JSON.stringify({
      session_id: sessionId,
      gcs_uri: gcs_uri,
      movements: movements || [], // Pass movements metadata
    }),
  });

  if (!completeRes.ok) {
    const err = await completeRes.text();
    throw new Error(`Failed to start analysis: ${err}`);
  }

  const result = await completeRes.json();
  console.log("✅ Analysis Started:", result);

  return {
    taskId: result.task_id,
    sessionId: result.session_id,
  };
}
