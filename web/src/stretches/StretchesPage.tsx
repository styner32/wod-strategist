import { useState, useMemo, useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { historyApi, type AnalysisResult, type StretchRecommendation } from '../api/history';
import {
  stretchesApi,
  buildStretchLookup,
  normalizeStretchKey,
  type Stretch,
} from '../api/stretches';
import { useAuth } from '../auth/useAuth';

interface EnrichedStretchRecommendation extends StretchRecommendation {
  session_id?: string;
  profile_id?: number;
  session_date?: string;
  workout_type?: string;
  resolvedCatalogEntry?: Stretch;
}

const TARGET_CATEGORIES = [
  'All',
  'Hips & Glutes',
  'Legs & Ankles',
  'Chest & Shoulders',
  'Spine & Back',
  'Arms & Neck',
] as const;

function getCategoryColor(category: string): { bg: string; text: string; border: string } {
  const normalized = category.toLowerCase();
  if (normalized.includes('hip') || normalized.includes('glute')) {
    return { bg: 'bg-indigo-500/10', text: 'text-indigo-400', border: 'border-indigo-500/20' };
  }
  if (normalized.includes('leg') || normalized.includes('ankle') || normalized.includes('calf') || normalized.includes('hamstring')) {
    return { bg: 'bg-emerald-500/10', text: 'text-emerald-400', border: 'border-emerald-500/20' };
  }
  if (normalized.includes('chest') || normalized.includes('shoulder') || normalized.includes('pec')) {
    return { bg: 'bg-amber-500/10', text: 'text-amber-400', border: 'border-amber-500/20' };
  }
  if (normalized.includes('spine') || normalized.includes('back') || normalized.includes('thoracic')) {
    return { bg: 'bg-purple-500/10', text: 'text-purple-400', border: 'border-purple-500/20' };
  }
  if (normalized.includes('arm') || normalized.includes('neck') || normalized.includes('wrist')) {
    return { bg: 'bg-sky-500/10', text: 'text-sky-400', border: 'border-sky-500/20' };
  }
  return { bg: 'bg-accent/10', text: 'text-accent', border: 'border-accent/20' };
}

function parseWorkoutSummary(output?: string): string | undefined {
  if (!output) return undefined;
  try {
    const parsed = JSON.parse(output);
    return parsed.workout_type || parsed.overall_summary;
  } catch {
    return undefined;
  }
}

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

function StretchTimerModal({
  stretch,
  resolvedInstruction,
  onClose,
}: {
  stretch: StretchRecommendation;
  resolvedInstruction?: Stretch;
  onClose: () => void;
}) {
  const [secondsLeft, setSecondsLeft] = useState(60);
  const [isRunning, setIsRunning] = useState(false);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    if (isRunning && secondsLeft > 0) {
      timerRef.current = setInterval(() => {
        setSecondsLeft((prev) => prev - 1);
      }, 1000);
    } else if (secondsLeft === 0) {
      setIsRunning(false);
    }
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, [isRunning, secondsLeft]);

  const toggleTimer = () => setIsRunning((prev) => !prev);
  const resetTimer = (secs: number) => {
    setIsRunning(false);
    setSecondsLeft(secs);
  };

  const progressPercent = Math.max(0, Math.min(100, ((60 - secondsLeft) / 60) * 100));

  const descriptionText = resolvedInstruction?.description || stretch.reason;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4 overflow-y-auto">
      <div className="bg-bg-elevated border border-border rounded-2xl p-6 max-w-md w-full shadow-2xl relative animate-in fade-in zoom-in-95 duration-200 my-8">
        <button
          onClick={onClose}
          className="absolute top-4 right-4 text-text-muted hover:text-text-primary p-1 rounded-lg hover:bg-bg-tertiary transition-colors z-10"
          aria-label="Close modal"
        >
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>

        {/* Media Preview inside timer modal */}
        {resolvedInstruction && (resolvedInstruction.image_url || resolvedInstruction.video_url) ? (
          <div className="mb-4 grid gap-2">
            {resolvedInstruction.image_url && (
              <div className="overflow-hidden rounded-xl border border-border bg-black max-h-48 flex items-center justify-center">
                <img
                  src={resolvedInstruction.image_url}
                  alt={stretch.stretch}
                  className="w-full h-48 object-cover rounded-xl"
                />
              </div>
            )}
            {resolvedInstruction.video_url && (
              <div className="overflow-hidden rounded-xl border border-border bg-black max-h-48 flex items-center justify-center">
                <video
                  src={resolvedInstruction.video_url}
                  controls
                  preload="metadata"
                  className="w-full h-48 object-contain rounded-xl"
                />
              </div>
            )}
          </div>
        ) : null}

        <div className="flex items-center gap-2 mb-2">
          <span className="text-xs font-semibold px-2.5 py-0.5 rounded-md bg-accent/10 text-accent border border-accent/20">
            {stretch.target_area}
          </span>
          {stretch.provisional && (
            <span className="text-xs font-semibold px-2 py-0.5 rounded bg-amber-500/20 text-amber-400">
              Provisional
            </span>
          )}
        </div>

        <h3 className="text-xl font-bold text-text-primary mb-1">{stretch.stretch}</h3>
        <p className="text-xs text-text-secondary mb-6 leading-relaxed">{descriptionText}</p>

        {/* Timer display */}
        <div className="bg-bg-secondary border border-border rounded-xl p-6 flex flex-col items-center justify-center mb-6 relative overflow-hidden">
          <div
            className="absolute bottom-0 left-0 top-0 bg-accent/10 transition-all duration-1000 ease-linear"
            style={{ width: `${progressPercent}%` }}
          />
          <div className="relative z-10 text-center">
            <div className="text-4xl font-mono font-bold text-accent mb-1">
              {Math.floor(secondsLeft / 60)}:{String(secondsLeft % 60).padStart(2, '0')}
            </div>
            <div className="text-xs text-text-muted">
              {stretch.duration_hint || 'Recommended 60s hold'}
            </div>
          </div>
        </div>

        {/* Preset time buttons */}
        <div className="flex justify-center gap-2 mb-6">
          {[30, 45, 60, 90, 120].map((secs) => (
            <button
              key={secs}
              onClick={() => resetTimer(secs)}
              className={`px-3 py-1 rounded-lg text-xs font-medium border transition-colors ${
                secondsLeft === secs && !isRunning
                  ? 'bg-accent text-white border-accent'
                  : 'bg-bg-secondary text-text-secondary border-border hover:bg-bg-tertiary'
              }`}
            >
              {secs}s
            </button>
          ))}
        </div>

        {/* Timer controls */}
        <div className="flex gap-3">
          <button
            onClick={toggleTimer}
            className={`flex-1 py-3 rounded-xl text-sm font-semibold text-white transition-all shadow-md ${
              isRunning
                ? 'bg-amber-600 hover:bg-amber-500'
                : 'bg-accent hover:bg-accent-hover'
            }`}
          >
            {isRunning ? 'Pause Timer' : secondsLeft === 0 ? 'Restart Timer' : 'Start Timer'}
          </button>
          <button
            onClick={() => resetTimer(60)}
            className="px-4 py-3 rounded-xl text-sm font-medium bg-bg-secondary border border-border text-text-secondary hover:text-text-primary hover:bg-bg-tertiary transition-colors"
          >
            Reset
          </button>
        </div>

        {stretch.caution && (
          <div className="mt-4 p-3 rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-400 text-xs flex items-start gap-2">
            <span>⚠️</span>
            <span>{stretch.caution}</span>
          </div>
        )}
      </div>
    </div>
  );
}

export function StretchesPage() {
  const { user } = useAuth();
  const profiles = user?.profiles || [];
  const [selectedProfileId, setSelectedProfileId] = useState<number | null>(
    profiles.length > 0 ? profiles[0].id : null,
  );
  const [activeCategory, setActiveCategory] = useState<string>('All');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [activeTimerStretch, setActiveTimerStretch] = useState<EnrichedStretchRecommendation | null>(null);

  // Fetch DB Catalog
  const { data: dbCatalog = [] } = useQuery({
    queryKey: ['stretches'],
    queryFn: stretchesApi.list,
    staleTime: 10 * 60 * 1000,
  });

  const stretchLookup = useMemo(() => buildStretchLookup(dbCatalog), [dbCatalog]);

  const { data, isLoading, error } = useInfiniteQuery({
    queryKey: ['history-stretches', selectedProfileId],
    queryFn: ({ pageParam }) => historyApi.list(selectedProfileId!, pageParam, 50),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (lastPage) => {
      if (!lastPage || lastPage.length < 50) return undefined;
      return lastPage[lastPage.length - 1].id;
    },
    enabled: !!selectedProfileId,
  });

  const allSessions: AnalysisResult[] = useMemo(() => {
    if (!data) return [];
    return data.pages.flatMap((page) => page);
  }, [data]);

  // Extract and de-duplicate stretch recommendations across all sessions, resolving against catalog
  const enrichedStretches = useMemo<EnrichedStretchRecommendation[]>(() => {
    const list: EnrichedStretchRecommendation[] = [];
    const seen = new Set<string>();

    for (const session of allSessions) {
      if (!session.stretch_recommendations) continue;
      try {
        const parsed = typeof session.stretch_recommendations === 'string'
          ? JSON.parse(session.stretch_recommendations)
          : session.stretch_recommendations;

        if (Array.isArray(parsed)) {
          for (const item of parsed as StretchRecommendation[]) {
            const key = `${item.stretch.toLowerCase()}-${session.session_id}`;
            if (!seen.has(key)) {
              seen.add(key);
              const resolved = stretchLookup.get(normalizeStretchKey(item.stretch));
              list.push({
                ...item,
                session_id: session.session_id,
                profile_id: session.profile_id,
                session_date: formatDate(session.created_at),
                workout_type: parseWorkoutSummary(session.output),
                resolvedCatalogEntry: resolved,
              });
            }
          }
        }
      } catch {
        // Skip invalid JSON
      }
    }

    return list;
  }, [allSessions, stretchLookup]);

  // Map DB Catalog as standard items when no AI recommendations exist yet
  const catalogAsFallbackStretches = useMemo<EnrichedStretchRecommendation[]>(() => {
    return dbCatalog.map((s) => ({
      stretch: s.name,
      target_area: s.target_area || 'General',
      reason: s.description || 'Standard catalog recovery stretch.',
      duration_hint: s.duration_hint,
      caution: s.caution,
      resolvedCatalogEntry: s,
    }));
  }, [dbCatalog]);

  const hasAIStretches = enrichedStretches.length > 0;
  const baseStretches = hasAIStretches ? enrichedStretches : catalogAsFallbackStretches;

  // Filtered stretches based on category and search query
  const filteredStretches = useMemo(() => {
    let result = baseStretches;

    if (activeCategory !== 'All') {
      const catLower = activeCategory.toLowerCase();
      result = result.filter((item) => {
        const target = item.target_area.toLowerCase();
        if (catLower.includes('hip') || catLower.includes('glute')) {
          return target.includes('hip') || target.includes('glute') || target.includes('pelvis');
        }
        if (catLower.includes('leg') || catLower.includes('ankle')) {
          return target.includes('leg') || target.includes('ankle') || target.includes('calf') || target.includes('hamstring') || target.includes('knee');
        }
        if (catLower.includes('chest') || catLower.includes('shoulder')) {
          return target.includes('chest') || target.includes('shoulder') || target.includes('pec') || target.includes('deltoid');
        }
        if (catLower.includes('spine') || catLower.includes('back')) {
          return target.includes('spine') || target.includes('back') || target.includes('thoracic') || target.includes('lumbar');
        }
        if (catLower.includes('arm') || catLower.includes('neck')) {
          return target.includes('arm') || target.includes('neck') || target.includes('wrist') || target.includes('forearm');
        }
        return target.includes(catLower);
      });
    }

    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase();
      result = result.filter(
        (item) =>
          item.stretch.toLowerCase().includes(q) ||
          item.target_area.toLowerCase().includes(q) ||
          item.reason.toLowerCase().includes(q) ||
          (item.caution && item.caution.toLowerCase().includes(q)) ||
          (item.resolvedCatalogEntry?.description &&
            item.resolvedCatalogEntry.description.toLowerCase().includes(q)),
      );
    }

    return result;
  }, [baseStretches, activeCategory, searchQuery]);

  return (
    <div className="max-w-6xl mx-auto space-y-8">
      {/* Header Banner */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-gradient-to-r from-bg-elevated via-bg-secondary to-bg-elevated p-6 md:p-8 shadow-xl">
        <div className="absolute -right-10 -top-10 w-60 h-60 bg-accent/10 rounded-full blur-3xl pointer-events-none" />
        <div className="relative z-10 flex flex-col md:flex-row md:items-center justify-between gap-6">
          <div>
            <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-accent/10 border border-accent/20 text-accent text-xs font-semibold mb-3">
              <span>🧘</span> AI Mobility & Recovery
            </div>
            <h1 className="text-3xl font-extrabold text-text-primary tracking-tight">
              Recommended Stretches
            </h1>
            <p className="text-sm text-text-secondary mt-1.5 max-w-2xl leading-relaxed">
              Targeted stretch routines curated by AI from your movement and mobility analysis.
              Prevent injury, reduce soreness, and enhance overall joint range of motion.
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <Link
              to="/stretches/manage"
              className="px-3.5 py-2 bg-bg-elevated hover:bg-bg-tertiary border border-border text-text-primary text-xs font-semibold rounded-xl transition-all shadow-sm flex items-center gap-1.5"
            >
              <span>⚙️</span>
              <span>Manage Catalog</span>
            </Link>

            {profiles.length > 1 && (
              <div className="flex items-center gap-2">
                <label htmlFor="profile-select" className="text-xs text-text-muted">Profile:</label>
                <select
                  id="profile-select"
                  value={selectedProfileId ?? ''}
                  onChange={(e) => setSelectedProfileId(Number(e.target.value))}
                  className="bg-bg-secondary border border-border rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-2 focus:ring-accent/50"
                >
                  {profiles.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Filter Bar & Search */}
      <div className="flex flex-col md:flex-row items-stretch md:items-center justify-between gap-4">
        {/* Category Pills */}
        <div className="flex items-center gap-1.5 overflow-x-auto pb-2 md:pb-0 scrollbar-none">
          {TARGET_CATEGORIES.map((cat) => {
            const isActive = activeCategory === cat;
            return (
              <button
                key={cat}
                onClick={() => setActiveCategory(cat)}
                className={`px-3.5 py-2 rounded-xl text-xs font-semibold whitespace-nowrap transition-all duration-200 ${
                  isActive
                    ? 'bg-accent text-white shadow-md shadow-accent/20'
                    : 'bg-bg-elevated text-text-secondary hover:text-text-primary hover:bg-bg-tertiary border border-border'
                }`}
              >
                {cat}
              </button>
            );
          })}
        </div>

        {/* Search Input */}
        <div className="relative min-w-[240px]">
          <input
            type="text"
            placeholder="Search stretches or joints..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full bg-bg-elevated border border-border rounded-xl px-4 py-2.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary text-xs"
            >
              ✕
            </button>
          )}
        </div>
      </div>

      {/* Notice Banner for Catalog Fallback */}
      {!hasAIStretches && !isLoading && (
        <div className="rounded-xl border border-indigo-500/20 bg-indigo-500/10 p-4 flex items-start gap-3">
          <span className="text-xl">💡</span>
          <div>
            <h4 className="text-sm font-semibold text-indigo-300">Essential Mobility Catalog</h4>
            <p className="text-xs text-indigo-200/80 mt-0.5 leading-relaxed">
              Showing standard mobility stretches from the master catalog. Upload workout videos to get personalized AI recommendations tailored to your joint movement needs!
            </p>
          </div>
        </div>
      )}

      {/* Loading Skeleton */}
      {isLoading && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3, 4, 5, 6].map((i) => (
            <div key={i} className="bg-bg-elevated border border-border rounded-xl p-5 animate-pulse space-y-3">
              <div className="h-4 bg-bg-tertiary rounded w-1/3" />
              <div className="h-5 bg-bg-tertiary rounded w-2/3" />
              <div className="h-12 bg-bg-tertiary rounded w-full" />
              <div className="h-4 bg-bg-tertiary rounded w-1/2" />
            </div>
          ))}
        </div>
      )}

      {/* Error state */}
      {error && !isLoading && (
        <div className="bg-error/10 border border-error/30 text-error rounded-xl p-4 text-sm">
          Failed to load workout history for stretch recommendations.
        </div>
      )}

      {/* Empty Results State */}
      {filteredStretches.length === 0 && !isLoading && (
        <div className="text-center py-16 bg-bg-elevated border border-border rounded-2xl p-8">
          <div className="w-12 h-12 rounded-full bg-bg-tertiary flex items-center justify-center mx-auto mb-3 text-2xl">
            🔍
          </div>
          <h3 className="text-base font-semibold text-text-primary">No matching stretches found</h3>
          <p className="text-xs text-text-secondary mt-1">
            Try adjusting your search query or selecting a different body category.
          </p>
          <button
            onClick={() => {
              setActiveCategory('All');
              setSearchQuery('');
            }}
            className="mt-4 px-4 py-2 bg-accent/10 text-accent border border-accent/20 rounded-xl text-xs font-semibold hover:bg-accent/20 transition-colors"
          >
            Reset Filters
          </button>
        </div>
      )}

      {/* Stretches Grid */}
      {filteredStretches.length > 0 && (
        <div className="grid gap-5 md:grid-cols-2 lg:grid-cols-3">
          {filteredStretches.map((rec, idx) => {
            const colors = getCategoryColor(rec.target_area);
            const resolved = rec.resolvedCatalogEntry;

            return (
              <div
                key={`${rec.stretch}-${idx}`}
                className="bg-bg-elevated border border-border rounded-2xl p-5 flex flex-col justify-between hover:border-accent/40 transition-all duration-200 group shadow-sm hover:shadow-md"
              >
                <div>
                  {/* Media Thumbnail & Video */}
                  {resolved && (resolved.image_url || resolved.video_url) ? (
                    <div className="mb-4 grid gap-2">
                      {resolved.image_url && (
                        <div className="overflow-hidden rounded-xl border border-border bg-black max-h-48 flex items-center justify-center">
                          <img
                            src={resolved.image_url}
                            alt={rec.stretch}
                            className="w-full h-48 object-cover rounded-xl"
                          />
                        </div>
                      )}
                      {resolved.video_url && (
                        <div className="overflow-hidden rounded-xl border border-border bg-black max-h-48 flex items-center justify-center">
                          <video
                            src={resolved.video_url}
                            controls
                            preload="metadata"
                            className="w-full h-48 object-contain rounded-xl"
                          />
                        </div>
                      )}
                    </div>
                  ) : null}

                  {/* Card Header */}
                  <div className="flex items-center justify-between gap-2 mb-3">
                    <span className={`text-xs font-semibold px-2.5 py-1 rounded-lg border ${colors.bg} ${colors.text} ${colors.border}`}>
                      {rec.target_area}
                    </span>
                    {rec.provisional && (
                      <span className="text-[10px] font-semibold px-2 py-0.5 rounded bg-amber-500/20 text-amber-400">
                        Provisional
                      </span>
                    )}
                  </div>

                  {/* Title & Duration */}
                  <h3 className="text-lg font-bold text-text-primary group-hover:text-accent transition-colors leading-snug">
                    {rec.stretch}
                  </h3>

                  {(rec.duration_hint || resolved?.duration_hint) && (
                    <div className="mt-1.5 inline-flex items-center gap-1.5 text-xs text-text-secondary font-medium">
                      <span>⏱️</span>
                      <span>{rec.duration_hint || resolved?.duration_hint}</span>
                    </div>
                  )}

                  {/* Rationale / Description */}
                  <div className="mt-3 p-3 rounded-xl bg-bg-secondary border border-border-subtle space-y-1">
                    <p className="text-xs text-text-secondary leading-relaxed">
                      {rec.reason}
                    </p>
                    {resolved?.description && resolved.description !== rec.reason && (
                      <p className="text-[11px] text-text-muted leading-relaxed border-t border-border-subtle pt-1 mt-1">
                        {resolved.description}
                      </p>
                    )}
                  </div>

                  {/* Caution Warning */}
                  {(rec.caution || resolved?.caution) && (
                    <div className="mt-2.5 p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-400 text-[11px] leading-snug flex items-start gap-1.5">
                      <span className="shrink-0">⚠️</span>
                      <span>{rec.caution || resolved?.caution}</span>
                    </div>
                  )}
                </div>

                {/* Footer Actions */}
                <div className="mt-5 pt-3 border-t border-border-subtle flex items-center justify-between text-xs">
                  {rec.session_id ? (
                    <Link
                      to={`/sessions/${rec.session_id}?profile_id=${rec.profile_id ?? selectedProfileId}`}
                      className="text-text-muted hover:text-accent transition-colors flex items-center gap-1 font-medium text-[11px]"
                    >
                      <span>🔗</span>
                      <span>From {rec.session_date}</span>
                    </Link>
                  ) : (
                    <span className="text-[11px] text-text-muted">Standard Catalog</span>
                  )}

                  <button
                    onClick={() => setActiveTimerStretch(rec)}
                    className="px-3 py-1.5 rounded-lg bg-accent/10 hover:bg-accent/20 text-accent font-semibold border border-accent/20 transition-all flex items-center gap-1"
                  >
                    <span>⏱️</span>
                    <span>Start Timer</span>
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Timer Modal */}
      {activeTimerStretch && (
        <StretchTimerModal
          stretch={activeTimerStretch}
          resolvedInstruction={activeTimerStretch.resolvedCatalogEntry}
          onClose={() => setActiveTimerStretch(null)}
        />
      )}
    </div>
  );
}
