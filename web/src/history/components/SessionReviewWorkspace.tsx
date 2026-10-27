import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";

export interface ReviewPanel {
  id: string;
  label: string;
  content: ReactNode;
}

/** Keep one player and mounted editors while reviewing different evidence. */
export function SessionReviewWorkspace({
  player,
  panels,
  activePanelId,
  onSelectPanel,
}: {
  player: ReactNode;
  panels: ReviewPanel[];
  activePanelId?: string;
  onSelectPanel?: (id: string) => void;
}) {
  const prefix = useId();
  const [internalSelected, setInternalSelected] = useState(panels[0]?.id);
  const [isDocked, setIsDocked] = useState(false);
  const [isMinimized, setIsMinimized] = useState(false);
  const slotRef = useRef<HTMLDivElement>(null);
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  const content = useRef<HTMLDivElement>(null);

  const selected = activePanelId ?? internalSelected;
  const active = panels.some((panel) => panel.id === selected)
    ? selected
    : panels[0]?.id;

  const selectPanel = (id: string) => {
    setInternalSelected(id);
    onSelectPanel?.(id);
  };

  useLayoutEffect(() => {
    if (content.current) content.current.scrollTop = 0;
  }, [active]);

  useEffect(() => {
    const node = slotRef.current;
    if (!node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (!entry) return;
        setIsDocked(!entry.isIntersecting);
      },
      { threshold: 0.05 },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  const navigate = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let next = index;
    if (event.key === "ArrowRight") next = (index + 1) % panels.length;
    else if (event.key === "ArrowLeft")
      next = (index + panels.length - 1) % panels.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = panels.length - 1;
    else return;
    event.preventDefault();
    selectPanel(panels[next].id);
    buttons.current[next]?.focus();
  };

  return (
    <div className="grid min-w-0 grid-cols-1 items-start gap-5 lg:grid-cols-12">
      <div
        ref={slotRef}
        className="min-w-0 lg:sticky lg:top-20 lg:col-span-5"
      >
        {isDocked && (
          <div
            aria-hidden="true"
            className="flex h-48 items-center justify-center rounded-xl border border-dashed border-border bg-bg-secondary/30 p-4 text-xs text-text-muted"
          >
            영상이 우측 하단 미니 플레이어로 고정되어 있습니다.
          </div>
        )}
        <div
          data-testid="review-player"
          data-docked={isDocked ? "true" : "false"}
          className={
            isDocked
              ? `fixed bottom-4 right-4 z-40 w-80 sm:w-96 max-w-[calc(100vw-2rem)] rounded-xl border border-border bg-bg-elevated/95 p-3 shadow-lg backdrop-blur-md space-y-2 ${
                  isMinimized
                    ? "[&_video]:hidden [&_[data-tracking-overlay]]:hidden"
                    : "[&_video]:max-h-44"
                }`
              : "min-w-0 space-y-3"
          }
        >
          {isDocked && (
            <div className="flex items-center justify-between gap-2 border-b border-border pb-1.5">
              <span className="text-xs font-semibold text-accent">
                미니 플레이어
              </span>
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={() => setIsMinimized((prev) => !prev)}
                  className="rounded border border-border px-2 py-0.5 text-[11px] font-medium text-text-secondary hover:bg-bg-tertiary hover:text-text-primary"
                >
                  {isMinimized ? "영상 펼치기" : "최소화"}
                </button>
                <button
                  type="button"
                  onClick={() =>
                    slotRef.current?.scrollIntoView?.({
                      behavior: "smooth",
                      block: "start",
                    })
                  }
                  className="rounded border border-border px-2 py-0.5 text-[11px] font-medium text-text-secondary hover:bg-bg-tertiary hover:text-text-primary"
                >
                  원래 위치로
                </button>
              </div>
            </div>
          )}
          {player}
          {!isDocked && (
            <p className="text-xs leading-relaxed text-text-muted">
              분석의 시간이나 시계열을 선택하면 이 영상의 해당 위치로 이동합니다. (단축키: ←/→ ±5초, Shift+←/→ ±1초, J/L ±10초, K 재생)
            </p>
          )}
        </div>
      </div>

      <section
        aria-label="영상과 분석 함께 보기"
        className="min-w-0 lg:sticky lg:top-20 lg:col-span-7"
      >
        <div
          role="tablist"
          aria-label="확인할 분석"
          className="flex flex-wrap gap-1 rounded-t-xl border border-border bg-bg-secondary p-2"
        >
          {panels.map((panel, index) => (
            <button
              key={panel.id}
              ref={(element) => {
                buttons.current[index] = element;
              }}
              id={`${prefix}-tab-${panel.id}`}
              type="button"
              role="tab"
              aria-selected={active === panel.id}
              aria-controls={`${prefix}-panel-${panel.id}`}
              tabIndex={active === panel.id ? 0 : -1}
              onClick={() => selectPanel(panel.id)}
              onKeyDown={(event) => navigate(event, index)}
              className={`rounded-lg px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-accent ${
                active === panel.id
                  ? "bg-accent/15 text-accent"
                  : "text-text-secondary hover:bg-bg-tertiary hover:text-text-primary"
              }`}
            >
              {panel.label}
            </button>
          ))}
        </div>
        <div
          ref={content}
          className="rounded-b-xl border border-t-0 border-border bg-bg-secondary/30 p-3 lg:max-h-[calc(100dvh-12rem)] lg:overflow-y-auto lg:overscroll-contain lg:p-4"
        >
          {panels.map((panel) => (
            <div
              key={panel.id}
              id={`${prefix}-panel-${panel.id}`}
              role="tabpanel"
              aria-labelledby={`${prefix}-tab-${panel.id}`}
              hidden={active !== panel.id}
              tabIndex={0}
              className="space-y-4 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {panel.content}
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
