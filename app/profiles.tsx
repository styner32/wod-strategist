import { useProfileStore, type Profile } from "@/store/useProfileStore";
import { router } from "expo-router";
import React, { useCallback } from "react";
import {
  Alert,
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

export default function ProfilesScreen() {
  const { profiles, activeProfileId, selectProfile, archiveProfile } =
    useProfileStore();

  const handleSelect = useCallback(
    (profile: Profile) => {
      selectProfile(profile.id);
      router.back();
    },
    [selectProfile]
  );

  const handleEdit = useCallback((profile: Profile) => {
    router.push({ pathname: "/profile", params: { id: String(profile.id) } });
  }, []);

  const handleArchive = useCallback(
    (profile: Profile) => {
      Alert.alert(
        "Archive Profile",
        `Are you sure you want to archive "${profile.name || `Profile #${profile.id}`}"?\n\nWorkout history will be preserved.`,
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Archive",
            style: "destructive",
            onPress: () => archiveProfile(profile.id),
          },
        ]
      );
    },
    [archiveProfile]
  );

  const handleAddProfile = useCallback(() => {
    router.push("/profile");
  }, []);

  const genderLabel = (g: string) =>
    g === "male" ? "M" : g === "female" ? "F" : "O";

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

    return (
      <Pressable
        style={[styles.card, isActive && styles.cardActive]}
        onPress={() => handleSelect(item)}
        onLongPress={() => handleArchive(item)}
      >
        <View style={styles.cardContent}>
          {/* Avatar Circle */}
          <View
            style={[styles.avatar, isActive && styles.avatarActive]}
          >
            <Text style={styles.avatarText}>
              {item.name ? item.name[0].toUpperCase() : "👤"}
            </Text>
          </View>

          {/* Profile Info */}
          <View style={styles.info}>
            <Text style={styles.name} numberOfLines={1}>
              {item.name || `Profile #${item.id}`}
            </Text>
            <Text style={styles.summary}>{summaryLine(item)}</Text>
            {isActive && (
              <View style={styles.activeBadge}>
                <View style={styles.activeDot} />
                <Text style={styles.activeText}>Active</Text>
              </View>
            )}
          </View>

          {/* Edit Button */}
          <Pressable
            style={styles.editBtn}
            onPress={() => handleEdit(item)}
            hitSlop={12}
          >
            <Text style={styles.editBtnText}>Edit</Text>
          </Pressable>
        </View>
      </Pressable>
    );
  };

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <Pressable onPress={() => router.back()} style={styles.backBtn}>
          <Text style={styles.backBtnText}>← Back</Text>
        </Pressable>
        <Text style={styles.title}>Profiles</Text>
      </View>

      <FlatList
        data={profiles}
        keyExtractor={(item) => String(item.id)}
        renderItem={renderProfile}
        contentContainerStyle={styles.list}
        ListEmptyComponent={
          <View style={styles.emptyContainer}>
            <Text style={styles.emptyIcon}>👤</Text>
            <Text style={styles.emptyTitle}>No Profiles Yet</Text>
            <Text style={styles.emptyDesc}>
              Create your first profile to start tracking workouts
            </Text>
          </View>
        }
        ListFooterComponent={
          <Pressable style={styles.addBtn} onPress={handleAddProfile}>
            <Text style={styles.addBtnIcon}>+</Text>
            <Text style={styles.addBtnText}>Add New Profile</Text>
          </Pressable>
        }
      />

      <Text style={styles.hint}>Long-press a profile to archive it</Text>
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
  backBtnText: { color: "#007AFF", fontSize: 16 },
  title: { fontSize: 20, fontWeight: "bold", color: "#fff" },

  list: { padding: 16, paddingBottom: 20 },

  card: {
    backgroundColor: "#1A1A1A",
    borderRadius: 16,
    marginBottom: 12,
    borderWidth: 1,
    borderColor: "#333",
    overflow: "hidden",
  },
  cardActive: {
    borderColor: "#007AFF",
    backgroundColor: "#0B1A2F",
  },
  cardContent: {
    flexDirection: "row",
    alignItems: "center",
    padding: 16,
  },

  avatar: {
    width: 48,
    height: 48,
    borderRadius: 24,
    backgroundColor: "#333",
    justifyContent: "center",
    alignItems: "center",
    marginRight: 14,
  },
  avatarActive: {
    backgroundColor: "#0D3B66",
  },
  avatarText: {
    fontSize: 20,
    fontWeight: "bold",
    color: "#fff",
  },

  info: { flex: 1 },
  name: {
    color: "#fff",
    fontSize: 17,
    fontWeight: "700",
  },
  summary: {
    color: "#888",
    fontSize: 13,
    marginTop: 3,
  },
  activeBadge: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: 5,
  },
  activeDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: "#34C759",
    marginRight: 5,
  },
  activeText: {
    color: "#34C759",
    fontSize: 11,
    fontWeight: "600",
  },

  editBtn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: "#2A2A2A",
  },
  editBtnText: {
    color: "#007AFF",
    fontSize: 13,
    fontWeight: "600",
  },

  addBtn: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    padding: 18,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: "#333",
    borderStyle: "dashed",
    marginTop: 4,
  },
  addBtnIcon: {
    color: "#007AFF",
    fontSize: 22,
    fontWeight: "bold",
    marginRight: 8,
  },
  addBtnText: {
    color: "#007AFF",
    fontSize: 16,
    fontWeight: "600",
  },

  emptyContainer: {
    alignItems: "center",
    paddingVertical: 60,
  },
  emptyIcon: { fontSize: 48, marginBottom: 16 },
  emptyTitle: {
    color: "#fff",
    fontSize: 20,
    fontWeight: "bold",
    marginBottom: 8,
  },
  emptyDesc: {
    color: "#888",
    fontSize: 14,
    textAlign: "center",
    paddingHorizontal: 40,
  },

  hint: {
    color: "#555",
    fontSize: 11,
    textAlign: "center",
    paddingBottom: 16,
  },
});
