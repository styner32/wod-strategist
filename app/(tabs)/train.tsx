import { IconSymbol } from "@/components/ui/icon-symbol";
import { TacticalPressable } from "@/components/ui/TacticalBento";
import { Fonts, TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useVideoQueue } from "@/store/useVideoQueue";
import { router } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

const T = TacticalTheme.light;
const monoFont = Fonts?.mono ?? "monospace";

export default function TrainScreen() {
  useLocale();
  const queueCount = useVideoQueue((s) => s.items.length);

  return (
    <SafeAreaView style={styles.container} edges={["top", "left", "right"]}>
      <StatusBar style="dark" />
      <ScrollView
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
        {/* Tactical Top Bar */}
        <View style={styles.topBar}>
          <View style={styles.eyebrowRow}>
            <View style={styles.eyebrowDot} />
            <Text style={styles.eyebrowText}>{t("train.eyebrow")}</Text>
          </View>
          <View style={styles.titleRow}>
            <Text style={styles.heroTitle}>{t("train.heroTitle")}</Text>
            <View style={styles.voltDot} />
          </View>
          <Text style={styles.heroSubtitle}>{t("train.heroSubtitle")}</Text>
        </View>

        {/* Primary Actions */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t("train.startSession")}</Text>

          <TacticalPressable
            testID="train-start-workout-card"
            haptic
            style={styles.primaryCard}
            onPress={() => router.push("/workout/setup")}
          >
            <View style={styles.primaryIconBox}>
              <IconSymbol name="bolt.fill" size={26} color={T.voltInk} />
            </View>
            <View style={styles.cardBody}>
              <Text style={styles.primaryCardTitle}>
                {t("train.startWorkout")}
              </Text>
              <Text style={styles.primaryCardDesc}>
                {t("train.startWorkoutDesc")}
              </Text>
            </View>
            <Text style={styles.chevron}>›</Text>
          </TacticalPressable>

          <TacticalPressable
            testID="train-upload-card"
            haptic
            style={styles.card}
            onPress={() => router.push("/upload")}
          >
            <View style={[styles.iconBox, { backgroundColor: T.electricBlueBg }]}>
              <IconSymbol
                name="arrow.up.circle.fill"
                size={22}
                color={T.electricBlue}
              />
            </View>
            <View style={styles.cardBody}>
              <Text style={styles.cardTitle}>{t("train.analyseForm")}</Text>
              <Text style={styles.cardDesc}>{t("train.analyseFormDesc")}</Text>
            </View>
            <Text style={styles.chevron}>›</Text>
          </TacticalPressable>
        </View>

        {/* Queue Section */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t("train.processing")}</Text>

          <TacticalPressable
            testID="train-queue-card"
            haptic
            style={styles.card}
            onPress={() => router.push("/queue" as any)}
          >
            <View style={[styles.iconBox, { backgroundColor: T.statusWarningBg }]}>
              <IconSymbol
                name="arrow.clockwise"
                size={22}
                color={T.statusWarning}
              />
            </View>
            <View style={styles.cardBody}>
              <Text style={styles.cardTitle}>{t("train.videoQueue")}</Text>
              <Text style={styles.cardDesc}>{t("train.videoQueueDesc")}</Text>
            </View>
            {queueCount > 0 ? (
              <View style={styles.queueBadge}>
                <Text style={styles.queueBadgeText}>{queueCount}</Text>
              </View>
            ) : null}
            <Text style={styles.chevron}>›</Text>
          </TacticalPressable>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: T.canvas },
  scrollContent: { paddingHorizontal: 18, paddingTop: 10, paddingBottom: 112 },

  topBar: {
    marginBottom: 24,
    gap: 2,
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
    backgroundColor: T.electricBlue,
  },
  eyebrowText: {
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 1.1,
    color: T.electricBlue,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  titleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  heroTitle: {
    color: T.ink,
    fontSize: 26,
    fontWeight: "900",
    letterSpacing: -0.6,
    flexShrink: 1,
  },
  voltDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
  },
  heroSubtitle: {
    color: T.inkMuted,
    fontSize: 14,
    fontWeight: "500",
    marginTop: 4,
    lineHeight: 20,
  },

  section: { marginBottom: 24 },
  sectionTitle: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 1.1,
    marginBottom: 10,
    marginLeft: 2,
  },

  primaryCard: {
    backgroundColor: T.surface,
    borderWidth: 1.5,
    borderColor: T.electricBlueBorder,
    padding: 18,
    borderRadius: 18,
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 12,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 3 },
    shadowOpacity: 0.06,
    shadowRadius: 10,
    elevation: 2,
  },
  primaryIconBox: {
    width: 52,
    height: 52,
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    borderRadius: 14,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 14,
  },
  cardBody: {
    flex: 1,
    gap: 3,
  },
  primaryCardTitle: {
    color: T.ink,
    fontSize: 17,
    fontWeight: "900",
    letterSpacing: -0.3,
    flexShrink: 1,
  },
  primaryCardDesc: {
    color: T.inkMuted,
    fontSize: 13,
    lineHeight: 18,
    flexShrink: 1,
  },

  card: {
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    padding: 16,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 10,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 6,
    elevation: 1,
  },
  iconBox: {
    width: 44,
    height: 44,
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 14,
  },
  cardTitle: {
    color: T.ink,
    fontSize: 16,
    fontWeight: "800",
    flexShrink: 1,
  },
  cardDesc: {
    color: T.inkMuted,
    fontSize: 13,
    lineHeight: 18,
    flexShrink: 1,
  },
  queueBadge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 999,
    marginRight: 8,
  },
  queueBadgeText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
    fontFamily: monoFont,
  },
  chevron: { color: T.inkMuted, fontSize: 22, fontWeight: "600", marginLeft: 4 },
});
