import { IconSymbol } from "@/components/ui/icon-symbol";
import { TacticalCard, VoltButton } from "@/components/ui/TacticalBento";
import {
  animateTacticalLayout,
  useReduceMotionEnabled,
} from "@/constants/motion";
import { Fonts, TacticalTheme } from "@/constants/theme";
import { validateStoredToken } from "@/features/auth/jwt";
import { useAuthStore } from "@/features/auth/useAuthStore";
import {
  getRegisteredBleDevice,
  setRegisteredBleDevice,
  type RegisteredBleDevice,
} from "@/features/health/useBleHeartRate";
import { t, useLocale } from "@/features/i18n";
import { supports4K30Fps } from "@/features/video/cameraCapability";
import {
  fetchMovementGroups,
  fetchPreWodAdvice,
  fetchRelatedWods,
  parseWorkoutImage,
  type MovementGroup,
  type PreWodAdviceResponse,
} from "@/features/wod/api";
import { PreWodStrategyCard } from "@/features/wod/ui/PreWodStrategyCard";
import { RelatedWodsCard } from "@/features/wod/ui/RelatedWodsCard";
import {
  extractCatalogMovementsFromText,
  parseRecentWorkoutSetup,
  RECENT_WORKOUT_SETUP_KEY,
  RECENT_WORKOUT_TTL_MS,
  WORKOUT_TYPES,
  type RecentWorkoutSetup,
  type WorkoutType,
} from "@/features/wod/workoutType";
import { useActiveProfile } from "@/store/useProfileStore";
import { clearRecentSetup, createSetupSnapshot, loadRecentSetup, saveSetupForLater, serializeSetupSnapshot, verifyRecentSetup, type RecentSetupSnapshot, type SetupScope } from "@/features/wod/recentWorkoutSetup";
import { subscribeHistoryChanges } from "@/features/wod/archiveStates";
import AsyncStorage from "@react-native-async-storage/async-storage";
import * as ImagePicker from "expo-image-picker";
import * as ExpoRouter from "expo-router";
import { router, useFocusEffect } from "expo-router";
import { StatusBar } from "expo-status-bar";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  AppState,
  Alert,
  Animated,
  BackHandler,
  GestureResponderEvent,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useCameraDevice } from "react-native-vision-camera";

const T = TacticalTheme.light;

type InputMethod = "text" | "photo" | "movements";

const VIDEO_PREFS_KEY = "wod_video_preferences";
const ENV_ANALYSIS_DEFAULT_RESET_KEY = "wod_env_analysis_default_reset_v1";
const ALL_FILTER = "All";

export {
  parseRecentWorkoutSetup,
  RECENT_WORKOUT_SETUP_KEY,
  RECENT_WORKOUT_TTL_MS,
  type RecentWorkoutSetup,
};

interface VideoPreferences {
  onDeviceAi: boolean;
  environmentObservation: boolean;
  environmentAnalysis: boolean;
  observationIntervalSeconds: 120 | 300 | 600;
  showSkeleton: boolean;
  lowFps: boolean;
  skipCompression: boolean;
  serialUpload: boolean;
  continuousRecording: boolean;
  h10MemoryRecording: boolean;
  resolution: "480p" | "720p" | "1080p" | "2160p";
  landscapeMode: boolean;
  autoRecord: boolean;
  zoomMode: boolean;
  aspectRatio: "4:3" | "16:9";
}

function getDefaultVideoPrefs(): VideoPreferences {
  const isAndroid = Platform.OS === "android";
  return {
    onDeviceAi: false,
    environmentObservation: false,
    environmentAnalysis: false,
    observationIntervalSeconds: 300,
    showSkeleton: !isAndroid,
    lowFps: isAndroid,
    skipCompression: isAndroid,
    serialUpload: isAndroid,
    continuousRecording: false,
    h10MemoryRecording: false,
    resolution: "720p",
    landscapeMode: false,
    autoRecord: true,
    zoomMode: false,
    aspectRatio: "16:9",
  };
}

// Category icons for the card list (must match icon-symbol.tsx MAPPING keys)
const CATEGORY_ICONS: Record<string, string> = {
  Barbell: "dumbbell.fill",
  "Dumbbell & Kettlebell": "dumbbell.fill",
  Gymnastics: "figure.run",
  "Bodyweight & Plyo": "flame.fill",
  Cardio: "figure.run",
  Surfing: "figure.run",
  Yoga: "figure.run",
  Custom: "plus.circle.fill",
};

function getCategoryIcon(category: string): string {
  return CATEGORY_ICONS[category] || "dumbbell.fill";
}

export default function WorkoutSetup() {
  useLocale();
  const reduceMotion = useReduceMotionEnabled();
  const routeParams =
    typeof ExpoRouter.useLocalSearchParams === "function"
      ? ExpoRouter.useLocalSearchParams<{ mode?: string }>()
      : {};
  const requestedMode =
    routeParams?.mode === "text"
      ? "text"
      : routeParams?.mode === "photo"
        ? "photo"
        : null;

  const [workoutType, setWorkoutType] = useState<WorkoutType>("wod");
  const activeProfile = useActiveProfile();
  const ownerUserId = useAuthStore(s => s.userId);
  const setupScope: SetupScope | undefined = ownerUserId && activeProfile ? { ownerUserId, profileId: activeProfile.id } : undefined;
  const setupScopeKey = setupScope ? `${setupScope.ownerUserId}:${setupScope.profileId}` : "";
  const currentSetupScope = useRef(setupScopeKey);
  currentSetupScope.current = setupScopeKey;
  const recentRequest = useRef(0);
  const appliedRecentSource = useRef<RecentSetupSnapshot | undefined>(undefined);
  const scanRequest = useRef(0);

  // Video preferences (persisted to AsyncStorage)
  const [videoPrefs, setVideoPrefs] = useState<VideoPreferences>(
    getDefaultVideoPrefs(),
  );
  const [prefsLoaded, setPrefsLoaded] = useState(false);
  const [advancedExpanded, setAdvancedExpanded] = useState(false);

  // Movements
  const [movementGroups, setMovementGroups] = useState<MovementGroup[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [customMovements, setCustomMovements] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  // Search + filter
  const [searchText, setSearchText] = useState("");
  const [activeFilter, setActiveFilter] = useState(ALL_FILTER);

  // Setup state + Verify Workout confirmation popup
  const [verifyVisible, setVerifyVisible] = useState(false);
  const popupAnim = useRef(new Animated.Value(0)).current;
  const [inputMethod, setInputMethod] = useState<InputMethod>(
    requestedMode ?? "photo",
  );
  const [wodDescription, setWodDescription] = useState("");
  const [rawText, setRawText] = useState("");
  const [scanning, setScanning] = useState(false);
  const invalidateScan = useCallback(() => {
    setScanning(false);
    return ++scanRequest.current;
  }, []);
  const [registeredBleDevice, setRegisteredBleDeviceState] =
    useState<RegisteredBleDevice | null>(null);
  const [recentWorkoutSetup, setRecentWorkoutSetup] =
    useState<RecentSetupSnapshot | null>(null);

  useEffect(() => {
    if (requestedMode) {
      setInputMethod(requestedMode);
    }
  }, [requestedMode]);

  useEffect(() => {
    if (!verifyVisible) {
      popupAnim.setValue(0);
      return;
    }
    if (reduceMotion) {
      popupAnim.setValue(1);
      return;
    }
    Animated.timing(popupAnim, {
      toValue: 1,
      duration: 180,
      useNativeDriver: true,
    }).start();
  }, [verifyVisible, reduceMotion, popupAnim]);

  useEffect(() => {
    if (!verifyVisible) return;
    const sub = BackHandler.addEventListener("hardwareBackPress", () => {
      setVerifyVisible(false);
      return true;
    });
    return () => sub.remove();
  }, [verifyVisible]);

  // Load persisted video preferences, registered BLE HR sensor & recent workout setup
  useEffect(() => {
    Promise.all([
      AsyncStorage.getItem(VIDEO_PREFS_KEY),
      AsyncStorage.getItem(ENV_ANALYSIS_DEFAULT_RESET_KEY),
      getRegisteredBleDevice(),
    ])
      .then(([raw, migrated, bleDevice]) => {
        setRegisteredBleDeviceState(bleDevice);
        if (raw) {
          try {
            const saved = JSON.parse(raw) as Partial<VideoPreferences>;
            // Posture feedback is hidden from settings; ignore any previously saved opt-in.
            // Also normalize observationIntervalSeconds to 120 | 300 | 600, defaulting to 300.
            const interval = [120, 300, 600].includes(
              saved.observationIntervalSeconds as any,
            )
              ? (saved.observationIntervalSeconds as 120 | 300 | 600)
              : 300;
            // One-time migration: reset environmentAnalysis default to false for existing installs
            if (!migrated) {
              AsyncStorage.setItem(
                ENV_ANALYSIS_DEFAULT_RESET_KEY,
                "true",
              ).catch(() => {});
            }
            setVideoPrefs((prev) => ({
              ...prev,
              ...saved,
              environmentAnalysis: false,
              observationIntervalSeconds: interval,
              onDeviceAi: false,
            }));
          } catch {
            // ignore parse errors, use defaults
          }
        } else if (!migrated) {
          AsyncStorage.setItem(ENV_ANALYSIS_DEFAULT_RESET_KEY, "true").catch(
            () => {},
          );
        }
      })
      .finally(() => setPrefsLoaded(true));
  }, []);

  useEffect(() => {
    invalidateScan();
    appliedRecentSource.current = undefined;
    setVerifyVisible(false); setWodDescription(""); setRawText(""); setSelectedMovements([]); setCustomMovements([]); setSessionAppearance("");
  }, [setupScopeKey, invalidateScan]);

  const refreshRecentSetup = useCallback(async () => {
    const request = ++recentRequest.current;
    setRecentWorkoutSetup(null);
    if (!ownerUserId || !activeProfile?.id) return;
    const scope = { ownerUserId, profileId: activeProfile.id };
    const key = `${ownerUserId}:${activeProfile.id}`;
    try {
      const applied = appliedRecentSource.current;
      if (applied?.origin === "recording") {
        let reusable = false;
        try { reusable = await verifyRecentSetup(applied, scope); } catch { /* Fail closed. */ }
        if (!reusable && request === recentRequest.current && currentSetupScope.current === key
          && appliedRecentSource.current?.snapshotId === applied.snapshotId) {
          invalidateScan();
          appliedRecentSource.current = undefined;
          setVerifyVisible(false); setWodDescription(""); setRawText(""); setSelectedMovements([]); setCustomMovements([]); setSessionAppearance("");
        }
      }
      const result = await loadRecentSetup(scope);
      if (request === recentRequest.current && currentSetupScope.current === key) setRecentWorkoutSetup(result ?? null);
    } catch { /* Record-derived reuse remains hidden until a fresh check succeeds. */ }
  }, [ownerUserId, activeProfile?.id, invalidateScan]);
  useFocusEffect(useCallback(() => {
    void refreshRecentSetup();
    return () => { ++recentRequest.current; setRecentWorkoutSetup(null); };
  }, [refreshRecentSetup]));
  useEffect(() => {
    const app = AppState.addEventListener("change", state => {
      ++recentRequest.current;
      setRecentWorkoutSetup(null);
      if (state === "active") void refreshRecentSetup();
    });
    const unsubscribe = subscribeHistoryChanges(pid => { if (pid === activeProfile?.id) void refreshRecentSetup(); });
    return () => { app.remove(); unsubscribe(); ++recentRequest.current; };
  }, [activeProfile?.id, refreshRecentSetup]);

  const backDevice = useCameraDevice("back");
  const is4KCapable = useMemo(() => supports4K30Fps(backDevice), [backDevice]);

  // Graceful degradation: if saved pref is 2160p but current device does not support 4K 30fps, fallback to 1080p
  useEffect(() => {
    if (prefsLoaded && !is4KCapable && videoPrefs.resolution === "2160p") {
      setVideoPrefs((prev) => ({ ...prev, resolution: "1080p" }));
    }
  }, [prefsLoaded, is4KCapable, videoPrefs.resolution]);

  // Load movement groups
  useEffect(() => {
    fetchMovementGroups()
      .then(setMovementGroups)
      .catch((error) => {
        console.error("Failed to load movement groups", error);
      })
      .finally(() => setLoading(false));
  }, []);

  // Related WODs anchor derivation & fetch
  const anchorInfo = useMemo(() => {
    let weightKg: number | undefined;
    if (wodDescription) {
      const weightRegex = /(\d+(?:\.\d+)?)\s*(kg|lb)/i;
      const weightMatch = wodDescription.match(weightRegex);
      if (weightMatch) {
        const val = parseFloat(weightMatch[1]);
        const unit = weightMatch[2].toLowerCase();
        if (unit === "lb") {
          weightKg = Math.round(val * 0.45359237 * 10) / 10;
        } else {
          weightKg = val;
        }
      }
    }
    if (selectedMovements.length > 0) {
      return { movement: selectedMovements[0], weightKg };
    }
    if (wodDescription) {
      const detected = extractCatalogMovementsFromText(
        wodDescription,
        movementGroups,
      );
      if (detected.length > 0) {
        return { movement: detected[0], weightKg };
      }
    }
    return null;
  }, [selectedMovements, wodDescription, movementGroups]);

  const [relatedWods, setRelatedWods] = useState<any[]>([]);
  const [preWodAdvice, setPreWodAdvice] = useState<PreWodAdviceResponse | null>(
    null,
  );
  const [adviceLoading, setAdviceLoading] = useState(false);
  const [historyRevision, setHistoryRevision] = useState(0);
  useEffect(() => subscribeHistoryChanges(pid => {
    if (pid === activeProfile?.id) { setPreWodAdvice(null); setRelatedWods([]); setHistoryRevision(value => value + 1); }
  }), [activeProfile?.id]);

  useEffect(() => {
    if (!verifyVisible || !activeProfile) {
      setPreWodAdvice(null);
      return;
    }

    let isMounted = true;
    setAdviceLoading(true);

    (async () => {
      const source = appliedRecentSource.current;
      if (source?.origin === "recording" && (!setupScope || !await verifyRecentSetup(source, setupScope))) throw new Error("archived_source");
      return fetchPreWodAdvice({ profile_id: activeProfile.id, wod_description: wodDescription.trim(), movements: selectedMovements });
    })()
      .then((res) => {
        if (isMounted) {
          setPreWodAdvice(res);
        }
      })
      .catch((err) => {
        console.warn("Failed to fetch pre-WOD advice:", err);
        if (isMounted) setPreWodAdvice(null);
      })
      .finally(() => {
        if (isMounted) setAdviceLoading(false);
      });

    return () => {
      isMounted = false;
    };
  }, [verifyVisible, activeProfile?.id, wodDescription, selectedMovements, historyRevision]);

  useEffect(() => {
    if (!verifyVisible || !activeProfile || !anchorInfo?.movement) {
      setRelatedWods([]);
      return;
    }

    let isMounted = true;
    (async () => {
      const source = appliedRecentSource.current;
      if (source?.origin === "recording" && (!setupScope || !await verifyRecentSetup(source, setupScope))) throw new Error("archived_source");
      return fetchRelatedWods({ profileId: activeProfile.id, movement: anchorInfo.movement, weightKg: anchorInfo.weightKg, limit: 5 });
    })()
      .then((res) => {
        if (isMounted && res?.related) {
          setRelatedWods(res.related);
        }
      })
      .catch((err) => {
        console.warn("Failed to fetch related WODs:", err);
        if (isMounted) setRelatedWods([]);
      });

    return () => {
      isMounted = false;
    };
  }, [verifyVisible, activeProfile?.id, anchorInfo, historyRevision]);

  // Derive category filter tabs (include "Custom" when custom movements exist)
  const categoryTabs = useMemo(() => {
    const cats = movementGroups.map((g) => g.category);
    if (customMovements.length > 0 && !cats.includes("Custom")) {
      cats.push("Custom");
    }
    return [ALL_FILTER, ...cats];
  }, [movementGroups, customMovements]);

  // Build the flat list of all known movements (for search matching)
  const allKnownMovements = useMemo(() => {
    const set = new Set<string>();
    for (const g of movementGroups) {
      for (const m of g.movements) set.add(m);
    }
    return set;
  }, [movementGroups]);

  // Quick-pick top movements across categories (up to 6) for 1-tap toggle without leaving Photo/Text view
  const quickPickMovements = useMemo(() => {
    const picks: string[] = [];
    for (const g of movementGroups) {
      for (const m of g.movements.slice(0, 2)) {
        if (!picks.includes(m)) {
          picks.push(m);
        }
        if (picks.length >= 6) return picks;
      }
    }
    return picks;
  }, [movementGroups]);

  // Filter movements based on search + active category
  const filteredGroups = useMemo(() => {
    const query = searchText.toLowerCase().trim();
    let groups = movementGroups;

    // Add custom movements group if any
    if (customMovements.length > 0) {
      groups = [...groups, { category: "Custom", movements: customMovements }];
    }

    // Filter by active category tab
    if (activeFilter !== ALL_FILTER) {
      groups = groups.filter((g) => g.category === activeFilter);
    }

    // Filter by search text
    if (query) {
      groups = groups
        .map((g) => ({
          ...g,
          movements: g.movements.filter((m) => m.toLowerCase().includes(query)),
        }))
        .filter((g) => g.movements.length > 0);
    }

    return groups;
  }, [movementGroups, customMovements, activeFilter, searchText]);

  // Check if search text matches an un-added custom movement
  const canAddCustom = useMemo(() => {
    const trimmed = searchText.trim();
    if (!trimmed) return false;
    const lower = trimmed.toLowerCase();
    for (const m of allKnownMovements) {
      if (m.toLowerCase() === lower) return false;
    }
    for (const m of customMovements) {
      if (m.toLowerCase() === lower) return false;
    }
    return true;
  }, [searchText, allKnownMovements, customMovements]);

  const toggleMovement = (m: string) => {
    setSelectedMovements((prev) =>
      prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m],
    );
  };

  const addCustomMovement = () => {
    const trimmed = searchText.trim();
    if (!trimmed) return;
    setCustomMovements((prev) => [...prev, trimmed]);
    setSelectedMovements((prev) => [...prev, trimmed]);
    setSearchText("");
  };

  const removeCustomMovement = (m: string) => {
    setCustomMovements((prev) => prev.filter((x) => x !== m));
    setSelectedMovements((prev) => prev.filter((x) => x !== m));
  };

  const updatePref = <K extends keyof VideoPreferences>(
    key: K,
    value: VideoPreferences[K],
  ) => {
    setVideoPrefs((prev) => ({ ...prev, [key]: value }));
  };

  const pickImage = async (useCamera: boolean) => {
    const pickerFn = useCamera
      ? ImagePicker.launchCameraAsync
      : ImagePicker.launchImageLibraryAsync;
    const result = await pickerFn({
      mediaTypes: ["images"],
      quality: 0.8,
      allowsEditing: false,
    });
    if (result.canceled || !result.assets?.[0]) return null;
    return result.assets[0].uri;
  };

  const saveRecentWorkoutSetup = async (
    setup: Omit<RecentWorkoutSetup, "profileId">,
    inherited = appliedRecentSource.current,
    isCurrent?: () => boolean,
  ): Promise<boolean> => {
    const generation = scanRequest.current;
    const current = isCurrent ?? (() => currentSetupScope.current === setupScopeKey && scanRequest.current === generation
      && appliedRecentSource.current?.snapshotId === inherited?.snapshotId);
    if (!setupScope || !current()) return false;
    const { savedAt: _savedAt, ...fields } = setup;
    try {
      const snapshot = await saveSetupForLater(fields, setupScope, inherited, current);
      if (!current()) return false;
      appliedRecentSource.current = snapshot; setRecentWorkoutSetup(snapshot);
      return true;
    } catch {
      if (current()) Alert.alert(t("common.error"), t("setup.archiveSourceUnavailable"));
      return false;
    }
  };

  const resetWorkoutInputs = () => {
    invalidateScan();
    appliedRecentSource.current = undefined;
    setVerifyVisible(false); setWodDescription(""); setRawText(""); setSelectedMovements([]); setCustomMovements([]); setSessionAppearance("");
  };

  const handleApplyRecentWorkout = async (recent: RecentSetupSnapshot) => {
    if (!setupScope) return;
    try {
      if (!await verifyRecentSetup(recent, setupScope) || currentSetupScope.current !== setupScopeKey) {
        setRecentWorkoutSetup(null);
        Alert.alert(t("common.error"), t("setup.archiveSourceUnavailable"));
        return;
      }
      invalidateScan();
      appliedRecentSource.current = recent;
      setWorkoutType(recent.workoutType);
      setWodDescription(recent.wodDescription);
      setRawText(recent.rawText);
      setSelectedMovements(recent.selectedMovements);
      setCustomMovements(recent.customMovements);
      setSessionAppearance(recent.sessionAppearance);
      setVerifyVisible(true);
    } catch {
      if (currentSetupScope.current === setupScopeKey) {
        setRecentWorkoutSetup(null);
        Alert.alert(t("common.error"), t("setup.archiveSourceUnavailable"));
      }
    }
  };

  const handleClearRecentWorkout = async () => {
    if (!setupScope || !recentWorkoutSetup) return;
    const id = recentWorkoutSetup.snapshotId;
    setRecentWorkoutSetup(null);
    await clearRecentSetup(setupScope, id).catch(() => {});
  };

  const mergeAutoDetectedMovements = (
    desc: string,
    currentSelected: string[],
  ): string[] => {
    if (!desc.trim() || movementGroups.length === 0) return currentSelected;
    const detected = extractCatalogMovementsFromText(desc, movementGroups);
    if (detected.length === 0) return currentSelected;
    return Array.from(new Set([...currentSelected, ...detected]));
  };

  const handleSaveForLater = async () => {
    const trimmedDesc = wodDescription.trim();
    const mergedMovements = mergeAutoDetectedMovements(
      trimmedDesc,
      selectedMovements,
    );
    if (!trimmedDesc && !rawText.trim() && mergedMovements.length === 0) {
      return;
    }
    if (mergedMovements.length !== selectedMovements.length) {
      setSelectedMovements(mergedMovements);
    }
    const saved = await saveRecentWorkoutSetup({
      savedAt: Date.now(),
      workoutType,
      wodDescription: trimmedDesc,
      rawText,
      selectedMovements: mergedMovements,
      customMovements,
      sessionAppearance: sessionAppearance.trim(),
    });
    if (!saved) return;
    Alert.alert(t("setup.savedForLaterTitle"), t("setup.savedForLaterMessage"), [
      {
        text: t("common.ok"),
        onPress: () => router.back(),
      },
      {
        text: t("common.keep"),
        style: "cancel",
      },
    ]);
  };

  const formatSavedRelativeTime = (savedAt: number): string => {
    const elapsedMins = Math.max(1, Math.round((Date.now() - savedAt) / 60_000));
    if (elapsedMins >= 60) {
      return t("setup.reuseRecentHoursAgo", {
        hours: Math.floor(elapsedMins / 60),
      });
    }
    return t("setup.reuseRecentMinutesAgo", { minutes: elapsedMins });
  };

  const handleScanWhiteboard = async (useCamera: boolean) => {
    const request = invalidateScan();
    const key = setupScopeKey;
    const inherited = appliedRecentSource.current;
    const current = () => request === scanRequest.current && currentSetupScope.current === key
      && appliedRecentSource.current?.snapshotId === inherited?.snapshotId;
    try {
      const uri = await pickImage(useCamera);
      if (!current() || !uri) return;
      setScanning(true);
      const result = await parseWorkoutImage(uri);
      if (!current()) return;
      if (inherited && (!setupScope || !await verifyRecentSetup(inherited, setupScope))) throw new Error("archived_source");
      if (!current()) return;
      const nextDescription = result.wod_description || "";
      const nextRawText = result.raw_text || "";
      const nextMovements = result.movements?.length > 0 ? Array.from(new Set([...selectedMovements, ...result.movements])) : selectedMovements;
      const saved = await saveRecentWorkoutSetup({ savedAt: Date.now(), workoutType, wodDescription: nextDescription,
        rawText: nextRawText, selectedMovements: nextMovements, customMovements, sessionAppearance }, inherited, current);
      // Saving produces a new snapshot; scope and request remain the authoritative scan guard.
      if (!saved || request !== scanRequest.current || currentSetupScope.current !== key) return;
      setWodDescription(nextDescription); setRawText(nextRawText); setSelectedMovements(nextMovements); setVerifyVisible(true);
    } catch (err: any) {
      if (!current()) return;
      Alert.alert(t("common.error"), err?.message === "archived_source" ? t("setup.archiveSourceUnavailable") : t("setup.scanFailed"));
    } finally { if (request === scanRequest.current && currentSetupScope.current === key) setScanning(false); }
  };

  const handleNext = () => {
    const merged = mergeAutoDetectedMovements(wodDescription, selectedMovements);
    if (merged.length !== selectedMovements.length) {
      setSelectedMovements(merged);
    }
    setVerifyVisible(true);
  };

  const [sessionAppearance, setSessionAppearance] = useState("");
  const [isStarting, setIsStarting] = useState(false);

  const navigateToVisionTest = async () => {
    try {
      await AsyncStorage.setItem(VIDEO_PREFS_KEY, JSON.stringify(videoPrefs));
    } catch (e) {
      console.warn("Failed to persist video prefs:", e);
    }
    if (!setupScope) return;
    if (appliedRecentSource.current?.origin === "recording") {
      try {
        if (!await verifyRecentSetup(appliedRecentSource.current, setupScope)) throw new Error("archived_source");
      } catch {
        setVerifyVisible(false);
        Alert.alert(t("common.error"), t("setup.archiveSourceUnavailable"));
        return;
      }
    }
    if (currentSetupScope.current !== setupScopeKey) return;
    const recordingSetup = createSetupSnapshot({ workoutType, wodDescription: wodDescription.trim(), rawText,
      selectedMovements, customMovements, sessionAppearance: sessionAppearance.trim() }, setupScope, Date.now(), appliedRecentSource.current);
    const injuries = activeProfile?.injuries ?? [];
    const appearanceHints = sessionAppearance.trim();
    setVerifyVisible(false);

    router.push({
      pathname: "/workout/visionTestPage",
      params: {
        resolution: videoPrefs.resolution,
        continuousRecording: videoPrefs.continuousRecording ? "true" : "false",
        h10MemoryRecording:
          Platform.OS === "ios" && videoPrefs.h10MemoryRecording
            ? "true"
            : "false",
        workoutType,
        recordingSetup: serializeSetupSnapshot(recordingSetup),
        movements: selectedMovements.join(", "),
        injuries: injuries.join(", "),
        wodDescription: wodDescription.trim(),
        ...(appearanceHints ? { appearanceHints } : {}),
        autoRecord: videoPrefs.autoRecord ? "true" : "false",
        onDeviceAi: videoPrefs.onDeviceAi ? "true" : "false",
        environmentObservation: videoPrefs.environmentObservation
          ? "true"
          : "false",
        environmentAnalysis: videoPrefs.environmentAnalysis ? "true" : "false",
        observationIntervalSeconds: String(
          videoPrefs.observationIntervalSeconds,
        ),
        showSkeleton: videoPrefs.showSkeleton ? "true" : "false",
        lowFps: videoPrefs.lowFps ? "true" : "false",
        skipCompression: videoPrefs.skipCompression ? "true" : "false",
        serialUpload: videoPrefs.serialUpload ? "true" : "false",
        landscapeMode: videoPrefs.landscapeMode ? "true" : "false",
        zoomMode: videoPrefs.zoomMode ? "true" : "false",
        aspectRatio: videoPrefs.aspectRatio,
      },
    });
  };

  const handleStart = async () => {
    if (isStarting) return;
    setIsStarting(true);
    try {
      const tokenStatus = await validateStoredToken();
      if (!tokenStatus.valid) {
        Alert.alert(
          t("auth.sessionExpiredTitle"),
          t("auth.sessionExpiredMessage"),
          [
            {
              text: t("auth.login"),
              onPress: () => {
                useAuthStore.getState().handleUnauthorized();
              },
            },
          ],
        );
        return;
      }

      if (tokenStatus.expiringSoon) {
        Alert.alert(
          t("auth.tokenExpiringSoonTitle"),
          t("auth.tokenExpiringSoonMessage"),
          [
            {
              text: t("auth.relogin"),
              style: "destructive",
              onPress: () => {
                useAuthStore.getState().handleUnauthorized();
              },
            },
            {
              text: t("auth.continueWorkout"),
              onPress: () => {
                navigateToVisionTest();
              },
            },
          ],
        );
        return;
      }

      await navigateToVisionTest();
    } finally {
      setTimeout(() => setIsStarting(false), 1500);
    }
  };

  const handlePreview = () => {
    router.push({
      pathname: "/workout/visionTestPage",
      params: {
        resolution: videoPrefs.resolution,
        continuousRecording: videoPrefs.continuousRecording ? "true" : "false",
        showSkeleton: videoPrefs.showSkeleton ? "true" : "false",
        lowFps: videoPrefs.lowFps ? "true" : "false",
        landscapeMode: videoPrefs.landscapeMode ? "true" : "false",
        zoomMode: videoPrefs.zoomMode ? "true" : "false",
        aspectRatio: videoPrefs.aspectRatio,
        previewOnly: "true",
        autoRecord: "false",
        onDeviceAi: videoPrefs.onDeviceAi ? "true" : "false",
        environmentObservation: videoPrefs.environmentObservation
          ? "true"
          : "false",
        environmentAnalysis: videoPrefs.environmentAnalysis ? "true" : "false",
        observationIntervalSeconds: String(
          videoPrefs.observationIntervalSeconds,
        ),
      },
    });
  };

  const injuryCount = activeProfile?.injuries?.length ?? 0;

  const renderSensorSyncSnippet = () => (
    <View style={styles.sensorSyncCard}>
      <View
        style={[
          styles.sensorSyncIconWrap,
          registeredBleDevice && styles.sensorSyncIconWrapActive,
        ]}
      >
        <IconSymbol
          name="waveform.path.ecg"
          size={18}
          color={registeredBleDevice ? T.heartRose : T.inkMuted}
        />
      </View>
      <View style={styles.sensorSyncBody}>
        <Text style={styles.sensorSyncEyebrow}>
          {t("setup.sensorSyncLabel")}
        </Text>
        <Text style={styles.sensorSyncTitle} numberOfLines={1}>
          {registeredBleDevice
            ? registeredBleDevice.name
            : t("setup.bleHrDevice")}
        </Text>
      </View>
      <View
        style={[
          styles.sensorSyncBadge,
          registeredBleDevice
            ? styles.sensorSyncBadgeReady
            : styles.sensorSyncBadgeMuted,
        ]}
      >
        <Text
          style={[
            styles.sensorSyncBadgeText,
            registeredBleDevice
              ? styles.sensorSyncBadgeTextReady
              : styles.sensorSyncBadgeTextMuted,
          ]}
        >
          {registeredBleDevice
            ? t("setup.sensorReadyBadge")
            : t("setup.sensorOptionalBadge")}
        </Text>
      </View>
    </View>
  );

  const renderMovementAssistSection = () => (
    <View style={styles.quickMovementCard}>
      <View style={styles.quickMovementHeader}>
        <Text style={styles.quickMovementLabel}>
          {t("setup.frequentMovementsLabel")}
        </Text>
        {allKnownMovements.size > 0 && (
          <Text style={styles.quickMovementCount}>
            {t("setup.indexedMovementsCount", {
              count: allKnownMovements.size,
            })}
          </Text>
        )}
      </View>

      {quickPickMovements.length > 0 && (
        <View style={styles.quickPicksWrap}>
          {quickPickMovements.map((m) => {
            const active = selectedMovements.includes(m);
            return (
              <TouchableOpacity
                key={m}
                testID={`quick-pick-${m}`}
                style={[styles.quickPickChip, active && styles.quickPickChipActive]}
                onPress={() => toggleMovement(m)}
                activeOpacity={0.75}
              >
                <Text
                  style={[
                    styles.quickPickChipText,
                    active && styles.quickPickChipTextActive,
                  ]}
                  numberOfLines={1}
                >
                  {active ? `✓ ${m}` : `+ ${m}`}
                </Text>
              </TouchableOpacity>
            );
          })}
        </View>
      )}

      <TouchableOpacity
        style={styles.secondaryMovementsLink}
        onPress={() => setInputMethod("movements")}
        activeOpacity={0.75}
      >
        <IconSymbol name="dumbbell.fill" size={15} color={T.electricBlue} />
        <Text style={styles.secondaryMovementsText} numberOfLines={1}>
          {selectedMovements.length > 0
            ? t("setup.secondaryMovementsSelected", {
                count: selectedMovements.length,
              })
            : t("setup.secondaryMovementsBtn")}
        </Text>
        <IconSymbol name="chevron.right" size={16} color={T.inkMuted} />
      </TouchableOpacity>
    </View>
  );

  const popupTranslateY = popupAnim.interpolate({
    inputRange: [0, 1],
    outputRange: [24, 0],
  });

  return (
    <SafeAreaView style={styles.container} edges={["top", "left", "right"]}>
      <StatusBar style="dark" />
      {ExpoRouter.Stack?.Screen ? (
        <ExpoRouter.Stack.Screen options={{ gestureEnabled: !verifyVisible }} />
      ) : null}

      {/* Main Single-Step Workout Setup Screen */}
      <View
        style={styles.mainScreenLayer}
        pointerEvents={verifyVisible ? "none" : "auto"}
        accessibilityElementsHidden={verifyVisible}
        importantForAccessibility={
          verifyVisible ? "no-hide-descendants" : "auto"
        }
      >
        {/* Top Tactical Navigation Header (1-Tap Back to Home) */}
        <View style={styles.header}>
          <TouchableOpacity
            testID="setup-back-btn"
            onPress={() => router.back()}
            style={styles.backBtn}
            accessibilityRole="button"
          >
            <IconSymbol name="chevron.left" size={24} color={T.ink} />
          </TouchableOpacity>
          <View style={styles.headerCenter}>
            <Text style={styles.headerEyebrow} numberOfLines={1}>
              {t("setup.protocolSetup")}
            </Text>
            <Text style={styles.title} numberOfLines={1}>
              {t("setup.title")}
            </Text>
          </View>
          <View style={styles.headerRightSpacer} />
        </View>

        {/* Segmented Workout Stage Switcher */}
        <View style={styles.workoutTypeTrack}>
            {WORKOUT_TYPES.map((wt) => {
              const active = workoutType === wt;
              return (
                <TouchableOpacity
                  key={wt}
                  style={[
                    styles.workoutTypeTab,
                    active && styles.workoutTypeTabActive,
                  ]}
                  onPress={() => setWorkoutType(wt)}
                  activeOpacity={0.8}
                >
                  <Text
                    style={[
                      styles.workoutTypeTabText,
                      active && styles.workoutTypeTabTextActive,
                    ]}
                    numberOfLines={1}
                  >
                    {t(`workoutType.${wt}`)}
                  </Text>
                  {active && <View style={styles.workoutTypeVoltBar} />}
                </TouchableOpacity>
              );
            })}
          </View>

          {/* Input Format Toggle (Photo vs Text) or Back to Input */}
          {inputMethod !== "movements" ? (
            <View style={styles.inputMethodTabs}>
              {(["photo", "text"] as const).map((method) => {
                const active = inputMethod === method;
                const iconName =
                  method === "photo" ? "camera.fill" : "sparkles";
                const tKey = `inputMethod${method.charAt(0).toUpperCase() + method.slice(1)}`;
                return (
                  <TouchableOpacity
                    key={method}
                    style={[
                      styles.inputMethodTab,
                      active && styles.inputMethodTabActive,
                    ]}
                    onPress={() => setInputMethod(method)}
                    activeOpacity={0.8}
                  >
                    <View style={styles.inputMethodTabInner}>
                      <IconSymbol
                        name={iconName}
                        size={16}
                        color={active ? T.volt : T.inkMuted}
                      />
                      <Text
                        style={[
                          styles.inputMethodTabText,
                          active && styles.inputMethodTabTextActive,
                        ]}
                        numberOfLines={1}
                      >
                        {t(`setup.${tKey}`)}
                      </Text>
                    </View>
                  </TouchableOpacity>
                );
              })}
            </View>
          ) : (
            <TouchableOpacity
              style={styles.backToPrimaryBar}
              onPress={() => setInputMethod("photo")}
              activeOpacity={0.75}
            >
              <Text style={styles.backToPrimaryText}>
                {t("setup.backToWodInput")}
              </Text>
            </TouchableOpacity>
          )}

          {appliedRecentSource.current?.origin === "recording" && <TouchableOpacity
            testID="setup-fresh-inputs-btn" onPress={resetWorkoutInputs} accessibilityRole="button"
            style={styles.backToPrimaryBar}>
            <Text style={styles.backToPrimaryText}>{t("setup.startFreshInputs")}</Text>
          </TouchableOpacity>}

          {/* Saved Workout for Today 1-Tap Reuse Banner */}
          {recentWorkoutSetup &&
            recentWorkoutSetup.profileId === activeProfile?.id &&
            Date.now() - recentWorkoutSetup.savedAt <= RECENT_WORKOUT_TTL_MS && (
              <View style={styles.recentReuseCard}>
                <TouchableOpacity
                  testID="reuse-recent-workout-card"
                  style={styles.recentReuseMainTouch}
                  onPress={() => handleApplyRecentWorkout(recentWorkoutSetup)}
                  activeOpacity={0.8}
                  accessibilityRole="button"
                  accessibilityLabel={t("setup.reuseRecentTitle")}
                >
                  <View style={styles.recentReuseContent}>
                    <Text style={styles.recentReuseTitle}>
                      {t("setup.reuseRecentTitle")}
                    </Text>
                    <Text style={styles.recentReuseSummary} numberOfLines={2}>
                      {[
                        recentWorkoutSetup.wodDescription.trim() ||
                          recentWorkoutSetup.rawText.trim(),
                        recentWorkoutSetup.selectedMovements.join(", "),
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </Text>
                    <Text style={styles.recentReuseMeta}>
                      {t("setup.reuseRecentMeta", {
                        time: formatSavedRelativeTime(
                          recentWorkoutSetup.savedAt,
                        ),
                      })}
                    </Text>
                  </View>
                  <View style={styles.recentReuseBadge}>
                    <Text style={styles.recentReuseBadgeText}>
                      {t("setup.reuseRecentApply")}
                    </Text>
                  </View>
                </TouchableOpacity>
                <TouchableOpacity
                  onPress={handleClearRecentWorkout}
                  hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                  accessibilityRole="button"
                  accessibilityLabel={t("setup.reuseRecentClear")}
                >
                  <IconSymbol
                    name="xmark.circle.fill"
                    size={18}
                    color={T.inkMuted}
                  />
                </TouchableOpacity>
              </View>
            )}

          {/* Manual Text Entry Mode */}
          {inputMethod === "text" && (
            <ScrollView
              contentContainerStyle={styles.movementListContent}
              style={styles.movementList}
              showsVerticalScrollIndicator={false}
            >
              <TacticalCard style={styles.textModeBento}>
                <Text style={styles.sectionLabel}>
                  {t("setup.wodDescriptionLabel")}
                </Text>
                <Text style={styles.preInputHint}>
                  {t("setup.textPreInputHint")}
                </Text>
                <TextInput
                  style={styles.wodTextInput}
                  placeholder={t("setup.wodTextPlaceholder")}
                  placeholderTextColor={T.inkMuted}
                  value={wodDescription}
                  onChangeText={setWodDescription}
                  multiline
                  numberOfLines={5}
                  textAlignVertical="top"
                />
                {wodDescription.trim().length > 0 && (
                  <TouchableOpacity
                    testID="save-workout-for-later-text-btn"
                    style={styles.saveLaterBtn}
                    onPress={handleSaveForLater}
                    activeOpacity={0.8}
                  >
                    <IconSymbol
                      name="bookmark.fill"
                      size={16}
                      color={T.electricBlue}
                    />
                    <Text style={styles.saveLaterBtnText}>
                      {t("setup.saveForLater")}
                    </Text>
                  </TouchableOpacity>
                )}
              </TacticalCard>

              {renderMovementAssistSection()}
              {renderSensorSyncSnippet()}
            </ScrollView>
          )}

          {/* Whiteboard Photo OCR Mode */}
          {inputMethod === "photo" && (
            <ScrollView
              contentContainerStyle={styles.movementListContent}
              style={styles.movementList}
              showsVerticalScrollIndicator={false}
            >
              {scanning ? (
                <View style={styles.scanningContainer}>
                  <ActivityIndicator color={T.electricBlue} size="large" />
                  <Text style={styles.scanningText}>
                    {t("setup.scanningImage")}
                  </Text>
                </View>
              ) : (
                <>
                  <View style={styles.photoHeroHeader}>
                    <View style={styles.photoHeroTitleRow}>
                      <Text style={styles.photoHeroTitle}>
                        {t("setup.photoPrimaryTitle")}
                      </Text>
                      <View style={styles.autoOcrPill}>
                        <Text style={styles.autoOcrPillText}>
                          {t("setup.autoOcrBadge")}
                        </Text>
                      </View>
                    </View>
                    <Text style={styles.photoHeroSubtitle}>
                      {t("setup.photoPrimarySubtitle")}
                    </Text>
                  </View>

                  <TouchableOpacity
                    style={[styles.photoBtn, styles.photoBtnPrimary]}
                    onPress={() => handleScanWhiteboard(true)}
                    activeOpacity={0.8}
                  >
                    <View style={styles.photoIconBadge}>
                      <IconSymbol name="camera.fill" size={24} color={T.volt} />
                    </View>
                    <View style={styles.photoBtnBody}>
                      <View style={styles.photoBtnTitleRow}>
                        <Text style={styles.photoBtnText}>
                          {t("setup.takePhoto")}
                        </Text>
                        <View style={styles.recommendedPill}>
                          <Text style={styles.recommendedPillText}>
                            {t("setup.recommendedBadge")}
                          </Text>
                        </View>
                      </View>
                      <Text style={styles.photoBtnSubtext}>
                        {t("setup.takePhotoDesc")}
                      </Text>
                    </View>
                    <IconSymbol
                      name="chevron.right"
                      size={20}
                      color={T.inkMuted}
                    />
                  </TouchableOpacity>

                  <TouchableOpacity
                    style={styles.photoBtn}
                    onPress={() => handleScanWhiteboard(false)}
                    activeOpacity={0.8}
                  >
                    <View style={styles.photoIconBadgeSecondary}>
                      <IconSymbol
                        name="photo.fill"
                        size={22}
                        color={T.electricBlue}
                      />
                    </View>
                    <View style={styles.photoBtnBody}>
                      <Text style={styles.photoBtnText}>
                        {t("setup.chooseFromGallery")}
                      </Text>
                      <Text style={styles.photoBtnSubtext}>
                        {t("setup.chooseFromGalleryDesc")}
                      </Text>
                    </View>
                    <IconSymbol
                      name="chevron.right"
                      size={20}
                      color={T.inkMuted}
                    />
                  </TouchableOpacity>

                  {renderMovementAssistSection()}
                  {renderSensorSyncSnippet()}
                </>
              )}
            </ScrollView>
          )}

          {/* Secondary Full Movement Index Mode */}
          {inputMethod === "movements" && (
            <>
              <View style={styles.searchContainer}>
                <IconSymbol
                  name="magnifyingglass"
                  size={18}
                  color={T.inkMuted}
                />
                <TextInput
                  style={styles.searchInput}
                  placeholder={t("setup.searchMovements")}
                  placeholderTextColor={T.inkMuted}
                  value={searchText}
                  onChangeText={setSearchText}
                  autoCapitalize="none"
                  autoCorrect={false}
                />
                {searchText.length > 0 && (
                  <TouchableOpacity onPress={() => setSearchText("")}>
                    <IconSymbol
                      name="xmark.circle.fill"
                      size={18}
                      color={T.inkMuted}
                    />
                  </TouchableOpacity>
                )}
              </View>
              <ScrollView
                horizontal
                showsHorizontalScrollIndicator={false}
                contentContainerStyle={styles.filterTabsContent}
                style={styles.filterTabs}
              >
                {categoryTabs.map((tab) => {
                  const isActive = activeFilter === tab;
                  return (
                    <TouchableOpacity
                      key={tab}
                      style={[
                        styles.filterTab,
                        isActive && styles.filterTabActive,
                      ]}
                      onPress={() => setActiveFilter(tab)}
                    >
                      <Text
                        style={[
                          styles.filterTabText,
                          isActive && styles.filterTabTextActive,
                        ]}
                      >
                        {tab === ALL_FILTER ? t("setup.categoryAll") : tab}
                      </Text>
                    </TouchableOpacity>
                  );
                })}
              </ScrollView>
              <ScrollView
                contentContainerStyle={styles.movementListContent}
                style={styles.movementList}
              >
                {loading ? (
                  <ActivityIndicator
                    color={T.electricBlue}
                    style={{ marginTop: 40 }}
                  />
                ) : (
                  <>
                    {canAddCustom && (
                      <TouchableOpacity
                        style={styles.addCustomBtn}
                        onPress={addCustomMovement}
                      >
                        <IconSymbol
                          name="plus.circle.fill"
                          size={20}
                          color={T.electricBlue}
                        />
                        <Text style={styles.addCustomText}>
                          {t("setup.addCustomMovement", {
                            name: searchText.trim(),
                          })}
                        </Text>
                      </TouchableOpacity>
                    )}
                    {filteredGroups.map((group) => (
                      <View key={group.category} style={styles.groupSection}>
                        <Text style={styles.groupHeader}>
                          {group.category.toUpperCase()}
                        </Text>
                        {group.movements.map((movement) => {
                          const isSelected =
                            selectedMovements.includes(movement);
                          const isCustom = customMovements.includes(movement);
                          return (
                            <TouchableOpacity
                              key={movement}
                              style={[
                                styles.movementCard,
                                isSelected && styles.movementCardSelected,
                              ]}
                              onPress={() => toggleMovement(movement)}
                            >
                              <View
                                style={[
                                  styles.movementCardIcon,
                                  isSelected && styles.movementCardIconSelected,
                                ]}
                              >
                                <IconSymbol
                                  name={getCategoryIcon(group.category) as any}
                                  size={19}
                                  color={isSelected ? T.volt : T.inkSecondary}
                                />
                              </View>
                              <View style={styles.movementCardInfo}>
                                <Text
                                  style={[
                                    styles.movementName,
                                    isSelected && styles.movementNameSelected,
                                  ]}
                                >
                                  {movement.toUpperCase()}
                                </Text>
                                <Text style={styles.movementCategory}>
                                  {group.category.toUpperCase()}
                                </Text>
                              </View>
                              {isCustom ? (
                                <TouchableOpacity
                                  onPress={(e: GestureResponderEvent) => {
                                    e.stopPropagation();
                                    removeCustomMovement(movement);
                                  }}
                                  hitSlop={{
                                    top: 10,
                                    bottom: 10,
                                    left: 10,
                                    right: 10,
                                  }}
                                >
                                  <IconSymbol
                                    name="xmark.circle"
                                    size={22}
                                    color={T.inkMuted}
                                  />
                                </TouchableOpacity>
                              ) : (
                                <View
                                  style={[
                                    styles.checkbox,
                                    isSelected && styles.checkboxSelected,
                                  ]}
                                >
                                  {isSelected && (
                                    <IconSymbol
                                      name="checkmark"
                                      size={14}
                                      color={T.voltInk}
                                    />
                                  )}
                                </View>
                              )}
                            </TouchableOpacity>
                          );
                        })}
                      </View>
                    ))}
                    {filteredGroups.length === 0 && !canAddCustom && (
                      <View style={styles.emptyState}>
                        <Text style={styles.emptyText}>
                          {t("setup.noResults")}
                        </Text>
                      </View>
                    )}
                  </>
                )}
              </ScrollView>
            </>
          )}

          {/* Sticky Bottom Dock */}
          <View style={styles.bottomBar}>
            {selectedMovements.length > 0 && (
              <ScrollView
                horizontal
                showsHorizontalScrollIndicator={false}
                contentContainerStyle={styles.stagedChipsContent}
                style={styles.stagedChipsScroll}
              >
                {selectedMovements.map((m) => (
                  <View key={m} style={styles.stagedChip}>
                    <Text style={styles.stagedChipText}>{m}</Text>
                    <TouchableOpacity
                      onPress={() => toggleMovement(m)}
                      hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}
                    >
                      <IconSymbol
                        name="xmark.circle.fill"
                        size={14}
                        color={T.volt}
                      />
                    </TouchableOpacity>
                  </View>
                ))}
              </ScrollView>
            )}
            <VoltButton
              testID="setup-next-btn"
              label={t("setup.nextStep")}
              onPress={handleNext}
            />
          </View>
      </View>

      {/* ─── VERIFY WORKOUT CONFIRMATION POPUP ─── */}
      {verifyVisible && (
        <KeyboardAvoidingView
          behavior={Platform.OS === "ios" ? "padding" : undefined}
          style={styles.popupOverlay}
        >
          <Pressable
            testID="verify-popup-backdrop"
            style={styles.popupBackdrop}
            onPress={() => setVerifyVisible(false)}
          />
          <Animated.View
            testID="verify-workout-popup"
            style={[
              styles.popupSheet,
              {
                opacity: popupAnim,
                transform: [{ translateY: popupTranslateY }],
              },
            ]}
          >
            <View style={styles.popupHandleBar} />

            {/* Popup Header */}
            <View style={styles.popupHeader}>
              <View style={styles.popupHeaderLeft}>
                <View style={styles.popupShieldBadge}>
                  <IconSymbol
                    name="checkmark.shield.fill"
                    size={16}
                    color={T.volt}
                  />
                </View>
                <View style={styles.popupTitleCol}>
                  <Text style={styles.headerEyebrow} numberOfLines={1}>
                    {t("setup.protocolVerify")}
                  </Text>
                  <Text style={styles.popupTitle} numberOfLines={1}>
                    {t("setup.confirmTitle")}
                  </Text>
                </View>
              </View>
              <TouchableOpacity
                testID="verify-popup-close-btn"
                style={styles.popupCloseBtn}
                onPress={() => setVerifyVisible(false)}
                accessibilityRole="button"
                accessibilityLabel={t("common.cancel")}
              >
                <IconSymbol
                  name="xmark.circle.fill"
                  size={22}
                  color={T.inkMuted}
                />
              </TouchableOpacity>
            </View>

            <ScrollView
              contentContainerStyle={styles.popupScrollContent}
              style={styles.popupScroll}
              showsVerticalScrollIndicator={false}
              keyboardShouldPersistTaps="handled"
            >
              {/* Active Athlete Identity Bento */}
              {activeProfile && (
                <TouchableOpacity
                  style={styles.profileBanner}
                  onPress={() =>
                    router.push({
                      pathname: "/profile-edit",
                      params: { id: String(activeProfile.id) },
                    } as any)
                  }
                  activeOpacity={0.8}
                >
                  <View style={styles.profileBannerLeft}>
                    <View style={styles.miniAvatar}>
                      <Text style={styles.miniAvatarText}>
                        {activeProfile.name
                          ? activeProfile.name.slice(0, 2).toUpperCase()
                          : "?"}
                      </Text>
                    </View>
                    <View style={styles.profileBannerTextCol}>
                      <Text style={styles.profileBannerName} numberOfLines={1}>
                        {activeProfile.name || t("tabs.profile")}
                      </Text>
                      <Text style={styles.profileBannerMeta} numberOfLines={1}>
                        {[
                          t(
                            `profileEdit.${activeProfile.fitnessLevel ?? "intermediate"}`,
                          ),
                          injuryCount > 0
                            ? t("setup.injuries", { count: injuryCount })
                            : t("setup.noInjuries"),
                        ].join(" · ")}
                      </Text>
                    </View>
                  </View>
                  <IconSymbol
                    name="chevron.right"
                    size={18}
                    color={T.inkMuted}
                  />
                </TouchableOpacity>
              )}

              {/* WOD Verification & Planned Movements Bento */}
              <TacticalCard style={styles.verifyBentoCard}>
                <Text style={styles.confirmHint}>{t("setup.confirmHint")}</Text>
                <Text style={[styles.sectionLabel, { marginTop: 0 }]}>
                  {t("setup.wodDescriptionLabel")}
                </Text>
                <TextInput
                  style={[styles.wodTextInput, styles.popupWodTextInput]}
                  placeholder={t("setup.wodDescriptionPlaceholder")}
                  placeholderTextColor={T.inkMuted}
                  value={wodDescription}
                  onChangeText={setWodDescription}
                  multiline
                  numberOfLines={3}
                  textAlignVertical="top"
                />
                {rawText.length > 0 && (
                  <View style={styles.rawTextSection}>
                    <Text style={styles.rawTextLabel}>
                      {t("setup.rawTextLabel")}
                    </Text>
                    <Text style={styles.rawTextValue}>{rawText}</Text>
                  </View>
                )}

                <View style={styles.plannedHeaderRow}>
                  <Text
                    style={[
                      styles.sectionLabel,
                      { marginTop: 0, marginBottom: 0 },
                    ]}
                  >
                    {t("setup.plannedMovements")}
                  </Text>
                  <TouchableOpacity
                    style={styles.addMoreBtn}
                    onPress={() => {
                      setVerifyVisible(false);
                      setInputMethod("movements");
                    }}
                  >
                    <Text style={styles.addMoreBtnText}>
                      {t("setup.addMoreMovements")}
                    </Text>
                  </TouchableOpacity>
                </View>

                {selectedMovements.length > 0 ? (
                  <View style={styles.confirmChipsWrap}>
                    {selectedMovements.map((m) => (
                      <View key={m} style={styles.confirmChip}>
                        <Text style={styles.confirmChipText}>{m}</Text>
                        <TouchableOpacity
                          onPress={() => toggleMovement(m)}
                          hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}
                        >
                          <IconSymbol
                            name="xmark.circle.fill"
                            size={14}
                            color={T.volt}
                          />
                        </TouchableOpacity>
                      </View>
                    ))}
                  </View>
                ) : (
                  <Text style={styles.noMovementsText}>
                    {t("setup.noMovementsSelected")}
                  </Text>
                )}
              </TacticalCard>

              {/* Pre-WOD Fatigue Strategy & Related WODs */}
              <PreWodStrategyCard
                advice={preWodAdvice}
                loading={adviceLoading}
              />
              <RelatedWodsCard related={relatedWods} />

              {/* Optical Tracking Anchor (Appearance Hints) Bento */}
              <TacticalCard style={styles.appearanceCard}>
                <View style={styles.appearanceHeaderRow}>
                  <View style={styles.appearanceTitleLeft}>
                    <IconSymbol
                      name="viewfinder"
                      size={16}
                      color={T.electricBlue}
                    />
                    <Text style={styles.appearanceTitle} numberOfLines={1}>
                      {t("appleAi.appearanceLabel")}
                    </Text>
                  </View>
                  <View style={styles.optionalPill}>
                    <Text style={styles.optionalPillText}>
                      {t("setup.optionalBadge")}
                    </Text>
                  </View>
                </View>
                <Text style={styles.appearanceHelp}>
                  {t("appleAi.appearanceHelp")}
                </Text>
                <TextInput
                  style={styles.appearanceInput}
                  placeholder={t("appleAi.appearancePlaceholder")}
                  accessibilityLabel={t("appleAi.appearanceLabel")}
                  maxLength={300}
                  placeholderTextColor={T.inkMuted}
                  value={sessionAppearance}
                  onChangeText={setSessionAppearance}
                />
                {videoPrefs.environmentObservation &&
                  videoPrefs.environmentAnalysis && (
                    <Text style={styles.contextPreviewText}>
                      {t("environment.contextPreview", {
                        appearance:
                          sessionAppearance.trim() ||
                          t("environment.targetMissing"),
                        movements:
                          selectedMovements.join(", ") ||
                          t("appleAi.contextUnspecified"),
                        wod:
                          wodDescription.trim() ||
                          t("appleAi.contextUnspecified"),
                      })}
                    </Text>
                  )}
              </TacticalCard>

              {/* Stationary Rig Framing Guide Bento */}
              <View style={styles.rigGuideCard}>
                <View style={styles.rigGuideIconBox}>
                  <IconSymbol name="viewfinder" size={20} color={T.volt} />
                </View>
                <View style={styles.rigGuideBody}>
                  <View style={styles.rigGuideHeaderRow}>
                    <Text style={styles.rigGuideTitle}>
                      {t("setup.rigSetupTitle")}
                    </Text>
                    <View style={styles.rigDistanceBadge}>
                      <Text style={styles.rigDistanceText}>
                        {t("setup.rigSetupDistance")}
                      </Text>
                    </View>
                  </View>
                  <Text style={styles.rigGuideDesc}>
                    {t("setup.rigSetupDesc")}
                  </Text>
                </View>
              </View>

              {/* Advanced Options Accordion */}
              <TouchableOpacity
                style={styles.advancedHeader}
                onPress={() => {
                  animateTacticalLayout(reduceMotion);
                  setAdvancedExpanded(!advancedExpanded);
                }}
                activeOpacity={0.75}
              >
                <View style={styles.advancedHeaderLeft}>
                  <IconSymbol
                    name="slider.horizontal.3"
                    size={16}
                    color={T.inkSecondary}
                  />
                  <Text style={styles.advancedHeaderText}>
                    {t("setup.advancedOptions")}
                  </Text>
                </View>
                <IconSymbol
                  name={advancedExpanded ? "chevron.up" : "chevron.down"}
                  size={18}
                  color={T.inkMuted}
                />
              </TouchableOpacity>

              {advancedExpanded && (
                <View style={styles.advancedSection}>
                  <View style={styles.optionRow}>
                    <View style={{ flex: 1, paddingRight: 12 }}>
                      <Text style={styles.optionLabel}>
                        {t("environment.option")}
                      </Text>
                      <Text style={styles.optionHelp}>
                        {t("environment.description")}
                      </Text>
                    </View>
                    <Switch
                      accessibilityLabel={t("environment.option")}
                      value={videoPrefs.environmentObservation}
                      onValueChange={(v) =>
                        updatePref("environmentObservation", v)
                      }
                      trackColor={{
                        false: T.borderStrong,
                        true: T.electricBlue,
                      }}
                    />
                  </View>

                  {videoPrefs.environmentObservation && (
                    <View style={styles.optionRow}>
                      <View style={{ flex: 1, paddingRight: 12 }}>
                        <Text style={styles.optionLabel}>
                          {t("environment.analysisOption")}
                        </Text>
                        <Text style={styles.optionHelp}>
                          {t("environment.measurementsOnly")}
                        </Text>
                      </View>
                      <Switch
                        value={videoPrefs.environmentAnalysis}
                        onValueChange={(v) =>
                          updatePref("environmentAnalysis", v)
                        }
                        accessibilityLabel={t("environment.analysisOption")}
                        trackColor={{
                          false: T.borderStrong,
                          true: T.electricBlue,
                        }}
                      />
                    </View>
                  )}

                  {videoPrefs.environmentObservation && (
                    <View style={styles.optionRow}>
                      <Text style={styles.optionLabel}>
                        {t("environment.interval")}
                      </Text>
                      <View style={styles.toggleGroup}>
                        {([120, 300, 600] as const).map((seconds) => {
                          const active =
                            videoPrefs.observationIntervalSeconds === seconds;
                          return (
                            <TouchableOpacity
                              key={seconds}
                              style={[
                                styles.toggleBtn,
                                active && styles.toggleActive,
                              ]}
                              onPress={() =>
                                updatePref(
                                  "observationIntervalSeconds",
                                  seconds,
                                )
                              }
                            >
                              <Text
                                style={[
                                  styles.toggleText,
                                  active && styles.toggleTextActive,
                                ]}
                              >
                                {t("environment.seconds", { count: seconds })}
                              </Text>
                            </TouchableOpacity>
                          );
                        })}
                      </View>
                    </View>
                  )}

                  {Platform.OS === "ios" && (
                    <View style={styles.optionRow}>
                      <View style={{ flex: 1, marginRight: 12 }}>
                        <Text style={styles.optionLabel}>
                          {t("setup.continuousRecording")}
                        </Text>
                        <Text style={styles.optionHelp}>
                          {t("setup.continuousRecordingHelp")}
                        </Text>
                      </View>
                      <Switch
                        value={videoPrefs.continuousRecording}
                        onValueChange={(value) =>
                          updatePref("continuousRecording", value)
                        }
                        trackColor={{
                          false: T.borderStrong,
                          true: T.electricBlue,
                        }}
                      />
                    </View>
                  )}

                  {Platform.OS === "ios" && (
                    <View style={styles.optionRow}>
                      <View style={{ flex: 1, marginRight: 12 }}>
                        <Text style={styles.optionLabel}>
                          {t("setup.h10MemoryRecording")}
                        </Text>
                        <Text style={styles.optionHelp}>
                          {t("setup.h10MemoryRecordingHelp")}
                        </Text>
                      </View>
                      <Switch
                        value={videoPrefs.h10MemoryRecording}
                        accessibilityLabel={t("setup.h10MemoryRecording")}
                        onValueChange={(value) =>
                          updatePref("h10MemoryRecording", value)
                        }
                        trackColor={{
                          false: T.borderStrong,
                          true: T.electricBlue,
                        }}
                      />
                    </View>
                  )}

                  <View style={styles.optionRow}>
                    <View style={{ flex: 1, marginRight: 12 }}>
                      <Text style={styles.optionLabel}>
                        {t("setup.bleHrDevice")}
                      </Text>
                      <Text style={styles.optionHelp}>
                        {registeredBleDevice
                          ? t("setup.bleHrDeviceRegistered", {
                              name: registeredBleDevice.name,
                            })
                          : t("setup.bleHrDeviceUnregistered")}
                      </Text>
                    </View>
                    {registeredBleDevice && (
                      <TouchableOpacity
                        style={styles.unregisterBtn}
                        onPress={() => {
                          setRegisteredBleDeviceState(null);
                          void setRegisteredBleDevice(null);
                        }}
                      >
                        <Text style={styles.unregisterBtnText}>
                          {t("setup.bleHrDeviceClear")}
                        </Text>
                      </TouchableOpacity>
                    )}
                  </View>

                  <View style={styles.optionRow}>
                    <Text style={styles.optionLabel}>
                      {t("setup.resolution")}
                    </Text>
                    <View style={styles.toggleGroup}>
                      {(["720p", "1080p"] as const).map((res) => {
                        const active = videoPrefs.resolution === res;
                        return (
                          <TouchableOpacity
                            key={res}
                            style={[
                              styles.toggleBtn,
                              active && styles.toggleActive,
                            ]}
                            onPress={() => updatePref("resolution", res)}
                          >
                            <Text
                              style={[
                                styles.toggleText,
                                active && styles.toggleTextActive,
                              ]}
                            >
                              {res}
                            </Text>
                          </TouchableOpacity>
                        );
                      })}
                      <TouchableOpacity
                        style={[
                          styles.toggleBtn,
                          videoPrefs.resolution === "2160p" &&
                            styles.toggleActive,
                          !is4KCapable && styles.toggleDisabled,
                        ]}
                        onPress={() => {
                          if (!is4KCapable) {
                            Alert.alert(
                              t("setup.resolution"),
                              t("setup.resolution4kUnsupportedAlert"),
                            );
                            return;
                          }
                          updatePref("resolution", "2160p");
                        }}
                      >
                        <Text
                          style={[
                            styles.toggleText,
                            videoPrefs.resolution === "2160p" &&
                              styles.toggleTextActive,
                            !is4KCapable && styles.toggleTextDisabled,
                          ]}
                        >
                          {t("setup.resolution4k")}
                        </Text>
                      </TouchableOpacity>
                    </View>
                  </View>

                  {(videoPrefs.resolution === "2160p" || !is4KCapable) && (
                    <View style={styles.resolutionBadgeContainer}>
                      <Text
                        style={
                          videoPrefs.resolution === "2160p"
                            ? styles.resolutionBadge
                            : styles.resolutionOptimizedBadge
                        }
                      >
                        {videoPrefs.resolution === "2160p"
                          ? t("setup.resolution4kBadge")
                          : t("setup.resolutionOptimizedBadge")}
                      </Text>
                    </View>
                  )}

                  <View style={[styles.optionRow, { marginTop: 12 }]}>
                    <Text style={styles.optionLabel}>
                      {t("setup.skeletonOverlay")}
                    </Text>
                    <Switch
                      value={videoPrefs.showSkeleton}
                      onValueChange={(v) => updatePref("showSkeleton", v)}
                      trackColor={{
                        false: T.borderStrong,
                        true: T.electricBlue,
                      }}
                    />
                  </View>

                  <View
                    style={[
                      styles.optionRow,
                      { marginTop: 12, marginBottom: 0 },
                    ]}
                  >
                    <Text style={styles.optionLabel}>
                      {t("setup.autoRecord")}
                    </Text>
                    <Switch
                      value={videoPrefs.autoRecord}
                      onValueChange={(v) => updatePref("autoRecord", v)}
                      trackColor={{
                        false: T.borderStrong,
                        true: T.electricBlue,
                      }}
                    />
                  </View>
                </View>
              )}
            </ScrollView>

            {/* Popup Sticky Footer */}
            <View style={styles.popupFooter}>
              <View style={styles.confirmSecondaryActionsRow}>
                {(wodDescription.trim().length > 0 ||
                  rawText.trim().length > 0 ||
                  selectedMovements.length > 0) && (
                  <TouchableOpacity
                    testID="save-workout-for-later-confirm-btn"
                    style={[styles.previewBtn, { flex: 1 }]}
                    onPress={handleSaveForLater}
                    activeOpacity={0.75}
                  >
                    <IconSymbol
                      name="bookmark.fill"
                      size={15}
                      color={T.electricBlue}
                    />
                    <Text style={styles.previewBtnText} numberOfLines={1}>
                      {t("setup.saveForLater")}
                    </Text>
                  </TouchableOpacity>
                )}
                <TouchableOpacity
                  style={[styles.previewBtn, { flex: 1 }]}
                  onPress={handlePreview}
                  activeOpacity={0.75}
                >
                  <IconSymbol name="camera.fill" size={15} color={T.ink} />
                  <Text style={styles.previewBtnText} numberOfLines={1}>
                    {t("setup.previewCamera")}
                  </Text>
                </TouchableOpacity>
              </View>
              <VoltButton
                testID="setup-start-workout-btn"
                label={t("setup.startWorkout")}
                leftIcon={
                  <IconSymbol name="flame.fill" size={20} color={T.voltInk} />
                }
                onPress={handleStart}
                disabled={isStarting}
              />
            </View>
          </Animated.View>
        </KeyboardAvoidingView>
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: T.canvas,
  },
  mainScreenLayer: {
    flex: 1,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderBottomWidth: 1,
    borderBottomColor: T.border,
    backgroundColor: T.canvas,
  },
  backBtn: {
    width: 38,
    height: 38,
    borderRadius: 12,
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    justifyContent: "center",
    alignItems: "center",
  },
  headerCenter: {
    flex: 1,
    alignItems: "center",
    paddingHorizontal: 8,
  },
  headerEyebrow: {
    fontFamily: Fonts?.mono,
    fontSize: 10,
    fontWeight: "800",
    color: T.inkMuted,
    letterSpacing: 1.1,
    textTransform: "uppercase",
  },
  title: {
    fontSize: 17,
    fontWeight: "900",
    color: T.ink,
    letterSpacing: 0.4,
    textTransform: "uppercase",
    marginTop: 1,
  },
  headerRightSpacer: {
    width: 38,
  },

  // Segmented Workout Stage Switcher
  workoutTypeTrack: {
    flexDirection: "row",
    marginHorizontal: 16,
    marginTop: 12,
    padding: 4,
    borderRadius: 14,
    backgroundColor: T.surfaceContainer,
    gap: 4,
  },
  workoutTypeTab: {
    flex: 1,
    paddingVertical: 8,
    paddingHorizontal: 4,
    borderRadius: 10,
    alignItems: "center",
    justifyContent: "center",
  },
  workoutTypeTabActive: {
    backgroundColor: T.surface,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 1 },
    shadowOpacity: 0.08,
    shadowRadius: 3,
    elevation: 1,
  },
  workoutTypeTabText: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.3,
  },
  workoutTypeTabTextActive: {
    color: T.ink,
  },
  workoutTypeVoltBar: {
    width: 18,
    height: 3,
    borderRadius: 1.5,
    backgroundColor: T.volt,
    marginTop: 3,
  },

  // Input Method Tabs
  inputMethodTabs: {
    flexDirection: "row",
    paddingHorizontal: 16,
    paddingTop: 10,
    paddingBottom: 4,
    gap: 8,
  },
  inputMethodTab: {
    flex: 1,
    paddingVertical: 11,
    borderRadius: 12,
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    alignItems: "center",
  },
  inputMethodTabActive: {
    backgroundColor: T.surfaceInverse,
    borderColor: T.surfaceInverse,
  },
  inputMethodTabInner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  inputMethodTabText: {
    color: T.inkSecondary,
    fontSize: 13,
    fontWeight: "800",
    flexShrink: 1,
  },
  inputMethodTabTextActive: {
    color: T.inkInverse,
  },
  backToPrimaryBar: {
    marginHorizontal: 16,
    marginTop: 10,
    marginBottom: 4,
    paddingVertical: 9,
    paddingHorizontal: 14,
    alignSelf: "flex-start",
    borderRadius: 10,
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  backToPrimaryText: {
    color: T.electricBlue,
    fontSize: 13,
    fontWeight: "800",
  },

  // Saved Workout Reuse Banner
  recentReuseCard: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: T.surface,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: T.voltBorder,
    borderLeftWidth: 4,
    borderLeftColor: T.oliveAccent,
    marginHorizontal: 16,
    marginTop: 8,
    marginBottom: 4,
    paddingHorizontal: 14,
    paddingVertical: 12,
    gap: 10,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 6,
    elevation: 2,
  },
  recentReuseMainTouch: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 10,
  },
  recentReuseContent: {
    flex: 1,
    gap: 2,
  },
  recentReuseTitle: {
    color: T.oliveAccent,
    fontSize: 12,
    fontWeight: "900",
    letterSpacing: 0.2,
  },
  recentReuseSummary: {
    color: T.ink,
    fontSize: 13,
    fontWeight: "700",
  },
  recentReuseMeta: {
    color: T.inkMuted,
    fontSize: 11,
  },
  recentReuseBadge: {
    backgroundColor: T.volt,
    borderWidth: 1,
    borderColor: T.voltBorder,
    borderRadius: 8,
    paddingHorizontal: 11,
    paddingVertical: 6,
    flexShrink: 0,
  },
  recentReuseBadgeText: {
    color: T.voltInk,
    fontSize: 12,
    fontWeight: "900",
    textTransform: "uppercase",
  },

  // Scroll containers
  movementList: { flex: 1 },
  movementListContent: {
    paddingHorizontal: 16,
    paddingTop: 10,
    paddingBottom: 170,
  },
  confirmListContent: {
    paddingHorizontal: 16,
    paddingTop: 12,
    paddingBottom: 190,
  },

  // Text Mode Bento
  textModeBento: {
    padding: 16,
  },
  sectionLabel: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.8,
    marginBottom: 6,
  },
  preInputHint: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 18,
    marginBottom: 10,
  },
  wodTextInput: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 12,
    padding: 14,
    color: T.ink,
    fontSize: 15,
    borderWidth: 1,
    borderColor: T.borderStrong,
    minHeight: 110,
    textAlignVertical: "top",
  },
  saveLaterBtn: {
    marginTop: 12,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 8,
    paddingVertical: 11,
    borderRadius: 12,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  saveLaterBtnText: {
    color: T.electricBlue,
    fontSize: 13,
    fontWeight: "800",
  },

  // Photo Mode Bento
  photoHeroHeader: {
    marginTop: 4,
    marginBottom: 14,
    gap: 6,
  },
  photoHeroTitleRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
  },
  photoHeroTitle: {
    color: T.ink,
    fontSize: 20,
    fontWeight: "900",
    letterSpacing: -0.3,
    flexShrink: 1,
  },
  autoOcrPill: {
    backgroundColor: T.surfaceInverse,
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 3,
    flexShrink: 0,
  },
  autoOcrPillText: {
    fontFamily: Fonts?.mono,
    color: T.volt,
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 0.6,
  },
  photoHeroSubtitle: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 19,
  },
  photoBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 14,
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 16,
    marginBottom: 10,
    borderWidth: 1,
    borderColor: T.border,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 6,
    elevation: 1,
  },
  photoBtnPrimary: {
    borderColor: T.ink,
    borderWidth: 1.5,
  },
  photoIconBadge: {
    width: 48,
    height: 48,
    borderRadius: 14,
    backgroundColor: T.surfaceInverse,
    justifyContent: "center",
    alignItems: "center",
  },
  photoIconBadgeSecondary: {
    width: 48,
    height: 48,
    borderRadius: 14,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    justifyContent: "center",
    alignItems: "center",
  },
  photoBtnBody: {
    flex: 1,
  },
  photoBtnTitleRow: {
    flexDirection: "row",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 6,
  },
  photoBtnText: {
    color: T.ink,
    fontSize: 16,
    fontWeight: "800",
    flexShrink: 1,
  },
  recommendedPill: {
    backgroundColor: T.volt,
    borderRadius: 5,
    paddingHorizontal: 6,
    paddingVertical: 2,
    borderWidth: 1,
    borderColor: T.voltBorder,
  },
  recommendedPillText: {
    fontFamily: Fonts?.mono,
    color: T.voltInk,
    fontSize: 9,
    fontWeight: "900",
    letterSpacing: 0.4,
  },
  photoBtnSubtext: {
    color: T.inkMuted,
    fontSize: 12,
    marginTop: 3,
  },
  scanningContainer: {
    alignItems: "center",
    paddingVertical: 60,
    gap: 16,
  },
  scanningText: {
    color: T.electricBlue,
    fontSize: 14,
    fontWeight: "700",
  },

  // Quick Pick Movements + Secondary Link Card
  quickMovementCard: {
    marginTop: 8,
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    padding: 14,
    gap: 10,
  },
  quickMovementHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
  },
  quickMovementLabel: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 0.8,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  quickMovementCount: {
    fontFamily: Fonts?.mono,
    color: T.electricBlue,
    fontSize: 10,
    fontWeight: "700",
  },
  quickPicksWrap: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
  },
  quickPickChip: {
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 8,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
  },
  quickPickChipActive: {
    backgroundColor: T.surfaceInverse,
    borderColor: T.surfaceInverse,
  },
  quickPickChipText: {
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "700",
  },
  quickPickChipTextActive: {
    color: T.volt,
  },
  secondaryMovementsLink: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingVertical: 10,
    paddingHorizontal: 12,
    borderRadius: 10,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
  },
  secondaryMovementsText: {
    flex: 1,
    color: T.ink,
    fontSize: 13,
    fontWeight: "700",
  },

  // Sensor Sync Snippet Bento
  sensorSyncCard: {
    marginTop: 10,
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: T.surface,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: T.border,
    paddingHorizontal: 14,
    paddingVertical: 12,
    gap: 10,
  },
  sensorSyncIconWrap: {
    width: 36,
    height: 36,
    borderRadius: 10,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    justifyContent: "center",
    alignItems: "center",
  },
  sensorSyncIconWrapActive: {
    backgroundColor: T.heartRoseBg,
    borderColor: T.heartRoseBorder,
  },
  sensorSyncBody: {
    flex: 1,
  },
  sensorSyncEyebrow: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 9,
    fontWeight: "800",
    letterSpacing: 0.7,
    textTransform: "uppercase",
  },
  sensorSyncTitle: {
    color: T.ink,
    fontSize: 13,
    fontWeight: "800",
    marginTop: 1,
    flexShrink: 1,
  },
  sensorSyncBadge: {
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 6,
    borderWidth: 1,
    flexShrink: 0,
  },
  sensorSyncBadgeReady: {
    backgroundColor: T.statusFreshBg,
    borderColor: T.statusFreshBorder,
  },
  sensorSyncBadgeMuted: {
    backgroundColor: T.surfaceMuted,
    borderColor: T.border,
  },
  sensorSyncBadgeText: {
    fontFamily: Fonts?.mono,
    fontSize: 10,
    fontWeight: "800",
  },
  sensorSyncBadgeTextReady: {
    color: T.statusFresh,
  },
  sensorSyncBadgeTextMuted: {
    color: T.inkMuted,
  },

  // Search & Filter (Movements Mode)
  searchContainer: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 16,
    marginTop: 8,
    marginBottom: 8,
    backgroundColor: T.surface,
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderWidth: 1,
    borderColor: T.borderStrong,
  },
  searchInput: {
    flex: 1,
    color: T.ink,
    fontSize: 15,
    marginLeft: 10,
    paddingVertical: 0,
  },
  filterTabs: {
    maxHeight: 42,
    marginBottom: 4,
  },
  filterTabsContent: {
    paddingHorizontal: 16,
    gap: 8,
    alignItems: "center",
  },
  filterTab: {
    paddingHorizontal: 14,
    paddingVertical: 7,
    borderRadius: 20,
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
  },
  filterTabActive: {
    backgroundColor: T.surfaceInverse,
    borderColor: T.surfaceInverse,
  },
  filterTabText: {
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.4,
  },
  filterTabTextActive: {
    color: T.volt,
  },
  addCustomBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    backgroundColor: T.electricBlueBg,
    paddingVertical: 14,
    paddingHorizontal: 16,
    borderRadius: 12,
    marginBottom: 14,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  addCustomText: {
    color: T.electricBlue,
    fontSize: 14,
    fontWeight: "800",
    flexShrink: 1,
  },
  groupSection: {
    marginBottom: 12,
  },
  groupHeader: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "800",
    letterSpacing: 1.2,
    marginBottom: 8,
    marginTop: 6,
    paddingLeft: 4,
  },
  movementCard: {
    flexDirection: "row",
    alignItems: "center",
    backgroundColor: T.surface,
    borderRadius: 12,
    padding: 12,
    marginBottom: 6,
    borderWidth: 1,
    borderColor: T.border,
  },
  movementCardSelected: {
    borderColor: T.ink,
    borderWidth: 1.5,
    backgroundColor: T.surfaceMuted,
  },
  movementCardIcon: {
    width: 36,
    height: 36,
    borderRadius: 10,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 12,
  },
  movementCardIconSelected: {
    backgroundColor: T.surfaceInverse,
    borderColor: T.surfaceInverse,
  },
  movementCardInfo: {
    flex: 1,
  },
  movementName: {
    color: T.inkSecondary,
    fontSize: 14,
    fontWeight: "700",
    letterSpacing: 0.3,
  },
  movementNameSelected: {
    color: T.ink,
    fontWeight: "900",
  },
  movementCategory: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "600",
    marginTop: 2,
  },
  checkbox: {
    width: 24,
    height: 24,
    borderRadius: 12,
    borderWidth: 2,
    borderColor: T.borderStrong,
    justifyContent: "center",
    alignItems: "center",
  },
  checkboxSelected: {
    backgroundColor: T.volt,
    borderColor: T.voltBorder,
  },
  emptyState: {
    alignItems: "center",
    paddingVertical: 40,
  },
  emptyText: {
    color: T.inkMuted,
    fontSize: 14,
  },

  // Step 2: Confirm / Verify Workout
  profileBanner: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    backgroundColor: T.surface,
    padding: 14,
    borderRadius: 16,
    marginBottom: 12,
    borderWidth: 1,
    borderColor: T.border,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 1 },
    shadowOpacity: 0.04,
    shadowRadius: 4,
    elevation: 1,
  },
  profileBannerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    flex: 1,
  },
  miniAvatar: {
    width: 40,
    height: 40,
    borderRadius: 12,
    backgroundColor: T.surfaceInverse,
    justifyContent: "center",
    alignItems: "center",
  },
  miniAvatarText: {
    color: T.volt,
    fontSize: 15,
    fontWeight: "900",
  },
  profileBannerTextCol: {
    flex: 1,
  },
  profileBannerName: {
    color: T.ink,
    fontSize: 16,
    fontWeight: "800",
    flexShrink: 1,
  },
  profileBannerMeta: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "600",
    marginTop: 2,
    flexShrink: 1,
  },
  verifyBentoCard: {
    padding: 16,
  },
  confirmHint: {
    color: T.inkSecondary,
    fontSize: 13,
    marginBottom: 12,
    lineHeight: 18,
  },
  rawTextSection: {
    backgroundColor: T.surfaceMuted,
    borderRadius: 10,
    padding: 12,
    marginTop: 10,
    borderWidth: 1,
    borderColor: T.border,
  },
  rawTextLabel: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 10,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: 4,
  },
  rawTextValue: {
    color: T.inkSecondary,
    fontSize: 13,
    lineHeight: 18,
  },
  plannedHeaderRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginTop: 16,
    marginBottom: 10,
    gap: 8,
  },
  addMoreBtn: {
    paddingVertical: 4,
    paddingHorizontal: 8,
    borderRadius: 8,
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
  },
  addMoreBtnText: {
    color: T.electricBlue,
    fontSize: 12,
    fontWeight: "800",
  },
  confirmChipsWrap: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  confirmChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: T.surfaceInverse,
    borderRadius: 10,
    paddingVertical: 7,
    paddingLeft: 12,
    paddingRight: 8,
  },
  confirmChipText: {
    color: T.inkInverse,
    fontSize: 13,
    fontWeight: "800",
  },
  noMovementsText: {
    color: T.inkMuted,
    fontSize: 13,
    fontStyle: "italic",
  },

  // Optical Tracking Anchor Bento
  appearanceCard: {
    marginTop: 14,
    padding: 16,
  },
  appearanceHeaderRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 8,
    marginBottom: 6,
  },
  appearanceTitleLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    flex: 1,
  },
  appearanceTitle: {
    color: T.ink,
    fontSize: 14,
    fontWeight: "800",
    flexShrink: 1,
  },
  optionalPill: {
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    borderRadius: 6,
    paddingHorizontal: 7,
    paddingVertical: 2,
    flexShrink: 0,
  },
  optionalPillText: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 9,
    fontWeight: "800",
    letterSpacing: 0.4,
  },
  appearanceHelp: {
    color: T.inkMuted,
    fontSize: 12,
    lineHeight: 17,
    marginBottom: 10,
  },
  appearanceInput: {
    backgroundColor: T.surfaceMuted,
    color: T.ink,
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: T.borderStrong,
    fontSize: 14,
  },
  contextPreviewText: {
    color: T.electricBlue,
    fontSize: 12,
    lineHeight: 17,
    marginTop: 10,
  },

  // Stationary Rig Framing Guide
  rigGuideCard: {
    marginTop: 14,
    flexDirection: "row",
    alignItems: "flex-start",
    backgroundColor: T.surface,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: T.border,
    padding: 14,
    gap: 12,
  },
  rigGuideIconBox: {
    width: 40,
    height: 40,
    borderRadius: 12,
    backgroundColor: T.surfaceInverse,
    justifyContent: "center",
    alignItems: "center",
    marginTop: 2,
  },
  rigGuideBody: {
    flex: 1,
    gap: 4,
  },
  rigGuideHeaderRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    flexWrap: "wrap",
    gap: 6,
  },
  rigGuideTitle: {
    color: T.ink,
    fontSize: 14,
    fontWeight: "800",
    flexShrink: 1,
  },
  rigDistanceBadge: {
    backgroundColor: T.electricBlueBg,
    borderWidth: 1,
    borderColor: T.electricBlueBorder,
    borderRadius: 6,
    paddingHorizontal: 7,
    paddingVertical: 2,
  },
  rigDistanceText: {
    fontFamily: Fonts?.mono,
    color: T.electricBlue,
    fontSize: 10,
    fontWeight: "800",
  },
  rigGuideDesc: {
    color: T.inkSecondary,
    fontSize: 12,
    lineHeight: 17,
  },

  // Advanced Options
  advancedHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    backgroundColor: T.surface,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: T.border,
    paddingVertical: 13,
    paddingHorizontal: 14,
    marginTop: 14,
  },
  advancedHeaderLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    flexShrink: 1,
  },
  advancedHeaderText: {
    fontFamily: Fonts?.mono,
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "800",
    textTransform: "uppercase",
    letterSpacing: 0.7,
    flexShrink: 1,
  },
  advancedSection: {
    backgroundColor: T.surface,
    borderRadius: 16,
    padding: 16,
    marginTop: 8,
    marginBottom: 16,
    borderWidth: 1,
    borderColor: T.border,
  },
  optionRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 14,
  },
  optionLabel: {
    color: T.ink,
    fontSize: 14,
    fontWeight: "700",
    flexShrink: 1,
  },
  optionHelp: {
    color: T.inkMuted,
    fontSize: 12,
    lineHeight: 17,
    marginTop: 3,
  },
  toggleGroup: {
    flexDirection: "row",
    backgroundColor: T.surfaceContainer,
    borderRadius: 10,
    padding: 3,
    gap: 2,
  },
  toggleBtn: {
    paddingVertical: 6,
    paddingHorizontal: 11,
    borderRadius: 8,
  },
  toggleActive: {
    backgroundColor: T.surfaceInverse,
  },
  toggleDisabled: {
    opacity: 0.35,
  },
  toggleText: {
    color: T.inkSecondary,
    fontWeight: "800",
    fontSize: 12,
  },
  toggleTextActive: {
    color: T.volt,
  },
  toggleTextDisabled: {
    color: T.inkMuted,
  },
  unregisterBtn: {
    paddingVertical: 6,
    paddingHorizontal: 12,
    borderRadius: 8,
    backgroundColor: T.statusExhaustedBg,
    borderWidth: 1,
    borderColor: T.statusExhaustedBorder,
  },
  unregisterBtnText: {
    color: T.statusExhausted,
    fontSize: 12,
    fontWeight: "800",
  },
  resolutionBadgeContainer: {
    alignItems: "flex-end",
    marginTop: -6,
    marginBottom: 8,
  },
  resolutionBadge: {
    color: T.electricBlue,
    fontSize: 11,
    fontWeight: "700",
  },
  resolutionOptimizedBadge: {
    color: T.inkMuted,
    fontSize: 11,
    fontWeight: "600",
  },

  // Sticky Bottom Dock
  bottomBar: {
    position: "absolute",
    bottom: 0,
    left: 0,
    right: 0,
    paddingHorizontal: 16,
    paddingTop: 12,
    paddingBottom: 28,
    backgroundColor: T.dockBg,
    borderTopWidth: 1,
    borderTopColor: T.border,
  },
  stagedChipsScroll: {
    maxHeight: 36,
    marginBottom: 10,
  },
  stagedChipsContent: {
    gap: 6,
    paddingHorizontal: 2,
  },
  stagedChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: T.surfaceInverse,
    borderRadius: 10,
    paddingVertical: 6,
    paddingLeft: 11,
    paddingRight: 8,
  },
  stagedChipText: {
    color: T.inkInverse,
    fontSize: 12,
    fontWeight: "800",
  },
  confirmSecondaryActionsRow: {
    flexDirection: "row",
    gap: 8,
    marginBottom: 10,
  },
  previewBtn: {
    flexDirection: "row",
    justifyContent: "center",
    alignItems: "center",
    gap: 6,
    paddingVertical: 10,
    paddingHorizontal: 12,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: T.borderStrong,
    backgroundColor: T.surface,
  },
  previewBtnText: {
    color: T.ink,
    fontSize: 13,
    fontWeight: "800",
    flexShrink: 1,
  },

  // Verify Workout Confirmation Popup Sheet
  popupOverlay: {
    position: "absolute",
    top: 0,
    right: 0,
    bottom: 0,
    left: 0,
    justifyContent: "flex-end",
    zIndex: 50,
  },
  popupBackdrop: {
    position: "absolute",
    top: 0,
    right: 0,
    bottom: 0,
    left: 0,
    backgroundColor: "rgba(11, 14, 20, 0.56)",
  },
  popupSheet: {
    maxHeight: "88%",
    marginHorizontal: 10,
    marginBottom: Platform.OS === "ios" ? 20 : 12,
    backgroundColor: T.canvas,
    borderRadius: 24,
    borderWidth: 1,
    borderColor: T.borderStrong,
    overflow: "hidden",
    shadowColor: "#0B0E14",
    shadowOffset: { width: 0, height: -8 },
    shadowOpacity: 0.25,
    shadowRadius: 24,
    elevation: 18,
  },
  popupHandleBar: {
    alignSelf: "center",
    width: 38,
    height: 4,
    borderRadius: 2,
    backgroundColor: T.borderStrong,
    marginTop: 10,
    marginBottom: 4,
  },
  popupHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderBottomWidth: 1,
    borderBottomColor: T.border,
    backgroundColor: T.surface,
    gap: 10,
  },
  popupHeaderLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    flex: 1,
  },
  popupShieldBadge: {
    width: 34,
    height: 34,
    borderRadius: 10,
    backgroundColor: T.surfaceInverse,
    justifyContent: "center",
    alignItems: "center",
  },
  popupTitleCol: {
    flex: 1,
  },
  popupTitle: {
    fontSize: 16,
    fontWeight: "900",
    color: T.ink,
    letterSpacing: 0.3,
    textTransform: "uppercase",
    flexShrink: 1,
  },
  popupCloseBtn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    justifyContent: "center",
    alignItems: "center",
  },
  popupScroll: {
    flexGrow: 0,
  },
  popupScrollContent: {
    paddingHorizontal: 14,
    paddingTop: 12,
    paddingBottom: 16,
  },
  popupWodTextInput: {
    minHeight: 82,
  },
  popupFooter: {
    paddingHorizontal: 14,
    paddingTop: 10,
    paddingBottom: 14,
    backgroundColor: T.surface,
    borderTopWidth: 1,
    borderTopColor: T.border,
  },
});
