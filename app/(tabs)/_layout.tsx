import { IconSymbol } from "@/components/ui/icon-symbol";
import { t } from "@/features/i18n";
import { Tabs } from "expo-router";
import { StyleSheet } from "react-native";

export default function TabLayout() {
  return (
    <Tabs
      screenOptions={{
        headerStyle: { backgroundColor: "#000" },
        headerTintColor: "#fff",
        headerTitleStyle: { fontWeight: "bold" },
        tabBarActiveTintColor: "#00E5FF",
        tabBarInactiveTintColor: "#555",
        tabBarStyle: styles.tabBar,
        tabBarLabelStyle: styles.tabBarLabel,
      }}
    >
      <Tabs.Screen
        name="index"
        options={{
          title: t("tabs.home"),
          headerTitle: "WOD Strategist",
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="house.fill" size={size} color={color} />
          ),
        }}
      />
      <Tabs.Screen
        name="train"
        options={{
          title: t("tabs.train"),
          headerTitle: t("tabs.train"),
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="flame.fill" size={size} color={color} />
          ),
        }}
      />
      <Tabs.Screen
        name="history"
        options={{
          title: t("tabs.history"),
          headerTitle: t("tabs.history"),
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="clock.fill" size={size} color={color} />
          ),
        }}
      />
      <Tabs.Screen
        name="profile"
        options={{
          title: t("tabs.profile"),
          headerShown: false,
          tabBarIcon: ({ color, size }) => (
            <IconSymbol name="person.fill" size={size} color={color} />
          ),
        }}
      />
    </Tabs>
  );
}

const styles = StyleSheet.create({
  tabBar: {
    backgroundColor: "#0E0E0E",
    borderTopWidth: 0,
    elevation: 0,
    paddingTop: 4,
  },
  tabBarLabel: {
    fontSize: 10,
    fontWeight: "700",
    letterSpacing: 0.5,
    textTransform: "uppercase",
  },
});
