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
  available_videos?: string[];
  wod_description?: string;
}

export interface ChunkAnalysisResult {
  id: number;
  session_id: string;
  profile_id: number;
  status: string;
  gcs_uri?: string;
  file_path?: string;
  output: string;
  exercise_type?: string | null;
  observed_signals?: string | Record<string, unknown> | null;
  start_secs?: number | null;
  end_secs?: number | null;
  media_start_secs?: number | null;
  media_end_secs?: number | null;
  heart_rate_bpm: number;
  motion_score?: number | null;
  structured_inference?: StructuredChunkInference | null;
  updated_at?: string;
  created_at: string;
}

export interface StructuredChunkInference {
  evidence_id?: string;
  schema_version?: string;
  activity_state?: string;
  observed_movement_name?: string | null;
  movement_confidence?: number | null;
  visible_evidence?: unknown;
  alternative_movement_names?: string[];
  fatigue_state?: string | null;
  fatigue_confidence?: number | null;
  coverage_status?: string;
  [key: string]: unknown;
}

export interface SessionAnalysisResponse {
  session_id: string;
  analysis?: AnalysisResult | null;
  chunks?: ChunkAnalysisResult[];
  feedback?: AnalysisFeedback[];
  movement_hints?: string[];
  additional_observed_movements?: string[];
  coverage_status?: string;
  corrections_updated_at?: string | null;
}

export type FeedbackTargetType = 'session' | 'chunk';

export interface FeedbackCorrection {
  accurate?: boolean;
  movement_name?: string | null;
  activity_state?: string | null;
  fatigue_state?: string | null;
}

export interface AnalysisFeedback {
  id: number;
  session_id: string;
  profile_id?: number;
  feedback_key?: string;
  target_type: FeedbackTargetType;
  target_id?: number | null;
  chunk_id?: number | null;
  category: string;
  correction?: FeedbackCorrection | null;
  corrected_movement?: string | null;
  corrected_activity_state?: string | null;
  corrected_fatigue_state?: string | null;
  note?: string | null;
  original_prediction?: Record<string, unknown> | null;
  consent_to_improve?: boolean;
  revision: number;
  reanalysis_run_id?: number | null;
  source_reanalysis_run_id?: number | null;
  retracted?: boolean;
  retracted_at?: string | null;
  created_at: string;
  updated_at?: string;
}

export interface FeedbackCreateRequest {
  target_type: FeedbackTargetType;
  chunk_id?: number;
  category: string;
  correction?: FeedbackCorrection;
  note?: string;
  client_request_id: string;
  consent_to_improve?: boolean;
  reanalysis_run_id?: number;
}

export interface FeedbackUpdateRequest {
  client_request_id: string;
  expected_revision: number;
  category?: string;
  correction: FeedbackCorrection;
  note?: string;
  consent_to_improve?: boolean;
  reanalysis_run_id?: number;
}

export type ReanalysisStatus =
  | 'QUEUED'
  | 'RUNNING'
  | 'COMPLETED'
  | 'FAILED'
  | 'VIDEO_UNAVAILABLE'
  | 'INTERVAL_UNAVAILABLE';

export interface ChunkReanalysisCandidate {
  exercise_type?: string | null;
  output?: string;
  observed_signals?: Record<string, unknown> | null;
  structured_inference?: StructuredChunkInference | null;
  [key: string]: unknown;
}

export interface ReanalysisTokenUsage {
  prompt_tokens?: number;
  candidate_tokens?: number;
  total_tokens?: number;
}

export interface ChunkReanalysisRun {
  id?: number;
  run_id?: number;
  task_id?: string;
  session_id?: string;
  chunk_id?: number;
  status: ReanalysisStatus;
  source_kind?: 'session_video' | 'chunk';
  media_start_secs?: number | null;
  media_end_secs?: number | null;
  candidate?: ChunkReanalysisCandidate | null;
  candidate_result?: ChunkReanalysisCandidate | null;
  candidate_output?: string | null;
  candidate_exercise_type?: string | null;
  model?: string | null;
  prompt_version?: string | null;
  prompt_hash?: string | null;
  schema_version?: string | null;
  input_tokens?: number | null;
  output_tokens?: number | null;
  token_usage?: ReanalysisTokenUsage | null;
  duration_ms?: number | null;
  error?: string | null;
  created_at?: string;
  started_at?: string | null;
  completed_at?: string | null;
  updated_at?: string;
}

export interface ChunkPlayUrlResponse {
  session_id?: string;
  chunk_id?: number;
  play_url?: string;
  download_url?: string;
  source_kind?: 'session_video' | 'chunk';
  media_start_secs?: number | null;
  media_end_secs?: number | null;
  expires_at?: string;
}

export type FeedbackListResponse =
  | AnalysisFeedback[]
  | {
      current: AnalysisFeedback[];
      history: AnalysisFeedback[];
      has_active_corrections: boolean;
    };

export interface FeedbackMutationResponse {
  feedback: AnalysisFeedback;
}

export type ReanalysisListResponse =
  | ChunkReanalysisRun[]
  | { runs: ChunkReanalysisRun[] };

export interface VideoDownloadResponse {
  session_id: string;
  kind: string;
  download_url: string;
  filename: string;
  expires_at: string;
}

export type VideoKind = 'merged' | 'hardsubbed' | 'encoded';

export const historyApi = {
  list: (profileId: number) =>
    api.get<AnalysisResult[]>(`/history?profile_id=${profileId}`),

  getAnalysis: (sessionId: string) =>
    api.get<AnalysisResult[]>(`/analysis/${sessionId}`),

  getChunkAnalysis: (sessionId: string) =>
    api.get<ChunkAnalysisResult[]>(`/chunk-analysis/${sessionId}`),

  getSessionAnalysis: (sessionId: string) =>
    api.get<SessionAnalysisResponse>(`/sessions/${sessionId}/analysis`),

  getMovementSuggestions: () => api.get<string[]>('/movements'),

  listFeedback: (sessionId: string) =>
    api.get<FeedbackListResponse>(`/sessions/${sessionId}/feedback`),

  createFeedback: (sessionId: string, body: FeedbackCreateRequest) =>
    api.post<FeedbackMutationResponse>(`/sessions/${sessionId}/feedback`, body),

  updateFeedback: (
    sessionId: string,
    feedbackId: number,
    body: FeedbackUpdateRequest,
  ) => api.patch<FeedbackMutationResponse>(
    `/sessions/${sessionId}/feedback/${feedbackId}`,
    body,
  ),

  deleteFeedback: (
    sessionId: string,
    feedbackId: number,
    expectedRevision: number,
    clientRequestId: string,
  ) => api.delete<FeedbackMutationResponse>(`/sessions/${sessionId}/feedback/${feedbackId}`, {
    client_request_id: clientRequestId,
    expected_revision: expectedRevision,
  }),

  getChunkPlayUrl: (sessionId: string, chunkId: number) =>
    api.get<ChunkPlayUrlResponse>(
      `/sessions/${sessionId}/chunks/${chunkId}/play-url`,
    ),

  createChunkReanalysis: (
    sessionId: string,
    chunkId: number,
    clientRequestId: string,
  ) => api.post<ChunkReanalysisRun>(
    `/sessions/${sessionId}/chunks/${chunkId}/reanalyses`,
    { client_request_id: clientRequestId },
  ),

  listChunkReanalyses: (sessionId: string, chunkId: number) =>
    api.get<ReanalysisListResponse>(
      `/sessions/${sessionId}/chunks/${chunkId}/reanalyses`,
    ),

  getChunkReanalysis: (sessionId: string, chunkId: number, runId: number) =>
    api.get<ChunkReanalysisRun>(
      `/sessions/${sessionId}/chunks/${chunkId}/reanalyses/${runId}`,
    ),

  getVideoDownloadUrl: (
    sessionId: string,
    profileId: number,
    kind: VideoKind = 'merged',
  ) =>
    api.get<VideoDownloadResponse>(
      `/video-download/${sessionId}?profile_id=${profileId}&kind=${kind}`,
    ),
};
