import { IconSymbol } from "@/components/ui/icon-symbol";
import { t } from "@/features/i18n";
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
import { useAuthStore } from "@/features/auth/useAuthStore";
import { useActiveProfile, useProfileId, useProfileStore } from "@/store/useProfileStore";
import { Link, router } from "expo-router";
import React, { useState } from "react";
import {
  ActivityIndicator,
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
  const [containerDirs, setContainerDirs] = useState<{ name: string; path: string }[]>([]);
  const [dirItems, setDirItems] = useState<Record<string, CacheItem[]>>({});
  const [showDir, setShowDir] = useState<Record<string, boolean>>({});
  const [loadingDir, setLoadingDir] = useState<Record<string, boolean>>({});

  const formatSize = (bytes: number): string => {
    if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
  };

  const handleToggleItems = async (dirName: string) => {
    if (showDir[dirName]) {
      setShowDir((prev) => ({ ...prev, [dirName]: false }));
      return;
    }
    // Discover container dirs on first expand if not loaded
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

  const handleDeleteSingleItem = (
    item: CacheItem,
    dirName: string,
  ) => {
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
                [dirName]: (prev[dirName] ?? []).filter((i) => i.name !== item.name),
              }));
            } catch (e) {
              Alert.alert("Error", `Failed to delete: ${e}`);
            } finally {
              setDeletingItem(null);
            }
          },
        },
      ]
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
              const count = await deleteAllByRelPath(dirName, (file) => setDeleteProgress(file));
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
      ]
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
                `Copied ${count} files to Documents/Hidden_Cache/.\n\nNow connect to Xcode and download the container again.`
              );
            } catch (e) {
              Alert.alert("Error", `Extraction failed: ${e}`);
            } finally {
              setExtracting(false);
              setExtractProgress("");
            }
          },
        },
      ]
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
          <View style={[styles.menuIconBox, { backgroundColor: "#1A1A2A" }]}>
            <Text style={{ fontSize: 18 }}>📂</Text>
          </View>
          <Text style={styles.menuItemText}>
            {isLoading ? "Loading…" : isShown ? `Hide ${dirName}` : `Manage ${dirName}`}
          </Text>
          {isLoading && <ActivityIndicator color="#8B8BFF" size="small" />}
          {!isLoading && <Text style={styles.chevron}>{isShown ? "▼" : "›"}</Text>}
        </TouchableOpacity>

        {isShown && (
          <View style={{ marginTop: 4 }}>
            {items.length === 0 ? (
              <Text style={{ color: "#555", fontSize: 13, padding: 12, textAlign: "center" }}>
                {dirName} is empty
              </Text>
            ) : (
              <>
                <Text style={{ color: "#888", fontSize: 11, marginBottom: 8, marginLeft: 4 }}>
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
                        backgroundColor: "#111",
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
                        <Text style={{ color: "#ddd", fontSize: 13, fontWeight: "500" }} numberOfLines={1}>
                          {item.name}
                        </Text>
                        <Text style={{ color: "#666", fontSize: 11, marginTop: 2 }}>
                          {formatSize(item.sizeBytes)}
                        </Text>
                      </View>
                      <TouchableOpacity
                        onPress={() => handleDeleteSingleItem(item, dirName)}
                        disabled={isBusy}
                        style={{
                          backgroundColor: "#2A0E0E",
                          paddingHorizontal: 10,
                          paddingVertical: 6,
                          borderRadius: 8,
                        }}
                      >
                        {deletingItem === itemKey ? (
                          <ActivityIndicator color="#FF453A" size="small" />
                        ) : (
                          <Text style={{ color: "#FF453A", fontSize: 13, fontWeight: "600" }}>🗑️</Text>
                        )}
                      </TouchableOpacity>
                    </View>
                  );
                })}

                {/* Copy All → Documents (only for non-Documents dirs) */}
                {dirName !== "Documents" && (
                  <TouchableOpacity
                    style={{
                      marginTop: 10,
                      padding: 14,
                      borderRadius: 10,
                      borderWidth: 1,
                      borderColor: "#1A3A1A",
                      backgroundColor: "#0A1A0A",
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
                                const count = await extractDirToDocuments(dirName, (file) =>
                                  setCopyProgress(file)
                                );
                                Alert.alert(
                                  "Done!",
                                  `Copied ${count} files to Documents/Extracted_${dirName.replace(/\//g, "_")}/.\n\nConnect to Xcode and download the container.`
                                );
                              } catch (e) {
                                Alert.alert("Error", `Copy failed: ${e}`);
                              } finally {
                                setCopying(null);
                                setCopyProgress("");
                              }
                            },
                          },
                        ]
                      );
                    }}
                    disabled={isBusy || copying !== null}
                  >
                    {copying === dirName ? (
                      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                        <ActivityIndicator color="#53E16F" size="small" />
                        <Text style={{ color: "#53E16F", fontSize: 14, fontWeight: "600" }} numberOfLines={1}>
                          Copying… {copyProgress}
                        </Text>
                      </View>
                    ) : (
                      <Text style={{ color: "#53E16F", fontSize: 14, fontWeight: "600" }}>
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
                    borderColor: "#3A1515",
                    backgroundColor: "#1A0A0A",
                    alignItems: "center",
                    opacity: isThisDirDeleting ? 0.5 : 1,
                  }}
                  onPress={() => handleDeleteAll(dirName)}
                  disabled={isBusy}
                >
                  {isThisDirDeleting ? (
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                      <ActivityIndicator color="#FF453A" size="small" />
                      <Text style={{ color: "#FF453A", fontSize: 14, fontWeight: "600" }}>
                        Deleting… {deleteProgress}
                      </Text>
                    </View>
                  ) : (
                    <Text style={{ color: "#FF453A", fontSize: 14, fontWeight: "600" }}>
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

        {/* Debug: Storage Scanner */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>🔧 DEBUG — STORAGE</Text>

          <TouchableOpacity
            style={[styles.menuItem, scanning && { opacity: 0.5 }]}
            onPress={handleScanStorage}
            disabled={scanning || extracting}
          >
            <View style={[styles.menuIconBox, { backgroundColor: "#1A2A1A" }]}>
              <Text style={{ fontSize: 18 }}>🔍</Text>
            </View>
            <Text style={styles.menuItemText}>
              {scanning ? "Scanning…" : "Scan Storage"}
            </Text>
            {scanning && <ActivityIndicator color="#53E16F" size="small" />}
          </TouchableOpacity>

          <TouchableOpacity
            style={[styles.menuItem, extracting && { opacity: 0.5 }]}
            onPress={handleExtractCache}
            disabled={scanning || extracting}
          >
            <View style={[styles.menuIconBox, { backgroundColor: "#2A1A0E" }]}>
              <Text style={{ fontSize: 18 }}>📦</Text>
            </View>
            <View style={{ flex: 1 }}>
              <Text style={styles.menuItemText}>
                {extracting ? "Extracting…" : "Extract Cache → Documents"}
              </Text>
              {extracting && extractProgress ? (
                <Text style={{ color: "#888", fontSize: 11, marginTop: 4 }} numberOfLines={1}>
                  {extractProgress}
                </Text>
              ) : null}
            </View>
            {extracting && <ActivityIndicator color="#FF6B35" size="small" />}
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
              <View style={[styles.menuIconBox, { backgroundColor: "#2A2A1A" }]}>
                <Text style={{ fontSize: 18 }}>🔎</Text>
              </View>
              <Text style={styles.menuItemText}>Discover All Directories</Text>
              <Text style={styles.chevron}>›</Text>
            </TouchableOpacity>
          ) : (
            <>
              {containerDirs
                .filter((d) => d.name !== "Documents" && d.name !== "Library/Caches")
                .map((d) => renderDirManager(d.name))}
            </>
          )}

          <Text style={{ color: "#555", fontSize: 11, marginTop: 8, lineHeight: 16 }}>
            Step 1: Scan to see where hidden data lives.{"\n"}
            Step 2: Extract to copy cache → Documents.{"\n"}
            Step 3: Manage items to selectively or bulk delete.
          </Text>
        </View>

        {/* Account Actions */}
        <View style={styles.section}>
          <TouchableOpacity
            style={styles.logoutBtn}
            onPress={handleLogout}
          >
            <Text style={styles.logoutText}>{t("profileTab.signOut")}</Text>
          </TouchableOpacity>

          <TouchableOpacity
            style={[styles.logoutBtn, { marginTop: 8 }]}
            onPress={() => router.push("/settings/deleteAccount" as any)}
          >
            <Text style={[styles.logoutText, { color: "#888" }]}>{t("auth.deleteAccount")}</Text>
          </TouchableOpacity>
        </View>
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
