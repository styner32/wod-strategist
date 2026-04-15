// src/app/index.tsx
import { t, useLocale, setLanguage } from "@/features/i18n";
import { useActiveProfile, useProfileId } from "@/store/useProfileStore";
import { Link } from "expo-router";
import { Pressable, ScrollView, StyleSheet, Text, TouchableOpacity, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

export default function Dashboard() {
  const activeProfile = useActiveProfile();
  const profileId = useProfileId();
  const locale = useLocale();

  const profileName = activeProfile?.name || (profileId ? t("common.profileNumber", { id: profileId }) : null);

  const summaryLine = activeProfile
    ? [
        activeProfile.gender === "male"
          ? t("common.maleShort")
          : activeProfile.gender === "female"
            ? t("common.femaleShort")
            : t("common.otherShort"),
        String(activeProfile.birthYear),
        `${activeProfile.heightCm}cm`,
        `${activeProfile.weightKg}kg`,
      ].join(" · ")
    : null;

  return (
    <SafeAreaView style={styles.container}>
      <ScrollView
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
      <View style={styles.header}>
        <View style={styles.headerRow}>
          <View style={{ flex: 1 }}>
            <Text style={styles.title}>
              {profileName ? t("home.greeting", { name: profileName }) : t("home.welcome")}
            </Text>
            <Text style={styles.subtitle}>{t("home.subtitle")}</Text>
          </View>
          <TouchableOpacity
            style={styles.langToggle}
            onPress={() => setLanguage(locale === "ko" ? "en" : "ko")}
            activeOpacity={0.7}
          >
            <Text style={styles.langToggleText}>
              {locale === "ko" ? "EN" : "한국어"}
            </Text>
          </TouchableOpacity>
        </View>
      </View>

      {/* Active Profile Card */}
      <Link href={"/profiles" as any} asChild>
        <Pressable style={StyleSheet.flatten([styles.profileCard, !activeProfile && styles.profileCardEmpty])}>
          <View style={styles.profileIconBox}>
            <Text style={styles.profileIcon}>
              {activeProfile?.name ? activeProfile.name[0].toUpperCase() : "👤"}
            </Text>
          </View>
          <View style={{ flex: 1 }}>
            <Text style={styles.profileTitle}>
              {activeProfile ? profileName : t("home.selectProfile")}
            </Text>
            <Text style={styles.profileDesc}>
              {summaryLine ?? t("home.tapToChoose")}
            </Text>
            {activeProfile && (
              <View style={styles.syncRow}>
                <View style={styles.syncDot} />
                <Text style={styles.syncText}>
                  {t("home.activeId", { id: profileId })}
                </Text>
              </View>
            )}
          </View>
          <Text style={styles.profileChevron}>›</Text>
        </Pressable>
      </Link>

      <View style={styles.menu}>
        <Text style={styles.sectionTitle}>{t("home.devZone")}</Text>

        <Link href="/workout/setup" asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>👁️</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>{t("home.startWorkout")}</Text>
              <Text style={styles.cardDesc}>{t("home.startWorkoutDesc")}</Text>
            </View>
          </Pressable>
        </Link>

        <Link href="/upload" asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>📤</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>{t("home.uploadVideo")}</Text>
              <Text style={styles.cardDesc}>{t("home.uploadVideoDesc")}</Text>
            </View>
          </Pressable>
        </Link>

        <Link href={"/queue" as any} asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>📋</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>{t("home.videoQueue")}</Text>
              <Text style={styles.cardDesc}>{t("home.videoQueueDesc")}</Text>
            </View>
          </Pressable>
        </Link>

        <Link href="/history" asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>📜</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>{t("home.workoutHistory")}</Text>
              <Text style={styles.cardDesc}>{t("home.workoutHistoryDesc")}</Text>
            </View>
          </Pressable>
        </Link>
      </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000" },
  scrollContent: { padding: 20, paddingBottom: 40 },
  header: { marginBottom: 40, marginTop: 20 },
  headerRow: {
    flexDirection: "row",
    alignItems: "flex-start",
    justifyContent: "space-between",
  },
  title: { color: "#fff", fontSize: 28, fontWeight: "800" },
  subtitle: { color: "#666", fontSize: 16, marginTop: 5 },
  langToggle: {
    backgroundColor: "#1A1A1A",
    borderWidth: 1,
    borderColor: "#444",
    borderRadius: 20,
    paddingHorizontal: 14,
    paddingVertical: 8,
    marginLeft: 12,
    marginTop: 4,
  },
  langToggleText: {
    color: "#64D2FF",
    fontSize: 13,
    fontWeight: "700",
  },
  sectionTitle: {
    color: "#888",
    fontSize: 14,
    marginBottom: 15,
    textTransform: "uppercase",
    letterSpacing: 1,
  },
  menu: { gap: 15 },
  card: {
    backgroundColor: "#1A1A1A",
    padding: 20,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#333",
  },
  iconBox: {
    width: 50,
    height: 50,
    backgroundColor: "#333",
    borderRadius: 12,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 15,
  },
  icon: { fontSize: 24 },
  cardTitle: { color: "#fff", fontSize: 18, fontWeight: "bold" },
  cardDesc: { color: "#888", fontSize: 14, marginTop: 2 },
  profileCard: {
    backgroundColor: "#1A1A1A",
    padding: 16,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#007AFF",
    marginBottom: 20,
  },
  profileCardEmpty: {
    borderColor: "#444",
    borderStyle: "dashed",
  },
  profileIconBox: {
    width: 44,
    height: 44,
    backgroundColor: "#0B1A2F",
    borderRadius: 22,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 12,
  },
  profileIcon: { fontSize: 18, fontWeight: "bold", color: "#fff" },
  profileTitle: { color: "#fff", fontSize: 16, fontWeight: "bold" },
  profileDesc: { color: "#8BC3FF", fontSize: 13, marginTop: 2 },
  profileChevron: { color: "#555", fontSize: 24, fontWeight: "300" },
  syncRow: { flexDirection: "row", alignItems: "center", marginTop: 4 },
  syncDot: { width: 6, height: 6, borderRadius: 3, marginRight: 5, backgroundColor: "#34C759" },
  syncText: { color: "#888", fontSize: 11 },
});
