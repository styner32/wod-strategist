import {
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
} from 'react';
import type {
  AnalysisFeedback,
  FeedbackCorrection,
  FeedbackTargetType,
} from '../../api/history';

export interface FeedbackDialogValue {
  category: string;
  correction: FeedbackCorrection;
  note: string;
  consent_to_improve: boolean;
}

interface Props {
  targetType: FeedbackTargetType;
  chunkLabel?: string;
  movementSuggestions: string[];
  existing?: AnalysisFeedback;
  preset?: FeedbackCorrection;
  pending: boolean;
  error?: string;
  onClose: () => void;
  onSubmit: (value: FeedbackDialogValue) => Promise<void>;
  onUndo?: () => Promise<void>;
}

const ACTIVITY_OPTIONS = [
  { value: 'exercise', label: 'Exercise / movement' },
  { value: 'walking', label: 'Walking' },
  { value: 'rest_setup', label: 'Rest or setup' },
  { value: 'not_exercise', label: 'Not exercise' },
  { value: 'unknown', label: 'Unknown / cannot tell' },
] as const;

const FATIGUE_OPTIONS = [
  { value: '', label: 'No fatigue correction' },
  { value: 'fatigued', label: 'Fatigued' },
  { value: 'not_fatigued', label: 'Not fatigued' },
  { value: 'walking_rest', label: 'Walking / recovery' },
  { value: 'unknown', label: 'Unknown / cannot tell' },
] as const;

function correctionFor(feedback?: AnalysisFeedback, preset?: FeedbackCorrection) {
  if (preset) return preset;
  if (feedback?.correction) return feedback.correction;
  return {
    movement_name: feedback?.corrected_movement ?? undefined,
    activity_state: feedback?.corrected_activity_state ?? undefined,
    fatigue_state: feedback?.corrected_fatigue_state ?? undefined,
  } satisfies FeedbackCorrection;
}

export function FeedbackDialog({
  targetType,
  chunkLabel,
  movementSuggestions,
  existing,
  preset,
  pending,
  error,
  onClose,
  onSubmit,
  onUndo,
}: Props) {
  const initial = correctionFor(existing, preset);
  const initialActivityState = initial.activity_state ?? 'exercise';
  const [activityState, setActivityState] = useState(initialActivityState);
  const [movementName, setMovementName] = useState(initial.movement_name ?? '');
  const [fatigueState, setFatigueState] = useState(
    initialActivityState !== 'exercise' && initial.fatigue_state === 'fatigued'
      ? 'walking_rest'
      : initial.fatigue_state ?? '',
  );
  const [note, setNote] = useState(existing?.note ?? '');
  const [consent, setConsent] = useState(existing?.consent_to_improve ?? false);
  const dialogRef = useRef<HTMLDivElement>(null);
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);
  const titleId = useId();
  const descriptionId = useId();
  const movementListId = useId();

  useEffect(() => {
    previouslyFocusedRef.current = document.activeElement as HTMLElement | null;
    const dialog = dialogRef.current;
    const focusableSelector =
      'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled])';
    const focusable = dialog?.querySelectorAll<HTMLElement>(focusableSelector);
    focusable?.[0]?.focus();

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && dialog?.dataset.pending !== 'true') {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== 'Tab' || !dialog) return;
      const items = Array.from(
        dialog.querySelectorAll<HTMLElement>(focusableSelector),
      );
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    dialog?.addEventListener('keydown', handleKeyDown);
    return () => {
      dialog?.removeEventListener('keydown', handleKeyDown);
      previouslyFocusedRef.current?.focus();
    };
  }, [onClose]);

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    try {
      if (targetType === 'session') {
        await onSubmit({
          category: 'session_accuracy',
          correction: { accurate: false },
          note: note.trim(),
          consent_to_improve: consent,
        });
        return;
      }

      let category = 'other';
      const correction: FeedbackCorrection = {};
      if (activityState !== 'exercise') {
        category = 'activity';
        correction.activity_state = activityState;
        if (fatigueState) correction.fatigue_state = fatigueState;
      } else if (movementName.trim()) {
        category = 'movement';
        correction.activity_state = activityState;
        correction.movement_name = movementName.trim();
        if (fatigueState) correction.fatigue_state = fatigueState;
      } else if (fatigueState) {
        category = 'fatigue';
        correction.activity_state = activityState;
        correction.fatigue_state = fatigueState;
      }

      await onSubmit({
        category,
        correction,
        note: note.trim(),
        consent_to_improve: consent,
      });
    } catch {
      // Mutation state renders the server error without closing the dialog.
    }
  };

  const canSubmit = targetType === 'session'
    ? note.trim().length > 0
    : activityState !== 'exercise'
      || movementName.trim().length > 0
      || fatigueState.length > 0
      || note.trim().length > 0;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !pending) onClose();
      }}
    >
      <div
        ref={dialogRef}
        data-pending={pending ? 'true' : 'false'}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        className="w-full max-w-lg max-h-[90vh] overflow-y-auto rounded-xl border border-border bg-bg-elevated p-5 shadow-xl"
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 id={titleId} className="text-lg font-semibold text-text-primary">
              {preset ? 'Use re-analysis candidate?' : existing ? 'Edit your correction' : 'Report a mistake'}
            </h2>
            <p id={descriptionId} className="mt-1 text-sm text-text-muted">
              {targetType === 'chunk'
                ? preset
                  ? `Review the candidate for ${chunkLabel ?? 'this chunk'} before saving it as your correction.`
                  : `Correct ${chunkLabel ?? 'this chunk'} without changing the original analysis.`
                : 'Tell us what was wrong with this session analysis.'}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={pending}
            aria-label="Close correction dialog"
            className="rounded-md p-1.5 text-text-muted hover:bg-bg-tertiary hover:text-text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
          >
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <form onSubmit={handleSubmit} className="mt-5 space-y-4">
          {targetType === 'chunk' && (
            <>
              <div>
                <label htmlFor={`${titleId}-activity`} className="mb-1.5 block text-sm font-medium text-text-secondary">
                  What was happening?
                </label>
                <select
                  id={`${titleId}-activity`}
                  value={activityState}
                  onChange={(event) => {
                    const nextActivity = event.target.value;
                    setActivityState(nextActivity);
                    if (nextActivity !== 'exercise' && fatigueState === 'fatigued') {
                      setFatigueState('walking_rest');
                    }
                  }}
                  disabled={pending}
                  className="w-full rounded-lg border border-border bg-bg-secondary px-3 py-2.5 text-text-primary focus:outline-none focus:ring-2 focus:ring-accent/60"
                >
                  {ACTIVITY_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </div>

              {activityState === 'exercise' && (
                <div>
                  <label htmlFor={`${titleId}-movement`} className="mb-1.5 block text-sm font-medium text-text-secondary">
                    Correct movement
                  </label>
                  <input
                    id={`${titleId}-movement`}
                    list={movementListId}
                    value={movementName}
                    onChange={(event) => setMovementName(event.target.value)}
                    disabled={pending}
                    maxLength={100}
                    placeholder="Choose a suggestion or enter a custom movement"
                    className="w-full rounded-lg border border-border bg-bg-secondary px-3 py-2.5 text-text-primary placeholder-text-muted focus:outline-none focus:ring-2 focus:ring-accent/60"
                  />
                  <datalist id={movementListId}>
                    {movementSuggestions.map((movement) => (
                      <option key={movement} value={movement} />
                    ))}
                  </datalist>
                </div>
              )}

              <div>
                <label htmlFor={`${titleId}-fatigue`} className="mb-1.5 block text-sm font-medium text-text-secondary">
                  Fatigue correction
                </label>
                <select
                  id={`${titleId}-fatigue`}
                  value={fatigueState}
                  onChange={(event) => setFatigueState(event.target.value)}
                  disabled={pending}
                  className="w-full rounded-lg border border-border bg-bg-secondary px-3 py-2.5 text-text-primary focus:outline-none focus:ring-2 focus:ring-accent/60"
                >
                  {FATIGUE_OPTIONS.map((option) => (
                    <option
                      key={option.value}
                      value={option.value}
                      disabled={option.value === 'fatigued' && activityState !== 'exercise'}
                    >
                      {option.label}
                    </option>
                  ))}
                </select>
              </div>
            </>
          )}

          <div>
            <div className="mb-1.5 flex items-center justify-between gap-3">
              <label htmlFor={`${titleId}-note`} className="text-sm font-medium text-text-secondary">
                {targetType === 'session' ? 'What was wrong?' : 'Note (optional)'}
              </label>
              <span className="text-xs text-text-muted">{note.length}/500</span>
            </div>
            <textarea
              id={`${titleId}-note`}
              value={note}
              onChange={(event) => setNote(event.target.value)}
              disabled={pending}
              required={targetType === 'session'}
              maxLength={500}
              rows={3}
              className="w-full resize-y rounded-lg border border-border bg-bg-secondary px-3 py-2.5 text-text-primary placeholder-text-muted focus:outline-none focus:ring-2 focus:ring-accent/60"
              placeholder="Add details that will help review this result"
            />
          </div>

          <label className="flex items-start gap-2.5 text-sm text-text-secondary">
            <input
              type="checkbox"
              checked={consent}
              onChange={(event) => setConsent(event.target.checked)}
              disabled={pending}
              className="mt-0.5 h-4 w-4 rounded border-border bg-bg-secondary text-accent focus:ring-accent"
            />
            Allow this correction and associated video interval to help improve detection after review.
          </label>

          {error && (
            <p role="alert" className="rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
              {error}
            </p>
          )}

          <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
            <div>
              {existing && onUndo && (
                <button
                  type="button"
                  onClick={() => void onUndo()}
                  disabled={pending}
                  className="rounded-lg px-3 py-2 text-sm font-medium text-error hover:bg-error/10 focus:outline-none focus:ring-2 focus:ring-error disabled:opacity-50"
                >
                  Undo correction
                </button>
              )}
            </div>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={onClose}
                disabled={pending}
                className="rounded-lg border border-border px-4 py-2 text-sm font-medium text-text-secondary hover:bg-bg-tertiary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={pending || !canSubmit}
                className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-accent-hover focus:outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-bg-elevated disabled:cursor-not-allowed disabled:opacity-50"
              >
                {pending ? 'Saving…' : preset ? 'Apply correction' : existing ? 'Save correction' : 'Submit correction'}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
}
