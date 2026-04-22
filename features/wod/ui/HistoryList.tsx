import React, { useCallback, useEffect, useRef, useState } from "react";
import { t } from "@/features/i18n";
import { router } from "expo-router";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import * as FileSystem from "expo-file-system/legacy";
import * as MediaLibrary from "expo-media-library";

import { MarkdownText } from "@/components/ui/MarkdownText";
import { useColorScheme } from "@/hooks/use-color-scheme";
import { useProfileId } from "@/store/useProfileStore";
import { useVideoQueue } from "@/store/useVideoQueue";
import { useShallow } from "zustand/shallow";
import {
  fetchVideoDownloadURL,
  generateHighlight,
  fetchHighlightResults,
  fetchHighlightDownloadURL,
  retryAnalysis,
  generateHardSub,
} from "../api";
import { AnalysisResult, HighlightResult, fetchAnalysisHistory } from "../history";
import { HighlightVideoPlayer } from "./HighlightVideoPlayer";

/**
 * Extracts a human-readable label from session_id.
 * New format: "WOD-20260401-01JQXYZ..." → "WOD"
 * Old format: "P1-WOD-2026-04-01-14-30" → "WOD"
 */
function formatSessionLabel(sessionId: string): string {
  const parts = sessionId.split("-");
  if (parts.length === 0) return t("common.workout");
  // First segment is the workout type (WOD, STRENGTH, etc.)
  const type = parts[0].toUpperCase();
  switch (type) {
    case "WOD":
      return "WOD";
    case "STRENGTH":
      return "Strength";
    case "CARDIO":
      return "Cardio";
    case "FLEXIBILITY":
      return "Flexibility";
    case "HIIT":
      return "HIIT";
    default:
      // Old format with P{id} prefix: skip to second part
      if (type.startsWith("P") && parts.length > 1) {
        return parts[1].toUpperCase();
      }
      return type;
  }
}

/** Badge config for analysis status */
function getStatusConfig(status: string) {
  switch (status) {
    case "COMPLETED":
      return { label: t("historyList.statusComplete"), color: "#30D158", bgColor: "rgba(48,209,88,0.15)" };
    case "FAILED":
      return { label: t("historyList.statusFailed"), color: "#FF453A", bgColor: "rgba(255,69,58,0.15)" };
    case "PENDING":
      return { label: t("historyList.statusPending"), color: "#FFD60A", bgColor: "rgba(255,214,10,0.15)" };
    default:
      return { label: status, color: "#A0A0A0", bgColor: "rgba(160,160,160,0.15)" };
  }
}

/** Badge config for analysis type */
function getTypeBadge(type: string) {
  switch (type) {
    case "injury_supplement":
      return { label: t("historyList.typeInjury"), color: "#FF6B6B", bgColor: "rgba(255,107,107,0.12)" };
    case "wod":
    default:
      return { label: t("historyList.typeWod"), color: "#64D2FF", bgColor: "rgba(100,210,255,0.12)" };
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
const HIGHLIGHT_VARIANT_CONFIG: Record<string, { emoji: string; color: string }> = {
  "Highlight Reel": { emoji: "🎬", color: "#BF5AF2" },
  "Best Forms": { emoji: "🏆", color: "#30D158" },
  "Areas for Improvement": { emoji: "📈", color: "#FF9F0A" },
  "Key Moments": { emoji: "⭐", color: "#64D2FF" },
};

const HIGHLIGHT_POLL_INTERVAL_MS = 5_000;
const HIGHLIGHT_POLL_TIMEOUT_MS = 5 * 60 * 1_000;

function HistoryCard({ item }: { item: AnalysisResult }) {
  const [expanded, setExpanded] = useState(false);
  const [downloading, setDownloading] = useState<string | null>(null); // 'merged' | 'hardsubbed' | 'encoded'
  const [retrying, setRetrying] = useState(false);
  const [generatingHardsub, setGeneratingHardsub] = useState(false);
  const scheme = useColorScheme() ?? "light";
  const isDark = scheme === "dark";
  const statusConfig = getStatusConfig(item.status);
  const typeBadge = getTypeBadge(item.analysis_type);

  const hasOutput = item.output && item.output.trim().length > 0;
  const hasInjuryOutput = item.injury_output && item.injury_output.trim().length > 0;
  const isCompleted = item.status === "COMPLETED";
  const availableVideos = item.available_videos ?? [];
  const hasHighlightSegments = !!(item.highlight_segments && item.highlight_segments.trim().length > 0);

  // ---------------------
  // Highlight state
  // ---------------------
  const [highlights, setHighlights] = useState<HighlightResult[]>([]);
  const [highlightLoading, setHighlightLoading] = useState(false);
  const [highlightPolling, setHighlightPolling] = useState(false);
  const [highlightDownloading, setHighlightDownloading] = useState<number | null>(null);
  const [playingHighlight, setPlayingHighlight] = useState<{ hl: HighlightResult; url: string } | null>(null);
  const [loadingPlayId, setLoadingPlayId] = useState<number | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const pollStartRef = useRef<number>(0);

  // On mount, fetch existing highlight results (if any)
  useEffect(() => {
    if (isCompleted && item.analysis_type === "wod") {
      fetchHighlightResults(item.session_id)
        .then((results) => setHighlights(results.filter((r) => r.status === "COMPLETED")))
        .catch(() => {});
    }
  }, [item.session_id, isCompleted, item.analysis_type]);

  // Cleanup polling on unmount
  useEffect(() => {
    return () => {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current);
    };
  }, []);

  const startPolling = useCallback(() => {
    setHighlightPolling(true);
    pollStartRef.current = Date.now();

    pollTimerRef.current = setInterval(async () => {
      try {
        const results = await fetchHighlightResults(item.session_id);
        const completed = results.filter((r) => r.status === "COMPLETED");
        const failed = results.filter((r) => r.status === "FAILED");

        if (completed.length > 0) {
          setHighlights(completed);
        }

        // Stop polling if all results are terminal or timeout
        const allTerminal = results.length > 0 && results.every((r) => r.status === "COMPLETED" || r.status === "FAILED");
        const timedOut = Date.now() - pollStartRef.current > HIGHLIGHT_POLL_TIMEOUT_MS;

        if (allTerminal || timedOut) {
          if (pollTimerRef.current) clearInterval(pollTimerRef.current);
          setHighlightPolling(false);
          if (timedOut && completed.length === 0) {
            Alert.alert(t("historyList.timeout"), t("historyList.timeoutDesc"));
          }
          if (failed.length > 0 && completed.length === 0) {
            Alert.alert(t("historyList.generationFailed"), t("historyList.generationFailedDesc"));
          }
        }
      } catch {
        // Keep polling on transient errors
      }
    }, HIGHLIGHT_POLL_INTERVAL_MS);
  }, [item.session_id]);

  const handleCreateHighlights = async () => {
    try {
      setHighlightLoading(true);
      await generateHighlight(item.session_id, item.profile_id ?? 0);
      startPolling();
    } catch (e: any) {
      Alert.alert(t("common.error"), e?.message || t("historyList.generationFailedDesc"));
    } finally {
      setHighlightLoading(false);
    }
  };

  const handleHighlightDownload = async (hl: HighlightResult) => {
    try {
      setHighlightDownloading(hl.id);
      const { download_url, filename } = await fetchHighlightDownloadURL(hl.id);
      const localUri = FileSystem.cacheDirectory + filename;
      const { uri } = await FileSystem.downloadAsync(download_url, localUri);

      Alert.alert(
        t("historyList.highlightDownloaded"),
        t("historyList.highlightReady", { title: hl.title, duration: Math.round(hl.duration_sec) }),
        [
          {
            text: t("historyList.saveToGallery"),
            onPress: async () => {
              try {
                const { status } = await MediaLibrary.requestPermissionsAsync();
                if (status !== "granted") {
                  Alert.alert(t("common.permissionRequired"), t("common.galleryPermission"));
                  return;
                }
                await MediaLibrary.saveToLibraryAsync(uri);
                Alert.alert(t("common.saved"), t("historyList.highlightSaved", { title: hl.title }));
              } catch {
                Alert.alert(t("common.error"), t("common.failedSaveGallery"));
              } finally {
                try { await FileSystem.deleteAsync(uri, { idempotent: true }); } catch {}
              }
            },
          },
          {
            text: t("common.delete"),
            style: "destructive",
            onPress: async () => {
              try { await FileSystem.deleteAsync(uri, { idempotent: true }); } catch {}
            },
          },
          { text: t("common.keep"), style: "cancel" },
        ]
      );
    } catch (e: any) {
      const msg = e?.message?.includes("404") ? t("historyList.highlightNotFound") : String(e);
      Alert.alert(t("historyList.downloadFailed"), msg);
    } finally {
      setHighlightDownloading(null);
    }
  };

  const handleSaveAllHighlights = async () => {
    const { status } = await MediaLibrary.requestPermissionsAsync();
    if (status !== "granted") {
      Alert.alert(t("common.permissionRequired"), t("common.galleryPermission"));
      return;
    }

    setHighlightDownloading(-1); // -1 = saving all
    let savedCount = 0;
    for (const hl of highlights) {
      try {
        const { download_url, filename } = await fetchHighlightDownloadURL(hl.id);
        const localUri = FileSystem.cacheDirectory + filename;
        const { uri } = await FileSystem.downloadAsync(download_url, localUri);
        await MediaLibrary.saveToLibraryAsync(uri);
        try { await FileSystem.deleteAsync(uri, { idempotent: true }); } catch {}
        savedCount++;
      } catch {
        // Continue saving remaining highlights
      }
    }
    setHighlightDownloading(null);
    Alert.alert(t("common.saved"), t("historyList.savedCount", { saved: savedCount, total: highlights.length }));
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
      const msg = e?.message?.includes("404") ? t("historyList.highlightNotFound") : String(e);
      Alert.alert(t("historyList.playbackFailed"), msg);
    } finally {
      setLoadingPlayId(null);
    }
  };

  const handleDownload = async (kind: "merged" | "hardsubbed" | "encoded") => {
    try {
      setDownloading(kind);
      const { download_url, filename } = await fetchVideoDownloadURL(item.session_id, item.profile_id ?? 0, kind);

      const localUri = FileSystem.cacheDirectory + filename;
      const { uri } = await FileSystem.downloadAsync(download_url, localUri);

      const kindLabel = kind === "hardsubbed" ? t("historyList.videoKindGuided") : kind === "encoded" ? t("historyList.videoKindEncoded") : t("historyList.videoKindOriginal");
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
                  Alert.alert(t("common.permissionRequired"), t("common.galleryPermission"));
                  return;
                }
                await MediaLibrary.saveToLibraryAsync(uri);
                Alert.alert(t("common.saved"), t("common.savedToGallery"));
              } catch (e) {
                Alert.alert(t("common.error"), t("common.failedSaveGallery"));
              } finally {
                // Clean up temp file
                try { await FileSystem.deleteAsync(uri, { idempotent: true }); } catch {}
              }
            },
          },
          {
            text: t("common.delete"),
            style: "destructive",
            onPress: async () => {
              try { await FileSystem.deleteAsync(uri, { idempotent: true }); } catch {}
            },
          },
          { text: t("common.keep"), style: "cancel" },
        ]
      );
    } catch (e: any) {
      const msg = e?.message?.includes("404") ? t("historyList.videoNotFound") : String(e);
      Alert.alert(t("historyList.downloadFailed"), msg);
    } finally {
      setDownloading(null);
    }
  };

  const cardBg = isDark ? "#2C2C2E" : "#F5F5F5";
  const cardBorder = isDark ? "#48484A" : "rgba(0,0,0,0.08)";
  const sessionColor = isDark ? "#FFFFFF" : "#1C1C1E";
  const dateColor = isDark ? "#D4D4D4" : "#6B6B6B";
  const previewColor = isDark ? "#FFFFFF" : "#333333";
  const expandColor = isDark ? "#64D2FF" : "#0A84FF";
  const hrDividerColor = isDark ? "rgba(255,107,107,0.2)" : "rgba(255,107,107,0.3)";
  const highlightSectionBg = isDark ? "rgba(191,90,242,0.08)" : "rgba(191,90,242,0.05)";

  return (
    <View style={[styles.card, { backgroundColor: cardBg, borderColor: cardBorder }]}>
      {/* Header Row */}
      <View style={styles.cardHeader}>
        <View style={styles.headerLeft}>
          <Text style={[styles.sessionLabel, { color: sessionColor }]}>
            {formatSessionLabel(item.session_id)}
          </Text>
          <View style={[styles.badge, { backgroundColor: typeBadge.bgColor }]}>
            <Text style={[styles.badgeText, { color: typeBadge.color }]}>
              {typeBadge.label}
            </Text>
          </View>
        </View>
        <View style={[styles.badge, { backgroundColor: statusConfig.bgColor }]}>
          <Text style={[styles.badgeText, { color: statusConfig.color }]}>
            {statusConfig.label}
          </Text>
        </View>
      </View>

      {/* Date */}
      <Text style={[styles.date, { color: dateColor }]}>{formatDate(item.created_at)}</Text>

      {/* Content */}
      {hasOutput && (
        <TouchableOpacity
          activeOpacity={0.8}
          onPress={() => setExpanded((p) => !p)}
        >
          {expanded ? (
            <View style={styles.contentBody}>
              <MarkdownText>{item.output}</MarkdownText>

              {/* Injury supplement output */}
              {hasInjuryOutput && (
               <View style={[styles.injurySection, { borderTopColor: hrDividerColor }]}>
                  <View style={styles.injurySectionHeader}>
                    <Text style={styles.injurySectionLabel}>
                      {t("historyList.injuryAnalysis")}
                    </Text>
                  </View>
                  <MarkdownText color="#FFB4B4">
                    {item.injury_output!}
                  </MarkdownText>
                </View>
              )}
            </View>
          ) : (
            <Text style={[styles.previewText, { color: previewColor }]} numberOfLines={4}>
              {stripMarkdown(item.output)}
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
            <ActivityIndicator size="small" color="#FF9F0A" />
          ) : (
            <Text style={styles.retryBtnText}>{t("historyList.retryAnalysis")}</Text>
          )}
        </TouchableOpacity>
      )}

      {/* Watch button — opens full workout player */}
      {isCompleted && item.analysis_type !== "injury_supplement" && (availableVideos.includes("merged") || availableVideos.includes("hardsubbed") || availableVideos.includes("encoded")) && (
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
          activeOpacity={0.8}
        >
          <Text style={styles.watchBtnText}>{t("historyList.watchWorkout")}</Text>
        </TouchableOpacity>
      )}

      {/* Download buttons */}
      {isCompleted && item.analysis_type !== "injury_supplement" && availableVideos.length > 0 && (
        <View style={styles.downloadRow}>
          {availableVideos.includes("merged") && (
            <TouchableOpacity
              style={styles.downloadBtn}
              onPress={() => handleDownload("merged")}
              disabled={downloading !== null}
            >
              {downloading === "merged" ? (
                <ActivityIndicator size="small" color="#fff" />
              ) : (
                <Text style={styles.downloadBtnText}>{t("historyList.videoBtn")}</Text>
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
                <ActivityIndicator size="small" color="#000" />
              ) : (
                <Text style={[styles.downloadBtnText, styles.downloadBtnGuidedText]}>{t("historyList.guidedBtn")}</Text>
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
                <ActivityIndicator size="small" color="#fff" />
              ) : (
                <Text style={[styles.downloadBtnText, styles.downloadBtnEncodedText]}>{t("historyList.encodedBtn")}</Text>
              )}
            </TouchableOpacity>
          )}
        </View>
      )}

      {/* Create Guided Video button — when completed but no hardsubbed version */}
      {isCompleted && item.profile_id && !availableVideos.includes("hardsubbed") && availableVideos.includes("merged") && (
        <TouchableOpacity
          style={[styles.hardsubBtn, generatingHardsub && { opacity: 0.5 }]}
          disabled={generatingHardsub}
          onPress={async () => {
            try {
              setGeneratingHardsub(true);
              await generateHardSub(item.session_id, item.profile_id!);
              Alert.alert(t("upload.success"), t("historyList.hardsubStarted"));
            } catch (err) {
              Alert.alert(t("common.error"), t("historyList.hardsubFailed"));
            } finally {
              setGeneratingHardsub(false);
            }
          }}
          activeOpacity={0.8}
        >
          {generatingHardsub ? (
            <ActivityIndicator size="small" color="#FFD60A" />
          ) : (
            <Text style={styles.hardsubBtnText}>{t("historyList.createHardsub")}</Text>
          )}
        </TouchableOpacity>
      )}

      {/* ==================== Highlights Section ==================== */}
      {isCompleted && item.analysis_type === "wod" && hasHighlightSegments && (
        <View style={[styles.highlightSection, { backgroundColor: highlightSectionBg }]}>
          <Text style={styles.highlightSectionTitle}>{t("historyList.highlights")}</Text>

          {/* Show existing completed highlights */}
          {highlights.length > 0 && (
            <>
              <View style={styles.highlightVariantList}>
                {highlights.map((hl) => {
                  const variantCfg = HIGHLIGHT_VARIANT_CONFIG[hl.title] ?? { emoji: "🎥", color: "#A0A0A0" };
                  const isLoadingPlay = loadingPlayId === hl.id;
                  const isPlaying = playingHighlight?.hl.id === hl.id;
                  return (
                    <TouchableOpacity
                      key={hl.id}
                      style={[
                        styles.highlightVariantBtn,
                        { borderColor: variantCfg.color + "40" },
                        isPlaying && { borderColor: variantCfg.color, backgroundColor: variantCfg.color + "20" },
                      ]}
                      onPress={() => handlePlayHighlight(hl)}
                      onLongPress={() => handleHighlightDownload(hl)}
                      disabled={loadingPlayId !== null && loadingPlayId !== hl.id}
                    >
                      {isLoadingPlay ? (
                        <ActivityIndicator size="small" color={variantCfg.color} />
                      ) : (
                        <>
                          <Text style={styles.highlightVariantEmoji}>
                            {isPlaying ? "⏸" : "▶"}
                          </Text>
                          <Text style={[styles.highlightVariantLabel, { color: variantCfg.color }]}>
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
                    <ActivityIndicator size="small" color="#BF5AF2" />
                  ) : (
                    <Text style={styles.saveAllBtnText}>
                      {t("historyList.saveAllGallery", { count: highlights.length })}
                    </Text>
                  )}
                </TouchableOpacity>
              )}
            </>
          )}

          {/* Polling indicator */}
          {highlightPolling && highlights.length === 0 && (
            <View style={styles.highlightPollingBanner}>
              <ActivityIndicator size="small" color="#BF5AF2" />
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
                <Text style={styles.createHighlightBtnText}>{t("historyList.createHighlights")}</Text>
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
                <ActivityIndicator size="small" color="#BF5AF2" />
              ) : (
                <Text style={styles.regenerateBtnText}>{t("historyList.regenerate")}</Text>
              )}
            </TouchableOpacity>
          )}
        </View>
      )}

      {!hasOutput && item.status === "PENDING" && (
        <View style={styles.pendingBanner}>
          <ActivityIndicator size="small" color="#FFD60A" />
          <Text style={styles.pendingText}>{t("historyList.analysisInProgress")}</Text>
        </View>
      )}
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

export function useHistoryData() {
  const profileId = useProfileId();
  const [data, setData] = useState<AnalysisResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadData = useCallback(async () => {
    if (!profileId) {
      setData([]);
      setLoading(false);
      setRefreshing(false);
      return;
    }
    try {
      const history = await fetchAnalysisHistory(profileId);
      setData(history);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [profileId]);

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

  return { data, loading, refreshing, onRefresh };
}

// ---------------------
// Processing Section
// ---------------------

function ProcessingSection({ items }: { items: AnalysisResult[] }) {
  const scheme = useColorScheme() ?? "light";
  const isDark = scheme === "dark";

  // Pull ALL non-completed items from the local video queue
  // useShallow prevents infinite re-renders from .filter() creating new array refs
  const queueItems = useVideoQueue(
    useShallow((s) =>
      s.items.filter((i) => i.status !== "UPLOADED")
    )
  );

  if (items.length === 0 && queueItems.length === 0) return null;

  const processingBg = isDark ? "rgba(255,214,10,0.06)" : "rgba(255,214,10,0.08)";
  const processingBorder = isDark ? "rgba(255,214,10,0.15)" : "rgba(255,214,10,0.2)";
  const textColor = isDark ? "#FFD60A" : "#B8960A";
  const subtextColor = isDark ? "#AAA" : "#777";

  return (
    <View style={processingStyles.container}>
      <View style={processingStyles.headerRow}>
        <Text style={[processingStyles.sectionTitle, { color: textColor }]}>
          {t("historyList.processingSection")}
        </Text>
        <ActivityIndicator size="small" color="#FFD60A" />
      </View>

      {/* Server-side PENDING analysis results */}
      {items.map((item) => (
        <View
          key={item.id}
          style={[processingStyles.card, { backgroundColor: processingBg, borderColor: processingBorder }]}
        >
          <View style={processingStyles.cardRow}>
            <Text style={processingStyles.icon}>🔬</Text>
            <View style={processingStyles.cardContent}>
              <Text style={[processingStyles.cardTitle, { color: isDark ? "#FFF" : "#1C1C1E" }]}>
                {formatSessionLabel(item.session_id)}
              </Text>
              <Text style={[processingStyles.cardSubtitle, { color: subtextColor }]}>
                {t("historyList.aiAnalyzing")}
              </Text>
            </View>
            <View style={[processingStyles.statusDot, { backgroundColor: "#FFD60A" }]} />
          </View>
          <Text style={[processingStyles.cardDate, { color: subtextColor }]}>
            {formatDate(item.created_at)}
          </Text>
        </View>
      ))}

      {/* Local video queue items (all stages) */}
      {queueItems.map((item) => {
        const pct = Math.round(item.progress * 100);
        const hasProgress = item.status === "ENCODING" || item.status === "UPLOADING";

        // Icon and label per status
        let icon = "📹";
        let subtitle = "";
        let dotColor = "#A0A0A0";
        switch (item.status) {
          case "RECORDED":
            icon = "🎬";
            subtitle = t("historyList.waitingToEncode");
            dotColor = "#A0A0A0";
            break;
          case "ENCODING":
            icon = "⚙️";
            subtitle = t("historyList.encodingProgress", { percent: pct });
            dotColor = "#FF9F0A";
            break;
          case "ENCODED":
            icon = "✅";
            subtitle = t("historyList.readyToUpload");
            dotColor = "#30D158";
            break;
          case "UPLOADING":
            icon = "☁️";
            subtitle = t("historyList.uploadingProgress", { percent: pct });
            dotColor = "#64D2FF";
            break;
        }

        return (
          <View
            key={item.id}
            style={[processingStyles.card, { backgroundColor: processingBg, borderColor: processingBorder }]}
          >
            <View style={processingStyles.cardRow}>
              <Text style={processingStyles.icon}>{icon}</Text>
              <View style={processingStyles.cardContent}>
                <Text style={[processingStyles.cardTitle, { color: isDark ? "#FFF" : "#1C1C1E" }]}>
                  {formatSessionLabel(item.sessionId)}
                </Text>
                <Text style={[processingStyles.cardSubtitle, { color: subtextColor }]}>
                  {subtitle}
                </Text>
              </View>
              <View style={[processingStyles.statusDot, { backgroundColor: dotColor }]} />
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
    gap: 8,
    marginBottom: 16,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 4,
  },
  sectionTitle: {
    fontSize: 14,
    fontWeight: "700",
    letterSpacing: 0.3,
  },
  card: {
    borderRadius: 14,
    padding: 14,
    borderWidth: 1,
    gap: 8,
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
    fontWeight: "700",
  },
  cardSubtitle: {
    fontSize: 12,
  },
  cardDate: {
    fontSize: 11,
    marginLeft: 30,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  progressBarBg: {
    height: 3,
    backgroundColor: "rgba(255,255,255,0.08)",
    borderRadius: 2,
    overflow: "hidden",
    marginLeft: 30,
  },
  progressBarFill: {
    height: "100%",
    borderRadius: 2,
  },
});

// ---------------------
// HistoryList (presentation)
// ---------------------

interface HistoryListProps {
  data: AnalysisResult[];
  loading: boolean;
}

export function HistoryList({ data, loading }: HistoryListProps) {
  // Split pending items into processing section vs completed list
  const pendingItems = data.filter((item) => item.status === "PENDING");
  const nonPendingItems = data.filter((item) => item.status !== "PENDING");

  const renderItem = useCallback(
    ({ item }: { item: AnalysisResult }) => <HistoryCard item={item} />,
    []
  );

  if (loading) {
    return (
      <View style={styles.loadingContainer}>
        <ActivityIndicator size="large" color="#64D2FF" />
        <Text style={styles.loadingText}>{t("historyList.loadingHistory")}</Text>
      </View>
    );
  }

  return (
    <>
      <ProcessingSection items={pendingItems} />
      <FlatList
        data={nonPendingItems}
        keyExtractor={(item) => item.id.toString()}
        renderItem={renderItem}
        contentContainerStyle={styles.list}
        ListEmptyComponent={
          pendingItems.length === 0 ? (
            <View style={styles.emptyContainer}>
              <Text style={styles.emptyIcon}>📋</Text>
              <Text style={styles.emptyTitle}>{t("historyList.noHistory")}</Text>
              <Text style={styles.emptySubtitle}>
                {t("historyList.noHistoryDesc")}
              </Text>
            </View>
          ) : null
        }
        scrollEnabled={false}
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

  // Card
  card: {
    borderRadius: 16,
    padding: 16,
    borderWidth: 1,
  },
  cardHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
  },
  headerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  sessionLabel: {
    fontSize: 17,
    fontWeight: "700",
  },

  // Badges
  badge: {
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderRadius: 6,
  },
  badgeText: {
    fontSize: 11,
    fontWeight: "600",
    letterSpacing: 0.3,
  },

  // Date
  date: {
    fontSize: 12,
    marginBottom: 12,
    marginTop: 2,
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
    fontWeight: "600",
    marginTop: 10,
    textAlign: "center",
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
    fontWeight: "700",
    color: "#FF6B6B",
  },

  // Failed/Pending banners
  failedBanner: {
    backgroundColor: "rgba(255,69,58,0.1)",
    borderRadius: 8,
    padding: 12,
    marginTop: 4,
  },
  failedText: {
    fontSize: 13,
    color: "#FF453A",
    lineHeight: 18,
  },

  // Retry button
  retryBtn: {
    marginTop: 10,
    backgroundColor: "rgba(255,159,10,0.12)",
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "rgba(255,159,10,0.3)",
    paddingVertical: 12,
    alignItems: "center",
    justifyContent: "center",
  },
  retryBtnText: {
    fontSize: 14,
    fontWeight: "700",
    color: "#FF9F0A",
    letterSpacing: 0.3,
  },

  // Create Guided Video button
  hardsubBtn: {
    marginTop: 10,
    backgroundColor: "rgba(255,214,10,0.12)",
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "rgba(255,214,10,0.3)",
    paddingVertical: 12,
    alignItems: "center",
    justifyContent: "center",
  },
  hardsubBtnText: {
    fontSize: 14,
    fontWeight: "700",
    color: "#FFD60A",
    letterSpacing: 0.3,
  },
  pendingBanner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: "rgba(255,214,10,0.08)",
    borderRadius: 8,
    padding: 12,
    marginTop: 4,
  },
  pendingText: {
    fontSize: 13,
    color: "#FFD60A",
    lineHeight: 18,
  },

  // Watch button
  watchBtn: {
    marginTop: 14,
    backgroundColor: "rgba(100,210,255,0.12)",
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "rgba(100,210,255,0.3)",
    paddingVertical: 14,
    alignItems: "center",
    justifyContent: "center",
  },
  watchBtnText: {
    fontSize: 15,
    fontWeight: "700",
    color: "#64D2FF",
    letterSpacing: 0.3,
  },

  // Download buttons
  downloadRow: {
    flexDirection: "row",
    gap: 10,
    marginTop: 12,
  },
  downloadBtn: {
    flex: 1,
    backgroundColor: "rgba(100,210,255,0.15)",
    borderRadius: 10,
    paddingVertical: 10,
    alignItems: "center",
    justifyContent: "center",
  },
  downloadBtnText: {
    fontSize: 13,
    fontWeight: "700",
    color: "#64D2FF",
  },
  downloadBtnGuided: {
    backgroundColor: "rgba(255,214,10,0.12)",
  },
  downloadBtnGuidedText: {
    color: "#FFD60A",
  },
  downloadBtnEncoded: {
    backgroundColor: "rgba(160,160,160,0.15)",
  },
  downloadBtnEncodedText: {
    color: "#A0A0A0",
  },

  // Highlights
  highlightSection: {
    marginTop: 14,
    borderRadius: 12,
    padding: 14,
  },
  highlightSectionTitle: {
    fontSize: 14,
    fontWeight: "700",
    color: "#BF5AF2",
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
    backgroundColor: "rgba(191,90,242,0.1)",
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
    fontWeight: "700",
  },
  highlightVariantDuration: {
    fontSize: 11,
    color: "#888",
    marginLeft: 2,
  },
  saveAllBtn: {
    marginTop: 10,
    backgroundColor: "rgba(191,90,242,0.15)",
    borderRadius: 10,
    paddingVertical: 10,
    alignItems: "center",
  },
  saveAllBtnText: {
    fontSize: 13,
    fontWeight: "700",
    color: "#BF5AF2",
  },
  createHighlightBtn: {
    backgroundColor: "#BF5AF2",
    borderRadius: 10,
    paddingVertical: 11,
    alignItems: "center",
  },
  createHighlightBtnText: {
    fontSize: 14,
    fontWeight: "700",
    color: "#FFF",
  },
  regenerateBtn: {
    marginTop: 8,
    alignItems: "center",
    paddingVertical: 6,
  },
  regenerateBtnText: {
    fontSize: 12,
    fontWeight: "600",
    color: "#BF5AF2",
  },
  highlightPollingBanner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    paddingVertical: 8,
  },
  highlightPollingText: {
    fontSize: 12,
    color: "#BF5AF2",
    flex: 1,
  },

  // Loading
  loadingContainer: {
    alignItems: "center",
    paddingVertical: 40,
    gap: 12,
  },
  loadingText: {
    fontSize: 14,
    color: "#888",
  },

  // Empty state
  emptyContainer: {
    alignItems: "center",
    paddingVertical: 60,
    gap: 8,
  },
  emptyIcon: {
    fontSize: 48,
    marginBottom: 8,
  },
  emptyTitle: {
    fontSize: 18,
    fontWeight: "700",
    color: "#FFF",
  },
  emptySubtitle: {
    fontSize: 14,
    color: "#888",
    textAlign: "center",
  },
});
