import { I18n } from "i18n-js";
import { getLocales } from "expo-localization";
import { create } from "zustand";

import en from "./locales/en.json";
import ko from "./locales/ko.json";

// ─── i18n instance ───────────────────────────────────
const i18n = new I18n({ en, ko });

const deviceLang = getLocales()[0]?.languageCode ?? "en";
i18n.locale = deviceLang === "ko" ? "ko" : "en";
i18n.enableFallback = true;
i18n.defaultLocale = "en";

// ─── Zustand locale store ────────────────────────────
// Drives reactive re-renders when the user switches language.
interface LocaleStore {
  locale: string;
  setLocale: (code: string) => void;
}

export const useLocaleStore = create<LocaleStore>((set) => ({
  locale: i18n.locale,
  setLocale: (code: string) => {
    const resolved = code === "ko" ? "ko" : "en";
    i18n.locale = resolved;
    set({ locale: resolved });
  },
}));

/**
 * Switch the app language at runtime.
 * Components subscribed to `useLocaleStore` will re-render.
 */
export function setLanguage(code: string) {
  useLocaleStore.getState().setLocale(code);
}

/**
 * Hook that returns the current locale string.
 * Subscribing to this ensures the component re-renders on language switch.
 */
export function useLocale(): string {
  return useLocaleStore((s) => s.locale);
}

/**
 * Translate a key with optional interpolation values.
 *
 * @example
 *   t("home.greeting", { name: "Sun Jin" })
 *   // en → "Hey, Sun Jin"
 *   // ko → "안녕하세요, Sun Jin"
 */
export function t(key: string, options?: Record<string, any>): string {
  return i18n.t(key, options);
}

export default i18n;
