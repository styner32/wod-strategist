import {
  MotionTokens,
  triggerLightHaptic,
  useReduceMotionEnabled,
} from "@/constants/motion";
import { TacticalTheme } from "@/constants/theme";
import React, { useRef } from "react";
import {
  Animated,
  GestureResponderEvent,
  StyleSheet,
  Text,
  TouchableOpacity,
  type StyleProp,
  type TouchableOpacityProps,
  type ViewProps,
  type ViewStyle,
  View,
} from "react-native";

const T = TacticalTheme.light;

export function TacticalCard({
  style,
  children,
  ...rest
}: ViewProps & { style?: StyleProp<ViewStyle> }) {
  return (
    <View style={[styles.card, style]} {...rest}>
      {children}
    </View>
  );
}

interface TacticalPressableProps extends TouchableOpacityProps {
  style?: StyleProp<ViewStyle>;
  haptic?: boolean;
}

export function TacticalPressable({
  style,
  children,
  haptic = false,
  onPressIn,
  onPressOut,
  onPress,
  disabled,
  activeOpacity = 0.88,
  ...rest
}: TacticalPressableProps) {
  const reduceMotion = useReduceMotionEnabled();
  const scaleAnim = useRef(new Animated.Value(1)).current;

  const handlePressIn = (e: GestureResponderEvent) => {
    if (!reduceMotion && !disabled) {
      Animated.timing(scaleAnim, {
        toValue: MotionTokens.pressScale,
        duration: MotionTokens.fastMs,
        useNativeDriver: true,
      }).start();
    }
    onPressIn?.(e);
  };

  const handlePressOut = (e: GestureResponderEvent) => {
    if (!reduceMotion) {
      Animated.timing(scaleAnim, {
        toValue: 1,
        duration: MotionTokens.fastMs,
        useNativeDriver: true,
      }).start();
    }
    onPressOut?.(e);
  };

  const handlePress = (e: GestureResponderEvent) => {
    if (haptic && !disabled) {
      triggerLightHaptic();
    }
    onPress?.(e);
  };

  return (
    <Animated.View style={{ transform: [{ scale: scaleAnim }] }}>
      <TouchableOpacity
        {...rest}
        style={style}
        disabled={disabled}
        activeOpacity={activeOpacity}
        onPressIn={handlePressIn}
        onPressOut={handlePressOut}
        onPress={handlePress}
      >
        {children}
      </TouchableOpacity>
    </Animated.View>
  );
}

interface VoltButtonProps {
  label: string;
  onPress: () => void;
  leftIcon?: React.ReactNode;
  rightBadgeText?: string;
  rightIcon?: React.ReactNode;
  disabled?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}

export function VoltButton({
  label,
  onPress,
  leftIcon,
  rightBadgeText,
  rightIcon,
  disabled = false,
  style,
  testID,
}: VoltButtonProps) {
  return (
    <TacticalPressable
      testID={testID}
      haptic
      disabled={disabled}
      onPress={onPress}
      accessibilityRole="button"
      style={[
        styles.voltButton,
        rightBadgeText ? styles.voltButtonSpaceBetween : styles.voltButtonCenter,
        disabled && styles.voltButtonDisabled,
        style,
      ]}
    >
      <View style={styles.voltMainGroup}>
        {leftIcon}
        <Text style={styles.voltLabel} numberOfLines={1}>
          {label}
        </Text>
      </View>
      {rightBadgeText ? (
        <View style={styles.voltRightBadge}>
          <Text style={styles.voltRightBadgeText} numberOfLines={1}>
            {rightBadgeText}
          </Text>
        </View>
      ) : (
        rightIcon ?? null
      )}
    </TacticalPressable>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    padding: 16,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 8,
    elevation: 2,
  },
  voltButton: {
    backgroundColor: T.volt,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: T.voltBorder,
    minHeight: 54,
    paddingVertical: 14,
    paddingHorizontal: 16,
    flexDirection: "row",
    alignItems: "center",
    shadowColor: "#84CC16",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.25,
    shadowRadius: 10,
    elevation: 3,
  },
  voltButtonCenter: {
    justifyContent: "center",
    gap: 8,
  },
  voltButtonSpaceBetween: {
    justifyContent: "space-between",
    gap: 10,
  },
  voltButtonDisabled: {
    opacity: 0.55,
  },
  voltMainGroup: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    flexShrink: 1,
  },
  voltLabel: {
    color: T.voltInk,
    fontSize: 16,
    fontWeight: "900",
    letterSpacing: 0.6,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  voltRightBadge: {
    backgroundColor: "rgba(11, 14, 20, 0.12)",
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 6,
    flexShrink: 0,
  },
  voltRightBadgeText: {
    color: T.voltInk,
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 0.4,
    textTransform: "uppercase",
  },
});
