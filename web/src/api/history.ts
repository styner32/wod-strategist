import { api } from './client';

export interface AnalysisResult {
  id: number;
  session_id: string;
  profile_id: number;
  status: string;
  workout_type: string;
  output: string;
  created_at: string;
  updated_at: string;
  archived_at: string | null;
}

export interface ChunkAnalysisResult {
  id: number;
  session_id: string;
  profile_id: number;
  status: string;
  gcs_uri: string;
  output: string;
  start_secs: number;
  end_secs: number;
  heart_rate_bpm: number;
  created_at: string;
}

export interface VideoDownloadResponse {
  session_id: string;
  kind: string;
  download_url: string;
  filename: string;
  expires_at: string;
}

export const historyApi = {
  list: (profileId: number) =>
    api.get<AnalysisResult[]>(`/history?profile_id=${profileId}`),

  getAnalysis: (sessionId: string) =>
    api.get<AnalysisResult[]>(`/analysis/${sessionId}`),

  getChunkAnalysis: (sessionId: string) =>
    api.get<ChunkAnalysisResult[]>(`/chunk-analysis/${sessionId}`),

  getVideoDownloadUrl: (sessionId: string, profileId: number) =>
    api.get<VideoDownloadResponse>(
      `/video-download/${sessionId}?profile_id=${profileId}`,
    ),
};
