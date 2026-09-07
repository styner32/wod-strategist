import { create } from "zustand";
import {
  signup as apiSignup,
  login as apiLogin,
  logout as apiLogout,
  deleteAccount as apiDeleteAccount,
} from "./api";
import { isTokenExpired } from "./jwt";
import {
  getToken,
  getUserID,
  clearToken,
  clearUserID,
} from "./storage";

// ==========================================
// Types
// ==========================================

interface AuthState {
  /** True after initial hydration from SecureStore */
  isReady: boolean;
  /** True when a valid token exists */
  isLoggedIn: boolean;
  /** The authenticated user's ID */
  userId: number | null;
  /** True when a workout video recording is actively in progress */
  isRecordingActive: boolean;
  /** True if a 401 occurred during active recording and logout was deferred */
  sessionExpiredDuringRecording: boolean;

  /** Check SecureStore on app launch and validate token expiry */
  hydrate: () => Promise<void>;
  /** Track whether recording is currently in progress */
  setRecordingActive: (active: boolean) => void;
  /** Sign up, auto-create a default profile, log in */
  signup: (username: string, password: string) => Promise<void>;
  /** Log in with existing credentials */
  login: (username: string, password: string) => Promise<void>;
  /** Log out and clear all local state */
  logout: () => Promise<void>;
  /** Delete account permanently */
  deleteAccount: (password: string) => Promise<void>;
  /** Called on 401 — clears auth state unless recording is active (in which case it defers) */
  handleUnauthorized: () => void;
  /** Executed after recording finishes to finalize a deferred 401 logout */
  finishDeferredUnauthorized: () => void;
}

// ==========================================
// Store
// ==========================================

export const useAuthStore = create<AuthState>((set, get) => ({
  isReady: false,
  isLoggedIn: false,
  userId: null,
  isRecordingActive: false,
  sessionExpiredDuringRecording: false,

  setRecordingActive: (active: boolean) => {
    set({ isRecordingActive: active });
  },

  hydrate: async () => {
    try {
      const [token, userId] = await Promise.all([getToken(), getUserID()]);
      if (token && userId && !isTokenExpired(token)) {
        set({ isReady: true, isLoggedIn: true, userId, sessionExpiredDuringRecording: false });
      } else {
        if (token && isTokenExpired(token)) {
          console.warn("🔐 Hydration: Stored JWT token has expired. Clearing auth state.");
          clearToken().catch(() => {});
          clearUserID().catch(() => {});
        }
        set({ isReady: true, isLoggedIn: false, userId: null, sessionExpiredDuringRecording: false });
      }
    } catch {
      set({ isReady: true, isLoggedIn: false, userId: null, sessionExpiredDuringRecording: false });
    }
  },

  signup: async (username, password) => {
    const res = await apiSignup(username, password);
    set({ isLoggedIn: true, userId: res.user_id, sessionExpiredDuringRecording: false });
  },

  login: async (username, password) => {
    const res = await apiLogin(username, password);
    set({ isLoggedIn: true, userId: res.user_id, sessionExpiredDuringRecording: false });
  },

  logout: async () => {
    await apiLogout();
    set({ isLoggedIn: false, userId: null, isRecordingActive: false, sessionExpiredDuringRecording: false });
  },

  deleteAccount: async (password) => {
    await apiDeleteAccount(password);
    set({ isLoggedIn: false, userId: null, isRecordingActive: false, sessionExpiredDuringRecording: false });
  },

  handleUnauthorized: () => {
    const { isRecordingActive } = get();
    if (isRecordingActive) {
      console.warn("⚠️ 401 Unauthorized received during active recording. Deferring logout until recording finishes.");
      set({ sessionExpiredDuringRecording: true });
      return;
    }

    clearToken().catch(() => {});
    clearUserID().catch(() => {});
    set({ isLoggedIn: false, userId: null, sessionExpiredDuringRecording: false });
  },

  finishDeferredUnauthorized: () => {
    clearToken().catch(() => {});
    clearUserID().catch(() => {});
    set({
      isLoggedIn: false,
      userId: null,
      isRecordingActive: false,
      sessionExpiredDuringRecording: false,
    });
  },
}));

