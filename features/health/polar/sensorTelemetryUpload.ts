import { createUploadQueue } from "../../debug/telemetryUpload";
import { getUploadUrl, uploadToGcs } from "../../wod/api";

/**
 * Uploads a single sensor telemetry file to GCS via a signed upload URL.
 */
export async function uploadSensorTelemetry(
  sessionId: string,
  profileId: number,
  fileUri: string,
): Promise<void> {
  const { upload_url } = await getUploadUrl(
    sessionId,
    "sensor_telemetry.ndjson",
    profileId,
  );
  await uploadToGcs(upload_url, fileUri, "application/x-ndjson");
}

export const sensorUploadQueue = createUploadQueue({
  subDir: "sensor",
  logTag: "📡 Sensor telemetry",
  uploadFn: async (entry) => {
    if (entry.profileId === undefined) {
      throw new Error(
        `Cannot upload sensor telemetry for ${entry.sessionId}: missing profileId`,
      );
    }
    await uploadSensorTelemetry(entry.sessionId, entry.profileId, entry.filePath);
  },
});

/**
 * Enqueue a sensor telemetry file for upload.
 */
export async function enqueueSensorUpload(
  sessionId: string,
  profileId: number,
  filePath: string,
): Promise<void> {
  await sensorUploadQueue.enqueueUpload(sessionId, filePath, profileId);
}

/**
 * Flush all pending sensor telemetry uploads.
 */
export async function flushSensorUploads(): Promise<void> {
  await sensorUploadQueue.flushPendingUploads();
}
