// src/app/index.tsx
import { useActiveProfile, useProfileId } from "@/store/useProfileStore";
import { Link } from "expo-router";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

export default function Dashboard() {
  const activeProfile = useActiveProfile();
  const profileId = useProfileId();

  const profileName = activeProfile?.name || (profileId ? `Profile #${profileId}` : null);

  const summaryLine = activeProfile
    ? [
        activeProfile.gender === "male"
          ? "M"
          : activeProfile.gender === "female"
            ? "F"
            : "O",
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
        <Text style={styles.title}>
          {profileName ? `Hey, ${profileName}` : "Welcome Coach,"}
        </Text>
        <Text style={styles.subtitle}>Ready to verify AI Engine?</Text>
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
              {activeProfile ? profileName : "Select Profile"}
            </Text>
            <Text style={styles.profileDesc}>
              {summaryLine ?? "Tap to choose or create a profile"}
            </Text>
            {activeProfile && (
              <View style={styles.syncRow}>
                <View style={styles.syncDot} />
                <Text style={styles.syncText}>
                  Active · ID #{profileId}
                </Text>
              </View>
            )}
          </View>
          <Text style={styles.profileChevron}>›</Text>
        </Pressable>
      </Link>

      <View style={styles.menu}>
        <Text style={styles.sectionTitle}>Development Zone</Text>

        <Link href="/workout/setup" asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>👁️</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>Start Workout</Text>
              <Text style={styles.cardDesc}>Choose type, configure, and record</Text>
            </View>
          </Pressable>
        </Link>

        <Link href="/upload" asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>📤</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>Upload Video</Text>
              <Text style={styles.cardDesc}>Analyze from Gallery</Text>
            </View>
          </Pressable>
        </Link>

        <Link href={"/queue" as any} asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>📋</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>Video Queue</Text>
              <Text style={styles.cardDesc}>Encoding & upload status</Text>
            </View>
          </Pressable>
        </Link>

        <Link href="/history" asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>📜</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>Workout History</Text>
              <Text style={styles.cardDesc}>View AI Analysis Results</Text>
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
  title: { color: "#fff", fontSize: 28, fontWeight: "800" },
  subtitle: { color: "#666", fontSize: 16, marginTop: 5 },
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
