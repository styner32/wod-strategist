import { IconSymbol } from "@/components/ui/icon-symbol";
import { Fonts, TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import {
  fetchMovementGroups,
  getUploadUrl,
  notifyUploadComplete,
  uploadToGcs,
  type MovementGroup,
} from "@/features/wod/api";
import { buildWorkoutSessionId } from "@/features/wod/workoutType";
import { useProfileId } from "@/store/useProfileStore";
import * as ImagePicker from "expo-image-picker";
import { router } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  GestureResponderEvent,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

const T = TacticalTheme.light;
const monoFont = Fonts?.mono ?? "monospace";
const ALL_FILTER = "All";

const CATEGORY_ICONS: Record<string, string> = {
  Barbell: "dumbbell.fill",
  "Dumbbell & Kettlebell": "dumbbell.fill",
  Gymnastics: "figure.run",
  "Bodyweight & Plyo": "flame.fill",
  Cardio: "figure.run",
  Custom: "plus.circle.fill",
};

function getCategoryIcon(category: string): string {
  return CATEGORY_ICONS[category] || "dumbbell.fill";
}

export default function UploadScreen() {
  useLocale();
  const profileId = useProfileId();
  const [selectedVideo, setSelectedVideo] = useState<string | null>(null);
  const [fileName, setFileName] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);

  const [movementGroups, setMovementGroups] = useState<MovementGroup[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [customMovements, setCustomMovements] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState("");
  const [activeFilter, setActiveFilter] = useState(ALL_FILTER);

  useEffect(() => {
    fetchMovementGroups()
      .then(setMovementGroups)
      .catch((e) => console.error("Failed to load movement groups", e))
      .finally(() => setLoading(false));
  }, []);

  const categoryTabs = useMemo(() => {
    const cats = movementGroups.map((g) => g.category);
    if (customMovements.length > 0 && !cats.includes("Custom")) {
      cats.push("Custom");
    }
    return [ALL_FILTER, ...cats];
  }, [movementGroups, customMovements]);

  const allKnownMovements = useMemo(() => {
    const set = new Set<string>();
    for (const g of movementGroups) {
      for (const m of g.movements) set.add(m);
    }
    return set;
  }, [movementGroups]);

  const filteredGroups = useMemo(() => {
    const query = searchText.toLowerCase().trim();
    let groups = movementGroups;

    if (customMovements.length > 0) {
      groups = [...groups, { category: "Custom", movements: customMovements }];
    }
    if (activeFilter !== ALL_FILTER) {
      groups = groups.filter((g) => g.category === activeFilter);
    }
    if (query) {
      groups = groups
        .map((g) => ({
          ...g,
          movements: g.movements.filter((m) =>
            m.toLowerCase().includes(query),
          ),
        }))
        .filter((g) => g.movements.length > 0);
    }
    return groups;
  }, [movementGroups, customMovements, activeFilter, searchText]);

  const canAddCustom = useMemo(() => {
    const trimmed = searchText.trim();
    if (!trimmed) return false;
    const lower = trimmed.toLowerCase();
    for (const m of allKnownMovements) {
      if (m.toLowerCase() === lower) return false;
    }
    for (const m of customMovements) {
      if (m.toLowerCase() === lower) return false;
    }
    return true;
  }, [searchText, allKnownMovements, customMovements]);

  const toggleMovement = (m: string) => {
    setSelectedMovements((prev) =>
      prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m],
    );
  };

  const addCustomMovement = () => {
    const trimmed = searchText.trim();
    if (!trimmed) return;
    setCustomMovements((prev) => [...prev, trimmed]);
    setSelectedMovements((prev) => [...prev, trimmed]);
    setSearchText("");
  };

  const removeCustomMovement = (m: string) => {
    setCustomMovements((prev) => prev.filter((x) => x !== m));
    setSelectedMovements((prev) => prev.filter((x) => x !== m));
  };

  const pickVideo = async () => {
    const result = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ["videos"],
      quality: 1,
    });
    if (!result.canceled && result.assets[0]) {
      setSelectedVideo(result.assets[0].uri);
      setFileName(
        result.assets[0].fileName ??
          result.assets[0].uri.split("/").pop() ??
          "video.mp4",
      );
    }
  };

  const handleUpload = async () => {
    if (!selectedVideo) return;

    if (!profileId) {
      Alert.alert(t("upload.profileRequired"), t("upload.profileRequiredDesc"));
      return;
    }

    setUploading(true);
    setProgress(0);

    try {
      const sessionId = buildWorkoutSessionId("wod");
      const name = fileName ?? "video.mp4";

      const { upload_url, gcs_uri } = await getUploadUrl(
        sessionId,
        name,
        profileId,
      );

      await uploadToGcs(upload_url, selectedVideo, "video/mp4", (p) =>
        setProgress(p),
      );

      await notifyUploadComplete(
        sessionId,
        gcs_uri,
        selectedMovements,
        [],
        "wod",
        profileId,
      );

      Alert.alert(t("upload.success"), t("upload.analysisStarted"), [
        {
          text: t("upload.viewHistory"),
          onPress: () => router.push("/(tabs)/history"),
        },
      ]);

      setSelectedVideo(null);
      setFileName(null);
    } catch (e: any) {
      console.error("Upload error:", e?.message);
      const detail = String(e?.message ?? "");
      const msg = detail.startsWith("Network")
        ? t("upload.networkError")
        : `${t("upload.uploadFailed")}\n\n${detail}`;
      Alert.alert(t("common.error"), msg);
    } finally {
      setUploading(false);
      setProgress(0);
    }
  };

  return (
    <SafeAreaView style={styles.container}>
      <StatusBar style="dark" />
      <View style={styles.header}>
        <TouchableOpacity
          onPress={() => router.back()}
          style={styles.backBtn}
          accessibilityRole="button"
        >
          <IconSymbol name="chevron.left" size={22} color={T.ink} />
        </TouchableOpacity>
        <Text style={styles.title} numberOfLines={1}>
          {t("upload.title")}
        </Text>
        <View style={{ width: 38 }} />
      </View>

      {/* Search bar */}
      <View style={styles.searchContainer}>
        <IconSymbol name="magnifyingglass" size={18} color={T.inkMuted} />
        <TextInput
          style={styles.searchInput}
          placeholder={t("setup.searchMovements")}
          placeholderTextColor={T.inkMuted}
          value={searchText}
          onChangeText={setSearchText}
          autoCapitalize="none"
          autoCorrect={false}
        />
        {searchText.length > 0 && (
          <TouchableOpacity onPress={() => setSearchText("")}>
            <IconSymbol name="xmark.circle.fill" size={18} color={T.inkMuted} />
          </TouchableOpacity>
        )}
      </View>

      {/* Category filter tabs */}
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        contentContainerStyle={styles.filterTabsContent}
        style={styles.filterTabs}
      >
        {categoryTabs.map((tab) => {
          const isActive = activeFilter === tab;
          return (
            <TouchableOpacity
              key={tab}
              style={[styles.filterTab, isActive && styles.filterTabActive]}
              onPress={() => setActiveFilter(tab)}
            >
              <Text
                style={[
                  styles.filterTabText,
                  isActive && styles.filterTabTextActive,
                ]}
              >
                {tab === ALL_FILTER ? t("setup.categoryAll") : tab}
              </Text>
            </TouchableOpacity>
          );
        })}
      </ScrollView>

      {/* Movement list */}
      <ScrollView
        contentContainerStyle={styles.movementListContent}
        style={styles.movementList}
        showsVerticalScrollIndicator={false}
      >
        {/* Video Picker */}
        <TouchableOpacity
          style={[
            styles.pickerBox,
            selectedVideo && styles.pickerBoxSelected,
          ]}
          onPress={pickVideo}
        >
          {selectedVideo ? (
            <View style={styles.selectedVideoInfo}>
              <Text style={styles.selectedIcon}>✅</Text>
              <Text style={styles.selectedFileName} numberOfLines={1}>
                {fileName}
              </Text>
              <TouchableOpacity onPress={pickVideo}>
                <Text style={styles.changeVideoText}>
                  {t("upload.tapToSelect")}
                </Text>
              </TouchableOpacity>
            </View>
          ) : (
            <>
              <Text style={styles.pickerIcon}>🎬</Text>
              <Text style={styles.pickerText}>{t("upload.tapToSelect")}</Text>
            </>
          )}
        </TouchableOpacity>

        {loading ? (
          <ActivityIndicator color={T.electricBlue} style={{ marginTop: 40 }} />
        ) : (
          <>
            {canAddCustom && (
              <TouchableOpacity
                style={styles.addCustomBtn}
                onPress={addCustomMovement}
              >
                <IconSymbol
                  name="plus.circle.fill"
                  size={20}
                  color={T.electricBlue}
                />
                <Text style={styles.addCustomText}>
                  {t("setup.addCustomMovement", { name: searchText.trim() })}
                </Text>
              </TouchableOpacity>
            )}

            {filteredGroups.map((group) => (
              <View key={group.category} style={styles.groupSection}>
                <Text style={styles.groupHeader}>
                  {group.category.toUpperCase()}
                </Text>
                {group.movements.map((movement) => {
                  const isSelected = selectedMovements.includes(movement);
                  const isCustom = customMovements.includes(movement);
                  return (
                    <TouchableOpacity
                      key={movement}
                      style={[
                        styles.movementCard,
                        isSelected && styles.movementCardSelected,
                      ]}
                      onPress={() => toggleMovement(movement)}
                    >
                      <View
                        style={[
                          styles.movementCardIcon,
                          isSelected && styles.movementCardIconSelected,
                        ]}
                      >
                        <IconSymbol
                          name={getCategoryIcon(group.category) as any}
                          size={20}
                          color={isSelected ? T.electricBlue : T.inkMuted}
                        />
                      </View>
                      <View style={styles.movementCardInfo}>
                        <Text
                          style={[
                            styles.movementName,
                            isSelected && styles.movementNameSelected,
                          ]}
                        >
                          {movement.toUpperCase()}
                        </Text>
                        <Text style={styles.movementCategory}>
                          {group.category.toUpperCase()}
                        </Text>
                      </View>
                      {isCustom ? (
                        <TouchableOpacity
                          onPress={(e: GestureResponderEvent) => {
                            e.stopPropagation();
                            removeCustomMovement(movement);
                          }}
                          hitSlop={{
                            top: 10,
                            bottom: 10,
                            left: 10,
                            right: 10,
                          }}
                        >
                          <IconSymbol
                            name="xmark.circle"
                            size={22}
                            color={T.inkMuted}
                          />
                        </TouchableOpacity>
                      ) : (
                        <View
                          style={[
                            styles.checkbox,
                            isSelected && styles.checkboxSelected,
                          ]}
                        >
                          {isSelected && (
                            <IconSymbol
                              name="checkmark"
                              size={14}
                              color="#FFF"
                            />
                          )}
                        </View>
                      )}
                    </TouchableOpacity>
                  );
                })}
              </View>
            ))}

            {filteredGroups.length === 0 && !canAddCustom && (
              <View style={styles.emptyState}>
                <Text style={styles.emptyText}>{t("setup.noResults")}</Text>
              </View>
            )}
          </>
        )}
      </ScrollView>

      {/* Bottom bar with staging chips + upload button */}
      <View style={styles.bottomBar}>
        {selectedMovements.length > 0 && (
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            contentContainerStyle={styles.stagedChipsContent}
            style={styles.stagedChipsScroll}
          >
            {selectedMovements.map((m) => (
              <View key={m} style={styles.stagedChip}>
                <Text style={styles.stagedChipText}>{m}</Text>
                <TouchableOpacity
                  onPress={() => toggleMovement(m)}
                  hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}
                >
                  <IconSymbol
                    name="xmark.circle.fill"
                    size={14}
                    color={T.electricBlue}
                  />
                </TouchableOpacity>
              </View>
            ))}
          </ScrollView>
        )}
        <TouchableOpacity
          style={[
            styles.uploadBtn,
            (!selectedVideo || uploading) && styles.uploadBtnDisabled,
          ]}
          onPress={handleUpload}
          disabled={!selectedVideo || uploading}
        >
          {uploading ? (
            <View style={styles.uploadingRow}>
              <ActivityIndicator color={T.voltInk} size="small" />
              <Text style={styles.uploadBtnText}>
                {t("upload.uploading", {
                  percent: Math.round(progress * 100),
                })}
              </Text>
            </View>
          ) : (
            <Text style={styles.uploadBtnText}>{t("upload.analyzeWod")}</Text>
          )}
        </TouchableOpacity>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: T.canvas },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 18,
    paddingVertical: 12,
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
    textAlign: "center",
    fontSize: 18,
    fontWeight: "900",
    color: T.ink,
    letterSpacing: -0.3,
    flexShrink: 1,
  },

  // Search bar
  searchContainer: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 18,
    marginTop: 4,
    marginBottom: 8,
    backgroundColor: T.surface,
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 11,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  searchInput: {
    flex: 1,
    color: T.ink,
    fontSize: 15,
    fontWeight: "600",
    marginLeft: 10,
    paddingVertical: 0,
  },

  // Filter tabs
  filterTabs: {
    maxHeight: 44,
    marginBottom: 4,
  },
  filterTabsContent: {
    paddingHorizontal: 18,
    gap: 8,
    alignItems: "center",
  },
  filterTab: {
    paddingHorizontal: 14,
    paddingVertical: 7,
    borderRadius: 20,
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
  },
  filterTabActive: {
    backgroundColor: T.electricBlueBg,
    borderColor: T.electricBlue,
  },
  filterTabText: {
    color: T.inkMuted,
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  filterTabTextActive: {
    color: T.electricBlue,
    fontWeight: "800",
  },

  // Movement list
  movementList: { flex: 1 },
  movementListContent: { paddingHorizontal: 18, paddingBottom: 180 },

  // Video picker
  pickerBox: {
    height: 140,
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 2,
    borderColor: T.borderStrong,
    borderStyle: "dashed",
    justifyContent: "center",
    alignItems: "center",
    marginBottom: 20,
    marginTop: 8,
  },
  pickerBoxSelected: {
    borderColor: T.electricBlue,
    borderStyle: "solid",
    backgroundColor: T.electricBlueBg,
  },
  pickerIcon: { fontSize: 36, marginBottom: 8 },
  pickerText: { color: T.inkMuted, fontSize: 14, fontWeight: "700" },
  selectedVideoInfo: {
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 20,
  },
  selectedIcon: { fontSize: 24 },
  selectedFileName: {
    color: T.ink,
    fontSize: 13,
    fontWeight: "700",
    fontFamily: monoFont,
  },
  changeVideoText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
  },

  // Add custom movement
  addCustomBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: T.electricBlueBg,
    paddingVertical: 14,
    paddingHorizontal: 16,
    borderRadius: 12,
    marginBottom: 16,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  addCustomText: {
    color: T.electricBlue,
    fontSize: 14,
    fontWeight: "800",
    flexShrink: 1,
  },

  // Group section
  groupSection: { marginBottom: 12 },
  groupHeader: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 1.2,
    marginBottom: 8,
    marginTop: 8,
    paddingLeft: 4,
  },

  // Movement card
  movementCard: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: T.surface,
    borderRadius: 14,
    padding: 13,
    marginBottom: 6,
    borderWidth: 1,
    borderColor: T.border,
  },
  movementCardSelected: {
    borderColor: T.electricBlue,
    backgroundColor: T.electricBlueBg,
  },
  movementCardIcon: {
    width: 36,
    height: 36,
    borderRadius: 10,
    backgroundColor: T.surfaceMuted,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 12,
  },
  movementCardIconSelected: {
    backgroundColor: T.surface,
  },
  movementCardInfo: { flex: 1 },
  movementName: {
    color: T.ink,
    fontSize: 14,
    fontWeight: "800",
    letterSpacing: 0.3,
    flexShrink: 1,
  },
  movementNameSelected: {
    color: T.electricBlue,
  },
  movementCategory: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "600",
    letterSpacing: 0.4,
    marginTop: 2,
  },

  // Checkbox
  checkbox: {
    width: 24,
    height: 24,
    borderRadius: 12,
    borderWidth: 2,
    borderColor: T.borderStrong,
    justifyContent: "center",
    alignItems: "center",
  },
  checkboxSelected: {
    backgroundColor: T.electricBlue,
    borderColor: T.electricBlue,
  },

  // Empty state
  emptyState: {
    paddingVertical: 40,
    alignItems: "center",
  },
  emptyText: {
    color: T.inkMuted,
    fontSize: 14,
    fontWeight: "600",
  },

  // Bottom bar
  bottomBar: {
    position: "absolute",
    bottom: 0,
    left: 0,
    right: 0,
    paddingHorizontal: 18,
    paddingTop: 12,
    paddingBottom: 34,
    backgroundColor: "rgba(241, 244, 249, 0.96)",
    borderTopWidth: 1,
    borderTopColor: T.border,
  },
  stagedChipsScroll: {
    marginBottom: 10,
  },
  stagedChipsContent: {
    gap: 6,
    alignItems: "center",
  },
  stagedChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: T.electricBlueBg,
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  stagedChipText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
  },
  uploadBtn: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    padding: 16,
    borderRadius: 14,
    alignItems: "center",
  },
  uploadBtnDisabled: { opacity: 0.45 },
  uploadBtnText: {
    color: T.voltInk,
    fontSize: 16,
    fontWeight: "900",
    letterSpacing: 0.5,
    textTransform: "uppercase",
  },
  uploadingRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
});
