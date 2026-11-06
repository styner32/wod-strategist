import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  Share,
  StyleSheet,
  Switch,
  Text,
  View,
} from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { TacticalTheme } from "@/constants/theme";
import { t, useLocale } from "@/features/i18n";
import {
  buildWorkoutShareText,
  type WorkoutShareContext,
} from "../../../shared/workoutShare";
import { getWorkoutShareContext } from "../api";

const T = TacticalTheme.light;

export function WorkoutShareSheet({ visible, sessionId, profileId, onClose }: {
  visible: boolean;
  sessionId: string;
  profileId: number;
  onClose: () => void;
}) {
  return <Modal visible={visible} animationType="slide" onRequestClose={onClose}>
    {visible && <SafeAreaProvider>
      <WorkoutShareContent key={`${profileId}:${sessionId}`} sessionId={sessionId} profileId={profileId} onClose={onClose} />
    </SafeAreaProvider>}
  </Modal>;
}

function WorkoutShareContent({ sessionId, profileId, onClose }: {
  sessionId: string;
  profileId: number;
  onClose: () => void;
}) {
  const locale = useLocale() === "ko" ? "ko" : "en";
  const mounted = useRef(false);
  const [context, setContext] = useState<WorkoutShareContext>();
  const [includeAIObservations, setIncludeAIObservations] = useState(true);
  const [includeSensorSummary, setIncludeSensorSummary] = useState(true);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [sharing, setSharing] = useState(false);
  const [shareFailed, setShareFailed] = useState(false);
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    let timezone = "UTC";
    try {
      timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
    } catch {}
    getWorkoutShareContext(sessionId, profileId, timezone, controller.signal)
      .then(result => {
        if (active) setContext(result);
      })
      .catch(() => {
        if (active) setLoadFailed(true);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [sessionId, profileId, retry]);

  const text = useMemo(() => context ? buildWorkoutShareText(context, {
    includeAIObservations,
    includeSensorSummary,
  }, locale) : "", [context, includeAIObservations, includeSensorSummary, locale]);

  const share = async () => {
    if (!text || sharing) return;
    setShareFailed(false);
    setSharing(true);
    try {
      await Share.share({ message: text, title: t("workoutShare.title") });
    } catch {
      if (mounted.current) setShareFailed(true);
    } finally {
      if (mounted.current) setSharing(false);
    }
  };

  const retryLoad = () => {
    setContext(undefined);
    setLoadFailed(false);
    setLoading(true);
    setRetry(value => value + 1);
  };

  return <SafeAreaView style={styles.screen}>
      <View style={styles.header}>
        <Text accessibilityRole="header" style={styles.title}>{t("workoutShare.title")}</Text>
        <Pressable accessibilityRole="button" onPress={onClose} hitSlop={8} style={[styles.button, styles.closeButton]}>
          <Text style={styles.buttonText}>{t("workoutShare.close")}</Text>
        </Pressable>
      </View>
      <Text style={styles.description}>{t("workoutShare.description")}</Text>
      {loading && <View style={styles.loading}><ActivityIndicator color={T.electricBlue} /><Text style={styles.text}>{t("workoutShare.loading")}</Text></View>}
      {loadFailed && <View style={styles.errorBlock}>
        <Text accessibilityRole="alert" style={styles.error}>{t("workoutShare.loadFailed")}</Text>
        <Pressable accessibilityRole="button" onPress={retryLoad} style={styles.button}>
          <Text style={styles.buttonText}>{t("workoutShare.retry")}</Text>
        </Pressable>
      </View>}
      {context && <>
        <View style={styles.option}>
          <Text style={styles.optionLabel}>{t("workoutShare.includeAI")}</Text>
          <Switch accessibilityLabel={t("workoutShare.includeAI")} value={includeAIObservations} onValueChange={setIncludeAIObservations} disabled={sharing} />
        </View>
        <View style={styles.option}>
          <Text style={styles.optionLabel}>{t("workoutShare.includeSensors")}</Text>
          <Switch accessibilityLabel={t("workoutShare.includeSensors")} value={includeSensorSummary} onValueChange={setIncludeSensorSummary} disabled={sharing} />
        </View>
        <Text style={styles.previewLabel}>{t("workoutShare.preview")}</Text>
        <ScrollView style={styles.preview} contentContainerStyle={styles.previewContent}>
          <Text selectable accessibilityLabel={t("workoutShare.preview")} style={styles.previewText}>{text}</Text>
        </ScrollView>
        <Text style={styles.hint}>{t("workoutShare.manualCopy")}</Text>
        {shareFailed && <Text accessibilityRole="alert" style={styles.error}>{t("workoutShare.shareFailed")}</Text>}
        <Pressable accessibilityRole="button" disabled={sharing} onPress={share} style={[styles.shareButton, sharing && styles.disabled]}>
          <Text style={styles.shareButtonText}>{t(sharing ? "workoutShare.sharing" : shareFailed ? "workoutShare.retryShare" : "workoutShare.share")}</Text>
        </Pressable>
      </>}
    </SafeAreaView>;
}

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: T.canvas, padding: 18, gap: 12 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 12 },
  title: { flex: 1, color: T.ink, fontSize: 21, fontWeight: "800" },
  description: { color: T.inkMuted, fontSize: 14, lineHeight: 21 },
  text: { color: T.ink, fontSize: 14 },
  loading: { flexDirection: "row", gap: 10, paddingVertical: 20 },
  button: { paddingHorizontal: 12, paddingVertical: 10, borderRadius: 10, borderWidth: 1, borderColor: T.borderStrong },
  closeButton: { minWidth: 44, minHeight: 44, alignItems: "center", justifyContent: "center", flexShrink: 0 },
  buttonText: { color: T.electricBlue, fontWeight: "700", fontSize: 14 },
  errorBlock: { gap: 12, alignItems: "flex-start" },
  error: { color: T.statusExhausted, fontSize: 14 },
  option: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 12 },
  optionLabel: { flex: 1, color: T.ink, fontSize: 14, fontWeight: "600" },
  previewLabel: { color: T.ink, fontSize: 15, fontWeight: "700" },
  preview: { flex: 1, borderColor: T.borderStrong, borderWidth: 1, borderRadius: 12, backgroundColor: T.surfaceContainer },
  previewContent: { padding: 14 },
  previewText: { color: T.ink, fontSize: 13, lineHeight: 20 },
  hint: { color: T.inkMuted, fontSize: 12, lineHeight: 18 },
  shareButton: { backgroundColor: T.electricBlue, padding: 15, borderRadius: 12, alignItems: "center" },
  shareButtonText: { color: "#FFF", fontSize: 15, fontWeight: "800" },
  disabled: { opacity: 0.55 },
});
