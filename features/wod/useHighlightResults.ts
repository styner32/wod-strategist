import { useIsFocused } from "expo-router/react-navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, AppState } from "react-native";
import { t } from "../i18n";
import { fetchHighlightResults, generateHighlight } from "./api";
import type { HighlightResult } from "./history";

const POLL_INTERVAL_MS = 5_000;
const GENERATION_TIMEOUT_MS = 5 * 60 * 1_000;

interface HighlightRun {
  sessionId: string;
  profileId: number;
  deadline: number;
}

export function useHighlightResults(sessionId: string, profileId: number, enabled: boolean) {
  const focused = useIsFocused();
  const [appState, setAppState] = useState(AppState.currentState);
  useEffect(() => {
    const subscription = AppState.addEventListener("change", setAppState);
    return () => subscription.remove();
  }, []);
  const scope = useMemo(() => ({
    sessionId, profileId, active: enabled && focused && appState === "active",
  }), [sessionId, profileId, enabled, focused, appState]);
  const [results, setResults] = useState<{
    sessionId: string; profileId: number; values: HighlightResult[];
  } | null>(null);
  const [run, setRun] = useState<HighlightRun | null>(null);
  const pending = useRef<HighlightRun | null>(null);
  const posted = useRef<HighlightRun | null>(null);
  const initialRequest = useRef<AbortController | null>(null);
  const [phase, setPhase] = useState<{
    scope: typeof scope; run: HighlightRun; value: "loading" | "polling";
  } | null>(null);

  // Initial reads have their own lifetime; finishing a generation must not
  // accidentally trigger another request after polling has stopped.
  useEffect(() => {
    if (!scope.active || (pending.current?.sessionId === sessionId && pending.current.profileId === profileId)) return;
    const controller = new AbortController();
    initialRequest.current = controller;
    let active = true;
    void fetchHighlightResults(sessionId, controller.signal).then(values => {
      if (active && !controller.signal.aborted) setResults({ sessionId, profileId, values: values.filter(r => r.status === "COMPLETED") });
    }).catch(() => {});
    return () => {
      active = false;
      controller.abort();
      if (initialRequest.current === controller) initialRequest.current = null;
    };
  }, [scope, sessionId, profileId]);

  useEffect(() => {
    if (!scope.active || !run || run.sessionId !== sessionId || run.profileId !== profileId) return;
    const controller = new AbortController();
    let active = true;
    let pollTimer: ReturnType<typeof setTimeout> | undefined;
    let deadlineTimer: ReturnType<typeof setTimeout> | undefined;
    let hasCompleted = false;
    const current = () => active && !controller.signal.aborted && pending.current === run;
    const stop = () => {
      active = false;
      clearTimeout(pollTimer);
      clearTimeout(deadlineTimer);
      controller.abort();
    };
    const finish = () => {
      stop();
      if (pending.current === run) pending.current = null;
      setRun(previous => previous === run ? null : previous);
    };
    const expire = () => {
      if (!current()) return;
      finish();
      if (!hasCompleted) Alert.alert(t("historyList.timeout"), t("historyList.timeoutDesc"));
    };
    const poll = async () => {
      if (!current()) return;
      if (Date.now() >= run.deadline) { expire(); return; }
      try {
        const values = await fetchHighlightResults(sessionId, controller.signal);
        if (!current()) return;
        if (Date.now() >= run.deadline) { expire(); return; }
        const completed = values.filter(r => r.status === "COMPLETED");
        hasCompleted = completed.length > 0;
        if (hasCompleted) setResults({ sessionId, profileId, values: completed });
        if (values.length > 0 && values.every(r => r.status === "COMPLETED" || r.status === "FAILED")) {
          finish();
          if (!hasCompleted && values.some(r => r.status === "FAILED")) {
            Alert.alert(t("historyList.generationFailed"), t("historyList.generationFailedDesc"));
          }
        }
      } catch {
        // Transient errors may retry, but cannot extend the independent deadline.
      } finally {
        if (current()) {
          if (Date.now() >= run.deadline) expire();
          else pollTimer = setTimeout(() => void poll(), POLL_INTERVAL_MS);
        }
      }
    };
    const begin = async () => {
      if (!current()) return;
      if (Date.now() >= run.deadline) { expire(); return; }
      // A blur can abort a POST after the server accepted it. Resume result
      // checks within the original deadline without submitting the job twice.
      const alreadyPosted = posted.current === run;
      if (!alreadyPosted) {
        posted.current = run;
        try {
          await generateHighlight(sessionId, profileId, 60, controller.signal);
        } catch (error) {
          if (current()) {
            finish();
            Alert.alert(t("common.error"), error instanceof Error ? error.message : t("historyList.generationFailedDesc"));
          }
          return;
        }
      }
      if (!current()) return;
      if (Date.now() >= run.deadline) { expire(); return; }
      setPhase({ scope, run, value: "polling" });
      if (alreadyPosted) void poll();
      else pollTimer = setTimeout(() => void poll(), POLL_INTERVAL_MS);
    };
    deadlineTimer = setTimeout(expire, Math.max(0, run.deadline - Date.now()));
    void Promise.resolve().then(begin);
    return stop;
  }, [scope, run, sessionId, profileId]);

  const handleCreateHighlights = useCallback(() => {
    if (!scope.active) return;
    if (pending.current?.sessionId === sessionId && pending.current.profileId === profileId) return;
    initialRequest.current?.abort();
    const next = { sessionId, profileId, deadline: Date.now() + GENERATION_TIMEOUT_MS };
    pending.current = next;
    setRun(next);
  }, [scope, sessionId, profileId]);
  const currentRun = run?.sessionId === sessionId && run.profileId === profileId ? run : null;
  const currentPhase = phase?.scope === scope && phase.run === currentRun ? phase.value : "loading";
  return {
    highlights: results?.sessionId === sessionId && results.profileId === profileId ? results.values : [],
    highlightLoading: scope.active && currentRun !== null && currentPhase === "loading",
    highlightPolling: scope.active && currentRun !== null && currentPhase === "polling",
    handleCreateHighlights,
  };
}
