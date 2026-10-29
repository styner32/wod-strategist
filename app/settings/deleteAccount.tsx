import { IconSymbol } from "@/components/ui/icon-symbol";
import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useAuthStore } from "@/features/auth/useAuthStore";
import { useProfileStore } from "@/store/useProfileStore";
import { router } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useState } from "react";
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

export default function DeleteAccountScreen() {
  useLocale();
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const deleteAccount = useAuthStore((s) => s.deleteAccount);

  const handleDelete = () => {
    if (!password) return;

    Alert.alert(t("auth.deleteAccount"), t("auth.deleteConfirm"), [
      { text: t("common.cancel"), style: "cancel" },
      {
        text: t("common.delete"),
        style: "destructive",
        onPress: async () => {
          setLoading(true);
          try {
            await deleteAccount(password);
            // Clear profile store
            useProfileStore.getState().clearActiveProfile();
          } catch (e: any) {
            const msg = e?.message || "";
            if (msg.includes("401")) {
              Alert.alert(t("common.error"), t("auth.loginFailed"));
            } else {
              Alert.alert(t("common.error"), t("auth.deleteFailed"));
            }
          } finally {
            setLoading(false);
          }
        },
      },
    ]);
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
          {t("auth.deleteAccount")}
        </Text>
      </View>

      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          contentContainerStyle={styles.content}
          keyboardShouldPersistTaps="handled"
        >
          <View style={styles.warningBox}>
            <Text style={styles.warningText}>{t("auth.deleteConfirm")}</Text>
          </View>

          <View style={styles.formCard}>
            <Text style={styles.label}>{t("auth.deletePassword")}</Text>
            <TextInput
              style={styles.input}
              placeholder="••••••••"
              placeholderTextColor={T.inkMuted}
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              maxLength={128}
              returnKeyType="go"
              onSubmitEditing={handleDelete}
            />

            <TouchableOpacity
              style={[
                styles.deleteBtn,
                (loading || !password) && styles.btnDisabled,
              ]}
              onPress={handleDelete}
              disabled={loading || !password}
            >
              {loading ? (
                <ActivityIndicator color="#fff" />
              ) : (
                <Text style={styles.deleteBtnText}>
                  {t("auth.deleteAccount")}
                </Text>
              )}
            </TouchableOpacity>
          </View>
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
    color: T.statusExhausted,
  },
  content: {
    flexGrow: 1,
    paddingHorizontal: 18,
    paddingTop: 6,
    paddingBottom: 40,
    gap: 16,
  },
  warningBox: {
    backgroundColor: T.statusExhaustedBg,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
    borderRadius: 16,
    padding: 16,
  },
  warningText: {
    color: T.statusExhausted,
    fontSize: 14,
    fontWeight: "600",
    lineHeight: 21,
  },
  formCard: {
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    borderRadius: 16,
    padding: 16,
    gap: 12,
  },
  label: {
    color: T.statusExhausted,
    fontSize: 12,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.7,
  },
  input: {
    backgroundColor: T.surfaceMuted,
    color: T.ink,
    fontSize: 16,
    fontWeight: "700",
    padding: 14,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  deleteBtn: {
    backgroundColor: T.statusExhausted,
    padding: 16,
    borderRadius: 12,
    alignItems: "center",
    marginTop: 6,
  },
  btnDisabled: { opacity: 0.45 },
  deleteBtnText: {
    color: "#fff",
    fontSize: 16,
    fontWeight: "800",
  },
});
