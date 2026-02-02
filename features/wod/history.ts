const API_BASE_URL =
  process.env.EXPO_PUBLIC_API_URL || "http://localhost:8088/api/v1";
const API_KEY = process.env.EXPO_PUBLIC_API_KEY || "";

export interface AnalysisResult {
  id: number;
  session_id: string;
  status: string;
  output: string;
  created_at: string;
  updated_at: string;
}

export async function fetchAnalysisHistory(): Promise<AnalysisResult[]> {
  const fullUrl = `${API_BASE_URL}/history`;
  const res = await fetch(fullUrl, {
    headers: {
      "X-API-Key": API_KEY,
    },
  });

  if (!res.ok) {
    throw new Error(`Failed to fetch history: ${fullUrl} ${res.statusText}`);
  }

  return res.json();
}
