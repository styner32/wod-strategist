import { apiClient } from "./api";

export interface MuscleLoads {
  shoulders_push?: number;
  upper_pull_grip?: number;
  posterior_chain?: number;
  quads_squat?: number;
  core_midline?: number;
  cardio_metabolic?: number;
  [key: string]: number | undefined;
}

export interface SessionFatigue {
  overall_score: number;
  state: "fresh" | "moderate" | "fatigued" | "exhausted" | string;
  state_ko: string;
  muscles: MuscleLoads;
}

export interface AnalysisResult {
  id: number;
  session_id: string;
  profile_id?: number;
  analysis_type: string; // "wod" | "injury_supplement"
  status: string;
  output: string;
  injury_output?: string;
  highlight_segments?: string;
  session_score?: string;
  session_fatigue?: SessionFatigue;
  mobility_observations?: string;
  stretch_recommendations?: string;
  available_videos?: string[]; // ["merged", "hardsubbed", "encoded"]
  created_at: string;
  updated_at: string;
}

export async function fetchAnalysisHistory(
  profileId: number,
  limit?: number,
): Promise<AnalysisResult[]> {
  const params = new URLSearchParams({ profile_id: String(profileId) });
  if (limit) {
    params.set("limit", String(limit));
  }
  return apiClient<AnalysisResult[]>(`/history?${params.toString()}`);
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
