import { File } from "expo-file-system";

const API_BASE_URL = process.env.EXPO_PUBLIC_API_URL || "http://localhost:8088/api/v1";
const API_SECRET_KEY = process.env.EXPO_PUBLIC_API_KEY || "";

export interface UploadResult {
  taskId: string;
  sessionId: string;
}

export async function processWorkoutVideo(
  fileUri: string,
  sessionId: string = "session_dev_001"
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

  // 2. Upload to GCS (Directly) — recommended API: File + fetch with arrayBuffer()
  const file = new File(fileUri);
  const body = await file.arrayBuffer();
  const uploadRes = await fetch(upload_url, {
    method: "PUT",
    headers: {
      "Content-Type": "video/mp4",
      "X-API-Key": API_SECRET_KEY,
    },
    body,
  });

  if (uploadRes.status >= 300) {
    const err = await uploadRes.text();
    throw new Error(`Failed to upload to GCS: ${err}`);
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
