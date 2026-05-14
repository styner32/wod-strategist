import { t } from "@/features/i18n";
import { useAuthStore } from "@/features/auth/useAuthStore";
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

export default function LoginScreen() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const login = useAuthStore((s) => s.login);

  const handleLogin = async () => {
    if (!username.trim() || !password) return;
    setLoading(true);
    try {
      await login(username.trim().toLowerCase(), password);
      router.replace("/" as any);
    } catch (e: any) {
      const msg = e?.message || "";
      if (msg.includes("401")) {
        Alert.alert(t("common.error"), t("auth.loginFailed"));
      } else {
        Alert.alert(t("common.error"), t("auth.loginFailed"));
      }
    } finally {
      setLoading(false);
    }
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
            <Text style={styles.appName}>WOD Strategist</Text>
            <Text style={styles.subtitle}>{t("auth.login")}</Text>
          </View>

          <View style={styles.form}>
            <Text style={styles.label}>{t("auth.username")}</Text>
            <TextInput
              style={styles.input}
              placeholder="username"
              placeholderTextColor="#555"
              value={username}
              onChangeText={setUsername}
              autoCapitalize="none"
              autoCorrect={false}
              maxLength={20}
              returnKeyType="next"
            />

            <Text style={styles.label}>{t("auth.password")}</Text>
            <TextInput
              style={styles.input}
              placeholder="••••••••"
              placeholderTextColor="#555"
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              maxLength={128}
              returnKeyType="go"
              onSubmitEditing={handleLogin}
            />

            <TouchableOpacity
              style={[styles.primaryBtn, loading && styles.btnDisabled]}
              onPress={handleLogin}
              disabled={loading || !username.trim() || !password}
            >
              {loading ? (
                <ActivityIndicator color="#000" />
              ) : (
                <Text style={styles.primaryBtnText}>{t("auth.login")}</Text>
              )}
            </TouchableOpacity>
          </View>

          <View style={styles.footer}>
            <Text style={styles.footerText}>{t("auth.noAccount")}</Text>
            <TouchableOpacity onPress={() => router.replace("/auth/signup" as any)}>
              <Text style={styles.footerLink}>{t("auth.signup")}</Text>
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
    justifyContent: "center",
    padding: 24,
  },
  header: {
    alignItems: "center",
    marginBottom: 48,
  },
  appName: {
    fontSize: 32,
    fontWeight: "bold",
    color: "#fff",
    letterSpacing: 1,
  },
  subtitle: {
    fontSize: 16,
    color: "#888",
    marginTop: 8,
  },
  form: {
    gap: 12,
  },
  label: {
    color: "#007AFF",
    fontSize: 13,
    fontWeight: "bold",
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginTop: 8,
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
  primaryBtn: {
    backgroundColor: "#fff",
    padding: 18,
    borderRadius: 12,
    alignItems: "center",
    marginTop: 16,
  },
  btnDisabled: { opacity: 0.5 },
  primaryBtnText: {
    color: "#000",
    fontSize: 18,
    fontWeight: "bold",
  },
  footer: {
    flexDirection: "row",
    justifyContent: "center",
    alignItems: "center",
    marginTop: 32,
    gap: 6,
  },
  footerText: {
    color: "#888",
    fontSize: 15,
  },
  footerLink: {
    color: "#007AFF",
    fontSize: 15,
    fontWeight: "600",
  },
});
