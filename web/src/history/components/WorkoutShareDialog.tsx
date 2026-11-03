import { useCallback, useEffect, useId, useRef, useState } from "react";
import { buildWorkoutShareText, type WorkoutShareContext } from "../../../../shared/workoutShare";
import { historyApi } from "../../api/history";

interface Props {
  sessionId: string;
  profileId: number;
}

interface ContentProps extends Props {
  onClose: () => void;
}

function ShareContent({ sessionId, profileId, onClose }: ContentProps) {
  const [context, setContext] = useState<WorkoutShareContext>();
  const [loadError, setLoadError] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [includeAIObservations, setIncludeAIObservations] = useState(true);
  const [includeSensorSummary, setIncludeSensorSummary] = useState(true);
  const [copying, setCopying] = useState(false);
  const [notice, setNotice] = useState("");
  const dialogRef = useRef<HTMLDivElement>(null);
  const mountedRef = useRef(true);
  const titleId = useId();
  const descriptionId = useId();
  const previewId = useId();

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
    historyApi.getWorkoutShareContext(sessionId, profileId, timezone, controller.signal)
      .then(value => { if (active) setContext(value); })
      .catch(() => { if (active) setLoadError(true); });
    return () => {
      active = false;
      controller.abort();
    };
  }, [sessionId, profileId, attempt]);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const dialog = dialogRef.current;
    const selector = "button:not([disabled]), input:not([disabled]), textarea:not([disabled])";
    dialog?.querySelectorAll<HTMLElement>(selector)[0]?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      } else if (event.key === "Tab" && dialog) {
        const items = Array.from(dialog.querySelectorAll<HTMLElement>(selector));
        const first = items[0];
        const last = items[items.length - 1];
        if (event.shiftKey && document.activeElement === first && last) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last && first) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    dialog?.addEventListener("keydown", onKeyDown);
    return () => {
      dialog?.removeEventListener("keydown", onKeyDown);
      previous?.focus();
    };
  }, [onClose]);

  const text = context
    ? buildWorkoutShareText(context, { includeAIObservations, includeSensorSummary }, "ko")
    : "";

  const copy = async () => {
    if (!text || copying) return;
    setCopying(true);
    setNotice("");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(text);
      if (mountedRef.current) setNotice("복사했습니다. 원하는 AI 대화에 붙여 넣으세요.");
    } catch {
      if (mountedRef.current) setNotice("자동 복사를 사용할 수 없습니다. 아래 공유 내용을 선택해 복사하거나 텍스트 파일로 저장하세요.");
    } finally {
      if (mountedRef.current) setCopying(false);
    }
  };

  const download = () => {
    if (!text) return;
    setNotice("");
    let url: string | undefined;
    let link: HTMLAnchorElement | undefined;
    try {
      url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
      link = document.createElement("a");
      link.href = url;
      link.download = `workout-${sessionId.replace(/[^a-zA-Z0-9_-]/g, "_")}.txt`;
      document.body.appendChild(link);
      link.click();
    } catch {
      setNotice("파일을 저장하지 못했습니다. 공유 내용을 선택해 복사하세요.");
    } finally {
      link?.remove();
      if (url) {
        const downloadUrl = url;
        window.setTimeout(() => URL.revokeObjectURL(downloadUrl), 0);
      }
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4"
      onMouseDown={event => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        className="w-full max-w-3xl max-h-[90dvh] overflow-y-auto rounded-xl border border-border bg-bg-elevated p-5 shadow-xl"
      >
        <div className="flex items-start justify-between gap-4">
          <h2 id={titleId} className="text-lg font-semibold text-text-primary">AI에 물어보기</h2>
          <button type="button" onClick={onClose} className="rounded-md px-3 py-1 text-sm text-text-secondary hover:bg-bg-tertiary focus:ring-2 focus:ring-accent">닫기</button>
        </div>
        <p id={descriptionId} className="mt-2 text-sm text-text-secondary">
          선택한 운동과 과거 비교 자료입니다. 내용을 확인한 뒤 복사하거나 저장해 원하는 AI 대화에 전달하세요.
        </p>
        <p className="mt-2 text-xs text-text-muted">이 자료는 생성 시점의 기록이며, AI 관찰은 사용자가 확정한 기록과 구분됩니다.</p>

        {!context && !loadError && <p role="status" className="py-8 text-sm text-text-secondary">공유 자료를 불러오는 중입니다.</p>}
        {loadError && (
          <div role="alert" className="mt-4 rounded-lg border border-warning/30 p-3 text-sm text-text-secondary">
            <p>공유 자료를 불러오지 못했습니다.</p>
            <button type="button" onClick={() => { setContext(undefined); setLoadError(false); setAttempt(value => value + 1); }} className="mt-2 rounded border border-border px-3 py-1 focus:ring-2 focus:ring-accent">다시 시도</button>
          </div>
        )}
        {context && (
          <>
            <div className="my-4 flex flex-wrap gap-4 text-sm text-text-secondary">
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={includeAIObservations} disabled={copying} onChange={event => { setIncludeAIObservations(event.target.checked); setNotice(""); }} />
                미확정 AI 관찰 포함
              </label>
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={includeSensorSummary} disabled={copying} onChange={event => { setIncludeSensorSummary(event.target.checked); setNotice(""); }} />
                심박·체감 강도 요약 포함
              </label>
            </div>
            <label htmlFor={previewId} className="text-sm font-medium text-text-primary">공유 내용</label>
            <textarea id={previewId} readOnly value={text} rows={16} className="mt-2 w-full rounded-lg border border-border bg-bg-secondary p-3 font-mono text-xs leading-relaxed text-text-primary focus:ring-2 focus:ring-accent" />
            <div className="mt-4 flex flex-wrap gap-2">
              <button type="button" onClick={copy} disabled={copying} className="rounded-lg bg-accent px-4 py-2 text-sm font-semibold text-bg-primary focus:ring-2 focus:ring-accent disabled:opacity-50">{copying ? "복사 중" : "복사"}</button>
              <button type="button" onClick={download} disabled={copying} className="rounded-lg border border-border px-4 py-2 text-sm text-text-primary focus:ring-2 focus:ring-accent disabled:opacity-50">텍스트 파일 저장</button>
            </div>
          </>
        )}
        {notice && <p role="status" className="mt-3 text-sm text-text-secondary">{notice}</p>}
      </div>
    </div>
  );
}

export function WorkoutShareDialog({ sessionId, profileId }: Props) {
  const [open, setOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)} className="rounded-lg border border-border px-3 py-1.5 text-sm text-text-primary hover:bg-bg-tertiary focus:ring-2 focus:ring-accent">AI에 물어보기</button>
      {open && <ShareContent key={`${sessionId}:${profileId}`} sessionId={sessionId} profileId={profileId} onClose={close} />}
    </>
  );
}
