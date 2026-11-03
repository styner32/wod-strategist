/**
 * Below are the colors that are used in the app. The colors are defined in the light and dark mode.
 * There are many other ways to style your app. For example, [Nativewind](https://www.nativewind.dev/), [Tamagui](https://tamagui.dev/), [unistyles](https://reactnativeunistyles.vercel.app), etc.
 */

import { Platform } from 'react-native';

const tintColorLight = '#0a7ea4';
const tintColorDark = '#fff';

export const Colors = {
  light: {
    text: '#11181C',
    background: '#fff',
    tint: tintColorLight,
    icon: '#687076',
    tabIconDefault: '#687076',
    tabIconSelected: tintColorLight,
  },
  dark: {
    text: '#ECEDEE',
    background: '#151718',
    tint: tintColorDark,
    icon: '#9BA1A6',
    tabIconDefault: '#9BA1A6',
    tabIconSelected: tintColorDark,
  },
};

export const Fonts = Platform.select({
  ios: {
    /** iOS `UIFontDescriptorSystemDesignDefault` */
    sans: 'system-ui',
    /** iOS `UIFontDescriptorSystemDesignSerif` */
    serif: 'ui-serif',
    /** iOS `UIFontDescriptorSystemDesignRounded` */
    rounded: 'ui-rounded',
    /** iOS `UIFontDescriptorSystemDesignMonospaced` */
    mono: 'ui-monospace',
  },
  default: {
    sans: 'normal',
    serif: 'serif',
    rounded: 'normal',
    mono: 'monospace',
  },
  web: {
    sans: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif",
    serif: "Georgia, 'Times New Roman', serif",
    rounded: "'SF Pro Rounded', 'Hiragino Maru Gothic ProN', Meiryo, 'MS PGothic', sans-serif",
    mono: "SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace",
  },
});

/**
 * Stitch Design System: Tactical Bento Metrics (Bright default + Dark HUD companion)
 * Source: projects/11381319408891102008
 */
export const TacticalTheme = {
  light: {
    canvas: '#F1F4F9',
    surface: '#FFFFFF',
    surfaceMuted: '#F8FAFC',
    surfaceContainer: '#E6E8F1',
    surfaceInverse: '#0B0E14',
    border: '#E2E8F0',
    borderStrong: '#CBD5E1',
    ink: '#0B0E14',
    inkSecondary: '#334155',
    inkMuted: '#64748B',
    inkInverse: '#F8FAFC',
    volt: '#D4FF00',
    voltPressed: '#B2DA00',
    voltBorder: '#A3E635',
    voltInk: '#0B0E14',
    oliveAccent: '#4D5E00',
    electricBlue: '#003EC6',
    electricBlueBg: '#EEF2FF',
    electricBlueBorder: '#C7D2FE',
    statusFresh: '#059669',
    statusFreshBg: '#ECFDF5',
    statusFreshBorder: '#A7F3D0',
    statusModerate: '#003EC6',
    statusModerateBg: '#EEF2FF',
    statusModerateBorder: '#C7D2FE',
    statusWarning: '#B45309',
    statusWarningBg: '#FFFBEB',
    statusWarningBorder: '#FDE68A',
    statusFatigued: '#EA580C',
    statusFatiguedBg: '#FFF7ED',
    statusFatiguedBorder: '#FED7AA',
    statusExhausted: '#DC2626',
    statusExhaustedBg: '#FEF2F2',
    statusExhaustedBorder: '#FECACA',
    heartRose: '#E11D48',
    heartRoseBg: '#FFF1F2',
    heartRoseBorder: '#FECDD3',
    dockBg: 'rgba(255, 255, 255, 0.96)',
  },
  dark: {
    canvas: '#080A0E',
    surface: '#11141B',
    surfaceMuted: '#161B24',
    surfaceContainer: '#1E2633',
    surfaceInverse: '#F8FAFC',
    border: 'rgba(255, 255, 255, 0.08)',
    borderStrong: 'rgba(255, 255, 255, 0.16)',
    ink: '#F8FAFC',
    inkSecondary: '#CBD5E1',
    inkMuted: '#94A3B8',
    inkInverse: '#0B0E14',
    volt: '#D4FF00',
    voltPressed: '#B2DA00',
    voltBorder: 'rgba(212, 255, 0, 0.5)',
    voltInk: '#0B0E14',
    oliveAccent: '#D4FF00',
    electricBlue: '#00E5FF',
    electricBlueBg: 'rgba(0, 229, 255, 0.14)',
    electricBlueBorder: 'rgba(0, 229, 255, 0.35)',
    statusFresh: '#10B981',
    statusFreshBg: 'rgba(16, 185, 129, 0.16)',
    statusFreshBorder: 'rgba(16, 185, 129, 0.4)',
    statusModerate: '#00E5FF',
    statusModerateBg: 'rgba(0, 229, 255, 0.15)',
    statusModerateBorder: 'rgba(0, 229, 255, 0.4)',
    statusWarning: '#F59E0B',
    statusWarningBg: 'rgba(245, 158, 11, 0.16)',
    statusWarningBorder: 'rgba(245, 158, 11, 0.4)',
    statusFatigued: '#F97316',
    statusFatiguedBg: 'rgba(249, 115, 22, 0.16)',
    statusFatiguedBorder: 'rgba(249, 115, 22, 0.45)',
    statusExhausted: '#EF4444',
    statusExhaustedBg: 'rgba(239, 68, 68, 0.16)',
    statusExhaustedBorder: 'rgba(239, 68, 68, 0.45)',
    heartRose: '#FF375F',
    heartRoseBg: 'rgba(255, 55, 95, 0.16)',
    heartRoseBorder: 'rgba(255, 55, 95, 0.4)',
    dockBg: 'rgba(17, 20, 27, 0.94)',
  },
} as const;

export type TacticalMode = keyof typeof TacticalTheme;

export interface TacticalFatigueTone {
  fg: string;
  bg: string;
  border: string;
  bar: string;
}

export function getTacticalFatigueTone(
  scoreOrState: number | string,
  mode: TacticalMode = 'light',
): TacticalFatigueTone {
  const palette = TacticalTheme[mode];

  if (typeof scoreOrState === 'string') {
    switch (scoreOrState) {
      case 'fresh':
        return {
          fg: palette.statusFresh,
          bg: palette.statusFreshBg,
          border: palette.statusFreshBorder,
          bar: palette.statusFresh,
        };
      case 'moderate':
        return {
          fg: palette.statusModerate,
          bg: palette.statusModerateBg,
          border: palette.statusModerateBorder,
          bar: palette.statusModerate,
        };
      case 'fatigued':
        return {
          fg: palette.statusFatigued,
          bg: palette.statusFatiguedBg,
          border: palette.statusFatiguedBorder,
          bar: palette.statusFatigued,
        };
      case 'exhausted':
        return {
          fg: palette.statusExhausted,
          bg: palette.statusExhaustedBg,
          border: palette.statusExhaustedBorder,
          bar: palette.statusExhausted,
        };
      default:
        return {
          fg: palette.statusModerate,
          bg: palette.statusModerateBg,
          border: palette.statusModerateBorder,
          bar: palette.statusModerate,
        };
    }
  }

  if (scoreOrState <= 25) {
    return getTacticalFatigueTone('fresh', mode);
  }
  if (scoreOrState <= 50) {
    return getTacticalFatigueTone('moderate', mode);
  }
  if (scoreOrState <= 75) {
    return getTacticalFatigueTone('fatigued', mode);
  }
  return getTacticalFatigueTone('exhausted', mode);
}
