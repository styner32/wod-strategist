import * as SecureStore from "expo-secure-store";
import { create } from "zustand";
import {
  listProfiles as apiListProfiles,
  createProfile as apiCreateProfile,
  updateProfile as apiUpdateProfile,
  archiveProfile as apiArchiveProfile,
  type ProfileResponse,
  type CreateProfileRequest,
  type UpdateProfileRequest,
} from "@/features/wod/api";

// ==========================================
// Types
// ==========================================

export type Gender = "male" | "female" | "other";
export type FitnessLevel = "beginner" | "intermediate" | "advanced";

export interface Profile {
  id: number;
  name: string;
  birthYear: number;
  birthMonth: number;
  birthDay: number;
  gender: Gender;
  heightCm: number;
  weightKg: number;
  fitnessLevel: FitnessLevel;
  injuries: string[];
}

interface ProfileState {
  isLoaded: boolean;
  profiles: Profile[];
  /** Currently selected profile ID (persisted locally) */
  activeProfileId: number | null;

  /** Load profiles from backend + restore activeProfileId from local storage */
  hydrate: () => Promise<void>;

  /** Refresh profiles list from backend */
  loadProfiles: () => Promise<void>;

  /** Select a profile as active */
  selectProfile: (id: number) => void;

  /** Create a new profile on the backend and add to local list */
  createProfile: (data: CreateProfileRequest) => Promise<Profile>;

  /** Update an existing profile on the backend */
  updateProfile: (id: number, data: UpdateProfileRequest) => Promise<Profile>;

  /** Archive a profile (soft-delete) */
  archiveProfile: (id: number) => Promise<void>;

  /** Clear the active profile selection */
  clearActiveProfile: () => void;
}

// ==========================================
// Constants
// ==========================================

const ACTIVE_PROFILE_KEY = "wod_active_profile_id";

// ==========================================
// Helpers
// ==========================================

function toProfile(res: ProfileResponse): Profile {
  return {
    id: res.id,
    name: res.name,
    birthYear: res.birth_year,
    birthMonth: res.birth_month,
    birthDay: res.birth_day,
    gender: res.gender as Gender,
    heightCm: res.height_cm,
    weightKg: res.weight_kg,
    fitnessLevel: (res.fitness_level as FitnessLevel) || 'intermediate',
    injuries: res.injuries ?? [],
  };
}

async function persistActiveId(id: number | null): Promise<void> {
  try {
    if (id !== null) {
      await SecureStore.setItemAsync(ACTIVE_PROFILE_KEY, String(id));
    } else {
      await SecureStore.deleteItemAsync(ACTIVE_PROFILE_KEY);
    }
  } catch (e) {
    console.warn("⚠️ Failed to persist active profile ID:", e);
  }
}

async function loadActiveId(): Promise<number | null> {
  try {
    const raw = await SecureStore.getItemAsync(ACTIVE_PROFILE_KEY);
    if (raw) {
      const id = parseInt(raw, 10);
      return isNaN(id) ? null : id;
    }
  } catch (e) {
    console.warn("⚠️ Failed to load active profile ID:", e);
  }
  return null;
}

// ==========================================
// Store
// ==========================================

export const useProfileStore = create<ProfileState>((set, get) => ({
  isLoaded: false,
  profiles: [],
  activeProfileId: null,

  hydrate: async () => {
    const [savedId, profiles] = await Promise.all([
      loadActiveId(),
      apiListProfiles().catch(() => [] as ProfileResponse[]),
    ]);

    const profileList = profiles.map(toProfile);

    // Validate that savedId still exists in the list
    const validId =
      savedId !== null && profileList.some((p) => p.id === savedId)
        ? savedId
        : null;

    set({
      isLoaded: true,
      profiles: profileList,
      activeProfileId: validId,
    });
  },

  loadProfiles: async () => {
    try {
      const profiles = await apiListProfiles();
      set({ profiles: profiles.map(toProfile) });
    } catch (e) {
      console.warn("⚠️ Failed to load profiles:", e);
    }
  },

  selectProfile: (id) => {
    set({ activeProfileId: id });
    persistActiveId(id);
  },

  createProfile: async (data) => {
    const res = await apiCreateProfile(data);
    const profile = toProfile(res);
    set((state) => ({
      profiles: [...state.profiles, profile],
      activeProfileId: profile.id,
    }));
    await persistActiveId(profile.id);
    return profile;
  },

  updateProfile: async (id, data) => {
    const res = await apiUpdateProfile(id, data);
    const updated = toProfile(res);
    set((state) => ({
      profiles: state.profiles.map((p) => (p.id === id ? updated : p)),
    }));
    return updated;
  },

  archiveProfile: async (id) => {
    await apiArchiveProfile(id);
    set((state) => {
      const newProfiles = state.profiles.filter((p) => p.id !== id);
      const newActiveId =
        state.activeProfileId === id ? null : state.activeProfileId;
      if (state.activeProfileId === id) {
        persistActiveId(null);
      }
      return { profiles: newProfiles, activeProfileId: newActiveId };
    });
  },

  clearActiveProfile: () => {
    set({ activeProfileId: null });
    persistActiveId(null);
  },
}));

// ==========================================
// Selectors
// ==========================================

/** Returns the currently active profile, or null if none selected */
export function useActiveProfile(): Profile | null {
  return useProfileStore((s) => {
    if (s.activeProfileId === null) return null;
    return s.profiles.find((p) => p.id === s.activeProfileId) ?? null;
  });
}

/** Returns a short summary string like "M · 1990 · 178cm · 85kg" or null if no active profile */
export function useProfileSummary(): string | null {
  return useProfileStore((s) => {
    if (s.activeProfileId === null) return null;
    const profile = s.profiles.find((p) => p.id === s.activeProfileId);
    if (!profile) return null;

    const parts: string[] = [];
    if (profile.gender)
      parts.push(
        profile.gender === "male"
          ? "M"
          : profile.gender === "female"
            ? "F"
            : "O"
      );
    if (profile.birthYear) parts.push(String(profile.birthYear));
    if (profile.heightCm) parts.push(`${profile.heightCm}cm`);
    if (profile.weightKg) parts.push(`${profile.weightKg}kg`);
    return parts.length > 0 ? parts.join(" · ") : null;
  });
}

/** Returns true if a profile is currently selected */
export function useIsProfileComplete(): boolean {
  return useProfileStore((s) => s.activeProfileId !== null);
}

/** Returns the backend profile ID, or undefined if not yet selected */
export function useProfileId(): number | undefined {
  return useProfileStore((s) => s.activeProfileId ?? undefined);
}
