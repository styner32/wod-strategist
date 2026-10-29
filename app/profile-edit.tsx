import { IconSymbol } from "@/components/ui/icon-symbol";
import { VoltButton } from "@/components/ui/TacticalBento";
import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { fetchInjuries } from "@/features/wod/api";
import {
  useProfileStore,
  type Gender,
  type FitnessLevel,
} from "@/store/useProfileStore";
import { router, useLocalSearchParams } from "expo-router";
import { StatusBar } from "expo-status-bar";
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

const T = TacticalTheme.light;

export default function ProfileScreen() {
  useLocale();
  const { id } = useLocalSearchParams<{ id?: string | string[] }>();
  const rawId = Array.isArray(id) ? id[0] : id;
  const store = useProfileStore();

  const genderOptions: { label: string; value: Gender }[] = [
    { label: t("common.male"), value: "male" },
    { label: t("common.female"), value: "female" },
    { label: t("common.other"), value: "other" },
  ];

  const fitnessLevelOptions: { label: string; value: FitnessLevel }[] = [
    { label: t("profileEdit.beginner"), value: "beginner" },
    { label: t("profileEdit.intermediate"), value: "intermediate" },
    { label: t("profileEdit.advanced"), value: "advanced" },
  ];

  // Find existing profile if editing
  const existingProfile = useMemo(() => {
    if (!rawId) return null;
    const profileId = parseInt(rawId, 10);
    return store.profiles.find((p) => p.id === profileId) ?? null;
  }, [rawId, store.profiles]);

  const isEditing = existingProfile !== null;

  const [name, setName] = useState(existingProfile?.name ?? "");
  const [birthYear, setBirthYear] = useState(
    existingProfile?.birthYear?.toString() ?? "",
  );
  const [birthMonth, setBirthMonth] = useState(
    existingProfile?.birthMonth?.toString() ?? "",
  );
  const [birthDay, setBirthDay] = useState(
    existingProfile?.birthDay?.toString() ?? "",
  );
  const [gender, setGender] = useState<Gender | null>(
    existingProfile?.gender ?? null,
  );
  const [heightCm, setHeightCm] = useState(
    existingProfile?.heightCm?.toString() ?? "",
  );
  const [weightKg, setWeightKg] = useState(
    existingProfile?.weightKg?.toString() ?? "",
  );
  const [restingHrBpm, setRestingHrBpm] = useState(
    existingProfile?.restingHrBpm?.toString() ?? "",
  );
  const [fitnessLevel, setFitnessLevel] = useState<FitnessLevel>(
    existingProfile?.fitnessLevel ?? "intermediate",
  );
  const [appearance, setAppearance] = useState(existingProfile?.appearance ?? "");
  const [parsingImage, setParsingImage] = useState(false);
  const [saving, setSaving] = useState(false);
  const [selectedInjuries, setSelectedInjuries] = useState<string[]>(
    existingProfile?.injuries ?? [],
  );
  const [injuryOptions, setInjuryOptions] = useState<string[]>([]);
  const [loadingInjuries, setLoadingInjuries] = useState(true);

  useEffect(() => {
    let isMounted = true;
    fetchInjuries()
      .then((data) => {
        if (isMounted) {
          setInjuryOptions(data);
        }
      })
      .catch((err) => {
        console.error("Failed to load injuries:", err);
      })
      .finally(() => {
        if (isMounted) {
          setLoadingInjuries(false);
        }
      });
    return () => {
      isMounted = false;
    };
  }, []);

  const toggleInjury = (injury: string) => {
    setSelectedInjuries((prev) =>
      prev.includes(injury)
        ? prev.filter((item) => item !== injury)
        : [...prev, injury],
    );
  };

  const handleFillFromPhoto = async () => {
    try {
      const ImagePicker = await import("expo-image-picker");
      const perm = await ImagePicker.requestMediaLibraryPermissionsAsync();
      if (!perm.granted) {
        Alert.alert(t("common.permissionRequired"), t("common.galleryPermission"));
        return;
      }
      const result = await ImagePicker.launchImageLibraryAsync({
        mediaTypes: ImagePicker.MediaTypeOptions.Images,
        quality: 0.8,
      });
      if (result.canceled || !result.assets[0]?.uri) return;

      setParsingImage(true);
      const { parseAppearanceImage } = await import("@/features/wod/api");
      const res = await parseAppearanceImage(result.assets[0].uri);
      if (res.appearance) {
        setAppearance(res.appearance);
      }
      Alert.alert(t("common.saved"), t("profileEdit.appearanceFilled"));
    } catch (err) {
      console.error("Failed to parse appearance image:", err);
      Alert.alert(t("common.error"), t("profileEdit.parseImageFailed"));
    } finally {
      setParsingImage(false);
    }
  };

  const handleSave = async () => {
    const y = birthYear ? parseInt(birthYear, 10) : 0;
    const m = birthMonth ? parseInt(birthMonth, 10) : 0;
    const d = birthDay ? parseInt(birthDay, 10) : 0;
    const h = heightCm ? parseInt(heightCm, 10) : 0;
    const w = weightKg ? parseFloat(weightKg) : 0;
    const trimmedRhr = restingHrBpm.trim();
    const rhr = trimmedRhr ? Number(trimmedRhr) : 0;

    // Only validate fields that have values (all are optional now)
    if (birthYear && (isNaN(y) || y < 1900 || y > new Date().getFullYear())) {
      Alert.alert(t("profileEdit.invalidInput"), t("profileEdit.invalidYear"));
      return;
    }
    if (birthMonth && (isNaN(m) || m < 1 || m > 12)) {
      Alert.alert(t("profileEdit.invalidInput"), t("profileEdit.invalidMonth"));
      return;
    }
    if (birthDay && (isNaN(d) || d < 1 || d > 31)) {
      Alert.alert(t("profileEdit.invalidInput"), t("profileEdit.invalidDay"));
      return;
    }
    if (heightCm && (isNaN(h) || h < 50 || h > 300)) {
      Alert.alert(t("profileEdit.invalidInput"), t("profileEdit.invalidHeight"));
      return;
    }
    if (weightKg && (isNaN(w) || w < 20 || w > 500)) {
      Alert.alert(t("profileEdit.invalidInput"), t("profileEdit.invalidWeight"));
      return;
    }
    if (trimmedRhr && (!Number.isInteger(rhr) || rhr < 30 || rhr > 120)) {
      Alert.alert(t("profileEdit.invalidInput"), t("profileEdit.invalidRestingHr"));
      return;
    }

    setSaving(true);

    try {
      const payload = {
        name: name.trim(),
        ...(y ? { birth_year: y } : {}),
        ...(m ? { birth_month: m } : {}),
        ...(d ? { birth_day: d } : {}),
        ...(gender ? { gender } : {}),
        ...(h ? { height_cm: h } : {}),
        ...(w ? { weight_kg: Math.round(w * 10) / 10 } : {}),
        ...(rhr ? { resting_hr_bpm: rhr } : {}),
        fitness_level: fitnessLevel,
        injuries: selectedInjuries,
        ...(appearance.trim() ? { appearance: appearance.trim() } : {}),
      };

      if (isEditing) {
        await store.updateProfile(existingProfile.id, {
          ...payload,
          ...(!trimmedRhr && existingProfile.restingHrBpm != null
            ? { clear_resting_hr: true }
            : {}),
        });
      } else {
        await store.createProfile(payload);
      }
      router.back();
    } catch (e) {
      Alert.alert(t("common.error"), t("profileEdit.saveFailed"));
      console.error("Profile save error:", e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <SafeAreaView style={styles.container}>
      <StatusBar style="dark" />
      <View style={styles.header}>
        <TouchableOpacity
          testID="profile-edit-back-btn"
          onPress={() => router.back()}
          style={styles.backBtn}
          accessibilityRole="button"
        >
          <IconSymbol name="chevron.left" size={22} color={T.ink} />
        </TouchableOpacity>
        <Text style={styles.title} numberOfLines={1}>
          {isEditing
            ? t("profileEdit.editProfile")
            : t("profileEdit.newProfile")}
        </Text>
      </View>

        <KeyboardAvoidingView
          style={{ flex: 1 }}
          behavior={Platform.OS === "ios" ? "padding" : undefined}
        >
          <ScrollView
            contentContainerStyle={styles.content}
            showsVerticalScrollIndicator={false}
          >
            {/* Name */}
            <View style={styles.sectionCard}>
              <Text style={styles.sectionTitle}>{t("profileEdit.nameLabel")}</Text>
              <TextInput
                style={styles.input}
                placeholder={t("profileEdit.namePlaceholder")}
                placeholderTextColor={T.inkMuted}
                value={name}
                onChangeText={setName}
                maxLength={50}
                returnKeyType="next"
                autoCapitalize="words"
              />
            </View>

            {/* Birth Date & Gender */}
            <View style={styles.sectionCard}>
              <Text style={styles.sectionTitle}>{t("profileEdit.dob")}</Text>
              <View style={styles.dateRow}>
                <View style={styles.dateField}>
                  <Text style={styles.fieldLabel}>{t("profileEdit.year")}</Text>
                  <TextInput
                    style={styles.input}
                    keyboardType="number-pad"
                    placeholder="1990"
                    placeholderTextColor={T.inkMuted}
                    value={birthYear}
                    onChangeText={setBirthYear}
                    maxLength={4}
                    returnKeyType="next"
                  />
                </View>
                <View style={styles.dateFieldSmall}>
                  <Text style={styles.fieldLabel}>{t("profileEdit.month")}</Text>
                  <TextInput
                    style={styles.input}
                    keyboardType="number-pad"
                    placeholder="03"
                    placeholderTextColor={T.inkMuted}
                    value={birthMonth}
                    onChangeText={setBirthMonth}
                    maxLength={2}
                    returnKeyType="next"
                  />
                </View>
                <View style={styles.dateFieldSmall}>
                  <Text style={styles.fieldLabel}>{t("profileEdit.day")}</Text>
                  <TextInput
                    style={styles.input}
                    keyboardType="number-pad"
                    placeholder="15"
                    placeholderTextColor={T.inkMuted}
                    value={birthDay}
                    onChangeText={setBirthDay}
                    maxLength={2}
                    returnKeyType="next"
                  />
                </View>
              </View>

              <Text style={[styles.sectionTitle, { marginTop: 18 }]}>
                {t("profileEdit.gender")}
              </Text>
              <View style={styles.genderRow}>
                {genderOptions.map((opt) => {
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

            {/* Physical Metrics */}
            <View style={styles.sectionCard}>
              <Text style={styles.sectionTitle}>{t("profileEdit.height")}</Text>
              <View style={styles.inputRow}>
                <TextInput
                  style={[styles.input, { flex: 1 }]}
                  keyboardType="number-pad"
                  placeholder="178"
                  placeholderTextColor={T.inkMuted}
                  value={heightCm}
                  onChangeText={setHeightCm}
                  maxLength={3}
                  returnKeyType="next"
                />
                <Text style={styles.unit}>cm</Text>
              </View>

              <Text style={[styles.sectionTitle, { marginTop: 18 }]}>
                {t("profileEdit.weight")}
              </Text>
              <View style={styles.inputRow}>
                <TextInput
                  style={[styles.input, { flex: 1 }]}
                  keyboardType="decimal-pad"
                  placeholder="85"
                  placeholderTextColor={T.inkMuted}
                  value={weightKg}
                  onChangeText={setWeightKg}
                  maxLength={5}
                  returnKeyType="next"
                />
                <Text style={styles.unit}>kg</Text>
              </View>

              <Text style={[styles.sectionTitle, { marginTop: 18 }]}>
                {t("profileEdit.restingHr")}
              </Text>
              <Text style={styles.hint}>{t("profileEdit.restingHrHint")}</Text>
              <View style={styles.inputRow}>
                <TextInput
                  style={[styles.input, { flex: 1 }]}
                  keyboardType="number-pad"
                  placeholder="60"
                  placeholderTextColor={T.inkMuted}
                  value={restingHrBpm}
                  onChangeText={setRestingHrBpm}
                  maxLength={3}
                  returnKeyType="done"
                />
                <Text style={styles.unit}>bpm</Text>
              </View>
            </View>

            {/* Known Injuries */}
            <View style={styles.sectionCard}>
              <Text style={styles.sectionTitle}>{t("profileEdit.injuries")}</Text>
              <Text style={styles.hint}>{t("profileEdit.injuriesHint")}</Text>
              {loadingInjuries ? (
                <ActivityIndicator color={T.electricBlue} />
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

            {/* Appearance Cues */}
            <View style={styles.sectionCard}>
              <View style={styles.appearanceHeaderRow}>
                <Text style={[styles.sectionTitle, { marginBottom: 0 }]}>
                  {t("profileEdit.appearance")}
                </Text>
                <TouchableOpacity
                  style={styles.fillPhotoBtn}
                  onPress={handleFillFromPhoto}
                  disabled={parsingImage}
                >
                  {parsingImage ? (
                    <ActivityIndicator size="small" color={T.electricBlue} />
                  ) : (
                    <Text style={styles.fillPhotoText}>
                      {t("profileEdit.fillFromPhoto")}
                    </Text>
                  )}
                </TouchableOpacity>
              </View>
              <Text style={[styles.hint, { marginTop: 8 }]}>
                {t("profileEdit.appearanceHint")}
              </Text>

              <TextInput
                style={styles.input}
                placeholder={t("profileEdit.appearancePlaceholder")}
                placeholderTextColor={T.inkMuted}
                value={appearance}
                onChangeText={setAppearance}
                multiline
              />
            </View>

            {/* Fitness Level */}
            <View style={styles.sectionCard}>
              <Text style={styles.sectionTitle}>
                {t("profileEdit.fitnessLevel")}
              </Text>
              <Text style={styles.hint}>
                {t("profileEdit.fitnessLevelHint")}
              </Text>
              <View style={styles.genderRow}>
                {fitnessLevelOptions.map((opt) => {
                  const isSelected = fitnessLevel === opt.value;
                  return (
                    <TouchableOpacity
                      key={opt.value}
                      style={[
                        styles.genderBtn,
                        isSelected && styles.genderBtnActive,
                      ]}
                      onPress={() => setFitnessLevel(opt.value)}
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

            {/* Save Button */}
            <VoltButton
              testID="save-profile-btn"
              label={
                saving
                  ? t("profileEdit.saving")
                  : isEditing
                    ? t("profileEdit.updateProfile")
                    : t("profileEdit.createProfile")
              }
              onPress={handleSave}
              disabled={saving}
              style={{ marginTop: 4 }}
            />
          </ScrollView>
        </KeyboardAvoidingView>
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
  content: { paddingHorizontal: 18, paddingTop: 4, paddingBottom: 48 },

  sectionCard: {
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    padding: 16,
    marginBottom: 14,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 6,
    elevation: 1,
  },
  sectionTitle: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
    textTransform: "uppercase",
    marginBottom: 10,
    letterSpacing: 0.7,
    flexShrink: 1,
  },

  dateRow: { flexDirection: "row", gap: 10 },
  dateField: { flex: 2 },
  dateFieldSmall: { flex: 1 },
  fieldLabel: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "700",
    marginBottom: 6,
    textTransform: "uppercase",
  },

  input: {
    backgroundColor: T.surfaceMuted,
    color: T.ink,
    fontSize: 16,
    fontWeight: "700",
    padding: 13,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },

  genderRow: { flexDirection: "row", gap: 8 },
  genderBtn: {
    flex: 1,
    paddingVertical: 13,
    paddingHorizontal: 6,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.borderStrong,
    backgroundColor: T.surfaceMuted,
    alignItems: "center",
  },
  genderBtnActive: {
    borderColor: T.electricBlue,
    backgroundColor: T.electricBlueBg,
  },
  genderBtnText: {
    color: T.inkSecondary,
    fontSize: 14,
    fontWeight: "700",
    flexShrink: 1,
  },
  genderBtnTextActive: {
    color: T.electricBlue,
    fontWeight: "800",
  },

  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  unit: {
    color: T.inkMuted,
    fontSize: 16,
    fontWeight: "800",
  },

  hint: {
    color: T.inkMuted,
    fontSize: 12,
    marginBottom: 12,
    lineHeight: 18,
  },
  chipContainer: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  chip: {
    paddingVertical: 8,
    paddingHorizontal: 14,
    borderRadius: 999,
    borderWidth: 1,
    borderColor: T.borderStrong,
    backgroundColor: T.surfaceMuted,
  },
  chipActive: {
    backgroundColor: T.statusFatiguedBg,
    borderColor: T.statusFatigued,
  },
  chipText: {
    color: T.inkSecondary,
    fontSize: 13,
    fontWeight: "600",
  },
  chipTextActive: {
    color: T.statusFatigued,
    fontWeight: "800",
  },

  appearanceHeaderRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    gap: 8,
  },
  fillPhotoBtn: {
    backgroundColor: T.electricBlueBg,
    borderColor: T.electricBlueBorder,
    borderWidth: 1,
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 8,
  },
  fillPhotoText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
  },
});
