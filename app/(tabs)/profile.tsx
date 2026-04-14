import { IconSymbol } from "@/components/ui/icon-symbol";
import { t } from "@/features/i18n";
import { useActiveProfile, useProfileId, useProfileStore } from "@/store/useProfileStore";
import { Link, router } from "expo-router";
import {
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

export default function ProfileTab() {
  const activeProfile = useActiveProfile();
  const profileId = useProfileId();
  const clearActiveProfile = useProfileStore((s) => s.clearActiveProfile);

  const summaryLine = activeProfile
    ? [
        activeProfile.gender === "male"
          ? t("common.male")
          : activeProfile.gender === "female"
            ? t("common.female")
            : t("common.other"),
        String(activeProfile.birthYear),
        `${activeProfile.heightCm} cm`,
        `${activeProfile.weightKg} kg`,
      ].join("  ·  ")
    : null;

  const handleLogout = () => {
    Alert.alert(t("profileTab.signOut"), t("profileTab.signOutConfirm"), [
      { text: t("common.cancel"), style: "cancel" },
      {
        text: t("profileTab.signOut"),
        style: "destructive",
        onPress: () => {
          clearActiveProfile();
          router.replace("/");
        },
      },
    ]);
  };

  return (
    <SafeAreaView style={styles.container}>
      <ScrollView
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
        {/* Avatar & Name */}
        <View style={styles.avatarSection}>
          <View style={styles.avatarCircle}>
            <Text style={styles.avatarText}>
              {activeProfile?.name
                ? activeProfile.name[0].toUpperCase()
                : "?"}
            </Text>
          </View>
          <Text style={styles.profileName}>
            {activeProfile?.name || t("profileTab.noProfile")}
          </Text>
          {summaryLine && (
            <Text style={styles.profileSummary}>{summaryLine}</Text>
          )}
          {activeProfile && activeProfile.injuries.length > 0 && (
            <View style={styles.injuryRow}>
              {activeProfile.injuries.map((injury) => (
                <View key={injury} style={styles.injuryPill}>
                  <Text style={styles.injuryPillText}>{injury}</Text>
                </View>
              ))}
            </View>
          )}
          {profileId && (
            <View style={styles.idBadge}>
              <Text style={styles.idBadgeText}>ID #{profileId}</Text>
            </View>
          )}
        </View>

        {/* Actions */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t("profileTab.account")}</Text>

          {activeProfile ? (
            <Link
              href={`/profile?id=${profileId}` as any}
              asChild
            >
              <Pressable style={styles.menuItem}>
                <View style={styles.menuIconBox}>
                  <IconSymbol name="person.fill" size={18} color="#00E5FF" />
                </View>
                <Text style={styles.menuItemText}>{t("profileTab.editProfile")}</Text>
                <Text style={styles.chevron}>›</Text>
              </Pressable>
            </Link>
          ) : (
            <Link href="/profile" asChild>
              <Pressable style={styles.menuItem}>
                <View style={styles.menuIconBox}>
                  <IconSymbol name="person.fill" size={18} color="#53E16F" />
                </View>
                <Text style={styles.menuItemText}>{t("profileTab.createProfile")}</Text>
                <Text style={styles.chevron}>›</Text>
              </Pressable>
            </Link>
          )}

          <Link href={"/profiles" as any} asChild>
            <Pressable style={styles.menuItem}>
              <View style={styles.menuIconBox}>
                <IconSymbol name="person.fill" size={18} color="#FFD60A" />
              </View>
              <Text style={styles.menuItemText}>{t("profileTab.switchProfile")}</Text>
              <Text style={styles.chevron}>›</Text>
            </Pressable>
          </Link>
        </View>

        {/* Danger Zone */}
        {activeProfile && (
          <View style={styles.section}>
            <TouchableOpacity
              style={styles.logoutBtn}
              onPress={handleLogout}
            >
              <Text style={styles.logoutText}>{t("profileTab.signOut")}</Text>
            </TouchableOpacity>
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000" },
  scrollContent: { padding: 20, paddingBottom: 40 },

  avatarSection: {
    alignItems: "center",
    paddingTop: 20,
    paddingBottom: 32,
  },
  avatarCircle: {
    width: 80,
    height: 80,
    borderRadius: 40,
    backgroundColor: "#002B3D",
    justifyContent: "center",
    alignItems: "center",
    marginBottom: 16,
  },
  avatarText: {
    color: "#00E5FF",
    fontSize: 32,
    fontWeight: "800",
  },
  profileName: {
    color: "#fff",
    fontSize: 24,
    fontWeight: "800",
    marginBottom: 6,
  },
  profileSummary: {
    color: "#888",
    fontSize: 14,
    marginBottom: 10,
  },
  idBadge: {
    backgroundColor: "#1A1A1A",
    paddingHorizontal: 14,
    paddingVertical: 6,
    borderRadius: 20,
  },
  idBadgeText: {
    color: "#555",
    fontSize: 12,
    fontWeight: "600",
    fontFamily: "monospace",
  },

  section: { marginBottom: 28 },
  sectionTitle: {
    color: "#888",
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 1.2,
    marginBottom: 14,
  },

  menuItem: {
    backgroundColor: "#1A1A1A",
    padding: 16,
    borderRadius: 14,
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 8,
  },
  menuIconBox: {
    width: 36,
    height: 36,
    backgroundColor: "#252525",
    borderRadius: 10,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 14,
  },
  menuItemText: {
    color: "#fff",
    fontSize: 16,
    fontWeight: "600",
    flex: 1,
  },
  chevron: { color: "#444", fontSize: 22, fontWeight: "300" },

  injuryRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
    marginTop: 10,
    marginBottom: 10,
    justifyContent: "center",
  },
  injuryPill: {
    backgroundColor: "#2A1A0E",
    paddingHorizontal: 12,
    paddingVertical: 5,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: "#FF6B35",
  },
  injuryPillText: {
    color: "#FF6B35",
    fontSize: 12,
    fontWeight: "600",
  },

  logoutBtn: {
    backgroundColor: "#1A1A1A",
    padding: 16,
    borderRadius: 14,
    alignItems: "center",
  },
  logoutText: {
    color: "#FF453A",
    fontSize: 16,
    fontWeight: "600",
  },
});
