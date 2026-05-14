import { t } from "@/features/i18n";
import { useAuthStore } from "@/features/auth/useAuthStore";
import { useProfileStore } from "@/store/useProfileStore";
import { router } from "expo-router";
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

export default function DeleteAccountScreen() {
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const deleteAccount = useAuthStore((s) => s.deleteAccount);

  const handleDelete = () => {
    if (!password) return;

    Alert.alert(
      t("auth.deleteAccount"),
      t("auth.deleteConfirm"),
      [
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
      ]
    );
  };

  return (
    <SafeAreaView style={styles.container}>
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          contentContainerStyle={styles.content}
          keyboardShouldPersistTaps="handled"
        >
          <View style={styles.header}>
            <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
              <Text style={styles.backText}>← {t("common.back")}</Text>
            </TouchableOpacity>
            <Text style={styles.title}>{t("auth.deleteAccount")}</Text>
          </View>

          <View style={styles.warningBox}>
            <Text style={styles.warningText}>{t("auth.deleteConfirm")}</Text>
          </View>

          <View style={styles.form}>
            <Text style={styles.label}>{t("auth.deletePassword")}</Text>
            <TextInput
              style={styles.input}
              placeholder="••••••••"
              placeholderTextColor="#555"
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              maxLength={128}
              returnKeyType="go"
              onSubmitEditing={handleDelete}
            />

            <TouchableOpacity
              style={[styles.deleteBtn, loading && styles.btnDisabled]}
              onPress={handleDelete}
              disabled={loading || !password}
            >
              {loading ? (
                <ActivityIndicator color="#fff" />
              ) : (
                <Text style={styles.deleteBtnText}>{t("auth.deleteAccount")}</Text>
              )}
            </TouchableOpacity>
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000" },
  content: {
    flexGrow: 1,
    padding: 24,
  },
  header: {
    marginBottom: 32,
  },
  backBtn: {
    marginBottom: 16,
  },
  backText: {
    color: "#007AFF",
    fontSize: 16,
  },
  title: {
    fontSize: 24,
    fontWeight: "bold",
    color: "#FF3B30",
  },
  warningBox: {
    backgroundColor: "#1A0000",
    borderWidth: 1,
    borderColor: "#FF3B30",
    borderRadius: 12,
    padding: 16,
    marginBottom: 32,
  },
  warningText: {
    color: "#FF8A80",
    fontSize: 15,
    lineHeight: 22,
  },
  form: {
    gap: 12,
  },
  label: {
    color: "#FF3B30",
    fontSize: 13,
    fontWeight: "bold",
    textTransform: "uppercase",
    letterSpacing: 0.5,
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
  deleteBtn: {
    backgroundColor: "#FF3B30",
    padding: 18,
    borderRadius: 12,
    alignItems: "center",
    marginTop: 16,
  },
  btnDisabled: { opacity: 0.5 },
  deleteBtnText: {
    color: "#fff",
    fontSize: 18,
    fontWeight: "bold",
  },
});
