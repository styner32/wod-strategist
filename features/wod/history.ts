import { apiClient } from "./api";

export interface AnalysisResult {
  id: number;
  session_id: string;
  profile_id?: number;
  analysis_type: string; // "wod" | "injury_supplement"
  status: string;
  output: string;
  injury_output?: string;
  highlight_segments?: string;
  available_videos?: string[]; // ["merged", "hardsubbed", "encoded"]
  created_at: string;
  updated_at: string;
}

export async function fetchAnalysisHistory(profileId: number): Promise<AnalysisResult[]> {
  return apiClient<AnalysisResult[]>(`/history?profile_id=${profileId}`);
}

export interface HighlightResult {
  id: number;
  session_id: string;
  profile_id?: number;
  title: string;
  status: string; // PENDING, PROCESSING, COMPLETED, FAILED
  gcs_uri: string;
  music_gcs_uri: string;
  segments: string;
  duration_sec: number;
  output: string;
  created_at: string;
  updated_at: string;
}
