import { IconSymbol } from "@/components/ui/icon-symbol";
import {
  TacticalCard,
  TacticalPressable,
  VoltButton,
} from "@/components/ui/TacticalBento";
import { Fonts, TacticalTheme } from "@/constants/theme";
import { HomeBleHeartRateCard } from "@/features/health/HomeBleHeartRateCard";
import { setLanguage, t, useLocale } from "@/features/i18n";
import {
  fetchProfileReadiness,
  type ProfileReadinessResponse,
} from "@/features/wod/api";
import { subscribeHistoryChanges } from "@/features/wod/archiveStates";
import { HomeReadinessCard } from "@/features/wod/ui/HomeReadinessCard";
import {
  useActiveProfile,
  useProfileId,
  useProfileStore,
} from "@/store/useProfileStore";
import { router, useFocusEffect } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

const T = TacticalTheme.light;

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
  const readinessRequest = useRef(0);
  const readinessFocused = useRef(false);
  const currentReadinessProfile = useRef(profileId);
  currentReadinessProfile.current = profileId;

  const loadReadiness = useCallback(
    async (pid: number | undefined, signal?: AbortSignal) => {
      const request = ++readinessRequest.current;
      const current = () => request === readinessRequest.current && pid === currentReadinessProfile.current && !signal?.aborted;
      setReadiness(null);
      if (!pid) {
        setReadiness(null);
        setReadinessError(false);
        return;
      }
      setReadinessLoading(true);
      setReadinessError(false);
      try {
        const res = await fetchProfileReadiness(pid, signal);
        if (current()) setReadiness(res);
      } catch (err: any) {
        if (current() && err?.name !== "AbortError") {
          setReadinessError(true);
        }
      } finally {
        if (current()) setReadinessLoading(false);
      }
    },
    [],
  );

  useFocusEffect(
    useCallback(() => {
      readinessFocused.current = true;
      const controller = new AbortController();
      void loadReadiness(profileId, controller.signal);
      return () => {
        readinessFocused.current = false;
        controller.abort();
        ++readinessRequest.current;
        setReadiness(null);
      };
    }, [profileId, loadReadiness]),
  );

  useEffect(() => subscribeHistoryChanges(pid => {
    if (pid === profileId) { setReadiness(null); if (readinessFocused.current) void loadReadiness(profileId); }
  }), [profileId, loadReadiness]);

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

  const compactMeta = activeProfile
    ? [
        t(`profileEdit.${activeProfile.fitnessLevel ?? "intermediate"}`),
        activeProfile.weightKg ? `${activeProfile.weightKg}kg` : null,
      ]
        .filter(Boolean)
        .join(" · ")
    : null;

  const monogram = activeProfile?.name
    ? activeProfile.name.slice(0, 2).toUpperCase()
    : null;

  const injuryCount = activeProfile?.injuries?.length ?? 0;

  return (
    <SafeAreaView style={styles.container} edges={["top", "left", "right"]}>
      <StatusBar style="dark" />
      <ScrollView
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={handleRefresh}
            tintColor={T.electricBlue}
          />
        }
      >
        {/* Top Tactical Bar */}
        <View style={styles.topBar}>
          <View style={styles.brandCol}>
            <View style={styles.eyebrowRow}>
              <View style={styles.eyebrowDot} />
              <Text style={styles.eyebrowText}>{t("home.brandEyebrow")}</Text>
            </View>
            <View style={styles.brandTitleRow}>
              <Text style={styles.brandTitle} numberOfLines={1}>
                WOD Strategist
              </Text>
              <View style={styles.brandVoltDot} />
            </View>
            <Text style={styles.greetingSub} numberOfLines={1}>
              {profileName
                ? t("home.greeting", { name: profileName })
                : t("home.welcome")}
            </Text>
          </View>

          <View style={styles.topActionsCol}>
            <TouchableOpacity
              style={styles.langToggle}
              onPress={() => setLanguage(locale === "ko" ? "en" : "ko")}
              activeOpacity={0.75}
              accessibilityRole="button"
            >
              <Text style={styles.langToggleText}>
                {locale === "ko" ? "EN" : "한국어"}
              </Text>
            </TouchableOpacity>
          </View>
        </View>

        <View style={styles.stack}>
          {/* Athlete Quick-Switch Bento Strip */}
          <TacticalPressable
            testID="home-profile-card"
            haptic
            style={[
              styles.profileCard,
              !activeProfile && styles.profileCardEmpty,
            ]}
            onPress={() => router.push("/profiles" as any)}
            accessibilityRole="button"
          >
            <View style={styles.monogramBox}>
              {monogram ? (
                <Text style={styles.monogramText}>{monogram}</Text>
              ) : (
                <IconSymbol name="person.fill" size={18} color={T.volt} />
              )}
            </View>
            <View style={styles.profileInfoCol}>
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
              <Text style={styles.profileMeta} numberOfLines={1}>
                {compactMeta ?? t("home.tapToChoose")}
              </Text>
            </View>
            <IconSymbol name="chevron.down" size={18} color={T.inkMuted} />
          </TacticalPressable>

          {/* BLE Heart Rate Telemetry Strip */}
          <HomeBleHeartRateCard />

          {/* Primary Action HERO Bento */}
          <TacticalCard style={styles.heroCard}>
            <View style={styles.heroAccentGlow} pointerEvents="none" />
            <View style={styles.heroEyebrowPill}>
              <View style={styles.heroEyebrowDot} />
              <Text style={styles.heroEyebrowText}>
                {t("home.startWorkoutCtaBadge")}
              </Text>
            </View>

            <Text style={styles.heroHeadline}>{t("home.startWorkout")}</Text>
            <Text style={styles.heroSubtitle}>
              {t("home.startWorkoutDesc")}
            </Text>

            <VoltButton
              testID="home-start-workout-cta"
              label={t("home.launchAction")}
              rightBadgeText={t("home.fastEntry")}
              leftIcon={
                <IconSymbol
                  name="play.circle.fill"
                  size={20}
                  color={T.voltInk}
                />
              }
              style={styles.heroLaunchBtn}
              onPress={() => router.push("/workout/setup")}
            />

            {/* Secondary Split Quick Entry */}
            <View style={styles.quickSplitRow}>
              <TouchableOpacity
                testID="home-quick-scan-btn"
                style={styles.quickSplitBtn}
                activeOpacity={0.75}
                accessibilityRole="button"
                onPress={() =>
                  router.push({
                    pathname: "/workout/setup",
                    params: { mode: "photo" },
                  })
                }
              >
                <IconSymbol name="camera.fill" size={15} color={T.ink} />
                <Text style={styles.quickSplitBtnText} numberOfLines={1}>
                  {t("home.quickScanBoard")}
                </Text>
              </TouchableOpacity>

              <TouchableOpacity
                testID="home-quick-text-btn"
                style={styles.quickSplitBtn}
                activeOpacity={0.75}
                accessibilityRole="button"
                onPress={() =>
                  router.push({
                    pathname: "/workout/setup",
                    params: { mode: "text" },
                  })
                }
              >
                <IconSymbol
                  name="bookmark.fill"
                  size={15}
                  color={T.electricBlue}
                />
                <Text style={styles.quickSplitBtnText} numberOfLines={1}>
                  {t("home.quickTextSaved")}
                </Text>
              </TouchableOpacity>
            </View>
          </TacticalCard>

          {/* Recovery & Muscle Fatigue Intelligence Bento */}
          <HomeReadinessCard
            readiness={readiness}
            loading={readinessLoading}
            error={readinessError}
            hasProfile={Boolean(activeProfile)}
            onPressHistory={() => router.push("/history")}
          />
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: T.canvas,
  },
  scrollContent: {
    paddingHorizontal: 16,
    paddingTop: 8,
    paddingBottom: 112,
  },
  topBar: {
    flexDirection: "row",
    alignItems: "flex-start",
    justifyContent: "space-between",
    marginBottom: 14,
    gap: 12,
  },
  brandCol: {
    flex: 1,
  },
  eyebrowRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    marginBottom: 2,
  },
  eyebrowDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: T.ink,
  },
  eyebrowText: {
    fontFamily: Fonts?.mono,
    fontSize: 10,
    fontWeight: "700",
    letterSpacing: 1.2,
    color: T.inkMuted,
    textTransform: "uppercase",
  },
  brandTitleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  brandTitle: {
    fontSize: 24,
    fontWeight: "900",
    letterSpacing: -0.5,
    color: T.ink,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  brandVoltDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: "rgba(11, 14, 20, 0.25)",
  },
  greetingSub: {
    fontSize: 13,
    fontWeight: "600",
    color: T.inkSecondary,
    marginTop: 2,
    flexShrink: 1,
  },
  topActionsCol: {
    flexDirection: "row",
    alignItems: "center",
    paddingTop: 4,
  },
  langToggle: {
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.borderStrong,
    borderRadius: 12,
    paddingHorizontal: 12,
    paddingVertical: 6,
  },
  langToggleText: {
    color: T.ink,
    fontSize: 12,
    fontWeight: "800",
  },
  stack: {
    gap: 12,
  },
  profileCard: {
    backgroundColor: T.surface,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: T.border,
    paddingHorizontal: 12,
    paddingVertical: 10,
    flexDirection: "row",
    alignItems: "center",
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 1 },
    shadowOpacity: 0.04,
    shadowRadius: 4,
    elevation: 1,
  },
  profileCardEmpty: {
    borderColor: T.borderStrong,
    borderStyle: "dashed",
  },
  monogramBox: {
    width: 36,
    height: 36,
    borderRadius: 10,
    backgroundColor: T.surfaceInverse,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 10,
  },
  monogramText: {
    color: T.volt,
    fontSize: 13,
    fontWeight: "900",
    letterSpacing: 0.4,
  },
  profileInfoCol: {
    flex: 1,
    marginRight: 8,
  },
  profileTitleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  profileTitle: {
    color: T.ink,
    fontSize: 15,
    fontWeight: "800",
    flexShrink: 1,
  },
  injuryBadge: {
    backgroundColor: T.statusWarningBg,
    borderWidth: 1,
    borderColor: T.statusWarningBorder,
    borderRadius: 6,
    paddingHorizontal: 6,
    paddingVertical: 1,
    flexShrink: 0,
  },
  injuryBadgeText: {
    color: T.statusWarning,
    fontSize: 10,
    fontWeight: "800",
  },
  profileMeta: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "600",
    marginTop: 2,
    flexShrink: 1,
  },
  heroCard: {
    position: "relative",
    overflow: "hidden",
    padding: 16,
  },
  heroAccentGlow: {
    position: "absolute",
    top: -32,
    right: -32,
    width: 120,
    height: 120,
    borderRadius: 60,
    backgroundColor: "rgba(212, 255, 0, 0.24)",
  },
  heroEyebrowPill: {
    alignSelf: "flex-start",
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: T.surfaceInverse,
    paddingHorizontal: 9,
    paddingVertical: 4,
    borderRadius: 6,
    marginBottom: 10,
  },
  heroEyebrowDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: T.volt,
  },
  heroEyebrowText: {
    fontFamily: Fonts?.mono,
    color: T.volt,
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 0.8,
    textTransform: "uppercase",
  },
  heroHeadline: {
    color: T.ink,
    fontSize: 28,
    fontWeight: "900",
    letterSpacing: -0.6,
    textTransform: "uppercase",
  },
  heroSubtitle: {
    color: T.inkMuted,
    fontSize: 13,
    fontWeight: "500",
    marginTop: 4,
    lineHeight: 18,
  },
  heroLaunchBtn: {
    marginTop: 14,
  },
  quickSplitRow: {
    flexDirection: "row",
    gap: 8,
    marginTop: 10,
  },
  quickSplitBtn: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 6,
    paddingVertical: 10,
    paddingHorizontal: 10,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    borderRadius: 12,
  },
  quickSplitBtnText: {
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "700",
    flexShrink: 1,
  },
});
