import { api } from './client';

const MAX_FILE_SIZE = 2 * 1024 * 1024 * 1024; // 2 GB

export interface UploadUrlResponse {
  upload_url: string;
  gcs_uri: string;
}

export interface UploadCompleteResponse {
  message: string;
  task_id: string;
  session_id: string;
}

export function validateFileSize(file: File): string | null {
  if (file.size > MAX_FILE_SIZE) {
    return `File too large. Maximum size is 2 GB, your file is ${(file.size / (1024 * 1024 * 1024)).toFixed(1)} GB.`;
  }
  return null;
}

export const uploadApi = {
  getUploadUrl: (sessionId: string, filename: string, profileId: number) =>
    api.post<UploadUrlResponse>('/upload-url', {
      session_id: sessionId,
      filename,
      profile_id: profileId,
    }),

  completeUpload: (params: {
    session_id: string;
    gcs_uri: string;
    profile_id: number;
    workout_type: string;
    movements: string[];
    injuries: string[];
  }) => api.post<UploadCompleteResponse>('/upload-complete', params),
};

/**
 * Upload a file to GCS via a signed PUT URL.
 * Uses XMLHttpRequest for progress tracking.
 */
export function uploadToGCS(
  signedUrl: string,
  file: File,
  onProgress?: (percent: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();

    xhr.upload.addEventListener('progress', (e) => {
      if (e.lengthComputable && onProgress) {
        onProgress(Math.round((e.loaded / e.total) * 100));
      }
    });

    xhr.addEventListener('load', () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new Error(`Upload failed with status ${xhr.status}`));
      }
    });

    xhr.addEventListener('error', () => reject(new Error('Upload failed')));
    xhr.addEventListener('abort', () => reject(new Error('Upload cancelled')));

    xhr.open('PUT', signedUrl);
    xhr.setRequestHeader('Content-Type', file.type || 'video/mp4');
    xhr.send(file);
  });
}
