import { IconSymbol } from "@/components/ui/icon-symbol";
import { t } from "@/features/i18n";
import { fetchMovements } from "@/features/wod/api";
import { useProfileId } from "@/store/useProfileStore";
import * as ImagePicker from "expo-image-picker";
import { router } from "expo-router";
import React, { useEffect, useState } from "react";
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

export default function UploadScreen() {
  const profileId = useProfileId();
  const [selectedVideo, setSelectedVideo] = useState<string | null>(null);
  const [fileName, setFileName] = useState<string | null>(null);
  const [movementOptions, setMovementOptions] = useState<string[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);

  useEffect(() => {
    fetchMovements()
      .then(setMovementOptions)
      .catch((e) => console.error("Failed to load movements", e));
  }, []);

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

  const toggleMovement = (m: string) => {
    setSelectedMovements((prev) =>
      prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m]
    );
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
      const formData = new FormData();
      formData.append("video", {
        uri: selectedVideo,
        type: "video/mp4",
        name: fileName ?? "video.mp4",
      } as any);
      formData.append("workout_type", "wod");
      formData.append("profile_id", String(profileId));
      if (selectedMovements.length > 0) {
        formData.append("movements", selectedMovements.join(","));
      }

      const apiUrl = process.env.EXPO_PUBLIC_API_URL || "http://localhost:8088/api/v1";
      const xhr = new XMLHttpRequest();
      xhr.open("POST", `${apiUrl}/upload`);

      xhr.upload.addEventListener("progress", (e) => {
        if (e.lengthComputable) {
          setProgress(e.loaded / e.total);
        }
      });

      await new Promise<void>((resolve, reject) => {
        xhr.onload = () => {
          if (xhr.status >= 200 && xhr.status < 300) {
            resolve();
          } else {
            reject(new Error(`Upload failed: ${xhr.status}`));
          }
        };
        xhr.onerror = () => reject(new Error("Network error"));
        xhr.send(formData);
      });

      Alert.alert(t("upload.success"), t("upload.analysisStarted"), [
        {
          text: t("upload.viewHistory"),
          onPress: () => router.push("/(tabs)/history"),
        },
      ]);

      setSelectedVideo(null);
      setFileName(null);
    } catch (e: any) {
      const msg = String(e?.message ?? "").startsWith("Network")
        ? t("upload.networkError")
        : t("upload.uploadFailed");
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
      </View>

      <ScrollView contentContainerStyle={styles.content}>
        {/* Video Picker */}
        <TouchableOpacity
          style={[styles.pickerBox, selectedVideo && styles.pickerBoxSelected]}
          onPress={pickVideo}
        >
          {selectedVideo ? (
            <View style={styles.selectedVideoInfo}>
              <Text style={styles.selectedIcon}>✅</Text>
              <Text style={styles.selectedFileName} numberOfLines={1}>
                {fileName}
              </Text>
            </View>
          ) : (
            <>
              <Text style={styles.pickerIcon}>🎬</Text>
              <Text style={styles.pickerText}>{t("upload.tapToSelect")}</Text>
            </>
          )}
        </TouchableOpacity>

        {/* Movements */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t("upload.selectMovements")}</Text>
          <Text style={styles.hint}>
            {t("upload.movementsHint")}
          </Text>
          <View style={styles.chipContainer}>
            {movementOptions.map((m) => {
              const isSelected = selectedMovements.includes(m);
              return (
                <TouchableOpacity
                  key={m}
                  onPress={() => toggleMovement(m)}
                  style={[styles.chip, isSelected && styles.chipActive]}
                >
                  <Text
                    style={[
                      styles.chipText,
                      isSelected && styles.chipTextActive,
                    ]}
                  >
                    {m}
                  </Text>
                </TouchableOpacity>
              );
            })}
          </View>
        </View>
      </ScrollView>

      <View style={styles.bottomBar}>
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
                {t("upload.uploading", { percent: Math.round(progress * 100) })}
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
  container: { flex: 1, backgroundColor: "#000" },
  header: {
    flexDirection: "row",
    alignItems: "center",
    padding: 20,
    borderBottomWidth: 1,
    borderBottomColor: "#222",
  },
  backBtn: { marginRight: 15 },
  title: { fontSize: 20, fontWeight: "bold", color: "#fff" },
  content: { padding: 20, paddingBottom: 100 },

  pickerBox: {
    height: 200,
    backgroundColor: "#1A1A1A",
    borderRadius: 16,
    borderWidth: 2,
    borderColor: "#333",
    borderStyle: "dashed",
    justifyContent: "center",
    alignItems: "center",
    marginBottom: 30,
  },
  pickerBoxSelected: {
    borderColor: "#34C759",
    borderStyle: "solid",
  },
  pickerIcon: { fontSize: 48, marginBottom: 12 },
  pickerText: { color: "#888", fontSize: 16 },
  selectedVideoInfo: {
    alignItems: "center",
    gap: 12,
    paddingHorizontal: 20,
  },
  selectedIcon: { fontSize: 32 },
  selectedFileName: {
    color: "#fff",
    fontSize: 14,
    fontFamily: "monospace",
  },

  section: { marginBottom: 30 },
  sectionTitle: {
    color: "#007AFF",
    fontSize: 14,
    fontWeight: "bold",
    textTransform: "uppercase",
    marginBottom: 8,
  },
  hint: { color: "#666", fontSize: 13, marginBottom: 14 },
  chipContainer: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  chip: {
    paddingVertical: 8,
    paddingHorizontal: 16,
    borderRadius: 20,
    borderWidth: 1,
    borderColor: "#333",
    backgroundColor: "#111",
  },
  chipActive: {
    backgroundColor: "#007AFF",
    borderColor: "#007AFF",
  },
  chipText: { color: "#888", fontSize: 14 },
  chipTextActive: { color: "#fff", fontWeight: "bold" },

  bottomBar: {
    position: "absolute",
    bottom: 0,
    left: 0,
    right: 0,
    padding: 20,
    paddingBottom: 34,
    backgroundColor: "rgba(0,0,0,0.9)",
    borderTopWidth: 1,
    borderTopColor: "#222",
  },
  uploadBtn: {
    backgroundColor: "#fff",
    padding: 18,
    borderRadius: 12,
    alignItems: "center",
  },
  uploadBtnDisabled: { opacity: 0.5 },
  uploadBtnText: {
    color: "#000",
    fontSize: 18,
    fontWeight: "bold",
  },
  uploadingRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
});
