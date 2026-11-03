import { IconSymbol } from "@/components/ui/icon-symbol";
import { VoltButton } from "@/components/ui/TacticalBento";
import { Fonts, TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useProfileStore, type Profile } from "@/store/useProfileStore";
import { router } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useCallback } from "react";
import {
  Alert,
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

const T = TacticalTheme.light;
const monoFont = Fonts?.mono ?? "monospace";

export default function ProfilesScreen() {
  useLocale();
  const { profiles, activeProfileId, selectProfile, archiveProfile } =
    useProfileStore();

  const handleSelect = useCallback(
    (profile: Profile) => {
      selectProfile(profile.id);
      router.back();
    },
    [selectProfile],
  );

  const handleEdit = useCallback((profile: Profile) => {
    router.push({
      pathname: "/profile-edit",
      params: { id: String(profile.id) },
    } as any);
  }, []);

  const handleArchive = useCallback(
    (profile: Profile) => {
      Alert.alert(
        t("profiles.archiveProfile"),
        t("profiles.archiveConfirm", {
          name: profile.name || t("common.profileNumber", { id: profile.id }),
        }),
        [
          { text: t("common.cancel"), style: "cancel" },
          {
            text: t("profiles.archive"),
            style: "destructive",
            onPress: () => archiveProfile(profile.id),
          },
        ],
      );
    },
    [archiveProfile],
  );

  const handleAddProfile = useCallback(() => {
    router.push("/profile-edit" as any);
  }, []);

  const genderLabel = (g: string) =>
    g === "male"
      ? t("common.maleShort")
      : g === "female"
        ? t("common.femaleShort")
        : t("common.otherShort");

  const summaryLine = (p: Profile) => {
    const parts: string[] = [];
    if (p.gender) parts.push(genderLabel(p.gender));
    if (p.birthYear) parts.push(String(p.birthYear));
    if (p.heightCm) parts.push(`${p.heightCm}cm`);
    if (p.weightKg) parts.push(`${p.weightKg}kg`);
    return parts.join(" · ");
  };

  const renderProfile = ({ item }: { item: Profile }) => {
    const isActive = item.id === activeProfileId;
    const monogram = item.name ? item.name.slice(0, 2).toUpperCase() : "👤";
    const summary = summaryLine(item);

    return (
      <Pressable
        testID={`roster-card-${item.id}`}
        style={[styles.card, isActive && styles.cardActive]}
        onPress={() => handleSelect(item)}
        onLongPress={() => handleArchive(item)}
      >
        <View style={styles.cardContent}>
          {/* Avatar Circle */}
          <View style={[styles.avatar, isActive && styles.avatarActive]}>
            <Text style={[styles.avatarText, isActive && styles.avatarTextActive]}>
              {monogram}
            </Text>
          </View>

          {/* Profile Info */}
          <View style={styles.info}>
            <View style={styles.nameRow}>
              <Text style={styles.name} numberOfLines={1}>
                {item.name || t("common.profileNumber", { id: item.id })}
              </Text>
              {isActive && (
                <View style={styles.activeBadge}>
                  <Text style={styles.activeText}>{t("common.active")}</Text>
                </View>
              )}
            </View>
            {summary ? <Text style={styles.summary}>{summary}</Text> : null}
            {item.fitnessLevel ? (
              <Text style={styles.levelMeta}>
                {t(`profileEdit.${item.fitnessLevel}`)}
              </Text>
            ) : null}
          </View>

          {/* Actions */}
          <View style={styles.actionGroup}>
            {!isActive && (
              <Pressable
                testID={`switch-profile-${item.id}`}
                style={styles.switchBtn}
                onPress={() => handleSelect(item)}
                hitSlop={8}
              >
                <Text style={styles.switchBtnText}>
                  {t("profiles.switchBtn")}
                </Text>
              </Pressable>
            )}
            <Pressable
              testID={`edit-profile-${item.id}`}
              style={styles.editBtn}
              onPress={() => handleEdit(item)}
              hitSlop={8}
            >
              <Text style={styles.editBtnText}>{t("common.edit")}</Text>
            </Pressable>
          </View>
        </View>
      </Pressable>
    );
  };

  return (
    <SafeAreaView style={styles.container}>
      <StatusBar style="dark" />
      <View style={styles.header}>
        <TouchableOpacity
          testID="profiles-back-btn"
          onPress={() => router.back()}
          style={styles.backBtn}
          accessibilityRole="button"
        >
          <IconSymbol name="chevron.left" size={22} color={T.ink} />
        </TouchableOpacity>
        <Text style={styles.title} numberOfLines={1}>
          {t("profiles.title")}
        </Text>
        <View style={styles.rosterBadge}>
          <Text style={styles.rosterBadgeText}>
            {t("profiles.rosterCount", { count: profiles.length })}
          </Text>
        </View>
      </View>

      <FlatList
        data={profiles}
        keyExtractor={(item) => String(item.id)}
        renderItem={renderProfile}
        contentContainerStyle={styles.list}
        ListEmptyComponent={
          <View style={styles.emptyContainer}>
            <Text style={styles.emptyIcon}>👤</Text>
            <Text style={styles.emptyTitle}>{t("profiles.noProfiles")}</Text>
            <Text style={styles.emptyDesc}>
              {t("profiles.noProfilesDesc")}
            </Text>
          </View>
        }
        ListFooterComponent={
          <VoltButton
            testID="add-profile-btn"
            label={`+ ${t("profiles.addNew")}`}
            onPress={handleAddProfile}
            style={{ marginTop: 6 }}
          />
        }
      />

      <Text style={styles.hint}>{t("profiles.hint")}</Text>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: T.canvas },
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 18,
    paddingVertical: 14,
    gap: 12,
  },
  backBtn: {
    width: 38,
    height: 38,
    borderRadius: 12,
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    alignItems: "center",
    justifyContent: "center",
  },
  title: {
    flex: 1,
    flexShrink: 1,
    fontSize: 22,
    fontWeight: "900",
    letterSpacing: -0.5,
    color: T.ink,
  },
  rosterBadge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 8,
  },
  rosterBadgeText: {
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 0.6,
    fontFamily: monoFont,
  },

  list: { paddingHorizontal: 18, paddingTop: 4, paddingBottom: 24 },

  card: {
    backgroundColor: T.surface,
    borderRadius: 16,
    marginBottom: 12,
    borderWidth: 1,
    borderColor: T.border,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 8,
    elevation: 2,
  },
  cardActive: {
    borderWidth: 2,
    borderColor: T.electricBlue,
  },
  cardContent: {
    flexDirection: "row",
    alignItems: "center",
    padding: 16,
    gap: 12,
  },

  avatar: {
    width: 48,
    height: 48,
    borderRadius: 24,
    backgroundColor: T.surfaceContainer,
    justifyContent: "center",
    alignItems: "center",
  },
  avatarActive: {
    backgroundColor: T.electricBlue,
  },
  avatarText: {
    fontSize: 17,
    fontWeight: "900",
    color: T.inkSecondary,
  },
  avatarTextActive: {
    color: "#FFFFFF",
  },

  info: { flex: 1, gap: 2 },
  nameRow: {
    flexDirection: "row",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 8,
  },
  name: {
    color: T.ink,
    fontSize: 17,
    fontWeight: "800",
    flexShrink: 1,
  },
  summary: {
    color: T.inkMuted,
    fontSize: 13,
    fontWeight: "600",
    flexShrink: 1,
  },
  levelMeta: {
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "700",
    marginTop: 1,
  },
  activeBadge: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 6,
  },
  activeText: {
    color: T.voltInk,
    fontSize: 10,
    fontWeight: "900",
    letterSpacing: 0.5,
    textTransform: "uppercase",
  },

  actionGroup: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  switchBtn: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 10,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  switchBtnText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
  },
  editBtn: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 10,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  editBtnText: {
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "800",
  },

  emptyContainer: {
    alignItems: "center",
    paddingVertical: 48,
    paddingHorizontal: 24,
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    marginBottom: 14,
  },
  emptyIcon: { fontSize: 44, marginBottom: 12 },
  emptyTitle: {
    color: T.ink,
    fontSize: 18,
    fontWeight: "800",
    marginBottom: 6,
  },
  emptyDesc: {
    color: T.inkMuted,
    fontSize: 14,
    textAlign: "center",
    lineHeight: 20,
  },

  hint: {
    color: T.inkMuted,
    fontSize: 12,
    fontWeight: "600",
    textAlign: "center",
    paddingBottom: 16,
  },
});
