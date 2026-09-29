import { useEffect, useState, type RefObject } from "react";
import { useLocale } from "../i18n";
import { fetchActivitySummary, fetchChunkAnalysis, type ChunkAnalysisResult } from "./api";
import type { ActivitySummary } from "../../shared/activity";
import { captureAdviceAt, coachingText, latestCaptureResult } from "./liveFeedback";

export function useLiveWorkoutFeedback(recording: boolean, profileId: number, session: RefObject<string>, startedAt: RefObject<number>) {
  const locale = useLocale();
  const [result, setResult] = useState<ChunkAnalysisResult | null>(null);
  const [summary, setSummary] = useState<{ sessionId: string; profileId: number; value: ActivitySummary } | null>(null);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    setResult(null); setSummary(null);
    if (!recording || !profileId) return;
    let active = true, busy = false, currentSession = session.current;
    let request: AbortController | undefined;
    const poll = async () => {
      if (busy) return;
      const sid = session.current;
      if (!sid) return;
      if (sid !== currentSession) { currentSession = sid; setResult(null); setSummary(null); }
      busy = true;
      const controller = new AbortController();
      request = controller;
      const timeout = setTimeout(() => controller.abort(), 10000);
      const current = () => active && !controller.signal.aborted && sid === session.current;
      try {
        await Promise.allSettled([
          fetchChunkAnalysis(sid, controller.signal).then(chunks => {
            if (current()) setResult(old => latestCaptureResult(old, chunks, sid));
          }),
          fetchActivitySummary(sid, profileId, controller.signal).then(value => {
            if (current()) setSummary({ sessionId: sid, profileId, value });
          }),
        ]);
      } finally { clearTimeout(timeout); busy = false; }
    };
    void poll();
    const pollTimer = setInterval(() => void poll(), 3000);
    const clockTimer = setInterval(() => setNow(Date.now()), 1000);
    return () => { active = false; request?.abort(); clearInterval(pollTimer); clearInterval(clockTimer); };
  }, [recording, profileId, session, startedAt]);
  const current = recording && result?.session_id === session.current ? result : null;
  return {
    captureAdvice: captureAdviceAt(current, (now - startedAt.current) / 1000, locale),
    coaching: coachingText(current, locale),
    summary: recording && summary?.sessionId === session.current && summary.profileId === profileId ? summary.value : null,
  };
}
