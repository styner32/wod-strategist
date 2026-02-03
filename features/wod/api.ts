// import { File } from "expo-file-system"; // Not needed if using Blob

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

  // 2. Upload to GCS (Directly) using XHR for Progress
  // Read local file as Blob first
  const fileResponse = await fetch(fileUri);
  const blob = await fileResponse.blob();

  await new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", upload_url);
    xhr.setRequestHeader("Content-Type", "video/mp4");

    if (onProgress) {
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) {
          onProgress(event.loaded / event.total);
        }
      };
    }

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(
          new Error(
            `Failed to upload to GCS: ${xhr.status} ${xhr.responseText}`
          )
        );
      }
    };

    xhr.onerror = () => reject(new Error("Network error during upload"));
    xhr.send(blob);
  });

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
