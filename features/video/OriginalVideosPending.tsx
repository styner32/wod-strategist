import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, AppState, Linking, StyleSheet, Text, TouchableOpacity, View } from "react-native";

import { TacticalTheme } from "@/constants/theme";
import { useAuthStore } from "../auth/useAuthStore";
import { t, useLocale } from "../i18n";
import { useProfileId } from "../../store/useProfileStore";
import { formatSessionLabel } from "../wod/sessionLabel";
import {
  confirmOriginalAlreadySaved,
  discardOriginalSession,
  finalizeAndSaveOriginal,
  listOriginalSessions,
  subscribeOriginalVideos,
  type OriginalVideoSession,
} from "./originalVideoStore";

const T = TacticalTheme.light;

/** Independent of analysis history: an offline recording can still be recovered and saved. */
export function OriginalVideosPending() {
  useLocale();
  const profileId = useProfileId();
  const userId = useAuthStore(state => state.userId);
  const loggedIn = useAuthStore(state => state.isLoggedIn);
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

  const perform = async (session: OriginalVideoSession, action: "retry" | "retry_uncertain" | "confirmed" | "discard") => {
    if (busy) return;
    setBusy(session.sessionId);
    try {
      if (action === "confirmed") await confirmOriginalAlreadySaved(session);
      else if (action === "discard") await discardOriginalSession(session);
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
  const color = T.ink;
  const muted = T.inkMuted;

  return (
    <View style={styles.section}>
      <Text style={[styles.title, { color }]}>{t("originalVideos.title")}</Text>
      <Text style={[styles.description, { color: muted }]}>{t("originalVideos.description")}</Text>
      {visible.map(session => {
        const working = busy === session.sessionId || ["recording", "preparing", "saving"].includes(session.status);
        const canRetry = session.complete && !session.captureIssue && !working;
        const canDiscard = !working;
        return (
          <View key={session.sessionId} style={styles.card}>
            <View style={styles.row}>
              <Text style={[styles.label, { color }]}>{formatSessionLabel(session.sessionId)}</Text>
              {working && <ActivityIndicator size="small" color={T.electricBlue} />}
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
              {canDiscard && (
                <TouchableOpacity disabled={!!busy} accessibilityRole="button" style={styles.discardButton} onPress={() => {
                  Alert.alert(t("originalVideos.discardTitle"), t("originalVideos.discardBody"), [
                    { text: t("common.cancel"), style: "cancel" },
                    { text: t("originalVideos.discard"), style: "destructive", onPress: () => { void perform(session, "discard"); } },
                  ]);
                }}>
                  <Text style={styles.discardButtonText}>{t("originalVideos.discard")}</Text>
                </TouchableOpacity>
              )}
            </View>
          </View>
        );
      })}
    </View>
  );
}

const styles = StyleSheet.create({
  section: { gap: 8, marginBottom: 16 },
  title: { fontSize: 16, fontWeight: "800", letterSpacing: -0.2 },
  description: { fontSize: 13, lineHeight: 19 },
  card: {
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    padding: 16,
    gap: 8,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 8,
    elevation: 2,
  },
  row: { flexDirection: "row", alignItems: "center", gap: 8 },
  label: { fontSize: 14, fontWeight: "700", flex: 1, flexShrink: 1 },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 4 },
  button: {
    paddingVertical: 9,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  buttonText: { color: T.electricBlue, fontSize: 13, fontWeight: "700" },
  discardButton: {
    paddingVertical: 9,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: T.statusExhaustedBg,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
  },
  discardButtonText: { color: T.statusExhausted, fontSize: 13, fontWeight: "700" },
});
