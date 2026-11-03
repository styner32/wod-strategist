import { WODTrainingCard } from "./WODTrainingCard";
import { WorkoutProgressCard } from "./WorkoutProgressCard";
import { WorkoutShareSheet } from "./WorkoutShareSheet";
import { withoutWorkoutEvidence } from "../../../shared/analysisEvidence";
import { EnvironmentHistoryCard } from "../../environment/EnvironmentCard";
import { ActivitySummaryCard } from "./ActivitySummaryCard";
import { HeartRateSummaryCard } from "./HeartRateSummaryCard";
import { t } from "@/features/i18n";
import { OriginalVideosPending } from "@/features/video/OriginalVideosPending";
import * as FileSystem from "expo-file-system/legacy";
import * as MediaLibrary from "expo-media-library/legacy";
import { router } from "expo-router";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ActivityIndicator,
  Alert,
  AppState,
  ScrollView,
  SectionList,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";

import { MarkdownText } from "@/components/ui/MarkdownText";
import { TacticalTheme, getTacticalFatigueTone } from "@/constants/theme";
import { normalizeStretchKey } from "@/features/stretch/normalize";
import { useHasPendingMerge, useMergeStatus } from "@/store/useMergeStatus";
import { useProfileId } from "@/store/useProfileStore";
import { useVideoQueue } from "@/store/useVideoQueue";
import { useShallow } from "zustand/shallow";
import { useHighlightResults } from "../useHighlightResults";
import {
  archiveHistory,
  fetchHighlightDownloadURL,
  fetchRelatedWods,
  fetchVideoDownloadURL,
  generateHardSub,
  mergeChunks,
  retryAnalysis,
} from "../api";
import { notifyHistoryChanged, subscribeHistoryChanges } from "../archiveStates";
import { parseWorkoutType } from "../workoutType";
import {
  AnalysisResult,
  HighlightResult,
  fetchAnalysisHistory,
} from "../history";
import { formatSessionLabel } from "../sessionLabel";
import { HighlightVideoPlayer } from "./HighlightVideoPlayer";
import { RelatedWodsCard } from "./RelatedWodsCard";
import {
  StretchRecommendationItem,
  StretchRecommendationsCard,
} from "./StretchRecommendationsCard";
import { WorkoutFatigueCard } from "./WorkoutFatigueCard";

const T = TacticalTheme.light;

export { formatSessionLabel };

/** Badge config for analysis status */
function getStatusConfig(status: string) {
  switch (status) {
    case "COMPLETED":
      return {
        label: t("historyList.statusComplete"),
        color: T.statusFresh,
        bgColor: T.statusFreshBg,
        borderColor: T.statusFreshBorder,
      };
    case "CANCELLED":
      return { label: t("historyList.statusCancelled"), color: T.inkMuted, bgColor: T.surfaceMuted };
    case "FAILED":
      return {
        label: t("historyList.statusFailed"),
        color: T.statusExhausted,
        bgColor: T.statusExhaustedBg,
        borderColor: T.statusExhaustedBorder,
      };
    case "PENDING":
      return {
        label: t("historyList.statusPending"),
        color: T.statusWarning,
        bgColor: T.statusWarningBg,
        borderColor: T.statusWarningBorder,
      };
    default:
      return {
        label: status,
        color: T.inkMuted,
        bgColor: T.surfaceContainer,
        borderColor: T.borderStrong,
      };
  }
}

/** Badge config for analysis type */
function getTypeBadge(type: string) {
  switch (type) {
    case "injury_supplement":
      return {
        label: t("historyList.typeInjury"),
        color: T.heartRose,
        bgColor: T.heartRoseBg,
        borderColor: T.heartRoseBorder,
      };
    case "wod":
    default:
      return {
        label: t("historyList.typeWod"),
        color: T.electricBlue,
        bgColor: T.electricBlueBg,
        borderColor: T.electricBlueBorder,
      };
  }
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffHours = diffMs / (1000 * 60 * 60);

  if (diffHours < 1) {
    const mins = Math.floor(diffMs / (1000 * 60));
    return t("historyList.minutesAgo", { count: mins });
  }
  if (diffHours < 24) {
    return t("historyList.hoursAgo", { count: Math.floor(diffHours) });
  }
  if (diffHours < 48) {
    return t("historyList.yesterday");
  }

  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Emoji + label for each highlight variant title */
const HIGHLIGHT_VARIANT_CONFIG: Record<
  string,
  { emoji: string; color: string }
> = {
  "Highlight Reel": { emoji: "🎬", color: "#7C3AED" },
  "Best Forms": { emoji: "🏆", color: T.statusFresh },
  "Areas for Improvement": { emoji: "📈", color: T.statusFatigued },
  "Key Moments": { emoji: "⭐", color: T.electricBlue },
};

function HistoryCard({
  item,
  onArchive,
  focusSessionId,
  scrollViewRef,
  currentOffsetRef,
}: {
  item: AnalysisResult;
  onArchive?: (id: number) => void;
  focusSessionId?: string;
  scrollViewRef?: React.RefObject<ScrollView | null>;
  currentOffsetRef?: React.MutableRefObject<number>;
}) {
  const isFocused = Boolean(
    focusSessionId && item.session_id === focusSessionId,
  );
  const [expanded, setExpanded] = useState(isFocused);
  const [showOriginal, setShowOriginal] = useState(false);
  const [highlighted, setHighlighted] = useState(false);
  const cardRef = useRef<View>(null);

  useEffect(() => {
    if (isFocused) {
      setExpanded(true);
      setHighlighted(true);
      const timer = setTimeout(() => {
        setHighlighted(false);
      }, 2500);

      router.setParams({ focusSessionId: undefined });
      return () => clearTimeout(timer);
    }
  }, [isFocused]);

  const handleCardLayout = useCallback(() => {
    if (!isFocused || !cardRef.current || !scrollViewRef?.current) return;
    cardRef.current.measureInWindow((_cardX: number, cardY: number) => {
      const scrollNode = scrollViewRef.current as any;
      if (typeof scrollNode?.measureInWindow === "function") {
        scrollNode.measureInWindow((_scrollX: number, scrollY: number) => {
          const currentOffset = currentOffsetRef?.current ?? 0;
          const targetY = currentOffset + cardY - scrollY - 24;
          scrollViewRef.current?.scrollTo({
            y: Math.max(0, targetY),
            animated: true,
          });
        });
      }
    });
  }, [isFocused, scrollViewRef, currentOffsetRef]);

  const [downloading, setDownloading] = useState<string | null>(null); // 'merged' | 'hardsubbed' | 'encoded'
  const [retrying, setRetrying] = useState(false);
  const [shareVisible, setShareVisible] = useState(false);
  const [generatingHardsub, setGeneratingHardsub] = useState(false);
  const statusConfig = getStatusConfig(item.status);
  const typeBadge = getTypeBadge(item.analysis_type);

  const hasOutput = item.output && item.output.trim().length > 0;
  const hasInjuryOutput =
    item.injury_output && item.injury_output.trim().length > 0;
  const isCompleted = item.status === "COMPLETED";
  const availableVideos = item.available_videos ?? [];
  const hasHighlightSegments = !!(
    item.highlight_segments && item.highlight_segments.trim().length > 0
  );

  // ---------------------
  // Highlight state
  // ---------------------
  const { highlights, highlightLoading, highlightPolling, handleCreateHighlights } = useHighlightResults(
    item.session_id, item.profile_id ?? 0, isCompleted && item.analysis_type === "wod",
  );
  const [highlightDownloading, setHighlightDownloading] = useState<
    number | null
  >(null);
  const [playingHighlight, setPlayingHighlight] = useState<{
    hl: HighlightResult;
    url: string;
  } | null>(null);
  const [loadingPlayId, setLoadingPlayId] = useState<number | null>(null);

  // Related WODs lazy fetch on first expansion
  const [relatedWods, setRelatedWods] = useState<any[]>([]);
  const [relatedFetched, setRelatedFetched] = useState(false);

  useEffect(() => {
    if (
      expanded &&
      isCompleted &&
      item.analysis_type === "wod" &&
      !relatedFetched &&
      item.profile_id
    ) {
      setRelatedFetched(true);
      fetchRelatedWods({
        profileId: item.profile_id,
        sessionId: item.session_id,
        limit: 5,
      })
        .then((res) => {
          if (res?.related) {
            setRelatedWods(res.related);
          }
        })
        .catch(() => {});
    }
  }, [
    expanded,
    isCompleted,
    item.analysis_type,
    item.profile_id,
    item.session_id,
    relatedFetched,
  ]);

  const stretchRecommendations = useMemo<StretchRecommendationItem[]>(() => {
    if (!item.stretch_recommendations) return [];
    try {
      const parsed =
        typeof item.stretch_recommendations === "string"
          ? JSON.parse(item.stretch_recommendations)
          : item.stretch_recommendations;
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
  }, [item.stretch_recommendations]);

  const handleHighlightDownload = async (hl: HighlightResult) => {
    try {
      setHighlightDownloading(hl.id);
      const { download_url, filename } = await fetchHighlightDownloadURL(hl.id);
      const localUri = FileSystem.cacheDirectory + filename;
      const { uri } = await FileSystem.downloadAsync(download_url, localUri);

      Alert.alert(
        t("historyList.highlightDownloaded"),
        t("historyList.highlightReady", {
          title: hl.title,
          duration: Math.round(hl.duration_sec),
        }),
        [
          {
            text: t("historyList.saveToGallery"),
            onPress: async () => {
              try {
                const { status } = await MediaLibrary.requestPermissionsAsync();
                if (status !== "granted") {
                  Alert.alert(
                    t("common.permissionRequired"),
                    t("common.galleryPermission"),
                  );
                  return;
                }
                await MediaLibrary.saveToLibraryAsync(uri);
                Alert.alert(
                  t("common.saved"),
                  t("historyList.highlightSaved", { title: hl.title }),
                );
              } catch {
                Alert.alert(t("common.error"), t("common.failedSaveGallery"));
              } finally {
                try {
                  await FileSystem.deleteAsync(uri, { idempotent: true });
                } catch {}
              }
            },
          },
          {
            text: t("common.delete"),
            style: "destructive",
            onPress: async () => {
              try {
                await FileSystem.deleteAsync(uri, { idempotent: true });
              } catch {}
            },
          },
          { text: t("common.keep"), style: "cancel" },
        ],
      );
    } catch (e: any) {
      const msg = e?.message?.includes("404")
        ? t("historyList.highlightNotFound")
        : String(e);
      Alert.alert(t("historyList.downloadFailed"), msg);
    } finally {
      setHighlightDownloading(null);
    }
  };

  const handleSaveAllHighlights = async () => {
    const { status } = await MediaLibrary.requestPermissionsAsync();
    if (status !== "granted") {
      Alert.alert(
        t("common.permissionRequired"),
        t("common.galleryPermission"),
      );
      return;
    }

    setHighlightDownloading(-1); // -1 = saving all
    let savedCount = 0;
    for (const hl of highlights) {
      try {
        const { download_url, filename } = await fetchHighlightDownloadURL(
          hl.id,
        );
        const localUri = FileSystem.cacheDirectory + filename;
        const { uri } = await FileSystem.downloadAsync(download_url, localUri);
        await MediaLibrary.saveToLibraryAsync(uri);
        try {
          await FileSystem.deleteAsync(uri, { idempotent: true });
        } catch {}
        savedCount++;
      } catch {
        // Continue saving remaining highlights
      }
    }
    setHighlightDownloading(null);
    Alert.alert(
      t("common.saved"),
      t("historyList.savedCount", {
        saved: savedCount,
        total: highlights.length,
      }),
    );
  };

  const handlePlayHighlight = async (hl: HighlightResult) => {
    // If already playing this one, close it
    if (playingHighlight?.hl.id === hl.id) {
      setPlayingHighlight(null);
      return;
    }
    try {
      setLoadingPlayId(hl.id);
      const { download_url } = await fetchHighlightDownloadURL(hl.id);
      setPlayingHighlight({ hl, url: download_url });
    } catch (e: any) {
      const msg = e?.message?.includes("404")
        ? t("historyList.highlightNotFound")
        : String(e);
      Alert.alert(t("historyList.playbackFailed"), msg);
    } finally {
      setLoadingPlayId(null);
    }
  };

  const handleDownload = async (kind: "merged" | "hardsubbed" | "encoded") => {
    try {
      setDownloading(kind);
      const { download_url, filename } = await fetchVideoDownloadURL(
        item.session_id,
        item.profile_id ?? 0,
        kind,
      );

      const localUri = FileSystem.cacheDirectory + filename;
      const { uri } = await FileSystem.downloadAsync(download_url, localUri);

      const kindLabel =
        kind === "hardsubbed"
          ? t("historyList.videoKindGuided")
          : kind === "encoded"
            ? t("historyList.videoKindEncoded")
            : t("historyList.videoKindOriginal");
      Alert.alert(
        t("historyList.downloadComplete"),
        t("historyList.videoSaved", { kind: kindLabel }),
        [
          {
            text: t("historyList.saveToGallery"),
            onPress: async () => {
              try {
                const { status } = await MediaLibrary.requestPermissionsAsync();
                if (status !== "granted") {
                  Alert.alert(
                    t("common.permissionRequired"),
                    t("common.galleryPermission"),
                  );
                  return;
                }
                await MediaLibrary.saveToLibraryAsync(uri);
                Alert.alert(t("common.saved"), t("common.savedToGallery"));
              } catch (e) {
                Alert.alert(t("common.error"), t("common.failedSaveGallery"));
              } finally {
                // Clean up temp file
                try {
                  await FileSystem.deleteAsync(uri, { idempotent: true });
                } catch {}
              }
            },
          },
          {
            text: t("common.delete"),
            style: "destructive",
            onPress: async () => {
              try {
                await FileSystem.deleteAsync(uri, { idempotent: true });
              } catch {}
            },
          },
          { text: t("common.keep"), style: "cancel" },
        ],
      );
    } catch (e: any) {
      const msg = e?.message?.includes("404")
        ? t("historyList.videoNotFound")
        : String(e);
      Alert.alert(t("historyList.downloadFailed"), msg);
    } finally {
      setDownloading(null);
    }
  };

  const cardBg = T.surface;
  const cardBorder = T.border;
  const sessionColor = T.ink;
  const dateColor = T.inkMuted;
  const previewColor = T.inkSecondary;
  const expandColor = T.electricBlue;
  const hrDividerColor = T.heartRoseBorder;
  const highlightSectionBg = T.surfaceMuted;

  const handleArchive = () => {
    Alert.alert(
      t("historyList.archiveTitle"),
      t("historyList.archiveConfirm"),
      [
        { text: t("common.cancel"), style: "cancel" },
        {
          text: t("historyList.archive"),
          style: "destructive",
          onPress: async () => {
            try {
              await archiveHistory(item.id);
              onArchive?.(item.id);
              if (item.profile_id) notifyHistoryChanged(item.profile_id);
            } catch (err: any) {
              const message =
                err?.status === 409
                  ? t("historyList.archiveInProgress")
                  : t("historyList.archiveFailed");
              Alert.alert(t("common.error"), message);
            }
          },
        },
      ],
    );
  };

  const fatigueTone = item.session_fatigue
    ? getTacticalFatigueTone(item.session_fatigue.overall_score ?? 0, "light")
    : null;

  return (
    <View ref={cardRef} onLayout={handleCardLayout}>
      <TouchableOpacity
        activeOpacity={1}
        onLongPress={handleArchive}
        delayLongPress={600}
        style={[
          styles.card,
          { backgroundColor: cardBg, borderColor: cardBorder },
          highlighted && styles.focusedCard,
        ]}
      >
        {/* Header Row */}
        <View style={styles.cardHeader}>
          <View style={styles.headerLeft}>
            <Text style={[styles.sessionLabel, { color: sessionColor }]}>
              {formatSessionLabel(item.session_id)}
            </Text>
            <View
              style={[
                styles.badge,
                {
                  backgroundColor: typeBadge.bgColor,
                  borderColor: typeBadge.borderColor,
                },
              ]}
            >
              <Text style={[styles.badgeText, { color: typeBadge.color }]}>
                {typeBadge.label}
              </Text>
            </View>
          </View>
          <View
            style={[
              styles.badge,
              {
                backgroundColor: statusConfig.bgColor,
                borderColor: statusConfig.borderColor,
              },
            ]}
          >
            <Text style={[styles.badgeText, { color: statusConfig.color }]}>
              {statusConfig.label}
            </Text>
          </View>
        </View>

        {/* Date and Fatigue Pill */}
        <View style={styles.dateAndFatigueRow}>
          <Text style={[styles.date, { color: dateColor }]}>
            {formatDate(item.created_at)}
          </Text>
          {item.session_fatigue && fatigueTone && (
            <View
              style={[
                styles.fatiguePill,
                item.session_fatigue.status === "insufficient_evidence"
                  ? {
                      backgroundColor: T.surfaceContainer,
                      borderColor: T.borderStrong,
                    }
                  : {
                      backgroundColor: fatigueTone.bg,
                      borderColor: fatigueTone.border,
                    },
              ]}
            >
              <Text
                style={[
                  styles.fatiguePillText,
                  item.session_fatigue.status === "insufficient_evidence"
                    ? { color: T.inkMuted }
                    : { color: fatigueTone.fg },
                ]}
              >
                {item.session_fatigue.status === "insufficient_evidence" ? (
                  `⚡ ${t("historyList.insufficientEvidence") || "분석 근거 부족"}`
                ) : (
                  `⚡ ${item.session_fatigue.state_ko || item.session_fatigue.state} ${
                    item.session_fatigue.status === "available"
                      ? `${item.session_fatigue.overall_score}/100`
                      : `${item.session_fatigue.overall_score}%`
                  }`
                )}
              </Text>
            </View>
          )}
        </View>

        <WODTrainingCard key={item.session_id} sessionId={item.session_id} profileId={item.profile_id} scoreJSON={item.session_score} />
        {isCompleted && <WorkoutProgressCard key={`progress:${item.profile_id}:${item.session_id}`} sessionId={item.session_id} profileId={item.profile_id} onReviewVideo={availableVideos.includes("merged") ? (start, end) => router.push({ pathname: "/workout/player", params: { sessionId: item.session_id, profileId: String(item.profile_id), videoKind: "merged", startSecs: String(start), endSecs: String(end) } }) : undefined} />}
        <HeartRateSummaryCard summary={item.heart_rate} />
        <EnvironmentHistoryCard sessionId={item.session_id} profileId={item.profile_id} />
        <ActivitySummaryCard sessionId={item.session_id} profileId={item.profile_id} />

        {/* Content */}
        {hasOutput && (
          <TouchableOpacity
            activeOpacity={0.8}
            onPress={() => setExpanded((p) => !p)}
          >
            {expanded ? (
              <View style={styles.contentBody}>
                <MarkdownText>{withoutWorkoutEvidence(item.output)}</MarkdownText>
                <TouchableOpacity accessibilityRole="button" onPress={() => setShowOriginal(v => !v)}><Text style={{ color: T.electricBlue, fontWeight: "700", fontSize: 13, paddingVertical: 8 }}>{t("workoutProgress.originalAnalysis")}</Text></TouchableOpacity>
                {showOriginal && <Text selectable style={{ color: T.inkMuted, fontSize: 12 }}>{item.output}</Text>}

                {/* Workout Fatigue Breakdown */}
                {item.session_fatigue && (
                  <WorkoutFatigueCard fatigue={item.session_fatigue} />
                )}

                {/* Injury supplement output */}
                {hasInjuryOutput && (
                  <View
                    style={[
                      styles.injurySection,
                      { borderTopColor: hrDividerColor },
                    ]}
                  >
                    <View style={styles.injurySectionHeader}>
                      <Text style={styles.injurySectionLabel}>
                        {t("historyList.injuryAnalysis")}
                      </Text>
                    </View>
                    <MarkdownText color={T.heartRose} accentColor={T.heartRose}>
                      {item.injury_output!}
                    </MarkdownText>
                  </View>
                )}

                <StretchRecommendationsCard
                  recommendations={stretchRecommendations}
                  onPressItem={(rec) => {
                    router.push({
                      pathname: "/stretch/[key]" as any,
                      params: {
                        key: normalizeStretchKey(rec.stretch),
                        name: rec.stretch,
                      },
                    });
                  }}
                />
                <RelatedWodsCard related={relatedWods} />
              </View>
            ) : (
              <Text
                style={[styles.previewText, { color: previewColor }]}
                numberOfLines={4}
              >
                {stripMarkdown(withoutWorkoutEvidence(item.output))}
              </Text>
            )}

            <Text style={[styles.expandHint, { color: expandColor }]}>
              {expanded ? t("historyList.showLess") : t("historyList.showMore")}
            </Text>
          </TouchableOpacity>
        )}

        {/* Failed/Pending states */}
        {!hasOutput && item.status === "FAILED" && (
          <View style={styles.failedBanner}>
            <Text style={styles.failedText}>
              {t("historyList.analysisFailed")}
            </Text>
          </View>
        )}

        {/* Retry button for failed analysis */}
        {item.status === "FAILED" && item.profile_id && (
          <TouchableOpacity
            style={[styles.retryBtn, retrying && { opacity: 0.5 }]}
            disabled={retrying}
            onPress={async () => {
              try {
                setRetrying(true);
                await retryAnalysis(item.session_id, item.profile_id!);
                Alert.alert(t("upload.success"), t("historyList.retryStarted"));
              } catch (err) {
                Alert.alert(t("common.error"), t("historyList.retryFailed"));
              } finally {
                setRetrying(false);
              }
            }}
            activeOpacity={0.8}
          >
            {retrying ? (
              <ActivityIndicator size="small" color={T.statusFatigued} />
            ) : (
              <Text style={styles.retryBtnText}>
                {t("historyList.retryAnalysis")}
              </Text>
            )}
          </TouchableOpacity>
        )}

        {isCompleted && item.analysis_type === "wod" && !item.archived_at && !!item.profile_id && (
          <TouchableOpacity accessibilityRole="button" style={styles.watchBtn} onPress={() => setShareVisible(true)} activeOpacity={0.85}>
            <Text style={styles.watchBtnText}>{t("workoutShare.askAI")}</Text>
          </TouchableOpacity>
        )}

        {/* Watch button — opens full workout player */}
        {isCompleted &&
          item.analysis_type !== "injury_supplement" &&
          (availableVideos.includes("merged") ||
            availableVideos.includes("hardsubbed") ||
            availableVideos.includes("encoded")) && (
            <TouchableOpacity
              style={styles.watchBtn}
              onPress={() => {
                // Prefer hardsubbed (has guidance overlay) > merged > encoded
                const videoKind = availableVideos.includes("hardsubbed")
                  ? "hardsubbed"
                  : availableVideos.includes("merged")
                    ? "merged"
                    : "encoded";
                router.push({
                  pathname: "/workout/player",
                  params: {
                    sessionId: item.session_id,
                    profileId: String(item.profile_id ?? 0),
                    videoKind,
                  },
                });
              }}
              activeOpacity={0.85}
            >
              <Text style={styles.watchBtnText}>
                {t("historyList.watchWorkout")}
              </Text>
            </TouchableOpacity>
          )}

        {/* Download buttons */}
        {isCompleted &&
          item.analysis_type !== "injury_supplement" &&
          availableVideos.length > 0 && (
            <>
              <Text style={{ color: dateColor, fontSize: 12, fontWeight: "700", marginTop: 12, marginBottom: 4 }}>
                {t("originalVideos.serverVersions")}
              </Text>
            <View style={styles.downloadRow}>
              {availableVideos.includes("merged") && (
                <TouchableOpacity
                  style={styles.downloadBtn}
                  onPress={() => handleDownload("merged")}
                  disabled={downloading !== null}
                >
                  {downloading === "merged" ? (
                    <ActivityIndicator size="small" color={T.electricBlue} />
                  ) : (
                    <Text style={styles.downloadBtnText}>
                      {t("historyList.videoBtn")}
                    </Text>
                  )}
                </TouchableOpacity>
              )}
              {availableVideos.includes("hardsubbed") && (
                <TouchableOpacity
                  style={[styles.downloadBtn, styles.downloadBtnGuided]}
                  onPress={() => handleDownload("hardsubbed")}
                  disabled={downloading !== null}
                >
                  {downloading === "hardsubbed" ? (
                    <ActivityIndicator size="small" color={T.oliveAccent} />
                  ) : (
                    <Text
                      style={[
                        styles.downloadBtnText,
                        styles.downloadBtnGuidedText,
                      ]}
                    >
                      {t("historyList.guidedBtn")}
                    </Text>
                  )}
                </TouchableOpacity>
              )}
              {availableVideos.includes("encoded") && (
                <TouchableOpacity
                  style={[styles.downloadBtn, styles.downloadBtnEncoded]}
                  onPress={() => handleDownload("encoded")}
                  disabled={downloading !== null}
                >
                  {downloading === "encoded" ? (
                    <ActivityIndicator size="small" color={T.inkSecondary} />
                  ) : (
                    <Text
                      style={[
                        styles.downloadBtnText,
                        styles.downloadBtnEncodedText,
                      ]}
                    >
                      {t("historyList.encodedBtn")}
                    </Text>
                  )}
                </TouchableOpacity>
              )}
            </View>
            </>
          )}

        {/* Create Guided Video button — when completed but no hardsubbed version */}
        {isCompleted &&
          item.profile_id &&
          !availableVideos.includes("hardsubbed") &&
          availableVideos.includes("merged") && (
            <TouchableOpacity
              style={[styles.hardsubBtn, generatingHardsub && { opacity: 0.5 }]}
              disabled={generatingHardsub}
              onPress={async () => {
                try {
                  setGeneratingHardsub(true);
                  await generateHardSub(item.session_id, item.profile_id!);
                  Alert.alert(
                    t("upload.success"),
                    t("historyList.hardsubStarted"),
                  );
                } catch (err) {
                  Alert.alert(
                    t("common.error"),
                    t("historyList.hardsubFailed"),
                  );
                } finally {
                  setGeneratingHardsub(false);
                }
              }}
              activeOpacity={0.8}
            >
              {generatingHardsub ? (
                <ActivityIndicator size="small" color={T.electricBlue} />
              ) : (
                <Text style={styles.hardsubBtnText}>
                  {t("historyList.createHardsub")}
                </Text>
              )}
            </TouchableOpacity>
          )}

        {/* ==================== Highlights Section ==================== */}
        {isCompleted &&
          item.analysis_type === "wod" &&
          hasHighlightSegments && (
            <View
              style={[
                styles.highlightSection,
                { backgroundColor: highlightSectionBg },
              ]}
            >
              <Text style={styles.highlightSectionTitle}>
                {t("historyList.highlights")}
              </Text>

              {/* Show existing completed highlights */}
              {highlights.length > 0 && (
                <>
                  <View style={styles.highlightVariantList}>
                    {highlights.map((hl) => {
                      const variantCfg = HIGHLIGHT_VARIANT_CONFIG[hl.title] ?? {
                        emoji: "🎥",
                        color: T.inkSecondary,
                      };
                      const isLoadingPlay = loadingPlayId === hl.id;
                      const isPlaying = playingHighlight?.hl.id === hl.id;
                      return (
                        <TouchableOpacity
                          key={hl.id}
                          style={[
                            styles.highlightVariantBtn,
                            { borderColor: variantCfg.color + "45" },
                            isPlaying && {
                              borderColor: variantCfg.color,
                              backgroundColor: variantCfg.color + "18",
                            },
                          ]}
                          onPress={() => handlePlayHighlight(hl)}
                          onLongPress={() => handleHighlightDownload(hl)}
                          disabled={
                            loadingPlayId !== null && loadingPlayId !== hl.id
                          }
                        >
                          {isLoadingPlay ? (
                            <ActivityIndicator
                              size="small"
                              color={variantCfg.color}
                            />
                          ) : (
                            <>
                              <Text style={styles.highlightVariantEmoji}>
                                {isPlaying ? "⏸" : "▶"}
                              </Text>
                              <Text
                                style={[
                                  styles.highlightVariantLabel,
                                  { color: variantCfg.color },
                                ]}
                              >
                                {hl.title}
                              </Text>
                              <Text style={styles.highlightVariantDuration}>
                                {Math.round(hl.duration_sec)}s
                              </Text>
                            </>
                          )}
                        </TouchableOpacity>
                      );
                    })}
                  </View>

                  {/* Inline Video Player */}
                  {playingHighlight && (
                    <HighlightVideoPlayer
                      highlight={playingHighlight.hl}
                      videoUrl={playingHighlight.url}
                      onClose={() => setPlayingHighlight(null)}
                    />
                  )}

                  {/* Save All button */}
                  {highlights.length > 1 && (
                    <TouchableOpacity
                      style={styles.saveAllBtn}
                      onPress={handleSaveAllHighlights}
                      disabled={highlightDownloading !== null}
                    >
                      {highlightDownloading === -1 ? (
                        <ActivityIndicator size="small" color={T.electricBlue} />
                      ) : (
                        <Text style={styles.saveAllBtnText}>
                          {t("historyList.saveAllGallery", {
                            count: highlights.length,
                          })}
                        </Text>
                      )}
                    </TouchableOpacity>
                  )}
                </>
              )}

              {/* Polling indicator */}
              {highlightPolling && highlights.length === 0 && (
                <View style={styles.highlightPollingBanner}>
                  <ActivityIndicator size="small" color={T.electricBlue} />
                  <Text style={styles.highlightPollingText}>
                    {t("historyList.generatingHighlights")}
                  </Text>
                </View>
              )}

              {/* Create button (shown when no highlights and not polling) */}
              {highlights.length === 0 && !highlightPolling && (
                <TouchableOpacity
                  style={styles.createHighlightBtn}
                  onPress={handleCreateHighlights}
                  disabled={highlightLoading}
                >
                  {highlightLoading ? (
                    <ActivityIndicator size="small" color="#FFF" />
                  ) : (
                    <Text style={styles.createHighlightBtnText}>
                      {t("historyList.createHighlights")}
                    </Text>
                  )}
                </TouchableOpacity>
              )}

              {/* Regenerate button (shown when highlights already exist) */}
              {highlights.length > 0 && !highlightPolling && (
                <TouchableOpacity
                  style={styles.regenerateBtn}
                  onPress={handleCreateHighlights}
                  disabled={highlightLoading}
                >
                  {highlightLoading ? (
                    <ActivityIndicator size="small" color={T.electricBlue} />
                  ) : (
                    <Text style={styles.regenerateBtnText}>
                      {t("historyList.regenerate")}
                    </Text>
                  )}
                </TouchableOpacity>
              )}
            </View>
          )}

        {!hasOutput && item.status === "PENDING" && (
          <View style={styles.pendingBanner}>
            <ActivityIndicator size="small" color={T.statusWarning} />
            <Text style={styles.pendingText}>
              {t("historyList.analysisInProgress")}
            </Text>
          </View>
        )}
      </TouchableOpacity>
      {!!item.profile_id && <WorkoutShareSheet visible={shareVisible && isCompleted && item.analysis_type === "wod" && !item.archived_at} sessionId={item.session_id} profileId={item.profile_id} onClose={() => setShareVisible(false)} />}
    </View>
  );
}

/** Strip markdown formatting for a plain-text preview */
function stripMarkdown(text: string): string {
  return text
    .replace(/```[\s\S]*?```/g, "") // Remove code blocks
    .replace(/^---+$/gm, "") // Remove HR
    .replace(/^#{1,3}\s+/gm, "") // Remove headings
    .replace(/\*\*(.*?)\*\*/g, "$1") // Remove bold
    .replace(/^\s*[-*]\s+/gm, "• ") // Normalize bullets
    .replace(/^\s*\d+\.\s+/gm, "") // Remove numbered list markers
    .replace(/\n{2,}/g, "\n") // Collapse newlines
    .trim();
}

/**
 * Hook that manages history data fetching, exposed for the parent
 * to wire pull-to-refresh on ParallaxScrollView.
 *
 * Auto-polls every 10s while any PENDING items exist so the
 * processing section updates live without manual pull-to-refresh.
 */
const PENDING_POLL_INTERVAL_MS = 10_000;

export function useHistoryData(options?: { limit?: number }) {
  const profileId = useProfileId();
  const currentHistoryProfile = useRef(profileId); currentHistoryProfile.current = profileId;
  const historyRequest = useRef(0);
  const [data, setData] = useState<AnalysisResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadData = useCallback(async () => {
    const request = ++historyRequest.current;
    const current = () => request === historyRequest.current && profileId === currentHistoryProfile.current;
    if (!profileId) {
      setData([]);
      setLoading(false);
      setRefreshing(false);
      return;
    }
    try {
      const history = await fetchAnalysisHistory(profileId, options?.limit);
      if (current()) setData(history);
    } catch (e) {
      console.error(e);
    } finally {
      if (current()) { setLoading(false); setRefreshing(false); }
    }
  }, [profileId, options?.limit]);

  useEffect(() => {
    const unsubscribe = subscribeHistoryChanges(pid => { if (pid === profileId) void loadData(); });
    const app = AppState.addEventListener("change", state => { if (state === "active") void loadData(); });
    return () => { unsubscribe(); app.remove(); ++historyRequest.current; };
  }, [profileId, loadData]);


  useEffect(() => {
    setLoading(true);
    loadData();
  }, [loadData]);

  // Auto-poll while PENDING items exist
  const hasPending = data.some((item) => item.status === "PENDING");

  useEffect(() => {
    if (hasPending) {
      pollTimerRef.current = setInterval(() => {
        loadData();
      }, PENDING_POLL_INTERVAL_MS);
    } else {
      if (pollTimerRef.current) {
        clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    }

    return () => {
      if (pollTimerRef.current) {
        clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    };
  }, [hasPending, loadData]);

  const onRefresh = useCallback(() => {
    setRefreshing(true);
    loadData();
  }, [loadData]);

  const onArchive = useCallback((id: number) => {
    setData((prev) => prev.filter((item) => item.id !== id));
  }, []);

  return { data, loading, refreshing, onRefresh, onArchive };
}

// ---------------------
// Processing Section
// ---------------------

export function isStaleProcessing(
  createdAt: string,
  now: number = Date.now(),
): boolean {
  if (!createdAt) return false;
  const createdTime = new Date(createdAt).getTime();
  if (!Number.isFinite(createdTime)) return false;
  return now - createdTime >= 60 * 60 * 1000;
}

export function ProcessingSection({
  items,
  onRefresh,
}: {
  items: AnalysisResult[];
  onRefresh?: () => void;
}) {
  const activeProfileId = useProfileId();
  const [remergingId, setRemergingId] = useState<string | null>(null);
  const [localRequestedIds, setLocalRequestedIds] = useState<Record<string, boolean>>({});
  const [, setStoreRevision] = useState(0);

  // Pull ALL non-completed items from the local video queue
  // useShallow prevents infinite re-renders from .filter() creating new array refs
  const queueItems = useVideoQueue(
    useShallow((s) => s.items.filter((i) => i.status !== "UPLOADED")),
  );

  const executeRemerge = useCallback(
    async ({
      sessionId,
      profileId,
      workoutType,
    }: {
      sessionId: string;
      profileId: number;
      workoutType: ReturnType<typeof parseWorkoutType>;
    }) => {
      await mergeChunks(sessionId, {
        profileId,
        workoutType,
      });
    },
    [],
  );

  useEffect(() => {
    const store = useMergeStatus as any;
    void store.getState?.()?.hydrateRemergeState?.();
    if (typeof store.subscribe === "function") {
      return store.subscribe(() => {
        setStoreRevision((rev) => rev + 1);
      });
    }
  }, []);

  useEffect(() => {
    const pendingSessionIds = items.map((i) => i.session_id);
    const pendingSet = new Set(pendingSessionIds);
    setLocalRequestedIds((prev) => {
      let changed = false;
      const next: Record<string, boolean> = {};
      for (const sid of Object.keys(prev)) {
        if (pendingSet.has(sid)) {
          next[sid] = true;
        } else {
          changed = true;
        }
      }
      return changed ? next : prev;
    });
    const storeState = (useMergeStatus as any).getState?.();
    if (typeof storeState?.syncPendingSessions === "function") {
      void storeState.syncPendingSessions(pendingSessionIds, executeRemerge);
    }
  }, [items, executeRemerge]);

  const handleRemerge = useCallback(
    (item: AnalysisResult) => {
      const storeState = (useMergeStatus as any).getState?.();
      const alreadyRequested =
        Boolean(localRequestedIds[item.session_id]) ||
        Boolean(storeState?.isRemergeRequested?.(item.session_id));
      if (alreadyRequested || remergingId === item.session_id) {
        return;
      }

      const profileIdToUse = item.profile_id ?? activeProfileId;
      if (!profileIdToUse) {
        Alert.alert(t("common.error"), t("historyList.remergeFailed"));
        return;
      }

      Alert.alert(
        t("historyList.remergeConfirmTitle"),
        t("historyList.remergeConfirmBody"),
        [
          { text: t("common.cancel"), style: "cancel" },
          {
            text: t("historyList.remergeAction"),
            onPress: async () => {
              try {
                setRemergingId(item.session_id);
                const parts = item.session_id.split("-");
                const rawType =
                  parts[0]?.startsWith("P") && parts.length > 1
                    ? parts[1]
                    : parts[0];
                const workoutType = parseWorkoutType(rawType);

                const currentStore = (useMergeStatus as any).getState?.();
                if (typeof currentStore?.enqueueRemerge === "function") {
                  const outcome = await currentStore.enqueueRemerge(
                    {
                      sessionId: item.session_id,
                      profileId: profileIdToUse,
                      workoutType,
                    },
                    executeRemerge,
                  );
                  setLocalRequestedIds((prev) => ({
                    ...prev,
                    [item.session_id]: true,
                  }));
                  if (outcome === "duplicate") {
                    return;
                  }
                  Alert.alert(
                    t("upload.success"),
                    outcome === "queued"
                      ? t("historyList.remergeQueuedAlert")
                      : t("historyList.remergeStarted"),
                  );
                } else {
                  await executeRemerge({
                    sessionId: item.session_id,
                    profileId: profileIdToUse,
                    workoutType,
                  });
                  currentStore?.addPending?.(item.session_id, true);
                  setLocalRequestedIds((prev) => ({
                    ...prev,
                    [item.session_id]: true,
                  }));
                  Alert.alert(t("upload.success"), t("historyList.remergeStarted"));
                }
                onRefresh?.();
              } catch (err) {
                console.error("Failed to re-merge chunks:", err);
                Alert.alert(t("common.error"), t("historyList.remergeFailed"));
              } finally {
                setRemergingId(null);
              }
            },
          },
        ],
      );
    },
    [activeProfileId, executeRemerge, localRequestedIds, onRefresh, remergingId],
  );

  if (items.length === 0 && queueItems.length === 0) return null;

  const textColor = T.electricBlue;
  const subtextColor = T.inkMuted;

  return (
    <View style={processingStyles.container}>
      <View style={processingStyles.headerRow}>
        <Text style={[processingStyles.sectionTitle, { color: textColor }]}>
          {t("historyList.processingSection")}
        </Text>
        <ActivityIndicator size="small" color={T.electricBlue} />
      </View>

      {/* Server-side PENDING analysis results */}
      {items.map((item) => {
        const stale = isStaleProcessing(item.created_at);
        const storeState = (useMergeStatus as any).getState?.();
        const isRemerging = remergingId === item.session_id;
        const isRequested =
          Boolean(localRequestedIds[item.session_id]) ||
          Boolean(storeState?.isRemergeRequested?.(item.session_id));
        const queuePos: number =
          storeState?.getRemergeQueuePosition?.(item.session_id) ?? -1;
        const isDisabled = isRemerging || isRequested;
        const buttonLabel = isRequested
          ? queuePos > 0
            ? t("historyList.remergeQueuedWithPosition", { position: queuePos })
            : t("historyList.remergeInProgress")
          : t("historyList.remerge");

        return (
          <View
            key={item.id}
            style={[
              processingStyles.card,
              {
                backgroundColor: stale ? T.statusWarningBg : T.surface,
                borderColor: stale ? T.statusWarningBorder : T.electricBlueBorder,
              },
            ]}
          >
            <View style={processingStyles.cardRow}>
              <Text style={processingStyles.icon}>🔬</Text>
              <View style={processingStyles.cardContent}>
                <Text
                  style={[
                    processingStyles.cardTitle,
                    { color: T.ink },
                  ]}
                >
                  {formatSessionLabel(item.session_id)}
                </Text>
                <Text
                  style={[processingStyles.cardSubtitle, { color: subtextColor }]}
                >
                  {t("historyList.aiAnalyzing")}
                </Text>
              </View>
              <View
                style={[
                  processingStyles.statusDot,
                  { backgroundColor: stale ? T.statusWarning : T.electricBlue },
                ]}
              />
            </View>
            <View style={processingStyles.cardFooter}>
              <Text style={[processingStyles.cardDate, { color: subtextColor }]}>
                {formatDate(item.created_at)}
              </Text>
              {stale && (
                <TouchableOpacity
                  style={[
                    processingStyles.remergeBtn,
                    isDisabled && { opacity: 0.6 },
                  ]}
                  disabled={isDisabled}
                  onPress={() => handleRemerge(item)}
                  accessibilityRole="button"
                  accessibilityLabel={buttonLabel}
                >
                  {isRemerging ? (
                    <ActivityIndicator size="small" color={T.voltInk} />
                  ) : (
                    <>
                      <Text style={processingStyles.remergeIcon}>
                        {isRequested ? "⏳" : "🔄"}
                      </Text>
                      <Text style={processingStyles.remergeBtnText}>
                        {buttonLabel}
                      </Text>
                    </>
                  )}
                </TouchableOpacity>
              )}
            </View>
          </View>
        );
      })}

      {/* Local video queue items (all stages) */}
      {queueItems.map((item) => {
        const pct = Math.round(item.progress * 100);
        const hasProgress =
          item.status === "ENCODING" || item.status === "UPLOADING";

        // Icon and label per status
        let icon = "📹";
        let subtitle = "";
        let dotColor: string = T.inkMuted;
        switch (item.status) {
          case "RECORDED":
            icon = "🎬";
            subtitle = t("historyList.waitingToEncode");
            dotColor = T.inkMuted;
            break;
          case "ENCODING":
            icon = "⚙️";
            subtitle = t("historyList.encodingProgress", { percent: pct });
            dotColor = T.statusFatigued;
            break;
          case "ENCODED":
            icon = "✅";
            subtitle = t("historyList.readyToUpload");
            dotColor = T.statusFresh;
            break;
          case "UPLOADING":
            icon = "☁️";
            subtitle = t("historyList.uploadingProgress", { percent: pct });
            dotColor = T.electricBlue;
            break;
        }

        return (
          <View
            key={item.id}
            style={[
              processingStyles.card,
              { backgroundColor: T.surface, borderColor: T.border },
            ]}
          >
            <View style={processingStyles.cardRow}>
              <Text style={processingStyles.icon}>{icon}</Text>
              <View style={processingStyles.cardContent}>
                <Text
                  style={[
                    processingStyles.cardTitle,
                    { color: T.ink },
                  ]}
                >
                  {formatSessionLabel(item.sessionId)}
                </Text>
                <Text
                  style={[
                    processingStyles.cardSubtitle,
                    { color: subtextColor },
                  ]}
                >
                  {subtitle}
                </Text>
              </View>
              <View
                style={[
                  processingStyles.statusDot,
                  { backgroundColor: dotColor },
                ]}
              />
            </View>
            {/* Progress bar — only for encoding/uploading */}
            {hasProgress && (
              <View style={processingStyles.progressBarBg}>
                <View
                  style={[
                    processingStyles.progressBarFill,
                    {
                      width: `${pct}%`,
                      backgroundColor: dotColor,
                    },
                  ]}
                />
              </View>
            )}
          </View>
        );
      })}
    </View>
  );
}

const processingStyles = StyleSheet.create({
  container: {
    gap: 10,
    marginBottom: 16,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 2,
    paddingHorizontal: 2,
  },
  sectionTitle: {
    fontSize: 12,
    fontWeight: "800",
    letterSpacing: 0.8,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  card: {
    borderRadius: 16,
    padding: 14,
    borderWidth: 1,
    gap: 8,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 6,
    elevation: 1,
  },
  cardRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  icon: {
    fontSize: 20,
  },
  cardContent: {
    flex: 1,
    gap: 2,
  },
  cardTitle: {
    fontSize: 15,
    fontWeight: "800",
    flexShrink: 1,
  },
  cardSubtitle: {
    fontSize: 12,
    fontWeight: "600",
    flexShrink: 1,
  },
  cardFooter: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginLeft: 30,
    marginTop: 2,
    gap: 8,
  },
  cardDate: {
    fontSize: 11,
    flexShrink: 1,
  },
  remergeBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    paddingVertical: 6,
    paddingHorizontal: 12,
    borderRadius: 8,
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
  },
  remergeIcon: {
    fontSize: 11,
  },
  remergeBtnText: {
    fontSize: 12,
    fontWeight: "800",
    color: T.voltInk,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  progressBarBg: {
    height: 5,
    backgroundColor: T.surfaceContainer,
    borderRadius: 3,
    overflow: "hidden",
    marginLeft: 30,
  },
  progressBarFill: {
    height: "100%",
    borderRadius: 3,
  },
});

// ---------------------
// Merge Banner
// ---------------------

function MergeBanner() {
  const hasPending = useHasPendingMerge();
  if (!hasPending) return null;

  return (
    <View style={mergeBannerStyles.container}>
      <ActivityIndicator size="small" color={T.electricBlue} />
      <Text style={mergeBannerStyles.text}>
        {t("historyList.mergePreparing")}
      </Text>
    </View>
  );
}

const mergeBannerStyles = StyleSheet.create({
  container: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    borderRadius: 14,
    padding: 14,
    marginBottom: 12,
  },
  text: {
    color: T.electricBlue,
    fontSize: 13,
    fontWeight: "700",
    flex: 1,
    flexShrink: 1,
  },
});

// ---------------------
// HistoryList (presentation)
// ---------------------

interface HistoryListProps {
  data: AnalysisResult[];
  loading: boolean;
  onRefresh?: () => void;
  onArchive?: (id: number) => void;
  focusSessionId?: string;
  scrollViewRef?: React.RefObject<any>;
  currentOffsetRef?: React.MutableRefObject<number>;
}

/** Extract date key (YYYY-MM-DD) from a created_at timestamp */
function getDateKey(dateStr: string): string {
  const d = new Date(dateStr);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** Format a date key into a human-readable section header */
function formatSectionTitle(dateKey: string): string {
  const now = new Date();
  const todayKey = getDateKey(now.toISOString());
  const yesterday = new Date(now.getTime() - 86_400_000);
  const yesterdayKey = getDateKey(yesterday.toISOString());

  if (dateKey === todayKey) return t("history.today");
  if (dateKey === yesterdayKey) return t("history.yesterday");

  const d = new Date(dateKey + "T00:00:00");
  return d.toLocaleDateString(undefined, {
    weekday: "long",
    month: "long",
    day: "numeric",
  });
}

/** Compute minute gap between two consecutive sessions */
function getMinuteGap(olderDate: string, newerDate: string): number {
  const diff = new Date(newerDate).getTime() - new Date(olderDate).getTime();
  return Math.round(diff / (1000 * 60));
}

interface DateSection {
  title: string;
  data: AnalysisResult[];
}

/** Group items by date, preserving original order (newest first) */
function groupByDate(items: AnalysisResult[]): DateSection[] {
  const sections: DateSection[] = [];
  let currentKey = "";
  let currentSection: DateSection | null = null;

  for (const item of items) {
    const key = getDateKey(item.created_at);
    if (key !== currentKey) {
      currentKey = key;
      currentSection = { title: formatSectionTitle(key), data: [] };
      sections.push(currentSection);
    }
    currentSection!.data.push(item);
  }

  return sections;
}

export function HistoryList({
  data,
  loading,
  onRefresh,
  onArchive,
  focusSessionId,
  scrollViewRef,
  currentOffsetRef,
}: HistoryListProps) {
  // Split pending items into processing section vs completed list
  const pendingItems = data.filter((item) => item.status === "PENDING");
  const nonPendingItems = data.filter((item) => item.status !== "PENDING");

  const sections = useMemo(
    () => groupByDate(nonPendingItems),
    [nonPendingItems],
  );

  const renderItem = useCallback(
    ({
      item,
      index,
      section,
    }: {
      item: AnalysisResult;
      index: number;
      section: DateSection;
    }) => {
      const nodes: React.ReactNode[] = [];

      // Time gap between consecutive items in the same section
      if (index > 0) {
        const prevItem = section.data[index - 1];
        const gap = getMinuteGap(prevItem.created_at, item.created_at);
        if (gap > 0) {
          nodes.push(
            <View key={`gap-${item.id}`} style={styles.timeGapContainer}>
              <View style={styles.timeGapLine} />
              <Text style={styles.timeGapText}>
                {t("history.timeGap", { minutes: Math.abs(gap) })}
              </Text>
              <View style={styles.timeGapLine} />
            </View>,
          );
        }
      }

      nodes.push(
        <HistoryCard
          key={item.id}
          item={item}
          onArchive={onArchive}
          focusSessionId={focusSessionId}
          scrollViewRef={scrollViewRef}
          currentOffsetRef={currentOffsetRef}
        />,
      );
      return <>{nodes}</>;
    },
    [onArchive, focusSessionId, scrollViewRef, currentOffsetRef],
  );

  const renderSectionHeader = useCallback(
    ({ section }: { section: DateSection }) => (
      <View style={styles.sectionHeaderContainer}>
        <Text style={styles.sectionHeaderText}>{section.title}</Text>
      </View>
    ),
    [],
  );

  if (loading) {
    return (
      <>
        <OriginalVideosPending />
        <View style={styles.loadingContainer}>
          <ActivityIndicator size="large" color={T.electricBlue} />
          <Text style={styles.loadingText}>
            {t("historyList.loadingHistory")}
          </Text>
        </View>
      </>
    );
  }

  return (
    <>
      <OriginalVideosPending />
      <MergeBanner />
      <ProcessingSection items={pendingItems} onRefresh={onRefresh} />
      <SectionList
        sections={sections}
        keyExtractor={(item) => item.id.toString()}
        renderItem={renderItem}
        renderSectionHeader={renderSectionHeader}
        contentContainerStyle={styles.list}
        ListEmptyComponent={
          pendingItems.length === 0 ? (
            <View style={styles.emptyContainer}>
              <Text style={styles.emptyIcon}>📋</Text>
              <Text style={styles.emptyTitle}>
                {t("historyList.noHistory")}
              </Text>
              <Text style={styles.emptySubtitle}>
                {t("historyList.noHistoryDesc")}
              </Text>
            </View>
          ) : null
        }
        scrollEnabled={false}
        stickySectionHeadersEnabled={false}
      />
    </>
  );
}

const styles = StyleSheet.create({
  list: {
    paddingHorizontal: 0,
    paddingBottom: 40,
    gap: 12,
  },

  // Section headers (date grouping)
  sectionHeaderContainer: {
    paddingVertical: 8,
    paddingHorizontal: 4,
    marginTop: 8,
  },
  sectionHeaderText: {
    fontSize: 12,
    fontWeight: "800",
    color: T.inkMuted,
    letterSpacing: 1.0,
    textTransform: "uppercase",
  },

  // Time gap between sessions
  timeGapContainer: {
    flexDirection: "row",
    alignItems: "center",
    paddingVertical: 6,
    paddingHorizontal: 16,
    gap: 10,
  },
  timeGapLine: {
    flex: 1,
    height: 1,
    backgroundColor: T.borderStrong,
  },
  timeGapText: {
    fontSize: 11,
    color: T.inkMuted,
    fontWeight: "700",
  },
  card: {
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 8,
    elevation: 2,
  },
  focusedCard: {
    borderColor: T.electricBlue,
    borderWidth: 2,
  },
  cardHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
    gap: 8,
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 8,
    flex: 1,
  },
  sessionLabel: {
    fontSize: 17,
    fontWeight: "800",
    letterSpacing: -0.3,
    flexShrink: 1,
  },

  // Badges
  badge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
  },
  badgeText: {
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 0.4,
    textTransform: "uppercase",
  },

  // Date
  dateAndFatigueRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 10,
    marginTop: 2,
    gap: 8,
  },
  date: {
    fontSize: 12,
    fontWeight: "600",
    flexShrink: 1,
  },
  fatiguePill: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
    borderWidth: 1,
  },
  fatiguePillText: {
    fontSize: 11,
    fontWeight: "800",
  },

  // Preview text (collapsed)
  previewText: {
    fontSize: 15,
    lineHeight: 23,
    fontWeight: "500",
  },

  // Content body (expanded)
  contentBody: {
    marginTop: 4,
  },

  // Expand toggle
  expandHint: {
    fontSize: 12,
    fontWeight: "800",
    marginTop: 10,
    textAlign: "center",
    letterSpacing: 0.3,
  },

  // Injury supplement section
  injurySection: {
    marginTop: 16,
    borderTopWidth: 1,
    paddingTop: 12,
  },
  injurySectionHeader: {
    marginBottom: 8,
  },
  injurySectionLabel: {
    fontSize: 15,
    fontWeight: "800",
    color: T.heartRose,
  },

  // Failed/Pending banners
  failedBanner: {
    backgroundColor: T.statusExhaustedBg,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
    borderRadius: 10,
    padding: 12,
    marginTop: 6,
  },
  failedText: {
    fontSize: 13,
    fontWeight: "600",
    color: T.statusExhausted,
    lineHeight: 18,
  },

  // Retry button
  retryBtn: {
    marginTop: 10,
    backgroundColor: T.statusFatiguedBg,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.statusFatiguedBorder,
    paddingVertical: 12,
    alignItems: "center",
    justifyContent: "center",
  },
  retryBtnText: {
    fontSize: 14,
    fontWeight: "800",
    color: T.statusFatigued,
    letterSpacing: 0.3,
  },

  // Create Guided Video button
  hardsubBtn: {
    marginTop: 10,
    backgroundColor: T.electricBlueBg,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    paddingVertical: 12,
    alignItems: "center",
    justifyContent: "center",
  },
  hardsubBtnText: {
    fontSize: 14,
    fontWeight: "800",
    color: T.electricBlue,
    letterSpacing: 0.3,
  },
  pendingBanner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: T.statusWarningBg,
    borderWidth: 1,
    borderColor: T.statusWarningBorder,
    borderRadius: 10,
    padding: 12,
    marginTop: 6,
  },
  pendingText: {
    fontSize: 13,
    fontWeight: "600",
    color: T.statusWarning,
    lineHeight: 18,
    flexShrink: 1,
  },

  // Watch button
  watchBtn: {
    marginTop: 14,
    backgroundColor: T.volt,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.voltBorder,
    paddingVertical: 14,
    alignItems: "center",
    justifyContent: "center",
  },
  watchBtnText: {
    fontSize: 14,
    fontWeight: "900",
    color: T.voltInk,
    letterSpacing: 0.5,
    textTransform: "uppercase",
  },

  // Download buttons
  downloadRow: {
    flexDirection: "row",
    gap: 8,
    marginTop: 4,
  },
  downloadBtn: {
    flex: 1,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    borderRadius: 10,
    paddingVertical: 10,
    alignItems: "center",
    justifyContent: "center",
  },
  downloadBtnText: {
    fontSize: 12,
    fontWeight: "800",
    color: T.electricBlue,
  },
  downloadBtnGuided: {
    backgroundColor: T.surfaceMuted,
    borderColor: T.voltBorder,
  },
  downloadBtnGuidedText: {
    color: T.oliveAccent,
  },
  downloadBtnEncoded: {
    backgroundColor: T.surfaceContainer,
    borderColor: T.borderStrong,
  },
  downloadBtnEncodedText: {
    color: T.inkSecondary,
  },

  // Highlights
  highlightSection: {
    marginTop: 14,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.border,
    padding: 14,
  },
  highlightSectionTitle: {
    fontSize: 14,
    fontWeight: "800",
    color: T.ink,
    marginBottom: 10,
  },
  highlightVariantList: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  highlightVariantBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: T.surface,
    borderRadius: 10,
    borderWidth: 1,
    paddingHorizontal: 12,
    paddingVertical: 9,
  },
  highlightVariantEmoji: {
    fontSize: 15,
  },
  highlightVariantLabel: {
    fontSize: 12,
    fontWeight: "800",
    flexShrink: 1,
  },
  highlightVariantDuration: {
    fontSize: 11,
    fontWeight: "700",
    color: T.inkMuted,
    marginLeft: 2,
  },
  saveAllBtn: {
    marginTop: 10,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    borderRadius: 10,
    paddingVertical: 10,
    alignItems: "center",
  },
  saveAllBtnText: {
    fontSize: 13,
    fontWeight: "800",
    color: T.electricBlue,
  },
  createHighlightBtn: {
    backgroundColor: T.electricBlue,
    borderRadius: 10,
    paddingVertical: 11,
    alignItems: "center",
  },
  createHighlightBtnText: {
    fontSize: 14,
    fontWeight: "800",
    color: "#FFF",
  },
  regenerateBtn: {
    marginTop: 8,
    alignItems: "center",
    paddingVertical: 6,
  },
  regenerateBtnText: {
    fontSize: 12,
    fontWeight: "700",
    color: T.electricBlue,
  },
  highlightPollingBanner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    paddingVertical: 8,
  },
  highlightPollingText: {
    fontSize: 12,
    fontWeight: "600",
    color: T.electricBlue,
    flex: 1,
    flexShrink: 1,
  },

  // Loading
  loadingContainer: {
    alignItems: "center",
    paddingVertical: 40,
    gap: 12,
  },
  loadingText: {
    fontSize: 14,
    fontWeight: "600",
    color: T.inkMuted,
  },

  // Empty state
  emptyContainer: {
    alignItems: "center",
    paddingVertical: 48,
    paddingHorizontal: 24,
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    gap: 8,
  },
  emptyIcon: {
    fontSize: 44,
    marginBottom: 4,
  },
  emptyTitle: {
    fontSize: 18,
    fontWeight: "800",
    color: T.ink,
  },
  emptySubtitle: {
    fontSize: 14,
    color: T.inkMuted,
    textAlign: "center",
    lineHeight: 20,
  },
});
