import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { t } from "@/features/i18n";
import {
  ActivityIndicator,
  Animated,
  Dimensions,
  FlatList,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { useVideoPlayer, VideoView } from "expo-video";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import type { ChunkAnalysisResult } from "../api";

// ─────────────────────────────────────────
// Types
// ─────────────────────────────────────────

export interface HighlightSegment {
  start: string; // "M:SS" or "MM:SS"
  end: string;
  type: string; // best_form, worst_form, fatigue_point, key_moment
  movement?: string;
  reason: string;
}

export interface GuidanceCue {
  startSecs: number;
  endSecs: number;
  text: string;
  exerciseType?: string;
}

interface Props {
  videoUrl: string;
  sessionLabel: string;
  chunks: ChunkAnalysisResult[];
  highlightSegments: HighlightSegment[];
  onClose: () => void;
}

// ─────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────

function parseTimestamp(ts: string): number {
  const parts = ts.split(":");
  if (parts.length === 2) {
    return parseInt(parts[0], 10) * 60 + parseInt(parts[1], 10);
  }
  return parseInt(ts, 10) || 0;
}

function formatTime(secs: number): string {
  const m = Math.floor(secs / 60);
  const s = Math.floor(secs % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

function buildCues(chunks: ChunkAnalysisResult[]): GuidanceCue[] {
  return chunks
    .filter((c) => c.status === "COMPLETED" && c.start_secs != null && c.end_secs != null && c.output)
    .sort((a, b) => (a.start_secs ?? 0) - (b.start_secs ?? 0))
    .map((c) => ({
      startSecs: c.start_secs!,
      endSecs: c.end_secs!,
      text: c.output,
      exerciseType: c.exercise_type,
    }));
}

/** Find the active cue for a given playback position */
function findActiveCue(cues: GuidanceCue[], timeSecs: number): GuidanceCue | null {
  for (const cue of cues) {
    if (timeSecs >= cue.startSecs && timeSecs < cue.endSecs) {
      return cue;
    }
  }
  return null;
}

const HIGHLIGHT_TYPE_CONFIG: Record<string, { emoji: string; color: string; labelKey: string }> = {
  best_form: { emoji: "🏆", color: "#30D158", labelKey: "player.bestForm" },
  worst_form: { emoji: "⚠️", color: "#FF9F0A", labelKey: "player.needsWork" },
  fatigue_point: { emoji: "🫁", color: "#FF453A", labelKey: "player.fatigue" },
  key_moment: { emoji: "⭐", color: "#64D2FF", labelKey: "player.keyMoment" },
};

const PREROLL_SECS = 5;
const { width: SCREEN_WIDTH, height: SCREEN_HEIGHT } = Dimensions.get("window");
// Portrait video: use 9:16 aspect ratio but cap at ~50% screen height
// so guidance panel is always visible below
const VIDEO_HEIGHT = Math.min(SCREEN_WIDTH * (16 / 9), SCREEN_HEIGHT * 0.5);

// ─────────────────────────────────────────
// Component
// ─────────────────────────────────────────

export function WorkoutVideoPlayer({
  videoUrl,
  sessionLabel,
  chunks,
  highlightSegments,
  onClose,
}: Props) {
  const insets = useSafeAreaInsets();
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(0);
  const [activeHighlight, setActiveHighlight] = useState<HighlightSegment | null>(null);
  const highlightFade = useRef(new Animated.Value(0)).current;
  const highlightTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Build guidance cues from chunk analysis
  const cues = useMemo(() => buildCues(chunks), [chunks]);
  const activeCue = useMemo(() => findActiveCue(cues, currentTime), [cues, currentTime]);

  // Video player
  const player = useVideoPlayer(videoUrl, (p) => {
    p.loop = false;
    p.timeUpdateEventInterval = 0.25; // 4 updates per second
  });

  // Track playback position
  useEffect(() => {
    const sub = player.addListener("timeUpdate", (ev) => {
      setCurrentTime(ev.currentTime);
    });
    return () => sub.remove();
  }, [player]);

  // Track duration
  useEffect(() => {
    const sub = player.addListener("sourceLoad", (ev) => {
      setDuration(ev.duration);
    });
    return () => sub.remove();
  }, [player]);

  // Highlight overlay dismiss timer
  const showHighlightOverlay = useCallback(
    (hl: HighlightSegment) => {
      if (highlightTimer.current) clearTimeout(highlightTimer.current);
      setActiveHighlight(hl);
      Animated.timing(highlightFade, {
        toValue: 1,
        duration: 300,
        useNativeDriver: true,
      }).start();

      highlightTimer.current = setTimeout(() => {
        Animated.timing(highlightFade, {
          toValue: 0,
          duration: 500,
          useNativeDriver: true,
        }).start(() => setActiveHighlight(null));
      }, 4000);
    },
    [highlightFade]
  );

  // Jump to highlight
  const handleHighlightPress = useCallback(
    (hl: HighlightSegment) => {
      const targetSecs = Math.max(0, parseTimestamp(hl.start) - PREROLL_SECS);
      player.currentTime = targetSecs;
      player.play();
      showHighlightOverlay(hl);
    },
    [player, showHighlightOverlay]
  );

  // Cleanup
  useEffect(() => {
    return () => {
      if (highlightTimer.current) clearTimeout(highlightTimer.current);
    };
  }, []);

  const renderHighlightChip = useCallback(
    ({ item: hl }: { item: HighlightSegment }) => {
      const rawCfg = HIGHLIGHT_TYPE_CONFIG[hl.type] ?? { emoji: "🎯", color: "#A0A0A0", labelKey: hl.type };
      const cfg = { ...rawCfg, label: t(rawCfg.labelKey) };
      const isActive =
        currentTime >= parseTimestamp(hl.start) - 1 &&
        currentTime <= parseTimestamp(hl.end) + 1;

      return (
        <TouchableOpacity
          style={[
            styles.highlightChip,
            { borderColor: cfg.color + "60" },
            isActive && { borderColor: cfg.color, backgroundColor: cfg.color + "25" },
          ]}
          onPress={() => handleHighlightPress(hl)}
          activeOpacity={0.7}
        >
          <Text style={styles.chipEmoji}>{cfg.emoji}</Text>
          <View style={styles.chipTextCol}>
            <Text style={[styles.chipLabel, { color: cfg.color }]} numberOfLines={1}>
              {hl.movement ? `${hl.movement}` : cfg.label}
            </Text>
            <Text style={styles.chipTime}>
              {hl.start} – {hl.end}
            </Text>
          </View>
        </TouchableOpacity>
      );
    },
    [currentTime, handleHighlightPress]
  );

  return (
    <View style={[styles.root, { paddingTop: insets.top }]}>
      {/* ── Header ── */}
      <View style={styles.header}>
        <TouchableOpacity onPress={onClose} hitSlop={12} style={styles.backBtn}>
          <Text style={styles.backText}>{t("player.backBtn")}</Text>
        </TouchableOpacity>
        <Text style={styles.headerTitle} numberOfLines={1}>{sessionLabel}</Text>
        <View style={styles.headerSpacer} />
      </View>

      {/* ── Video ── */}
      <VideoView
        style={styles.video}
        player={player}
        contentFit="contain"
        nativeControls
      />

      {/* ── Highlight Overlay Card ── */}
      {activeHighlight && (
        <Animated.View style={[styles.highlightOverlay, { opacity: highlightFade }]}>
          <View style={styles.highlightOverlayInner}>
            <Text style={styles.highlightOverlayEmoji}>
              {(HIGHLIGHT_TYPE_CONFIG[activeHighlight.type]?.emoji) ?? "🎯"}
            </Text>
            <View style={styles.highlightOverlayTextCol}>
              <Text style={styles.highlightOverlayLabel}>
                {HIGHLIGHT_TYPE_CONFIG[activeHighlight.type] ? t(HIGHLIGHT_TYPE_CONFIG[activeHighlight.type].labelKey) : activeHighlight.type}
                {activeHighlight.movement ? ` · ${activeHighlight.movement}` : ""}
              </Text>
              <Text style={styles.highlightOverlayReason} numberOfLines={2}>
                {activeHighlight.reason}
              </Text>
            </View>
          </View>
        </Animated.View>
      )}

      <ScrollView
        style={styles.bottomPanel}
        contentContainerStyle={[styles.bottomContent, { paddingBottom: insets.bottom + 20 }]}
        showsVerticalScrollIndicator={false}
      >
        {/* ── Guidance Strip ── */}
        <View style={styles.guidanceSection}>
          <Text style={styles.sectionTitle}>{t("player.coachingGuidance")}</Text>
          {activeCue ? (
            <View style={styles.guidanceBubble}>
              {activeCue.exerciseType && (
                <View style={styles.exerciseBadge}>
                  <Text style={styles.exerciseBadgeText}>🏋️ {activeCue.exerciseType}</Text>
                </View>
              )}
              <Text style={styles.guidanceText}>{activeCue.text}</Text>
              <Text style={styles.guidanceTime}>
                {formatTime(activeCue.startSecs)} – {formatTime(activeCue.endSecs)}
              </Text>
            </View>
          ) : (
            <View style={styles.guidanceBubbleEmpty}>
              <Text style={styles.guidanceEmptyText}>
                {cues.length > 0
                  ? t("player.noGuidanceNow")
                  : t("player.noGuidanceSession")}
              </Text>
            </View>
          )}
        </View>

        {/* ── Highlights Section ── */}
        {highlightSegments.length > 0 && (
          <View style={styles.highlightsSection}>
            <Text style={styles.sectionTitle}>{t("player.highlightsSection")}</Text>
            <Text style={styles.sectionSubtitle}>
              {t("player.highlightsHint")}
            </Text>
            <FlatList
              data={highlightSegments}
              keyExtractor={(_, i) => `hl-${i}`}
              renderItem={renderHighlightChip}
              horizontal
              showsHorizontalScrollIndicator={false}
              contentContainerStyle={styles.highlightList}
              scrollEnabled
            />
          </View>
        )}

        {/* ── All Guidance Cues Timeline ── */}
        {cues.length > 0 && (
          <View style={styles.timelineSection}>
            <Text style={styles.sectionTitle}>{t("player.fullTimeline")}</Text>
            {cues.map((cue, i) => {
              const isActive = activeCue === cue;
              return (
                <TouchableOpacity
                  key={`cue-${i}`}
                  style={[
                    styles.timelineCue,
                    isActive && styles.timelineCueActive,
                  ]}
                  onPress={() => {
                    player.currentTime = cue.startSecs;
                    player.play();
                  }}
                  activeOpacity={0.7}
                >
                  <Text style={[styles.timelineCueTime, isActive && styles.timelineCueTimeActive]}>
                    {formatTime(cue.startSecs)}
                  </Text>
                  <View style={styles.timelineCueTextCol}>
                    {cue.exerciseType && (
                      <Text style={styles.timelineCueExercise}>
                        {cue.exerciseType}
                      </Text>
                    )}
                    <Text
                      style={[styles.timelineCueText, isActive && styles.timelineCueTextActive]}
                      numberOfLines={3}
                    >
                      {cue.text}
                    </Text>
                  </View>
                </TouchableOpacity>
              );
            })}
          </View>
        )}
      </ScrollView>
    </View>
  );
}

// ─────────────────────────────────────────
// Styles
// ─────────────────────────────────────────

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: "#000",
  },

  // Header
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 10,
  },
  backBtn: {
    paddingVertical: 4,
    paddingRight: 12,
  },
  backText: {
    fontSize: 16,
    fontWeight: "600",
    color: "#64D2FF",
  },
  headerTitle: {
    flex: 1,
    fontSize: 17,
    fontWeight: "700",
    color: "#FFF",
    textAlign: "center",
  },
  headerSpacer: {
    width: 60,
  },

  // Video
  video: {
    width: SCREEN_WIDTH,
    height: VIDEO_HEIGHT,
    backgroundColor: "#111",
  },

  // Highlight overlay
  highlightOverlay: {
    position: "absolute",
    top: 56 + VIDEO_HEIGHT - 80,
    left: 16,
    right: 16,
    zIndex: 10,
  },
  highlightOverlayInner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: "rgba(0,0,0,0.85)",
    borderRadius: 14,
    padding: 14,
    borderWidth: 1,
    borderColor: "rgba(100,210,255,0.3)",
  },
  highlightOverlayEmoji: {
    fontSize: 28,
  },
  highlightOverlayTextCol: {
    flex: 1,
  },
  highlightOverlayLabel: {
    fontSize: 13,
    fontWeight: "700",
    color: "#64D2FF",
    marginBottom: 2,
  },
  highlightOverlayReason: {
    fontSize: 13,
    color: "#E0E0E0",
    lineHeight: 18,
  },

  // Bottom panel
  bottomPanel: {
    flex: 1,
  },
  bottomContent: {
    paddingTop: 16,
    paddingHorizontal: 16,
    gap: 20,
  },

  // Sections
  sectionTitle: {
    fontSize: 15,
    fontWeight: "700",
    color: "#FFF",
    marginBottom: 6,
  },
  sectionSubtitle: {
    fontSize: 12,
    color: "#888",
    marginBottom: 10,
  },

  // Guidance
  guidanceSection: {},
  guidanceBubble: {
    backgroundColor: "rgba(100,210,255,0.08)",
    borderRadius: 14,
    padding: 14,
    borderLeftWidth: 3,
    borderLeftColor: "#64D2FF",
  },
  exerciseBadge: {
    alignSelf: "flex-start",
    backgroundColor: "rgba(100,210,255,0.15)",
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 3,
    marginBottom: 8,
  },
  exerciseBadgeText: {
    fontSize: 11,
    fontWeight: "700",
    color: "#64D2FF",
  },
  guidanceText: {
    fontSize: 14,
    color: "#E8E8E8",
    lineHeight: 21,
  },
  guidanceTime: {
    fontSize: 11,
    color: "#888",
    marginTop: 8,
    textAlign: "right",
  },
  guidanceBubbleEmpty: {
    backgroundColor: "rgba(255,255,255,0.04)",
    borderRadius: 14,
    padding: 14,
  },
  guidanceEmptyText: {
    fontSize: 13,
    color: "#666",
    fontStyle: "italic",
  },

  // Highlights
  highlightsSection: {},
  highlightList: {
    gap: 10,
    paddingRight: 16,
  },
  highlightChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    backgroundColor: "rgba(255,255,255,0.05)",
    borderRadius: 12,
    borderWidth: 1,
    paddingHorizontal: 12,
    paddingVertical: 10,
    minWidth: 140,
    maxWidth: 220,
  },
  chipEmoji: {
    fontSize: 20,
  },
  chipTextCol: {
    flex: 1,
  },
  chipLabel: {
    fontSize: 12,
    fontWeight: "700",
  },
  chipTime: {
    fontSize: 11,
    color: "#888",
    marginTop: 2,
  },

  // Timeline
  timelineSection: {},
  timelineCue: {
    flexDirection: "row",
    gap: 12,
    paddingVertical: 10,
    paddingHorizontal: 10,
    borderRadius: 10,
    marginBottom: 4,
  },
  timelineCueActive: {
    backgroundColor: "rgba(100,210,255,0.1)",
  },
  timelineCueTime: {
    fontSize: 12,
    fontWeight: "600",
    color: "#888",
    width: 44,
    paddingTop: 2,
  },
  timelineCueTimeActive: {
    color: "#64D2FF",
  },
  timelineCueTextCol: {
    flex: 1,
  },
  timelineCueExercise: {
    fontSize: 11,
    fontWeight: "700",
    color: "#BF5AF2",
    marginBottom: 3,
  },
  timelineCueText: {
    fontSize: 13,
    color: "#B0B0B0",
    lineHeight: 19,
  },
  timelineCueTextActive: {
    color: "#E8E8E8",
  },
});
