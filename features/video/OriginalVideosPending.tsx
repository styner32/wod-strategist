import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, AppState, Linking, StyleSheet, Text, TouchableOpacity, View } from "react-native";

import { useAuthStore } from "../auth/useAuthStore";
import { t, useLocale } from "../i18n";
import { useProfileId } from "../../store/useProfileStore";
import { useColorScheme } from "../../hooks/use-color-scheme";
import { formatSessionLabel } from "../wod/sessionLabel";
import {
  confirmOriginalAlreadySaved,
  finalizeAndSaveOriginal,
  listOriginalSessions,
  subscribeOriginalVideos,
  type OriginalVideoSession,
} from "./originalVideoStore";

/** Independent of analysis history: an offline recording can still be recovered and saved. */
export function OriginalVideosPending() {
  useLocale();
  const profileId = useProfileId();
  const userId = useAuthStore(state => state.userId);
  const loggedIn = useAuthStore(state => state.isLoggedIn);
  const dark = useColorScheme() === "dark";
  const [sessions, setSessions] = useState<OriginalVideoSession[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [details, setDetails] = useState<string | null>(null);
  const generation = useRef(0);

  const refresh = useCallback(() => {
    const current = ++generation.current;
    if (!userId || !loggedIn) return;
    void listOriginalSessions(profileId).then(results => {
      if (generation.current === current) setSessions(results.filter(item => item.status !== "saved"));
    }).catch(() => {
      // A read failure cannot change retention or mark a recording as saved.
    });
  }, [profileId, userId, loggedIn]);

  useEffect(() => {
    const requests = generation;
    void refresh();
    const unsubscribe = subscribeOriginalVideos(() => { void refresh(); });
    const subscription = AppState.addEventListener("change", state => { if (state === "active") void refresh(); });
    return () => { requests.current++; unsubscribe(); subscription.remove(); };
  }, [refresh]);

  const perform = async (session: OriginalVideoSession, action: "retry" | "retry_uncertain" | "confirmed") => {
    if (busy) return;
    setBusy(session.sessionId);
    try {
      if (action === "confirmed") await confirmOriginalAlreadySaved(session);
      else await finalizeAndSaveOriginal(session, { retryUncertain: action === "retry_uncertain" });
    } catch {
      Alert.alert(t("common.error"), t("originalVideos.operationFailed"));
    } finally {
      setBusy(null);
      void refresh();
    }
  };

  const visible = loggedIn ? sessions.filter(item => item.ownerUserId === userId && (profileId == null || item.profileId === profileId)) : [];
  if (visible.length === 0) return null;
  const color = dark ? "#F5F5F7" : "#1C1C1E";
  const muted = dark ? "#C7C7CC" : "#636366";

  return (
    <View style={styles.section}>
      <Text style={[styles.title, { color }]}>{t("originalVideos.title")}</Text>
      <Text style={[styles.description, { color: muted }]}>{t("originalVideos.description")}</Text>
      {visible.map(session => {
        const working = busy === session.sessionId || ["recording", "preparing", "saving"].includes(session.status);
        const canRetry = session.complete && !session.captureIssue && !working;
        return (
          <View key={session.sessionId} style={[styles.card, { backgroundColor: dark ? "#2C2C2E" : "#F5F5F5" }]}>
            <View style={styles.row}>
              <Text style={[styles.label, { color }]}>{formatSessionLabel(session.sessionId)}</Text>
              {working && <ActivityIndicator size="small" color="#64D2FF" />}
            </View>
            {profileId == null && <Text style={[styles.description, { color: muted }]}>{t("originalVideos.profile", { id: session.profileId })}</Text>}
            <Text style={[styles.description, { color: muted }]}>{t(`originalVideos.status.${session.status}`)}</Text>
            {session.status === "failed" && session.lastErrorStage && (
              <Text style={[styles.description, { color: muted }]}>{t(`originalVideos.failureStage.${session.lastErrorStage}`)}</Text>
            )}
            {session.status !== "recording" && <Text style={[styles.description, { color: muted }]}>{t("originalVideos.retained")}</Text>}
            {session.lastError && ["failed", "needs_attention"].includes(session.status) && (
              <>
                <TouchableOpacity accessibilityRole="button" onPress={() => setDetails(details === session.sessionId ? null : session.sessionId)}>
                  <Text style={styles.buttonText}>{t(details === session.sessionId ? "originalVideos.hideError" : "originalVideos.showError")}</Text>
                </TouchableOpacity>
                {details === session.sessionId && <Text selectable style={[styles.description, { color }]}>{session.lastError}</Text>}
              </>
            )}
            <View style={styles.actions}>
              {session.status === "uncertain" ? (
                <>
                  <TouchableOpacity disabled={!!busy} accessibilityRole="button" style={styles.button} onPress={() => {
                    Alert.alert(t("originalVideos.confirmSavedTitle"), t("originalVideos.confirmSavedBody"), [
                      { text: t("common.cancel"), style: "cancel" },
                      { text: t("originalVideos.alreadySaved"), onPress: () => { void perform(session, "confirmed"); } },
                    ]);
                  }}><Text style={styles.buttonText}>{t("originalVideos.alreadySaved")}</Text></TouchableOpacity>
                  <TouchableOpacity disabled={!!busy} accessibilityRole="button" style={styles.button} onPress={() => {
                    Alert.alert(t("originalVideos.retryUncertainTitle"), t("originalVideos.retryUncertainBody"), [
                      { text: t("common.cancel"), style: "cancel" },
                      { text: t("originalVideos.retry"), onPress: () => { void perform(session, "retry_uncertain"); } },
                    ]);
                  }}><Text style={styles.buttonText}>{t("originalVideos.retry")}</Text></TouchableOpacity>
                </>
              ) : canRetry ? (
                <TouchableOpacity disabled={!!busy} accessibilityRole="button" style={styles.button} onPress={() => { void perform(session, "retry"); }}>
                  <Text style={styles.buttonText}>{t("originalVideos.retry")}</Text>
                </TouchableOpacity>
              ) : null}
              {session.status === "permission_denied" && <TouchableOpacity accessibilityRole="button" style={styles.button} onPress={() => { void Linking.openSettings(); }}>
                <Text style={styles.buttonText}>{t("originalVideos.openSettings")}</Text>
              </TouchableOpacity>}
            </View>
          </View>
        );
      })}
    </View>
  );
}

const styles = StyleSheet.create({
  section: { gap: 8, marginBottom: 16 },
  title: { fontSize: 16, fontWeight: "700" },
  description: { fontSize: 13, lineHeight: 19 },
  card: { borderRadius: 14, padding: 14, gap: 8 },
  row: { flexDirection: "row", alignItems: "center", gap: 8 },
  label: { fontSize: 14, fontWeight: "600", flex: 1 },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  button: { paddingVertical: 9, paddingHorizontal: 12, borderRadius: 8, backgroundColor: "rgba(100,210,255,0.15)" },
  buttonText: { color: "#287EA3", fontSize: 13, fontWeight: "600" },
});
