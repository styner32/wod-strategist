import { create } from "zustand";
import {
  signup as apiSignup,
  login as apiLogin,
  logout as apiLogout,
  deleteAccount as apiDeleteAccount,
} from "./api";
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

  /** Check SecureStore on app launch */
  hydrate: () => Promise<void>;
  /** Sign up, auto-create a default profile, log in */
  signup: (username: string, password: string) => Promise<void>;
  /** Log in with existing credentials */
  login: (username: string, password: string) => Promise<void>;
  /** Log out and clear all local state */
  logout: () => Promise<void>;
  /** Delete account permanently */
  deleteAccount: (password: string) => Promise<void>;
  /** Called on 401 — clears auth state without server call */
  handleUnauthorized: () => void;
}

// ==========================================
// Store
// ==========================================

export const useAuthStore = create<AuthState>((set) => ({
  isReady: false,
  isLoggedIn: false,
  userId: null,

  hydrate: async () => {
    try {
      const [token, userId] = await Promise.all([getToken(), getUserID()]);
      if (token && userId) {
        set({ isReady: true, isLoggedIn: true, userId });
      } else {
        set({ isReady: true, isLoggedIn: false, userId: null });
      }
    } catch {
      set({ isReady: true, isLoggedIn: false, userId: null });
    }
  },

  signup: async (username, password) => {
    const res = await apiSignup(username, password);
    set({ isLoggedIn: true, userId: res.user_id });
  },

  login: async (username, password) => {
    const res = await apiLogin(username, password);
    set({ isLoggedIn: true, userId: res.user_id });
  },

  logout: async () => {
    await apiLogout();
    set({ isLoggedIn: false, userId: null });
  },

  deleteAccount: async (password) => {
    await apiDeleteAccount(password);
    set({ isLoggedIn: false, userId: null });
  },

  handleUnauthorized: () => {
    clearToken().catch(() => {});
    clearUserID().catch(() => {});
    set({ isLoggedIn: false, userId: null });
  },
}));
