import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import { useAuthStore } from "@/features/auth/useAuthStore";
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

export default function LoginScreen() {
  useLocale();
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
      <StatusBar style="dark" />
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          contentContainerStyle={styles.content}
          keyboardShouldPersistTaps="handled"
        >
          <View style={styles.header}>
            <View style={styles.eyebrowRow}>
              <View style={styles.eyebrowDot} />
              <Text style={styles.eyebrowText}>{t("home.brandEyebrow")}</Text>
            </View>
            <View style={styles.brandRow}>
              <Text style={styles.appName}>WOD Strategist</Text>
              <View style={styles.voltDot} />
            </View>
            <Text style={styles.subtitle}>{t("auth.login")}</Text>
          </View>

          <View style={styles.formCard}>
            <Text style={styles.label}>{t("auth.username")}</Text>
            <TextInput
              style={styles.input}
              placeholder="username"
              placeholderTextColor={T.inkMuted}
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
              placeholderTextColor={T.inkMuted}
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              maxLength={128}
              returnKeyType="go"
              onSubmitEditing={handleLogin}
            />

            <TouchableOpacity
              style={[
                styles.primaryBtn,
                (loading || !username.trim() || !password) &&
                  styles.btnDisabled,
              ]}
              onPress={handleLogin}
              disabled={loading || !username.trim() || !password}
            >
              {loading ? (
                <ActivityIndicator color={T.voltInk} />
              ) : (
                <Text style={styles.primaryBtnText}>{t("auth.login")}</Text>
              )}
            </TouchableOpacity>
          </View>

          <View style={styles.footer}>
            <Text style={styles.footerText}>{t("auth.noAccount")}</Text>
            <TouchableOpacity
              onPress={() => router.replace("/auth/signup" as any)}
            >
              <Text style={styles.footerLink}>{t("auth.signup")}</Text>
            </TouchableOpacity>
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: T.canvas },
  content: {
    flexGrow: 1,
    justifyContent: "center",
    padding: 22,
  },
  header: {
    alignItems: "center",
    marginBottom: 32,
    gap: 4,
  },
  eyebrowRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
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
    letterSpacing: 1.2,
    color: T.electricBlue,
    textTransform: "uppercase",
  },
  brandRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  appName: {
    fontSize: 30,
    fontWeight: "900",
    color: T.ink,
    letterSpacing: -0.7,
  },
  voltDot: {
    width: 10,
    height: 10,
    borderRadius: 5,
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
  },
  subtitle: {
    fontSize: 15,
    fontWeight: "600",
    color: T.inkMuted,
    marginTop: 2,
  },
  formCard: {
    backgroundColor: T.surface,
    borderRadius: 20,
    borderWidth: 1,
    borderColor: T.border,
    padding: 20,
    gap: 10,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.06,
    shadowRadius: 12,
    elevation: 2,
  },
  label: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.7,
    marginTop: 4,
  },
  input: {
    backgroundColor: T.surfaceMuted,
    color: T.ink,
    fontSize: 17,
    fontWeight: "700",
    padding: 14,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  primaryBtn: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    padding: 16,
    borderRadius: 14,
    alignItems: "center",
    marginTop: 12,
  },
  btnDisabled: { opacity: 0.5 },
  primaryBtnText: {
    color: T.voltInk,
    fontSize: 16,
    fontWeight: "900",
    letterSpacing: 0.5,
    textTransform: "uppercase",
  },
  footer: {
    flexDirection: "row",
    justifyContent: "center",
    alignItems: "center",
    marginTop: 28,
    gap: 6,
    flexWrap: "wrap",
  },
  footerText: {
    color: T.inkMuted,
    fontSize: 14,
    fontWeight: "600",
  },
  footerLink: {
    color: T.electricBlue,
    fontSize: 14,
    fontWeight: "800",
  },
});
