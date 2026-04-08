import { IconSymbol } from "@/components/ui/icon-symbol";
import { fetchInjuries } from "@/features/wod/api";
import {
  useProfileStore,
  type Gender,
} from "@/store/useProfileStore";
import { router, useLocalSearchParams } from "expo-router";
import React, { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

const GENDER_OPTIONS: { label: string; value: Gender }[] = [
  { label: "Male", value: "male" },
  { label: "Female", value: "female" },
  { label: "Other", value: "other" },
];

export default function ProfileScreen() {
  const { id } = useLocalSearchParams<{ id?: string }>();
  const store = useProfileStore();

  // Find existing profile if editing
  const existingProfile = useMemo(() => {
    if (!id) return null;
    const profileId = parseInt(id, 10);
    return store.profiles.find((p) => p.id === profileId) ?? null;
  }, [id, store.profiles]);

  const isEditing = existingProfile !== null;

  const [name, setName] = useState(existingProfile?.name ?? "");
  const [birthYear, setBirthYear] = useState(
    existingProfile?.birthYear?.toString() ?? ""
  );
  const [birthMonth, setBirthMonth] = useState(
    existingProfile?.birthMonth?.toString() ?? ""
  );
  const [birthDay, setBirthDay] = useState(
    existingProfile?.birthDay?.toString() ?? ""
  );
  const [gender, setGender] = useState<Gender | null>(
    existingProfile?.gender ?? null
  );
  const [heightCm, setHeightCm] = useState(
    existingProfile?.heightCm?.toString() ?? ""
  );
  const [weightKg, setWeightKg] = useState(
    existingProfile?.weightKg?.toString() ?? ""
  );
  const [saving, setSaving] = useState(false);

  // Injuries
  const [injuryOptions, setInjuryOptions] = useState<string[]>([]);
  const [selectedInjuries, setSelectedInjuries] = useState<string[]>(
    existingProfile?.injuries ?? []
  );
  const [loadingInjuries, setLoadingInjuries] = useState(true);

  useEffect(() => {
    fetchInjuries()
      .then(setInjuryOptions)
      .catch((e) => console.error("Failed to load injuries", e))
      .finally(() => setLoadingInjuries(false));
  }, []);

  const toggleInjury = (injury: string) => {
    setSelectedInjuries((prev) =>
      prev.includes(injury)
        ? prev.filter((x) => x !== injury)
        : [...prev, injury]
    );
  };

  const handleSave = async () => {
    const y = parseInt(birthYear, 10);
    const m = parseInt(birthMonth, 10);
    const d = parseInt(birthDay, 10);
    const h = parseInt(heightCm, 10);
    const w = parseFloat(weightKg);

    // Basic validation
    if (!birthYear || isNaN(y) || y < 1900 || y > new Date().getFullYear()) {
      Alert.alert("Invalid Input", "Please enter a valid birth year.");
      return;
    }
    if (!birthMonth || isNaN(m) || m < 1 || m > 12) {
      Alert.alert("Invalid Input", "Please enter a valid birth month (1–12).");
      return;
    }
    if (!birthDay || isNaN(d) || d < 1 || d > 31) {
      Alert.alert("Invalid Input", "Please enter a valid birth day (1–31).");
      return;
    }
    if (!gender) {
      Alert.alert("Invalid Input", "Please select your gender.");
      return;
    }
    if (!heightCm || isNaN(h) || h < 50 || h > 300) {
      Alert.alert("Invalid Input", "Please enter a valid height in cm.");
      return;
    }
    if (!weightKg || isNaN(w) || w < 20 || w > 500) {
      Alert.alert("Invalid Input", "Please enter a valid weight in kg.");
      return;
    }

    setSaving(true);

    try {
      if (isEditing) {
        await store.updateProfile(existingProfile.id, {
          name: name.trim(),
          birth_year: y,
          birth_month: m,
          birth_day: d,
          gender,
          height_cm: h,
          weight_kg: Math.round(w * 10) / 10,
          injuries: selectedInjuries,
        });
      } else {
        await store.createProfile({
          name: name.trim(),
          birth_year: y,
          birth_month: m,
          birth_day: d,
          gender,
          height_cm: h,
          weight_kg: Math.round(w * 10) / 10,
          injuries: selectedInjuries,
        });
      }
      router.back();
    } catch (e) {
      Alert.alert("Error", "Failed to save profile. Please try again.");
      console.error("Profile save error:", e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>
          {isEditing ? "Edit Profile" : "New Profile"}
        </Text>
      </View>

      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView contentContainerStyle={styles.content}>
          {/* Name */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Name (optional)</Text>
            <TextInput
              style={styles.input}
              placeholder="e.g. Sun Jin"
              placeholderTextColor="#555"
              value={name}
              onChangeText={setName}
              maxLength={50}
              returnKeyType="next"
              autoCapitalize="words"
            />
          </View>

          {/* Birth Date */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Date of Birth</Text>
            <View style={styles.dateRow}>
              <View style={styles.dateField}>
                <Text style={styles.fieldLabel}>Year</Text>
                <TextInput
                  style={styles.input}
                  keyboardType="number-pad"
                  placeholder="1990"
                  placeholderTextColor="#555"
                  value={birthYear}
                  onChangeText={setBirthYear}
                  maxLength={4}
                  returnKeyType="next"
                />
              </View>
              <View style={styles.dateFieldSmall}>
                <Text style={styles.fieldLabel}>Month</Text>
                <TextInput
                  style={styles.input}
                  keyboardType="number-pad"
                  placeholder="03"
                  placeholderTextColor="#555"
                  value={birthMonth}
                  onChangeText={setBirthMonth}
                  maxLength={2}
                  returnKeyType="next"
                />
              </View>
              <View style={styles.dateFieldSmall}>
                <Text style={styles.fieldLabel}>Day</Text>
                <TextInput
                  style={styles.input}
                  keyboardType="number-pad"
                  placeholder="15"
                  placeholderTextColor="#555"
                  value={birthDay}
                  onChangeText={setBirthDay}
                  maxLength={2}
                  returnKeyType="next"
                />
              </View>
            </View>
          </View>

          {/* Gender */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Gender</Text>
            <View style={styles.genderRow}>
              {GENDER_OPTIONS.map((opt) => {
                const isSelected = gender === opt.value;
                return (
                  <TouchableOpacity
                    key={opt.value}
                    style={[
                      styles.genderBtn,
                      isSelected && styles.genderBtnActive,
                    ]}
                    onPress={() => setGender(opt.value)}
                  >
                    <Text
                      style={[
                        styles.genderBtnText,
                        isSelected && styles.genderBtnTextActive,
                      ]}
                    >
                      {opt.label}
                    </Text>
                  </TouchableOpacity>
                );
              })}
            </View>
          </View>

          {/* Height */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Height</Text>
            <View style={styles.inputRow}>
              <TextInput
                style={[styles.input, { flex: 1 }]}
                keyboardType="number-pad"
                placeholder="178"
                placeholderTextColor="#555"
                value={heightCm}
                onChangeText={setHeightCm}
                maxLength={3}
                returnKeyType="next"
              />
              <Text style={styles.unit}>cm</Text>
            </View>
          </View>

          {/* Weight */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Weight</Text>
            <View style={styles.inputRow}>
              <TextInput
                style={[styles.input, { flex: 1 }]}
                keyboardType="decimal-pad"
                placeholder="85"
                placeholderTextColor="#555"
                value={weightKg}
                onChangeText={setWeightKg}
                maxLength={5}
                returnKeyType="done"
              />
              <Text style={styles.unit}>kg</Text>
            </View>
          </View>

          {/* Known Injuries */}
          <View style={styles.section}>
            <Text style={styles.sectionTitle}>Known Injuries</Text>
            <Text style={styles.hint}>
              Optional: add any current limitations the AI coach should
              consider during analysis.
            </Text>
            {loadingInjuries ? (
              <ActivityIndicator color="#007AFF" />
            ) : (
              <View style={styles.chipContainer}>
                {injuryOptions.map((injury) => {
                  const isSelected = selectedInjuries.includes(injury);
                  return (
                    <TouchableOpacity
                      key={injury}
                      onPress={() => toggleInjury(injury)}
                      style={[styles.chip, isSelected && styles.chipActive]}
                    >
                      <Text
                        style={[
                          styles.chipText,
                          isSelected && styles.chipTextActive,
                        ]}
                      >
                        {injury}
                      </Text>
                    </TouchableOpacity>
                  );
                })}
              </View>
            )}
          </View>

          {/* Save Button */}
          <TouchableOpacity
            style={[styles.saveBtn, saving && styles.saveBtnDisabled]}
            onPress={handleSave}
            disabled={saving}
          >
            <Text style={styles.saveBtnText}>
              {saving
                ? "Saving…"
                : isEditing
                  ? "Update Profile"
                  : "Create Profile"}
            </Text>
          </TouchableOpacity>
        </ScrollView>
      </KeyboardAvoidingView>
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
  content: { padding: 20, paddingBottom: 60 },

  section: { marginBottom: 30 },
  sectionTitle: {
    color: "#007AFF",
    fontSize: 14,
    fontWeight: "bold",
    textTransform: "uppercase",
    marginBottom: 12,
    letterSpacing: 0.5,
  },

  dateRow: { flexDirection: "row", gap: 12 },
  dateField: { flex: 2 },
  dateFieldSmall: { flex: 1 },
  fieldLabel: {
    color: "#888",
    fontSize: 12,
    marginBottom: 6,
    textTransform: "uppercase",
  },

  input: {
    backgroundColor: "#1A1A1A",
    color: "#fff",
    fontSize: 18,
    fontWeight: "600",
    padding: 14,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "#333",
  },

  genderRow: { flexDirection: "row", gap: 10 },
  genderBtn: {
    flex: 1,
    paddingVertical: 14,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "#333",
    backgroundColor: "#1A1A1A",
    alignItems: "center",
  },
  genderBtnActive: {
    borderColor: "#007AFF",
    backgroundColor: "#0B1A2F",
  },
  genderBtnText: {
    color: "#888",
    fontSize: 16,
    fontWeight: "600",
  },
  genderBtnTextActive: {
    color: "#8BC3FF",
  },

  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
  },
  unit: {
    color: "#888",
    fontSize: 18,
    fontWeight: "600",
  },

  hint: { color: "#666", fontSize: 13, marginBottom: 14, lineHeight: 18 },
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
    backgroundColor: "#FF6B35",
    borderColor: "#FF6B35",
  },
  chipText: { color: "#888", fontSize: 14 },
  chipTextActive: { color: "#fff", fontWeight: "bold" },

  saveBtn: {
    backgroundColor: "#fff",
    padding: 18,
    borderRadius: 12,
    alignItems: "center",
    marginTop: 10,
  },
  saveBtnDisabled: {
    opacity: 0.5,
  },
  saveBtnText: {
    color: "#000",
    fontSize: 18,
    fontWeight: "bold",
  },
});
