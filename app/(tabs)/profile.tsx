import { IconSymbol } from "@/components/ui/icon-symbol";
import { TacticalCard, TacticalPressable } from "@/components/ui/TacticalBento";
import { Fonts, TacticalTheme } from "@/constants/theme";
import { useAuthStore } from "@/features/auth/useAuthStore";
import {
  type CacheItem,
  deleteAllByRelPath,
  deleteItemByRelPath,
  extractCacheToDocuments,
  extractDirToDocuments,
  getContainerDirs,
  listItemsByRelPath,
  runStorageScanAndShare,
} from "@/features/debug/storageScanner";
import { t, useLocale } from "@/features/i18n";
import {
  DEFAULT_MAX_CACHE_BYTES,
  clearAllVideoCache,
  getVideoCacheStats,
  type VideoCacheStats,
} from "@/features/video/videoCacheManager";
import {
  useActiveProfile,
  useProfileId,
  useProfileStore,
} from "@/store/useProfileStore";
import { router, useFocusEffect } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

const T = TacticalTheme.light;
const monoFont = Fonts?.mono ?? "monospace";

export default function ProfileTab() {
  useLocale();
  const activeProfile = useActiveProfile();
  const profileId = useProfileId();
  const profiles = useProfileStore((s) => s.profiles);
  const clearActiveProfile = useProfileStore((s) => s.clearActiveProfile);
  const [videoCacheStats, setVideoCacheStats] =
    useState<VideoCacheStats | null>(null);
  const [clearingVideoCache, setClearingVideoCache] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [extracting, setExtracting] = useState(false);
  const [extractProgress, setExtractProgress] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [deleteProgress, setDeleteProgress] = useState("");
  const [copying, setCopying] = useState<string | null>(null);
  const [copyProgress, setCopyProgress] = useState("");
  const [deletingItem, setDeletingItem] = useState<string | null>(null);
  const [deletingDir, setDeletingDir] = useState<string | null>(null);
  // Dynamic directory management
  const [containerDirs, setContainerDirs] = useState<
    { name: string; path: string }[]
  >([]);
  const [dirItems, setDirItems] = useState<Record<string, CacheItem[]>>({});
  const [showDir, setShowDir] = useState<Record<string, boolean>>({});
  const [loadingDir, setLoadingDir] = useState<Record<string, boolean>>({});

  const refreshVideoCache = useCallback(async () => {
    try {
      const stats = await getVideoCacheStats();
      setVideoCacheStats(stats);
    } catch {}
  }, []);

  useFocusEffect(
    useCallback(() => {
      refreshVideoCache();
    }, [refreshVideoCache]),
  );

  const handleClearVideoCache = () => {
    Alert.alert(
      t("profileTab.videoCacheTitle"),
      t("profileTab.videoCacheClearConfirm"),
      [
        { text: t("common.cancel"), style: "cancel" },
        {
          text: t("profileTab.videoCacheClear"),
          style: "destructive",
          onPress: async () => {
            setClearingVideoCache(true);
            try {
              await clearAllVideoCache();
              await refreshVideoCache();
              Alert.alert(t("common.saved"), t("profileTab.videoCacheCleared"));
            } catch (e) {
              Alert.alert("Error", String(e));
            } finally {
              setClearingVideoCache(false);
            }
          },
        },
      ],
    );
  };

  const formatSize = (bytes: number): string => {
    if (bytes >= 1024 * 1024 * 1024)
      return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
  };

  const handleToggleItems = async (dirName: string) => {
    if (showDir[dirName]) {
      setShowDir((prev) => ({ ...prev, [dirName]: false }));
      return;
    }
    if (containerDirs.length === 0) {
      const dirs = await getContainerDirs();
      setContainerDirs(dirs);
    }
    setLoadingDir((prev) => ({ ...prev, [dirName]: true }));
    try {
      const items = await listItemsByRelPath(dirName);
      setDirItems((prev) => ({ ...prev, [dirName]: items }));
      setShowDir((prev) => ({ ...prev, [dirName]: true }));
    } catch (e) {
      Alert.alert("Error", `Failed to list: ${e}`);
    } finally {
      setLoadingDir((prev) => ({ ...prev, [dirName]: false }));
    }
  };

  const handleDeleteSingleItem = (item: CacheItem, dirName: string) => {
    Alert.alert(
      "Delete",
      `Delete "${item.name}" (${formatSize(item.sizeBytes)})?`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: async () => {
            setDeletingItem(`${dirName}:${item.name}`);
            try {
              await deleteItemByRelPath(dirName, item.name);
              setDirItems((prev) => ({
                ...prev,
                [dirName]: (prev[dirName] ?? []).filter(
                  (i) => i.name !== item.name,
                ),
              }));
            } catch (e) {
              Alert.alert("Error", `Failed to delete: ${e}`);
            } finally {
              setDeletingItem(null);
            }
          },
        },
      ],
    );
  };

  const handleDeleteAll = (dirName: string) => {
    Alert.alert(
      `⚠️ Delete ALL in ${dirName}`,
      `This will permanently delete all files in ${dirName}. This cannot be undone.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete Everything",
          style: "destructive",
          onPress: async () => {
            setDeleting(true);
            setDeletingDir(dirName);
            setDeleteProgress("Starting...");
            try {
              const count = await deleteAllByRelPath(dirName, (file) =>
                setDeleteProgress(file),
              );
              setDirItems((prev) => ({ ...prev, [dirName]: [] }));
              Alert.alert("Cleared", `Deleted ${count} items from ${dirName}.`);
            } catch (e) {
              Alert.alert("Error", `Delete failed: ${e}`);
            } finally {
              setDeleting(false);
              setDeletingDir(null);
              setDeleteProgress("");
            }
          },
        },
      ],
    );
  };

  const handleScanStorage = async () => {
    setScanning(true);
    try {
      await runStorageScanAndShare();
    } finally {
      setScanning(false);
    }
  };

  const handleExtractCache = async () => {
    Alert.alert(
      "Extract Cache",
      "This will copy ALL cache files to Documents/Hidden_Cache/. This could temporarily double your storage usage. Continue?",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Extract",
          onPress: async () => {
            setExtracting(true);
            setExtractProgress("Starting...");
            try {
              const count = await extractCacheToDocuments((file) => {
                setExtractProgress(file);
              });
              Alert.alert(
                "Done!",
                `Copied ${count} files to Documents/Hidden_Cache/.\n\nNow connect to Xcode and download the container again.`,
              );
            } catch (e) {
              Alert.alert("Error", `Extraction failed: ${e}`);
            } finally {
              setExtracting(false);
              setExtractProgress("");
            }
          },
        },
      ],
    );
  };

  const summaryLine = activeProfile
    ? [
        activeProfile.gender === "male"
          ? t("common.male")
          : activeProfile.gender === "female"
            ? t("common.female")
            : activeProfile.gender
              ? t("common.other")
              : null,
        activeProfile.birthYear ? String(activeProfile.birthYear) : null,
        activeProfile.heightCm ? `${activeProfile.heightCm} cm` : null,
        activeProfile.weightKg ? `${activeProfile.weightKg} kg` : null,
      ]
        .filter(Boolean)
        .join("  ·  ") || null
    : null;

  const monogram = activeProfile?.name
    ? activeProfile.name.slice(0, 2).toUpperCase()
    : "?";

  const usedCacheBytes = videoCacheStats?.totalSizeBytes ?? 0;
  const cacheRatio = Math.min(
    1,
    Math.max(0, usedCacheBytes / DEFAULT_MAX_CACHE_BYTES),
  );
  const cachePercent = Math.round(cacheRatio * 100);
  const usedCacheFormatted = videoCacheStats?.formattedSize ?? "0 B";

  const handleLogout = () => {
    Alert.alert(t("profileTab.signOut"), t("profileTab.signOutConfirm"), [
      { text: t("common.cancel"), style: "cancel" },
      {
        text: t("profileTab.signOut"),
        style: "destructive",
        onPress: async () => {
          clearActiveProfile();
          await useAuthStore.getState().logout();
        },
      },
    ]);
  };

  const renderDirManager = (dirName: string) => {
    const isBusy = deleting || deletingItem !== null;
    const isThisDirDeleting = deletingDir === dirName;
    const items = dirItems[dirName] ?? [];
    const isShown = showDir[dirName] ?? false;
    const isLoading = loadingDir[dirName] ?? false;

    return (
      <View key={dirName} style={{ marginBottom: 8 }}>
        <TouchableOpacity
          style={[styles.menuItem, isLoading && { opacity: 0.5 }]}
          onPress={() => handleToggleItems(dirName)}
          disabled={isLoading}
        >
          <View style={[styles.menuIconBox, { backgroundColor: T.electricBlueBg }]}>
            <Text style={{ fontSize: 18 }}>📂</Text>
          </View>
          <Text style={styles.menuItemText}>
            {isLoading
              ? "Loading…"
              : isShown
                ? `Hide ${dirName}`
                : `Manage ${dirName}`}
          </Text>
          {isLoading && <ActivityIndicator color={T.electricBlue} size="small" />}
          {!isLoading && (
            <Text style={styles.chevron}>{isShown ? "▼" : "›"}</Text>
          )}
        </TouchableOpacity>

        {isShown && (
          <View style={{ marginTop: 4 }}>
            {items.length === 0 ? (
              <Text
                style={{
                  color: T.inkMuted,
                  fontSize: 13,
                  padding: 12,
                  textAlign: "center",
                }}
              >
                {dirName} is empty
              </Text>
            ) : (
              <>
                <Text
                  style={{
                    color: T.inkMuted,
                    fontSize: 11,
                    marginBottom: 8,
                    marginLeft: 4,
                  }}
                >
                  {items.length} items — tap 🗑️ to delete individually
                </Text>
                {items.map((item) => {
                  const itemKey = `${dirName}:${item.name}`;
                  return (
                    <View
                      key={item.name}
                      style={{
                        flexDirection: "row",
                        alignItems: "center",
                        backgroundColor: T.surface,
                        borderWidth: 1,
                        borderColor: T.border,
                        borderRadius: 10,
                        padding: 12,
                        marginBottom: 4,
                        opacity: deletingItem === itemKey ? 0.4 : 1,
                      }}
                    >
                      <Text style={{ fontSize: 14, marginRight: 10 }}>
                        {item.isDirectory ? "📁" : "📄"}
                      </Text>
                      <View style={{ flex: 1, marginRight: 8 }}>
                        <Text
                          style={{
                            color: T.ink,
                            fontSize: 13,
                            fontWeight: "600",
                          }}
                          numberOfLines={1}
                        >
                          {item.name}
                        </Text>
                        <Text
                          style={{ color: T.inkMuted, fontSize: 11, marginTop: 2 }}
                        >
                          {formatSize(item.sizeBytes)}
                        </Text>
                      </View>
                      <TouchableOpacity
                        onPress={() => handleDeleteSingleItem(item, dirName)}
                        disabled={isBusy}
                        style={{
                          backgroundColor: T.statusExhaustedBg,
                          borderWidth: 1,
                          borderColor: T.statusExhaustedBorder,
                          paddingHorizontal: 10,
                          paddingVertical: 6,
                          borderRadius: 8,
                        }}
                      >
                        {deletingItem === itemKey ? (
                          <ActivityIndicator color={T.statusExhausted} size="small" />
                        ) : (
                          <Text
                            style={{
                              color: T.statusExhausted,
                              fontSize: 13,
                              fontWeight: "600",
                            }}
                          >
                            🗑️
                          </Text>
                        )}
                      </TouchableOpacity>
                    </View>
                  );
                })}

                {dirName !== "Documents" && (
                  <TouchableOpacity
                    style={{
                      marginTop: 10,
                      padding: 14,
                      borderRadius: 10,
                      borderWidth: 1,
                      borderColor: T.statusFreshBorder,
                      backgroundColor: T.statusFreshBg,
                      alignItems: "center",
                      opacity: copying === dirName ? 0.5 : 1,
                    }}
                    onPress={() => {
                      Alert.alert(
                        "Copy to Documents",
                        `Copy all ${items.length} items from ${dirName} → Documents/Extracted_${dirName.replace(/\//g, "_")}/?\n\nThis will temporarily increase storage usage.`,
                        [
                          { text: "Cancel", style: "cancel" },
                          {
                            text: "Copy",
                            onPress: async () => {
                              setCopying(dirName);
                              setCopyProgress("Starting...");
                              try {
                                const count = await extractDirToDocuments(
                                  dirName,
                                  (file) => setCopyProgress(file),
                                );
                                Alert.alert(
                                  "Done!",
                                  `Copied ${count} files to Documents/Extracted_${dirName.replace(/\//g, "_")}/.\n\nConnect to Xcode and download the container.`,
                                );
                              } catch (e) {
                                Alert.alert("Error", `Copy failed: ${e}`);
                              } finally {
                                setCopying(null);
                                setCopyProgress("");
                              }
                            },
                          },
                        ],
                      );
                    }}
                    disabled={isBusy || copying !== null}
                  >
                    {copying === dirName ? (
                      <View
                        style={{
                          flexDirection: "row",
                          alignItems: "center",
                          gap: 8,
                        }}
                      >
                        <ActivityIndicator color={T.statusFresh} size="small" />
                        <Text
                          style={{
                            color: T.statusFresh,
                            fontSize: 14,
                            fontWeight: "700",
                          }}
                          numberOfLines={1}
                        >
                          Copying… {copyProgress}
                        </Text>
                      </View>
                    ) : (
                      <Text
                        style={{
                          color: T.statusFresh,
                          fontSize: 14,
                          fontWeight: "700",
                        }}
                      >
                        📦 Copy All → Documents ({items.length} items)
                      </Text>
                    )}
                  </TouchableOpacity>
                )}

                <TouchableOpacity
                  style={{
                    marginTop: 10,
                    padding: 14,
                    borderRadius: 10,
                    borderWidth: 1,
                    borderColor: T.statusExhaustedBorder,
                    backgroundColor: T.statusExhaustedBg,
                    alignItems: "center",
                    opacity: isThisDirDeleting ? 0.5 : 1,
                  }}
                  onPress={() => handleDeleteAll(dirName)}
                  disabled={isBusy}
                >
                  {isThisDirDeleting ? (
                    <View
                      style={{
                        flexDirection: "row",
                        alignItems: "center",
                        gap: 8,
                      }}
                    >
                      <ActivityIndicator color={T.statusExhausted} size="small" />
                      <Text
                        style={{
                          color: T.statusExhausted,
                          fontSize: 14,
                          fontWeight: "700",
                        }}
                      >
                        Deleting… {deleteProgress}
                      </Text>
                    </View>
                  ) : (
                    <Text
                      style={{
                        color: T.statusExhausted,
                        fontSize: 14,
                        fontWeight: "700",
                      }}
                    >
                      Delete All ({items.length} items)
                    </Text>
                  )}
                </TouchableOpacity>
              </>
            )}
          </View>
        )}
      </View>
    );
  };

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
            <Text style={styles.eyebrowText}>{t("profileTab.eyebrow")}</Text>
          </View>
          <View style={styles.titleRow}>
            <Text style={styles.title} numberOfLines={1}>
              {t("tabs.profile")}
            </Text>
            <View style={styles.voltDot} />
          </View>
        </View>

        {/* Hero Athlete Bento Card */}
        <TacticalCard style={styles.heroCard}>
          <View style={styles.heroTopRow}>
            <View style={styles.activeAthletePill}>
              <View style={styles.activeAthleteDot} />
              <Text style={styles.activeAthleteText}>
                {t("profileTab.activeAthlete")}
              </Text>
            </View>
            {profileId ? (
              <View style={styles.idBadge}>
                <Text style={styles.idBadgeText}>ID #{profileId}</Text>
              </View>
            ) : null}
          </View>

          <View style={styles.identityRow}>
            <View style={styles.avatarWrap}>
              <View
                style={[
                  styles.avatarCircle,
                  !activeProfile && styles.avatarCircleEmpty,
                ]}
              >
                <Text
                  style={[
                    styles.avatarText,
                    !activeProfile && styles.avatarTextEmpty,
                  ]}
                >
                  {monogram}
                </Text>
              </View>
              {activeProfile ? <View style={styles.avatarVoltBadge} /> : null}
            </View>

            <View style={styles.identityTextCol}>
              <Text style={styles.profileName} numberOfLines={1}>
                {activeProfile?.name || t("profileTab.noProfile")}
              </Text>
              {summaryLine ? (
                <Text style={styles.profileSummary}>{summaryLine}</Text>
              ) : null}
            </View>
          </View>

          {activeProfile ? (
            <View style={styles.metricsRow}>
              <View style={styles.fitnessVoltBadge}>
                <Text style={styles.fitnessVoltText}>
                  {t(`profileEdit.${activeProfile.fitnessLevel ?? "intermediate"}`)}
                </Text>
              </View>
              {activeProfile.restingHrBpm != null ? (
                <View style={styles.restingHrBadge}>
                  <Text style={styles.restingHrText}>
                    ♥ {t("profileTab.restingHrBadge", { bpm: activeProfile.restingHrBpm })}
                  </Text>
                </View>
              ) : null}
            </View>
          ) : null}

          {activeProfile && activeProfile.injuries.length > 0 ? (
            <View style={styles.injuryRow}>
              {activeProfile.injuries.map((injury) => (
                <View key={injury} style={styles.injuryPill}>
                  <Text style={styles.injuryPillText}>{injury}</Text>
                </View>
              ))}
            </View>
          ) : null}
        </TacticalCard>

        {/* Account Actions */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t("profileTab.account")}</Text>

          {activeProfile ? (
            <TacticalPressable
              testID="profile-edit-btn"
              haptic
              style={styles.menuItem}
              onPress={() =>
                router.push({
                  pathname: "/profile-edit",
                  params: { id: String(profileId) },
                } as any)
              }
            >
              <View style={[styles.menuIconBox, { backgroundColor: T.electricBlueBg }]}>
                <IconSymbol name="person.fill" size={18} color={T.electricBlue} />
              </View>
              <Text style={styles.menuItemText}>
                {t("profileTab.editProfile")}
              </Text>
              <Text style={styles.chevron}>›</Text>
            </TacticalPressable>
          ) : (
            <TacticalPressable
              testID="profile-create-btn"
              haptic
              style={styles.menuItem}
              onPress={() => router.push("/profile-edit" as any)}
            >
              <View style={[styles.menuIconBox, { backgroundColor: T.statusFreshBg }]}>
                <IconSymbol name="person.fill" size={18} color={T.statusFresh} />
              </View>
              <Text style={styles.menuItemText}>
                {t("profileTab.createProfile")}
              </Text>
              <Text style={styles.chevron}>›</Text>
            </TacticalPressable>
          )}

          <TacticalPressable
            testID="profile-switch-btn"
            haptic
            style={styles.menuItem}
            onPress={() => router.push("/profiles" as any)}
          >
            <View style={[styles.menuIconBox, { backgroundColor: T.volt }]}>
              <IconSymbol name="person.2.fill" size={18} color={T.voltInk} />
            </View>
            <Text style={styles.menuItemText}>
              {t("profileTab.switchProfile")}
            </Text>
            {profiles.length > 0 ? (
              <View style={styles.rosterCountBadge}>
                <Text style={styles.rosterCountText}>{profiles.length}</Text>
              </View>
            ) : null}
            <Text style={styles.chevron}>›</Text>
          </TacticalPressable>
        </View>

        {/* Video Cache Management */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>
            {t("profileTab.videoCacheTitle")}
          </Text>
          <TacticalCard style={styles.cacheCard}>
            <View style={styles.cacheHeader}>
              <View style={[styles.menuIconBox, { backgroundColor: T.electricBlueBg }]}>
                <Text style={{ fontSize: 18 }}>🎬</Text>
              </View>
              <View style={{ flex: 1 }}>
                <Text style={styles.cacheStatusText}>
                  {videoCacheStats && videoCacheStats.count > 0
                    ? t("profileTab.videoCacheCount", {
                        size: videoCacheStats.formattedSize,
                        count: videoCacheStats.count,
                      })
                    : t("profileTab.videoCacheEmpty")}
                </Text>
                <Text style={styles.cacheDescText}>
                  {t("profileTab.videoCacheDesc")}
                </Text>
              </View>
            </View>

            {/* Real Cache Limit Gauge */}
            <View style={styles.cacheGaugeBox}>
              <View style={styles.cacheGaugeLabelRow}>
                <Text style={styles.cacheGaugeLabel}>
                  {t("profileTab.cacheLimitLabel", { used: usedCacheFormatted })}
                </Text>
                <Text style={styles.cacheGaugePct}>{cachePercent}%</Text>
              </View>
              <View style={styles.cacheGaugeTrack}>
                <View
                  testID="video-cache-progress-fill"
                  style={[
                    styles.cacheGaugeFill,
                    {
                      width: `${cachePercent}%`,
                      backgroundColor:
                        cacheRatio > 0.85 ? T.statusWarning : T.electricBlue,
                    },
                  ]}
                />
              </View>
            </View>

            <Text style={styles.cachePolicyText}>
              💡 {t("profileTab.videoCacheAutoPolicy")}
            </Text>

            {videoCacheStats && videoCacheStats.count > 0 && (
              <TouchableOpacity
                style={[
                  styles.clearCacheBtn,
                  clearingVideoCache && { opacity: 0.6 },
                ]}
                onPress={handleClearVideoCache}
                disabled={clearingVideoCache}
              >
                {clearingVideoCache ? (
                  <ActivityIndicator size="small" color={T.statusExhausted} />
                ) : (
                  <Text style={styles.clearCacheBtnText}>
                    🗑️ {t("profileTab.videoCacheClear")}
                  </Text>
                )}
              </TouchableOpacity>
            )}
          </TacticalCard>
        </View>

        {/* Debug: Storage Scanner */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>🔧 DEBUG — STORAGE</Text>

          <TouchableOpacity
            style={[styles.menuItem, scanning && { opacity: 0.5 }]}
            onPress={handleScanStorage}
            disabled={scanning || extracting}
          >
            <View style={[styles.menuIconBox, { backgroundColor: T.statusFreshBg }]}>
              <Text style={{ fontSize: 18 }}>🔍</Text>
            </View>
            <Text style={styles.menuItemText}>
              {scanning ? "Scanning…" : "Scan Storage"}
            </Text>
            {scanning && <ActivityIndicator color={T.statusFresh} size="small" />}
          </TouchableOpacity>

          <TouchableOpacity
            style={[styles.menuItem, extracting && { opacity: 0.5 }]}
            onPress={handleExtractCache}
            disabled={scanning || extracting}
          >
            <View style={[styles.menuIconBox, { backgroundColor: T.statusWarningBg }]}>
              <Text style={{ fontSize: 18 }}>📦</Text>
            </View>
            <View style={{ flex: 1 }}>
              <Text style={styles.menuItemText}>
                {extracting ? "Extracting…" : "Extract Cache → Documents"}
              </Text>
              {extracting && extractProgress ? (
                <Text
                  style={{ color: T.inkMuted, fontSize: 11, marginTop: 4 }}
                  numberOfLines={1}
                >
                  {extractProgress}
                </Text>
              ) : null}
            </View>
            {extracting && <ActivityIndicator color={T.statusWarning} size="small" />}
          </TouchableOpacity>

          {/* Default directories */}
          {renderDirManager("Documents")}
          {renderDirManager("Library/Caches")}

          {/* Discover all container directories */}
          {containerDirs.length === 0 ? (
            <TouchableOpacity
              style={styles.menuItem}
              onPress={async () => {
                const dirs = await getContainerDirs();
                setContainerDirs(dirs);
              }}
            >
              <View
                style={[styles.menuIconBox, { backgroundColor: T.surfaceMuted }]}
              >
                <Text style={{ fontSize: 18 }}>🔎</Text>
              </View>
              <Text style={styles.menuItemText}>Discover All Directories</Text>
              <Text style={styles.chevron}>›</Text>
            </TouchableOpacity>
          ) : (
            <>
              {containerDirs
                .filter(
                  (d) => d.name !== "Documents" && d.name !== "Library/Caches",
                )
                .map((d) => renderDirManager(d.name))}
            </>
          )}

          <Text
            style={{
              color: T.inkMuted,
              fontSize: 11,
              marginTop: 8,
              lineHeight: 16,
            }}
          >
            Step 1: Scan to see where hidden data lives.{"\n"}
            Step 2: Extract to copy cache → Documents.{"\n"}
            Step 3: Manage items to selectively or bulk delete.
          </Text>
        </View>

        {/* Session & Account Deletion Actions */}
        <View style={styles.section}>
          <TouchableOpacity style={styles.logoutBtn} onPress={handleLogout}>
            <Text style={styles.logoutText}>{t("profileTab.signOut")}</Text>
          </TouchableOpacity>

          <TouchableOpacity
            testID="delete-account-btn"
            style={styles.deleteAccountBtn}
            onPress={() => router.push("/settings/deleteAccount" as any)}
          >
            <Text style={styles.deleteAccountText}>
              {t("auth.deleteAccount")}
            </Text>
          </TouchableOpacity>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: T.canvas },
  scrollContent: { paddingHorizontal: 18, paddingTop: 10, paddingBottom: 112 },

  topBar: {
    marginBottom: 14,
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
  title: {
    fontSize: 24,
    fontWeight: "900",
    letterSpacing: -0.6,
    color: T.ink,
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

  heroCard: {
    marginBottom: 24,
    gap: 14,
  },
  heroTopRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
  },
  activeAthletePill: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 999,
  },
  activeAthleteDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: T.electricBlue,
  },
  activeAthleteText: {
    color: T.electricBlue,
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 0.7,
    textTransform: "uppercase",
  },
  idBadge: {
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 8,
  },
  idBadgeText: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "700",
    fontFamily: monoFont,
  },

  identityRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 14,
  },
  avatarWrap: {
    position: "relative",
  },
  avatarCircle: {
    width: 64,
    height: 64,
    borderRadius: 32,
    backgroundColor: T.electricBlue,
    justifyContent: "center",
    alignItems: "center",
  },
  avatarCircleEmpty: {
    backgroundColor: T.surfaceContainer,
  },
  avatarText: {
    color: "#FFFFFF",
    fontSize: 22,
    fontWeight: "900",
    letterSpacing: 0.5,
  },
  avatarTextEmpty: {
    color: T.inkMuted,
  },
  avatarVoltBadge: {
    position: "absolute",
    right: -1,
    bottom: -1,
    width: 16,
    height: 16,
    borderRadius: 8,
    backgroundColor: T.volt,
    borderWidth: 2,
    borderColor: T.surface,
  },
  identityTextCol: {
    flex: 1,
    gap: 4,
  },
  profileName: {
    color: T.ink,
    fontSize: 22,
    fontWeight: "900",
    letterSpacing: -0.4,
    flexShrink: 1,
  },
  profileSummary: {
    color: T.inkMuted,
    fontSize: 13,
    fontWeight: "600",
    flexShrink: 1,
  },

  metricsRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  fitnessVoltBadge: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 8,
  },
  fitnessVoltText: {
    color: T.voltInk,
    fontSize: 11,
    fontWeight: "900",
    letterSpacing: 0.5,
    textTransform: "uppercase",
  },
  restingHrBadge: {
    backgroundColor: T.heartRoseBg,
    borderWidth: 1,
    borderColor: T.heartRoseBorder,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 8,
  },
  restingHrText: {
    color: T.heartRose,
    fontSize: 11,
    fontWeight: "800",
  },

  injuryRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  injuryPill: {
    backgroundColor: T.statusFatiguedBg,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: T.statusFatiguedBorder,
  },
  injuryPillText: {
    color: T.statusFatigued,
    fontSize: 12,
    fontWeight: "700",
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

  menuItem: {
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    padding: 14,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 8,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 6,
    elevation: 1,
  },
  menuIconBox: {
    width: 38,
    height: 38,
    backgroundColor: T.surfaceMuted,
    borderRadius: 10,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 12,
  },
  menuItemText: {
    color: T.ink,
    fontSize: 15,
    fontWeight: "700",
    flex: 1,
    flexShrink: 1,
  },
  rosterCountBadge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 999,
    marginRight: 8,
  },
  rosterCountText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
    fontFamily: monoFont,
  },
  chevron: { color: T.inkMuted, fontSize: 22, fontWeight: "600" },

  cacheCard: {
    padding: 16,
  },
  cacheHeader: {
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 12,
  },
  cacheStatusText: {
    color: T.ink,
    fontSize: 15,
    fontWeight: "800",
    flexShrink: 1,
  },
  cacheDescText: {
    color: T.inkMuted,
    fontSize: 12,
    marginTop: 2,
    lineHeight: 16,
  },
  cacheGaugeBox: {
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    borderRadius: 12,
    padding: 12,
    marginBottom: 10,
    gap: 6,
  },
  cacheGaugeLabelRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
  },
  cacheGaugeLabel: {
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "700",
    fontFamily: monoFont,
    flexShrink: 1,
  },
  cacheGaugePct: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
    fontFamily: monoFont,
  },
  cacheGaugeTrack: {
    height: 8,
    backgroundColor: T.surfaceContainer,
    borderRadius: 4,
    overflow: "hidden",
  },
  cacheGaugeFill: {
    height: "100%",
    borderRadius: 4,
  },
  cachePolicyText: {
    color: T.inkMuted,
    fontSize: 11,
    lineHeight: 16,
    marginBottom: 12,
  },
  clearCacheBtn: {
    backgroundColor: T.statusExhaustedBg,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
    paddingVertical: 12,
    borderRadius: 12,
    alignItems: "center",
    justifyContent: "center",
  },
  clearCacheBtnText: {
    color: T.statusExhausted,
    fontSize: 14,
    fontWeight: "800",
  },

  logoutBtn: {
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
    padding: 15,
    borderRadius: 14,
    alignItems: "center",
  },
  logoutText: {
    color: T.statusExhausted,
    fontSize: 15,
    fontWeight: "800",
  },
  deleteAccountBtn: {
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    padding: 14,
    borderRadius: 14,
    alignItems: "center",
    marginTop: 10,
  },
  deleteAccountText: {
    color: T.inkMuted,
    fontSize: 14,
    fontWeight: "700",
  },
});
