import { apiClient } from "@/features/wod/api";

export interface RecommendedStretchSession {
  session_id: string;
  analysis_id: number;
  analysis_type: string;
  target_area: string;
  reason: string;
  duration_hint?: string;
  caution?: string;
  provisional: boolean;
  created_at: string;
}

export interface RecommendedStretch {
  id: number;
  name: string;
  target_area: string;
  description: string;
  duration_hint: string;
  caution: string;
  image_url?: string;
  video_url?: string;
  aliases: string[];
  created_at: string;
  updated_at: string;
  normalized_key: string;
  in_catalog: boolean;
  session_count: number;
  last_recommended_at: string;
  sessions: RecommendedStretchSession[];
}

export async function fetchRecommendedStretches(
  profileId: number,
  limit?: number
): Promise<RecommendedStretch[]> {
  const params = new URLSearchParams({ profile_id: String(profileId) });
  if (limit) {
    params.set("limit", String(limit));
  }
  return apiClient<RecommendedStretch[]>(`/stretches/recommended?${params.toString()}`);
}

export async function fetchRecommendedStretch(
  profileId: number,
  key: string
): Promise<RecommendedStretch | null> {
  const params = new URLSearchParams({
    profile_id: String(profileId),
    key,
  });
  const results = await apiClient<RecommendedStretch[]>(`/stretches/recommended?${params.toString()}`);
  return results && results.length > 0 ? results[0] : null;
}
