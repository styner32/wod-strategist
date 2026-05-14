import { useParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState } from 'react';
import { historyApi, type VideoKind } from '../api/history';
import { useAuth } from '../auth/useAuth';
import { ChunkMetricsChart } from './components/ChunkMetricsChart';
import { GuidanceTimeline } from './components/GuidanceTimeline';

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

const VIDEO_KIND_LABELS: Record<VideoKind, { label: string; icon: string; desc: string }> = {
  merged: { label: 'Original', icon: '📹', desc: 'Raw workout video' },
  hardsubbed: { label: 'Guided', icon: '📝', desc: 'With AI coaching overlay' },
  encoded: { label: 'Encoded', icon: '🎬', desc: 'Compressed version' },
};

export function SessionDetailPage() {
  const { sessionId } = useParams<{ sessionId: string }>();
  const { user } = useAuth();
  const profileId = user?.profiles?.[0]?.id;

  const videoRef = useRef<HTMLVideoElement>(null);
  const [currentTime, setCurrentTime] = useState(0);
  const [selectedKind, setSelectedKind] = useState<VideoKind>('merged');

  const { data: analyses, isLoading: analysisLoading } = useQuery({
    queryKey: ['analysis', sessionId],
    queryFn: () => historyApi.getAnalysis(sessionId!),
    enabled: !!sessionId,
  });

  const { data: chunks } = useQuery({
    queryKey: ['chunks', sessionId],
    queryFn: () => historyApi.getChunkAnalysis(sessionId!),
    enabled: !!sessionId,
  });

  // Fetch available videos from history to know which kinds exist
  const { data: historyList } = useQuery({
    queryKey: ['history', profileId],
    queryFn: () => historyApi.list(profileId!),
    enabled: !!profileId,
  });

  const historyItem = historyList?.find((h) => h.session_id === sessionId);
  const availableVideos = (historyItem?.available_videos ?? []) as VideoKind[];

  // Auto-select best available video kind
  useEffect(() => {
    if (availableVideos.length > 0) {
      // Prefer hardsubbed > merged > encoded
      if (availableVideos.includes('hardsubbed')) {
        setSelectedKind('hardsubbed');
      } else if (availableVideos.includes('merged')) {
        setSelectedKind('merged');
      } else if (availableVideos.includes('encoded')) {
        setSelectedKind('encoded');
      }
    }
  }, [availableVideos.join(',')]);

  const { data: videoUrl } = useQuery({
    queryKey: ['video-url', sessionId, selectedKind],
    queryFn: () =>
      historyApi.getVideoDownloadUrl(sessionId!, profileId!, selectedKind),
    enabled: !!sessionId && !!profileId && availableVideos.includes(selectedKind),
    staleTime: 10 * 60 * 1000, // 10 min (signed URLs expire)
  });

  const analysis = analyses?.[0];
  const parsedOutput = (() => {
    try {
      return analysis?.output ? JSON.parse(analysis.output) : null;
    } catch {
      return null;
    }
  })();

  // Track video playback position
  const handleTimeUpdate = useCallback(() => {
    if (videoRef.current) {
      setCurrentTime(videoRef.current.currentTime);
    }
  }, []);

  // Seek video to a specific time
  const handleSeek = useCallback((time: number) => {
    if (videoRef.current) {
      videoRef.current.currentTime = time;
      videoRef.current.play().catch(() => {});
    }
  }, []);

  if (analysisLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="w-8 h-8 border-2 border-accent border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  return (
    <div className="max-w-7xl mx-auto">
      <div className="flex items-center gap-3 mb-6">
        <Link
          to="/"
          className="text-text-muted hover:text-text-primary transition-colors"
        >
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5 8.25 12l7.5-7.5" />
          </svg>
        </Link>
        <h1 className="text-xl font-bold text-text-primary">Session Detail</h1>
      </div>

      {/* Side-by-side layout: Video + Guidance */}
      <div className="grid grid-cols-1 lg:grid-cols-5 gap-6">
        {/* Left: Video player (3/5 width) */}
        <div className="lg:col-span-3 space-y-4">
          <div className="bg-bg-elevated border border-border rounded-xl overflow-hidden">
            {videoUrl?.download_url ? (
              <video
                ref={videoRef}
                src={videoUrl.download_url}
                controls
                onTimeUpdate={handleTimeUpdate}
                className="w-full aspect-video bg-black"
              />
            ) : (
              <div className="w-full aspect-video bg-bg-secondary flex items-center justify-center">
                <div className="text-center">
                  <svg className="w-12 h-12 text-text-muted mx-auto mb-2" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z" />
                  </svg>
                  <p className="text-text-muted text-sm">
                    {availableVideos.length === 0
                      ? 'Video not available'
                      : 'Loading video...'}
                  </p>
                </div>
              </div>
            )}
          </div>

          {/* Video kind selector */}
          {availableVideos.length > 1 && (
            <div className="flex gap-2">
              {availableVideos.map((kind) => {
                const cfg = VIDEO_KIND_LABELS[kind];
                const isSelected = kind === selectedKind;
                return (
                  <button
                    key={kind}
                    onClick={() => setSelectedKind(kind)}
                    className={`
                      flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm font-medium
                      transition-all duration-200 border
                      ${
                        isSelected
                          ? 'bg-accent/15 border-accent/40 text-accent'
                          : 'bg-bg-secondary border-transparent text-text-secondary hover:text-text-primary hover:bg-bg-tertiary'
                      }
                    `}
                    title={cfg.desc}
                  >
                    <span>{cfg.icon}</span>
                    {cfg.label}
                  </button>
                );
              })}
            </div>
          )}

          {/* Session metadata */}
          {analysis && (
            <div className="bg-bg-elevated border border-border rounded-xl p-4">
              <div className="grid grid-cols-2 gap-4 text-sm">
                <div>
                  <p className="text-text-muted">Session ID</p>
                  <p className="text-text-primary font-mono text-xs mt-0.5 break-all">{analysis.session_id}</p>
                </div>
                <div>
                  <p className="text-text-muted">Status</p>
                  <p className="text-text-primary mt-0.5 capitalize">{analysis.status}</p>
                </div>
                <div>
                  <p className="text-text-muted">Created</p>
                  <p className="text-text-primary mt-0.5">{formatDate(analysis.created_at)}</p>
                </div>
                <div>
                  <p className="text-text-muted">Workout Type</p>
                  <p className="text-text-primary mt-0.5 capitalize">{analysis.workout_type || '—'}</p>
                </div>
              </div>
            </div>
          )}

          {/* AI Analysis */}
          <div className="bg-bg-elevated border border-border rounded-xl p-5">
            <h2 className="text-lg font-semibold text-text-primary mb-4">AI Analysis</h2>
            {parsedOutput ? (
              <div className="space-y-4">
                {parsedOutput.overall_summary && (
                  <div>
                    <h3 className="text-sm font-medium text-text-secondary mb-1">Summary</h3>
                    <p className="text-text-primary text-sm leading-relaxed whitespace-pre-wrap">
                      {parsedOutput.overall_summary}
                    </p>
                  </div>
                )}
                {parsedOutput.coaching_feedback && (
                  <div>
                    <h3 className="text-sm font-medium text-text-secondary mb-1">Coaching Feedback</h3>
                    <p className="text-text-primary text-sm leading-relaxed whitespace-pre-wrap">
                      {parsedOutput.coaching_feedback}
                    </p>
                  </div>
                )}
                {parsedOutput.key_observations && (
                  <div>
                    <h3 className="text-sm font-medium text-text-secondary mb-1">Key Observations</h3>
                    <ul className="text-text-primary text-sm space-y-1">
                      {(Array.isArray(parsedOutput.key_observations) ? parsedOutput.key_observations : []).map(
                        (obs: string, i: number) => (
                          <li key={i} className="flex gap-2">
                            <span className="text-accent mt-1">•</span>
                            <span>{obs}</span>
                          </li>
                        ),
                      )}
                    </ul>
                  </div>
                )}
              </div>
            ) : analysis?.status === 'completed' ? (
              <p className="text-text-muted text-sm">Analysis output not available.</p>
            ) : analysis?.status === 'processing' ? (
              <div className="flex items-center gap-3 text-text-secondary">
                <div className="w-5 h-5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
                <span className="text-sm">Analysis in progress...</span>
              </div>
            ) : (
              <p className="text-text-muted text-sm">No analysis data.</p>
            )}
          </div>
        </div>

        {/* Right: Guidance Timeline (2/5 width) */}
        <div className="lg:col-span-2">
          {chunks && chunks.length > 0 ? (
            <div className="lg:sticky lg:top-4">
              <GuidanceTimeline
                chunks={chunks}
                currentTime={currentTime}
                onSeek={handleSeek}
              />
            </div>
          ) : (
            <div className="bg-bg-elevated border border-border rounded-xl p-5">
              <h2 className="text-lg font-semibold text-text-primary mb-2">
                Guidance Timeline
              </h2>
              <p className="text-sm text-text-muted">
                No real-time coaching data available.
              </p>
            </div>
          )}
        </div>
      </div>

      {/* Chunk metrics chart — full width below */}
      {chunks && chunks.length > 0 && (
        <div className="mt-6">
          <ChunkMetricsChart
            chunks={chunks}
            currentTime={currentTime}
            onSeek={handleSeek}
          />
        </div>
      )}
    </div>
  );
}
