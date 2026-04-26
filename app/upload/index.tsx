import { IconSymbol } from "@/components/ui/icon-symbol";
import { t } from "@/features/i18n";
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

const ALL_FILTER = "All";

// Category icons (same as setup.tsx)
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
  const profileId = useProfileId();
  const [selectedVideo, setSelectedVideo] = useState<string | null>(null);
  const [fileName, setFileName] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);

  // Movement state (same pattern as setup.tsx)
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

  // Derive category filter tabs
  const categoryTabs = useMemo(() => {
    const cats = movementGroups.map((g) => g.category);
    if (customMovements.length > 0 && !cats.includes("Custom")) {
      cats.push("Custom");
    }
    return [ALL_FILTER, ...cats];
  }, [movementGroups, customMovements]);

  // Build flat set of all known movements (for custom dedup)
  const allKnownMovements = useMemo(() => {
    const set = new Set<string>();
    for (const g of movementGroups) {
      for (const m of g.movements) set.add(m);
    }
    return set;
  }, [movementGroups]);

  // Filter movements based on search + active category
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
            m.toLowerCase().includes(query)
          ),
        }))
        .filter((g) => g.movements.length > 0);
    }
    return groups;
  }, [movementGroups, customMovements, activeFilter, searchText]);

  // Check if search text can be added as custom
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
      prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m]
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
          "video.mp4"
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

      // Step 1: Get a signed GCS upload URL
      const { upload_url, gcs_uri } = await getUploadUrl(
        sessionId,
        name,
        profileId
      );

      // Step 2: Upload directly to GCS (bypasses Cloud Run body limit)
      await uploadToGcs(upload_url, selectedVideo, "video/mp4", (p) =>
        setProgress(p)
      );

      // Step 3: Notify backend to start analysis
      await notifyUploadComplete(
        sessionId,
        gcs_uri,
        selectedMovements,
        [], // no injuries from upload screen
        "wod",
        profileId
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
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>{t("upload.title")}</Text>
        <View style={{ width: 28 }} />
      </View>

      {/* Search bar */}
      <View style={styles.searchContainer}>
        <IconSymbol name="magnifyingglass" size={18} color="#666" />
        <TextInput
          style={styles.searchInput}
          placeholder={t("setup.searchMovements")}
          placeholderTextColor="#555"
          value={searchText}
          onChangeText={setSearchText}
          autoCapitalize="none"
          autoCorrect={false}
        />
        {searchText.length > 0 && (
          <TouchableOpacity onPress={() => setSearchText("")}>
            <IconSymbol name="xmark.circle.fill" size={18} color="#555" />
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
          <ActivityIndicator color="#00E5FF" style={{ marginTop: 40 }} />
        ) : (
          <>
            {/* Add custom movement button */}
            {canAddCustom && (
              <TouchableOpacity
                style={styles.addCustomBtn}
                onPress={addCustomMovement}
              >
                <IconSymbol
                  name="plus.circle.fill"
                  size={20}
                  color="#00E5FF"
                />
                <Text style={styles.addCustomText}>
                  {t("setup.addCustomMovement", { name: searchText.trim() })}
                </Text>
              </TouchableOpacity>
            )}

            {/* Movement cards by group */}
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
                      <View style={styles.movementCardIcon}>
                        <IconSymbol
                          name={getCategoryIcon(group.category) as any}
                          size={20}
                          color={isSelected ? "#00E5FF" : "#666"}
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
                            color="#666"
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
                              color="#000"
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
                    color="#00E5FF"
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
              <ActivityIndicator color="#000" size="small" />
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
  container: { flex: 1, backgroundColor: "#0A0E14" },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: 1,
    borderBottomColor: "#1A1F28",
  },
  backBtn: { width: 28 },
  title: {
    fontSize: 16,
    fontWeight: "800",
    color: "#00E5FF",
    letterSpacing: 1.5,
    textTransform: "uppercase",
  },

  // Search bar
  searchContainer: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 16,
    marginTop: 12,
    marginBottom: 8,
    backgroundColor: "#141A24",
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderWidth: 1,
    borderColor: "#1E2630",
  },
  searchInput: {
    flex: 1,
    color: "#fff",
    fontSize: 15,
    marginLeft: 10,
    paddingVertical: 0,
  },

  // Filter tabs
  filterTabs: {
    maxHeight: 44,
    marginBottom: 4,
  },
  filterTabsContent: {
    paddingHorizontal: 16,
    gap: 8,
    alignItems: "center",
  },
  filterTab: {
    paddingHorizontal: 14,
    paddingVertical: 7,
    borderRadius: 20,
    backgroundColor: "#141A24",
    borderWidth: 1,
    borderColor: "#1E2630",
  },
  filterTabActive: {
    backgroundColor: "#00303D",
    borderColor: "#00E5FF",
  },
  filterTabText: {
    color: "#667",
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  filterTabTextActive: {
    color: "#00E5FF",
  },

  // Movement list
  movementList: { flex: 1 },
  movementListContent: { paddingHorizontal: 16, paddingBottom: 180 },

  // Video picker
  pickerBox: {
    height: 140,
    backgroundColor: "#141A24",
    borderRadius: 14,
    borderWidth: 2,
    borderColor: "#1E2630",
    borderStyle: "dashed",
    justifyContent: "center",
    alignItems: "center",
    marginBottom: 20,
    marginTop: 8,
  },
  pickerBoxSelected: {
    borderColor: "#00E5FF40",
    borderStyle: "solid",
    backgroundColor: "#0D1A24",
  },
  pickerIcon: { fontSize: 36, marginBottom: 8 },
  pickerText: { color: "#667", fontSize: 14 },
  selectedVideoInfo: {
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 20,
  },
  selectedIcon: { fontSize: 24 },
  selectedFileName: {
    color: "#fff",
    fontSize: 13,
    fontFamily: "monospace",
  },
  changeVideoText: {
    color: "#00E5FF",
    fontSize: 12,
    fontWeight: "600",
  },

  // Add custom movement
  addCustomBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: "#00303D",
    paddingVertical: 14,
    paddingHorizontal: 16,
    borderRadius: 12,
    marginBottom: 16,
    borderWidth: 1,
    borderColor: "#00E5FF40",
  },
  addCustomText: {
    color: "#00E5FF",
    fontSize: 14,
    fontWeight: "700",
  },

  // Group section
  groupSection: { marginBottom: 12 },
  groupHeader: {
    color: "#4A5568",
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 1.5,
    marginBottom: 8,
    marginTop: 8,
    paddingLeft: 4,
  },

  // Movement card
  movementCard: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: "#141A24",
    borderRadius: 12,
    padding: 14,
    marginBottom: 6,
    borderWidth: 1,
    borderColor: "#1E2630",
  },
  movementCardSelected: {
    borderColor: "#00E5FF40",
    backgroundColor: "#0D1A24",
  },
  movementCardIcon: {
    width: 36,
    height: 36,
    borderRadius: 10,
    backgroundColor: "#1A2332",
    justifyContent: "center",
    alignItems: "center",
    marginRight: 12,
  },
  movementCardInfo: { flex: 1 },
  movementName: {
    color: "#C8D0DA",
    fontSize: 14,
    fontWeight: "700",
    letterSpacing: 0.5,
  },
  movementNameSelected: {
    color: "#fff",
  },
  movementCategory: {
    color: "#4A5568",
    fontSize: 11,
    fontWeight: "600",
    letterSpacing: 0.5,
    marginTop: 2,
  },

  // Checkbox
  checkbox: {
    width: 24,
    height: 24,
    borderRadius: 12,
    borderWidth: 2,
    borderColor: "#2D3748",
    justifyContent: "center",
    alignItems: "center",
  },
  checkboxSelected: {
    backgroundColor: "#00E5FF",
    borderColor: "#00E5FF",
  },

  // Empty state
  emptyState: {
    paddingVertical: 40,
    alignItems: "center",
  },
  emptyText: {
    color: "#4A5568",
    fontSize: 14,
  },

  // Bottom bar
  bottomBar: {
    position: "absolute",
    bottom: 0,
    left: 0,
    right: 0,
    paddingHorizontal: 16,
    paddingTop: 12,
    paddingBottom: 34,
    backgroundColor: "rgba(10,14,20,0.95)",
    borderTopWidth: 1,
    borderTopColor: "#1A1F28",
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
    backgroundColor: "#00303D",
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: "#00E5FF30",
  },
  stagedChipText: {
    color: "#00E5FF",
    fontSize: 12,
    fontWeight: "700",
  },
  uploadBtn: {
    backgroundColor: "#00E5FF",
    padding: 16,
    borderRadius: 12,
    alignItems: "center",
  },
  uploadBtnDisabled: { opacity: 0.4 },
  uploadBtnText: {
    color: "#000",
    fontSize: 16,
    fontWeight: "800",
    letterSpacing: 0.5,
  },
  uploadingRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
});
