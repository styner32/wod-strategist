import { apiClient } from "@/features/wod/api";
import {
  setToken,
  setUserID,
  clearToken,
  clearUserID,
} from "./storage";

// ==========================================
// Types
// ==========================================

interface AuthResponse {
  token: string;
  user_id: string;
}

// ==========================================
// Auth API
// ==========================================

/**
 * Create a new account. Stores the returned token + user_id in SecureStore.
 */
export async function signup(
  username: string,
  password: string
): Promise<AuthResponse> {
  const res = await apiClient<AuthResponse>("/auth/signup", {
    method: "POST",
    bodyPayload: { username, password },
  });
  await setToken(res.token);
  await setUserID(res.user_id);
  return res;
}

/**
 * Log in to an existing account. Stores the returned token + user_id.
 */
export async function login(
  username: string,
  password: string
): Promise<AuthResponse> {
  const res = await apiClient<AuthResponse>("/auth/login", {
    method: "POST",
    bodyPayload: { username, password },
  });
  await setToken(res.token);
  await setUserID(res.user_id);
  return res;
}

/**
 * Log out the current user. Clears local token + user_id.
 */
export async function logout(): Promise<void> {
  try {
    await apiClient("/auth/logout", { method: "POST" });
  } catch {
    // If the server rejects (e.g. token already expired), still clear local state
  }
  await clearToken();
  await clearUserID();
}

/**
 * Delete the current user's account. Requires password confirmation.
 * Clears all local auth state on success.
 */
export async function deleteAccount(password: string): Promise<void> {
  await apiClient("/auth/account", {
    method: "DELETE",
    bodyPayload: { password },
  });
  await clearToken();
  await clearUserID();
}
