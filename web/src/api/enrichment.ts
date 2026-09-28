import { api } from "./client";
export interface SummaryContent {
  overview: string;
  strengths: string[];
  improvements: string[];
  limitations: string[];
}
export interface AnalysisSummary {
  status?: string;
  stage?: string;
  updated_at?: string;
  result?: SummaryContent;
  last_success?: SummaryContent;
  error?: string;
  failed?: number;
  succeeded?: number;
}
export interface AgenticObservation {
  target_status: string;
  activity: string;
  movement: string;
  direct_observation: string;
  evidence: { start: number; end: number; observation: string }[];
  continuity: string;
  noteworthy: string[];
  limitations: string[];
}
export interface AgenticHighlight {
  key: string;
  highlight: { start: string; end: string; movement?: string; reason?: string };
  status: string;
  result?: AgenticObservation;
  last_success?: AgenticObservation;
  error?: string;
  metrics?: {
    elapsed_seconds: number;
    finish_reason: string;
    agentic_observed: boolean;
    media_tool_calls: number;
    media_tool_responses: number;
    usage?: Record<string, number | null>;
  };
}
export interface EnrichmentResponse {
  enabled: boolean;
  summary: AnalysisSummary;
  analysis: {
    run_id?: string;
    status?: string;
    updated_at?: string;
    items?: AgenticHighlight[];
    error?: string;
  };
}
export const getEnrichment = (session: string) =>
  api.get<EnrichmentResponse>(
    `/sessions/${encodeURIComponent(session)}/agentic-highlights`,
  );
export const startEnrichment = (session: string, agentic: boolean) =>
  api.post<{ run_id: string }>(
    `/sessions/${encodeURIComponent(session)}/${agentic ? "agentic-highlights" : "analysis-summary"}`,
  );
export const enrichmentPending = (s?: string) =>
  ["pending", "preparing", "running"].includes(s ?? "");
