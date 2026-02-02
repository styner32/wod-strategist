import { Platform } from "react-native";

// Replace with your computer's local network IP for physical device testing
const LOCAL_IP = "192.168.219.137";
const API_BASE_URL = `http://${LOCAL_IP}:8088/api/v1`;

// TODO: Securely manage this key
const API_KEY = "test-api-key"; 

export interface AnalysisResult {
  id: number;
  session_id: string;
  status: string;
  output: string;
  created_at: string;
  updated_at: string;
}

export async function fetchAnalysisHistory(): Promise<AnalysisResult[]> {
  const res = await fetch(`${API_BASE_URL}/history`, {
    headers: {
      "X-API-Key": API_KEY,
    },
  });

  if (!res.ok) {
    throw new Error(`Failed to fetch history: ${res.statusText}`);
  }

  return res.json();
}
