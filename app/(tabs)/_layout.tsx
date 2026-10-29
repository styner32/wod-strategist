import { IconSymbol } from "@/components/ui/icon-symbol";
import { triggerLightHaptic } from "@/constants/motion";
import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { Tabs } from "expo-router";
import React from "react";
import { StyleSheet, Text, TouchableOpacity, View } from "react-native";

const T = TacticalTheme.light;

function FloatingTacticalDock({
  state,
  descriptors,
  navigation,
  insets,
}: {
  state: any;
  descriptors: Record<string, any>;
  navigation: any;
  insets: { bottom: number };
}) {
  const bottomOffset = Math.max(insets.bottom, 12);

  return (
    <View
      pointerEvents="box-none"
      style={[styles.dockOuter, { bottom: bottomOffset }]}
    >
      <View style={styles.dockBar} accessibilityRole="tablist">
        {state.routes.map((route: any, index: number) => {
          const { options } = descriptors[route.key] ?? { options: {} };
          if (options.href === null) return null;

          const isFocused = state.index === index;
          const label =
            typeof options.tabBarLabel === "string"
              ? options.tabBarLabel
              : typeof options.title === "string"
                ? options.title
                : route.name;

          const onPress = () => {
            const event = navigation.emit({
              type: "tabPress",
              target: route.key,
              canPreventDefault: true,
            });

            if (!isFocused && !event.defaultPrevented) {
              triggerLightHaptic();
              navigation.navigate(route.name, route.params);
            }
          };

          const onLongPress = () => {
            navigation.emit({
              type: "tabLongPress",
              target: route.key,
            });
          };

          const iconColor = isFocused ? T.volt : T.inkMuted;

          return (
            <TouchableOpacity
              key={route.key}
              accessibilityRole="tab"
              accessibilityState={isFocused ? { selected: true } : {}}
              accessibilityLabel={options.tabBarAccessibilityLabel ?? label}
              testID={options.tabBarButtonTestID ?? `tab-${route.name}`}
              onPress={onPress}
              onLongPress={onLongPress}
              activeOpacity={0.8}
              style={[styles.tabItem, isFocused && styles.tabItemActive]}
            >
              {options.tabBarIcon
                ? options.tabBarIcon({
                    focused: isFocused,
                    color: iconColor,
                    size: 20,
                  })
                : null}
              <Text
                numberOfLines={1}
                style={[styles.tabLabel, isFocused && styles.tabLabelActive]}
              >
                {label}
              </Text>
            </TouchableOpacity>
          );
        })}
      </View>
    </View>
  );
}

export default function TabLayout() {
  useLocale();

  return (
    <Tabs
      tabBar={(props) => <FloatingTacticalDock {...props} />}
      screenOptions={{
        headerShown: false,
        sceneStyle: { backgroundColor: T.canvas },
      }}
    >
      <Tabs.Screen
        name="index"
        options={{
          title: t("tabs.home"),
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="house.fill" size={size} color={color} />
          ),
        }}
      />
      <Tabs.Screen
        name="train"
        options={{
          title: t("tabs.train"),
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="bolt.fill" size={size} color={color} />
          ),
        }}
      />
      <Tabs.Screen
        name="history"
        options={{
          title: t("tabs.history"),
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="clock.fill" size={size} color={color} />
          ),
        }}
      />
      <Tabs.Screen
        name="stretches"
        options={{
          title: t("tabs.stretches"),
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="figure.flexibility" size={size} color={color} />
          ),
        }}
      />
      <Tabs.Screen
        name="profile"
        options={{
          title: t("tabs.profile"),
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="slider.horizontal.3" size={size} color={color} />
          ),
        }}
      />
    </Tabs>
  );
}

const styles = StyleSheet.create({
  dockOuter: {
    position: "absolute",
    left: 16,
    right: 16,
    alignItems: "center",
    zIndex: 50,
  },
  dockBar: {
    width: "100%",
    maxWidth: 420,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    backgroundColor: T.dockBg,
    borderWidth: 1,
    borderColor: T.borderStrong,
    borderRadius: 18,
    padding: 6,
    gap: 4,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 8 },
    shadowOpacity: 0.14,
    shadowRadius: 20,
    elevation: 12,
  },
  tabItem: {
    flex: 1,
    minHeight: 48,
    paddingVertical: 6,
    paddingHorizontal: 4,
    borderRadius: 12,
    alignItems: "center",
    justifyContent: "center",
    gap: 2,
  },
  tabItemActive: {
    backgroundColor: T.surfaceInverse,
  },
  tabLabel: {
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 0.5,
    textTransform: "uppercase",
    color: T.inkMuted,
    flexShrink: 1,
  },
  tabLabelActive: {
    color: T.volt,
  },
});
