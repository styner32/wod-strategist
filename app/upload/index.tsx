import { IconSymbol } from "@/components/ui/icon-symbol";
import { fetchMovements, processWorkoutVideo } from "@/features/wod/api";
import { Image } from "expo-image";
import * as ImagePicker from "expo-image-picker";
import { router } from "expo-router";
import * as VideoThumbnails from "expo-video-thumbnails";
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
  const [videoUri, setVideoUri] = useState<string | null>(null);
  const [videoMimeType, setVideoMimeType] = useState<string | null>(null);
  const [thumbnailUri, setThumbnailUri] = useState<string | null>(null);
  const [isUploading, setIsUploading] = useState(false);
  const [progress, setProgress] = useState(0);

  const [movementOptions, setMovementOptions] = useState<string[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [isLoadingMovements, setIsLoadingMovements] = useState(true);

  useEffect(() => {
    fetchMovements()
      .then(setMovementOptions)
      .finally(() => setIsLoadingMovements(false));
  }, []);

  const toggleMovement = (m: string) => {
    setSelectedMovements((prev) =>
      prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m]
    );
  };

  const pickVideo = async () => {
    const result = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ImagePicker.MediaTypeOptions.Videos,
      allowsEditing: false,
      quality: 1,
    });

    if (!result.canceled) {
      const { uri, mimeType } = result.assets[0];
      setVideoUri(uri);
      setVideoMimeType(mimeType || "video/mp4");

      try {
        const { uri: thumb } = await VideoThumbnails.getThumbnailAsync(uri, {
          time: 1000,
        });
        setThumbnailUri(thumb);
      } catch (e) {
        console.warn("Failed to generate thumbnail", e);
        setThumbnailUri(uri);
      }
    }
  };

  const handleUpload = async () => {
    if (!videoUri) return;

    try {
      setIsUploading(true);
      setProgress(0);
      const now = new Date();
      const year = now.getFullYear();
      const month = String(now.getMonth() + 1).padStart(2, "0");
      const day = String(now.getDate()).padStart(2, "0");
      const hours = String(now.getHours()).padStart(2, "0");
      const minutes = String(now.getMinutes()).padStart(2, "0");
      const sessionId = `WOD-${year}-${month}-${day}-${hours}:${minutes}`;

      await processWorkoutVideo(
        videoUri,
        sessionId,
        (p) => setProgress(p),
        selectedMovements,
        videoMimeType || "video/mp4"
      );

      Alert.alert("Success", "Analysis started!", [
        { text: "View History", onPress: () => router.replace("/history") },
      ]);
    } catch (e) {
      Alert.alert("Error", String(e));
    } finally {
      setIsUploading(false);
      setProgress(0);
    }
  };

  const clearSelection = () => {
    setVideoUri(null);
    setVideoMimeType(null);
    setThumbnailUri(null);
  };

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>Upload Video</Text>
      </View>

      <ScrollView contentContainerStyle={styles.content}>
        {!videoUri ? (
          <TouchableOpacity style={styles.pickBox} onPress={pickVideo}>
            <IconSymbol name="paperplane.fill" size={48} color="#666" />
            <Text style={styles.pickText}>Tap to select video</Text>
          </TouchableOpacity>
        ) : (
          <View style={styles.previewContainer}>
            {thumbnailUri && (
              <Image
                source={{ uri: thumbnailUri }}
                style={styles.thumbnail}
                contentFit="cover"
              />
            )}
            <TouchableOpacity style={styles.clearBtn} onPress={clearSelection}>
              <IconSymbol name="xmark.circle.fill" size={24} color="#fff" />
            </TouchableOpacity>
          </View>
        )}

        {videoUri && (
          <View>
            <View style={styles.section}>
              <Text style={styles.sectionTitle}>Select Movements</Text>
              {isLoadingMovements ? (
                <ActivityIndicator color="#007AFF" />
              ) : (
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
              )}
            </View>

            <TouchableOpacity
              style={[styles.uploadBtn, isUploading && styles.disabledBtn]}
              onPress={handleUpload}
              disabled={isUploading}
            >
              {isUploading ? (
                <Text style={styles.uploadText}>
                  Uploading... {Math.round(progress * 100)}%
                </Text>
              ) : (
                <Text style={styles.uploadText}>Analyze WOD</Text>
              )}
            </TouchableOpacity>

            {isUploading && (
              <View style={styles.progressBarBg}>
                <View
                  style={[
                    styles.progressBarFill,
                    { width: `${progress * 100}%` },
                  ]}
                />
              </View>
            )}
          </View>
        )}
      </ScrollView>
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
  content: { padding: 20 },
  pickBox: {
    height: 200,
    backgroundColor: "#1A1A1A",
    borderRadius: 16,
    justifyContent: "center",
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#333",
    borderStyle: "dashed",
  },
  pickText: { color: "#888", marginTop: 10, fontSize: 16 },
  previewContainer: {
    height: 300,
    borderRadius: 16,
    overflow: "hidden",
    backgroundColor: "#111",
    position: "relative",
    marginBottom: 20,
  },
  thumbnail: { width: "100%", height: "100%" },
  clearBtn: {
    position: "absolute",
    top: 10,
    right: 10,
    padding: 5,
    backgroundColor: "rgba(0,0,0,0.5)",
    borderRadius: 20,
  },
  section: { marginBottom: 20 },
  sectionTitle: {
    color: "#888",
    fontSize: 14,
    fontWeight: "bold",
    textTransform: "uppercase",
    marginBottom: 10,
  },
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
  uploadBtn: {
    backgroundColor: "#fff",
    padding: 18,
    borderRadius: 12,
    alignItems: "center",
  },
  disabledBtn: { opacity: 0.7 },
  uploadText: { color: "#000", fontSize: 16, fontWeight: "bold" },
  progressBarBg: {
    height: 6,
    backgroundColor: "#333",
    borderRadius: 3,
    marginTop: 10,
    overflow: "hidden",
  },
  progressBarFill: {
    height: "100%",
    backgroundColor: "#007AFF",
  },
});
