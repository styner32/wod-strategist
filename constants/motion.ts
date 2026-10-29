import * as Haptics from "expo-haptics";
import { useEffect, useState } from "react";
import { AccessibilityInfo, LayoutAnimation, Platform } from "react-native";

/**
 * Stitch Motion Design Audit & Tokens (projects/11381319408891102008)
 */
export const MotionTokens = {
  pressScale: 0.975,
  fastMs: 120,
  normalMs: 220,
} as const;

export function useReduceMotionEnabled(): boolean {
  const [reduceMotion, setReduceMotion] = useState(false);

  useEffect(() => {
    let mounted = true;
    AccessibilityInfo.isReduceMotionEnabled?.()
      .then((enabled) => {
        if (mounted && Boolean(enabled)) {
          setReduceMotion(true);
        }
      })
      .catch(() => {});

    const subscription = AccessibilityInfo.addEventListener?.(
      "reduceMotionChanged",
      (enabled) => {
        if (mounted) setReduceMotion(Boolean(enabled));
      },
    );

    return () => {
      mounted = false;
      subscription?.remove?.();
    };
  }, []);

  return reduceMotion;
}

export function animateTacticalLayout(reduceMotion = false): void {
  if (reduceMotion) return;
  try {
    LayoutAnimation.configureNext(LayoutAnimation.Presets.easeInEaseOut);
  } catch {
    // Ignore in environments where LayoutAnimation is unavailable
  }
}

export function triggerLightHaptic(): void {
  if (Platform.OS !== "ios" && Platform.OS !== "android") return;
  try {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
  } catch {
    // Ignore haptic failures on simulator / unsupported hardware
  }
}
