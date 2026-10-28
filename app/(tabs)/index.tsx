import { IconSymbol } from "@/components/ui/icon-symbol";
import { HomeBleHeartRateCard } from "@/features/health/HomeBleHeartRateCard";
import { setLanguage, t, useLocale } from "@/features/i18n";
import {
  fetchProfileReadiness,
  type ProfileReadinessResponse,
} from "@/features/wod/api";
import { HomeReadinessCard } from "@/features/wod/ui/HomeReadinessCard";
import {
  useActiveProfile,
  useProfileId,
  useProfileStore,
} from "@/store/useProfileStore";
import { router, useFocusEffect } from "expo-router";
import React, { useCallback, useState } from "react";
import {
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

export default function Dashboard() {
  const activeProfile = useActiveProfile();
  const profileId = useProfileId();
  const loadProfiles = useProfileStore((s) => s.loadProfiles);
  const locale = useLocale();

  const [readiness, setReadiness] = useState<ProfileReadinessResponse | null>(
    null,
  );
  const [readinessLoading, setReadinessLoading] = useState(false);
  const [readinessError, setReadinessError] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  const loadReadiness = useCallback(
    async (pid: number | undefined, signal?: AbortSignal) => {
      if (!pid) {
        setReadiness(null);
        setReadinessError(false);
        return;
      }
      setReadinessLoading(true);
      setReadinessError(false);
      try {
        const res = await fetchProfileReadiness(pid, signal);
        setReadiness(res);
      } catch (err: any) {
        if (err?.name !== "AbortError") {
          setReadinessError(true);
        }
      } finally {
        setReadinessLoading(false);
      }
    },
    [],
  );

  useFocusEffect(
    useCallback(() => {
      const controller = new AbortController();
      void loadReadiness(profileId, controller.signal);
      return () => {
        controller.abort();
      };
    }, [profileId, loadReadiness]),
  );

  const handleRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await Promise.all([loadProfiles(), loadReadiness(profileId)]);
    } finally {
      setRefreshing(false);
    }
  }, [loadProfiles, loadReadiness, profileId]);

  const profileName =
    activeProfile?.name ||
    (profileId ? t("common.profileNumber", { id: profileId }) : null);

  const summaryLine = activeProfile
    ? [
        activeProfile.gender === "male"
          ? t("common.maleShort")
          : activeProfile.gender === "female"
            ? t("common.femaleShort")
            : t("common.otherShort"),
        activeProfile.birthYear ? String(activeProfile.birthYear) : null,
        activeProfile.heightCm ? `${activeProfile.heightCm}cm` : null,
        activeProfile.weightKg ? `${activeProfile.weightKg}kg` : null,
        t(`profileEdit.${activeProfile.fitnessLevel ?? "intermediate"}`),
      ]
        .filter(Boolean)
        .join(" · ")
    : null;

  const injuryCount = activeProfile?.injuries?.length ?? 0;

  return (
    <SafeAreaView style={styles.container} edges={["left", "right"]}>
      <ScrollView
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={handleRefresh}
            tintColor="#00E5FF"
          />
        }
      >
        {/* Header */}
        <View style={styles.headerRow}>
          <Text style={styles.title} numberOfLines={1}>
            {profileName
              ? t("home.greeting", { name: profileName })
              : t("home.welcome")}
          </Text>
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

        <View style={styles.stack}>
          {/* Active Profile Card */}
          <TouchableOpacity
            testID="home-profile-card"
            style={[
              styles.profileCard,
              !activeProfile && styles.profileCardEmpty,
            ]}
            activeOpacity={0.8}
            onPress={() => router.push("/profiles" as any)}
            accessibilityRole="button"
          >
            <View style={styles.profileIconBox}>
              {activeProfile?.name ? (
                <Text style={styles.profileIconText}>
                  {activeProfile.name[0].toUpperCase()}
                </Text>
              ) : (
                <IconSymbol name="person.fill" size={20} color="#00E5FF" />
              )}
            </View>
            <View style={{ flex: 1, marginRight: 8 }}>
              <View style={styles.profileTitleRow}>
                <Text style={styles.profileTitle} numberOfLines={1}>
                  {activeProfile ? profileName : t("home.selectProfile")}
                </Text>
                {injuryCount > 0 && (
                  <View style={styles.injuryBadge}>
                    <Text style={styles.injuryBadgeText}>
                      {t("setup.injuries", { count: injuryCount })}
                    </Text>
                  </View>
                )}
              </View>
              <Text style={styles.profileDesc} numberOfLines={1}>
                {summaryLine ?? t("home.tapToChoose")}
              </Text>
            </View>
            <IconSymbol name="chevron.right" size={20} color="#475569" />
          </TouchableOpacity>

          {/* BLE Heart Rate Sensor Status / Connect Chip */}
          <HomeBleHeartRateCard />

          {/* Recent Workout Recovery & Muscle Fatigue Card */}
          <HomeReadinessCard
            readiness={readiness}
            loading={readinessLoading}
            error={readinessError}
            hasProfile={Boolean(activeProfile)}
            onPressHistory={() => router.push("/history")}
          />

          {/* Single Primary CTA: Start Workout */}
          <TouchableOpacity
            testID="home-start-workout-cta"
            style={styles.startCtaCard}
            activeOpacity={0.85}
            onPress={() => router.push("/workout/setup")}
            accessibilityRole="button"
          >
            <View style={styles.startCtaIconBox}>
              <IconSymbol name="flame.fill" size={28} color="#001219" />
            </View>
            <View style={styles.startCtaTextCol}>
              <Text style={styles.startCtaBadge}>
                {t("home.startWorkoutCtaBadge")}
              </Text>
              <Text style={styles.startCtaTitle}>
                {t("home.startWorkout")}
              </Text>
              <Text style={styles.startCtaDesc}>
                {t("home.startWorkoutDesc")}
              </Text>
            </View>
            <IconSymbol name="play.circle.fill" size={34} color="#00E5FF" />
          </TouchableOpacity>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000" },
  scrollContent: { padding: 20, paddingTop: 12, paddingBottom: 40 },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 20,
  },
  title: {
    color: "#FFFFFF",
    fontSize: 26,
    fontWeight: "800",
    flex: 1,
    marginRight: 12,
  },
  langToggle: {
    backgroundColor: "#121720",
    borderWidth: 1,
    borderColor: "#243042",
    borderRadius: 20,
    paddingHorizontal: 14,
    paddingVertical: 7,
  },
  langToggleText: {
    color: "#64D2FF",
    fontSize: 13,
    fontWeight: "700",
  },
  stack: {
    gap: 14,
  },
  profileCard: {
    backgroundColor: "#121720",
    padding: 15,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#1E3A5F",
  },
  profileCardEmpty: {
    borderColor: "#334155",
    borderStyle: "dashed",
  },
  profileIconBox: {
    width: 44,
    height: 44,
    backgroundColor: "#002B3D",
    borderRadius: 22,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 12,
  },
  profileIconText: {
    fontSize: 18,
    fontWeight: "800",
    color: "#00E5FF",
  },
  profileTitleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  profileTitle: {
    color: "#F8FAFC",
    fontSize: 16,
    fontWeight: "700",
    flexShrink: 1,
  },
  injuryBadge: {
    backgroundColor: "rgba(255, 159, 10, 0.16)",
    borderRadius: 6,
    paddingHorizontal: 6,
    paddingVertical: 2,
    flexShrink: 0,
  },
  injuryBadgeText: {
    color: "#FF9F0A",
    fontSize: 11,
    fontWeight: "700",
  },
  profileDesc: {
    color: "#94A3B8",
    fontSize: 13,
    marginTop: 3,
  },
  startCtaCard: {
    marginTop: 6,
    backgroundColor: "#091E28",
    borderRadius: 20,
    padding: 20,
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1.5,
    borderColor: "#00E5FF",
  },
  startCtaIconBox: {
    width: 54,
    height: 54,
    borderRadius: 16,
    backgroundColor: "#00E5FF",
    justifyContent: "center",
    alignItems: "center",
    marginRight: 16,
    flexShrink: 0,
  },
  startCtaTextCol: {
    flex: 1,
    marginRight: 12,
  },
  startCtaBadge: {
    color: "#00E5FF",
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 1.2,
    marginBottom: 2,
  },
  startCtaTitle: {
    color: "#FFFFFF",
    fontSize: 20,
    fontWeight: "800",
  },
  startCtaDesc: {
    color: "#94A3B8",
    fontSize: 13,
    marginTop: 3,
  },
});
