import * as SecureStore from "expo-secure-store";
import { create } from "zustand";
import { createProfile } from "@/features/wod/api";

// ==========================================
// Types
// ==========================================

export type Gender = "male" | "female" | "other";

export interface UserProfile {
  birthYear: number | null;
  birthMonth: number | null;
  birthDay: number | null;
  gender: Gender | null;
  heightCm: number | null;
  weightKg: number | null;
}

interface ProfileState extends UserProfile {
  isLoaded: boolean;
  /** Backend ID returned after profile creation */
  backendId: number | null;

  /** Load profile from on-device storage (call once on app start) */
  hydrate: () => Promise<void>;

  /** Update one or more profile fields and persist */
  updateProfile: (patch: Partial<UserProfile>) => void;

  /** Clear all profile data */
  clearProfile: () => void;
}

// ==========================================
// Constants
// ==========================================

const STORAGE_KEY = "wod_user_profile";

const EMPTY_PROFILE: UserProfile = {
  birthYear: null,
  birthMonth: null,
  birthDay: null,
  gender: null,
  heightCm: null,
  weightKg: null,
};

// ==========================================
// Helpers
// ==========================================

interface PersistedProfile extends UserProfile {
  backendId?: number | null;
}

async function persistProfile(
  profile: UserProfile,
  backendId: number | null
): Promise<void> {
  try {
    const data: PersistedProfile = { ...profile, backendId };
    await SecureStore.setItemAsync(STORAGE_KEY, JSON.stringify(data));
  } catch (e) {
    console.warn("⚠️ Failed to persist profile:", e);
  }
}

async function loadProfile(): Promise<PersistedProfile | null> {
  try {
    const raw = await SecureStore.getItemAsync(STORAGE_KEY);
    if (raw) return JSON.parse(raw) as PersistedProfile;
  } catch (e) {
    console.warn("⚠️ Failed to load profile:", e);
  }
  return null;
}

/** Fire-and-forget sync to backend; updates store with backend ID on success */
async function syncToBackend(
  profile: UserProfile,
  set: (partial: Partial<ProfileState>) => void
): Promise<void> {
  if (
    profile.birthYear == null ||
    profile.birthMonth == null ||
    profile.birthDay == null ||
    profile.gender == null ||
    profile.heightCm == null ||
    profile.weightKg == null
  ) {
    return;
  }

  try {
    const res = await createProfile({
      birth_year: profile.birthYear,
      birth_month: profile.birthMonth,
      birth_day: profile.birthDay,
      gender: profile.gender,
      height_cm: profile.heightCm,
      weight_kg: profile.weightKg,
    });
    set({ backendId: res.id });
    await persistProfile(profile, res.id);
    console.log("✅ Profile synced to backend, id:", res.id);
  } catch (e) {
    console.warn("⚠️ Failed to sync profile to backend:", e);
  }
}

// ==========================================
// Store
// ==========================================

export const useProfileStore = create<ProfileState>((set, get) => ({
  ...EMPTY_PROFILE,
  isLoaded: false,
  backendId: null,

  hydrate: async () => {
    const saved = await loadProfile();
    if (saved) {
      set({
        ...saved,
        backendId: saved.backendId ?? null,
        isLoaded: true,
      });
    } else {
      set({ isLoaded: true });
    }
  },

  updateProfile: (patch) => {
    set((state) => {
      const updated: UserProfile = {
        birthYear: patch.birthYear ?? state.birthYear,
        birthMonth: patch.birthMonth ?? state.birthMonth,
        birthDay: patch.birthDay ?? state.birthDay,
        gender: patch.gender ?? state.gender,
        heightCm: patch.heightCm ?? state.heightCm,
        weightKg: patch.weightKg ?? state.weightKg,
      };
      persistProfile(updated, state.backendId);
      // Sync to backend in the background
      syncToBackend(updated, set);
      return updated;
    });
  },

  clearProfile: () => {
    set({ ...EMPTY_PROFILE, backendId: null });
    SecureStore.deleteItemAsync(STORAGE_KEY).catch(() => {});
  },
}));

// ==========================================
// Selectors
// ==========================================

/** Returns a short summary string like "M · 1990 · 178cm · 85kg" or null if profile is empty */
export function useProfileSummary(): string | null {
  return useProfileStore((s) => {
    const parts: string[] = [];
    if (s.gender) parts.push(s.gender === "male" ? "M" : s.gender === "female" ? "F" : "O");
    if (s.birthYear) parts.push(String(s.birthYear));
    if (s.heightCm) parts.push(`${s.heightCm}cm`);
    if (s.weightKg) parts.push(`${s.weightKg}kg`);
    return parts.length > 0 ? parts.join(" · ") : null;
  });
}

/** Returns true if the profile has been fully filled out */
export function useIsProfileComplete(): boolean {
  return useProfileStore(
    (s) =>
      s.birthYear !== null &&
      s.birthMonth !== null &&
      s.birthDay !== null &&
      s.gender !== null &&
      s.heightCm !== null &&
      s.weightKg !== null
  );
}

/** Returns the backend profile ID, or undefined if not yet synced */
export function useProfileId(): number | undefined {
  return useProfileStore((s) => s.backendId ?? undefined);
}

