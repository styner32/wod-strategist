import type { HighlightObservationType, HighlightSegment } from '../highlights';

const HIGHLIGHT_TYPE_LABELS: Record<string, { icon: string; label: string }> = {
  best_form: { icon: '🏆', label: 'Best form' },
  worst_form: { icon: '⚠️', label: 'Needs work' },
  fatigue_point: { icon: '🫁', label: 'Fatigue point' },
  mixed_form: { icon: '⚖️', label: 'Mixed form' },
  key_moment: { icon: '⭐', label: 'Key moment' },
};

const HIGHLIGHT_OBSERVATION_LABELS: Record<HighlightObservationType, string> = {
  positive_form: 'Positive form',
  form_issue: 'Form issue',
  fatigue_onset: 'Fatigue onset',
  technique_event: 'Technique event',
};

interface HighlightEventCardProps {
  highlight: HighlightSegment;
  isActive: boolean;
  disabled: boolean;
  onSelect: () => void;
}

export function HighlightEventCard({
  highlight,
  isActive,
  disabled,
  onSelect,
}: HighlightEventCardProps) {
  const typeConfig = HIGHLIGHT_TYPE_LABELS[highlight.type] ?? {
    icon: '🎯',
    label: highlight.type.replaceAll('_', ' '),
  };
  const label = highlight.movement || typeConfig.label;

  return (
    <button
      type="button"
      onClick={onSelect}
      disabled={disabled}
      aria-label={`Play ${label} highlight from ${highlight.startLabel} to ${highlight.endLabel}`}
      className={`w-full rounded-lg border p-3 text-left transition-colors focus:outline-none focus:ring-2 focus:ring-accent disabled:cursor-not-allowed disabled:opacity-50 ${
        isActive
          ? 'border-accent bg-accent/10'
          : 'border-border bg-bg-secondary hover:border-accent/40 hover:bg-bg-tertiary'
      }`}
    >
      <span className="flex items-start justify-between gap-3">
        <span className="text-sm font-medium text-text-primary">
          <span aria-hidden="true" className="mr-1.5">{typeConfig.icon}</span>
          {label}
        </span>
        <span className="shrink-0 font-mono text-xs text-accent">
          {highlight.startLabel}–{highlight.endLabel}
        </span>
      </span>
      {highlight.movement && (
        <span className="mt-1 block text-xs capitalize text-text-muted">
          {typeConfig.label}
        </span>
      )}
      {highlight.type !== 'key_moment' && highlight.tags?.includes('key_moment') && (
        <span
          data-highlight-tag="key_moment"
          className="mt-2 inline-flex rounded-full bg-info/10 px-2 py-0.5 text-[11px] font-medium text-info"
        >
          ⭐ Key moment
        </span>
      )}
      {highlight.reason && (
        <span className="mt-2 block text-xs leading-relaxed text-text-secondary">
          {highlight.reason}
        </span>
      )}
      {highlight.observations && highlight.observations.length > 0 && (
        <span className="mt-2 block space-y-1.5 border-t border-border/70 pt-2">
          {highlight.observations.map((observation, observationIndex) => (
            <span
              key={`${observation.startSeconds}-${observation.endSeconds}-${observation.type}-${observationIndex}`}
              className="block rounded-md bg-bg-elevated/70 px-2 py-1.5"
            >
              <span className="flex items-baseline justify-between gap-2">
                <span className="text-[11px] font-medium text-text-primary">
                  {HIGHLIGHT_OBSERVATION_LABELS[observation.type]}
                  {observation.confidence != null
                    ? ` · ${Math.round(observation.confidence * 100)}% confidence`
                    : ''}
                </span>
                <span className="shrink-0 font-mono text-[10px] text-text-muted">
                  {observation.startLabel}–{observation.endLabel}
                </span>
              </span>
              {observation.reason && (
                <span className="mt-0.5 block text-[11px] leading-relaxed text-text-secondary">
                  {observation.reason}
                </span>
              )}
            </span>
          ))}
        </span>
      )}
      {isActive && (
        <span className="mt-2 block text-xs font-medium text-accent">Playing selected range</span>
      )}
    </button>
  );
}
