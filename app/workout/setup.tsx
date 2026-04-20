import { IconSymbol } from '@/components/ui/icon-symbol';
import { t } from '@/features/i18n';
import { fetchMovementGroups, type MovementGroup } from '@/features/wod/api';
import { useActiveProfile } from '@/store/useProfileStore';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { router } from 'expo-router';
import React, { useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  GestureResponderEvent,
  Platform,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';

const VIDEO_PREFS_KEY = 'wod_video_preferences';
const ALL_FILTER = 'All';

interface VideoPreferences {
  showSkeleton: boolean;
  lowFps: boolean;
  force720p: boolean;
  skipCompression: boolean;
  serialUpload: boolean;
  resolution: '720p' | '1080p';
}

function getDefaultVideoPrefs(): VideoPreferences {
  const isAndroid = Platform.OS === 'android';
  return {
    showSkeleton: !isAndroid,
    lowFps: isAndroid,
    force720p: isAndroid,
    skipCompression: isAndroid,
    serialUpload: isAndroid,
    resolution: '720p',
  };
}

// Category icons for the card list (must match icon-symbol.tsx MAPPING keys)
const CATEGORY_ICONS: Record<string, string> = {
  'Barbell': 'dumbbell.fill',
  'Dumbbell & Kettlebell': 'dumbbell.fill',
  'Gymnastics': 'figure.run',
  'Bodyweight & Plyo': 'flame.fill',
  'Cardio': 'figure.run',
  'Custom': 'plus.circle.fill',
};

function getCategoryIcon(category: string): string {
  return CATEGORY_ICONS[category] || 'dumbbell.fill';
}

export default function WorkoutSetup() {
  const workoutType = 'wod';
  const activeProfile = useActiveProfile();

  // Video preferences (persisted to AsyncStorage)
  const [videoPrefs, setVideoPrefs] = useState<VideoPreferences>(getDefaultVideoPrefs());
  const [prefsLoaded, setPrefsLoaded] = useState(false);
  const [advancedExpanded, setAdvancedExpanded] = useState(false);

  // Movements
  const [movementGroups, setMovementGroups] = useState<MovementGroup[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [customMovements, setCustomMovements] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  // Search + filter
  const [searchText, setSearchText] = useState('');
  const [activeFilter, setActiveFilter] = useState(ALL_FILTER);

  // Load persisted video preferences
  useEffect(() => {
    AsyncStorage.getItem(VIDEO_PREFS_KEY)
      .then((raw) => {
        if (raw) {
          try {
            const saved = JSON.parse(raw) as Partial<VideoPreferences>;
            setVideoPrefs((prev) => ({ ...prev, ...saved }));
          } catch {
            // ignore parse errors, use defaults
          }
        }
      })
      .finally(() => setPrefsLoaded(true));
  }, []);

  // Load movement groups
  useEffect(() => {
    fetchMovementGroups()
      .then(setMovementGroups)
      .catch((error) => {
        console.error("Failed to load movement groups", error);
      })
      .finally(() => setLoading(false));
  }, []);

  // Derive category filter tabs (include "Custom" when custom movements exist)
  const categoryTabs = useMemo(() => {
    const cats = movementGroups.map(g => g.category);
    if (customMovements.length > 0 && !cats.includes('Custom')) {
      cats.push('Custom');
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

  // Filter movements based on search + active category
  const filteredGroups = useMemo(() => {
    const query = searchText.toLowerCase().trim();
    let groups = movementGroups;

    // Add custom movements group if any
    if (customMovements.length > 0) {
      groups = [...groups, { category: 'Custom', movements: customMovements }];
    }

    // Filter by active category tab
    if (activeFilter !== ALL_FILTER) {
      groups = groups.filter(g => g.category === activeFilter);
    }

    // Filter by search text
    if (query) {
      groups = groups
        .map(g => ({
          ...g,
          movements: g.movements.filter(m => m.toLowerCase().includes(query)),
        }))
        .filter(g => g.movements.length > 0);
    }

    return groups;
  }, [movementGroups, customMovements, activeFilter, searchText]);

  // Check if search text matches an un-added custom movement
  const canAddCustom = useMemo(() => {
    const trimmed = searchText.trim();
    if (!trimmed) return false;
    // Check if it matches any existing movement (case-insensitive)
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
    setSelectedMovements(prev =>
      prev.includes(m) ? prev.filter(x => x !== m) : [...prev, m]
    );
  };

  const addCustomMovement = () => {
    const trimmed = searchText.trim();
    if (!trimmed) return;
    setCustomMovements(prev => [...prev, trimmed]);
    setSelectedMovements(prev => [...prev, trimmed]);
    setSearchText('');
  };

  const removeCustomMovement = (m: string) => {
    setCustomMovements(prev => prev.filter(x => x !== m));
    setSelectedMovements(prev => prev.filter(x => x !== m));
  };

  const updatePref = <K extends keyof VideoPreferences>(key: K, value: VideoPreferences[K]) => {
    setVideoPrefs(prev => ({ ...prev, [key]: value }));
  };

  const handleStart = async () => {

    // Persist video preferences for next session
    try {
      await AsyncStorage.setItem(VIDEO_PREFS_KEY, JSON.stringify(videoPrefs));
    } catch (e) {
      console.warn('Failed to persist video prefs:', e);
    }

    // Get injuries from active profile
    const injuries = activeProfile?.injuries ?? [];

    router.push({
      pathname: '/workout/visionTestPage',
      params: {
        resolution: videoPrefs.force720p ? '720p' : videoPrefs.resolution,
        workoutType,
        movements: selectedMovements.join(', '),
        injuries: injuries.join(', '),
        autoRecord: 'true',
        showSkeleton: videoPrefs.showSkeleton ? 'true' : 'false',
        lowFps: videoPrefs.lowFps ? 'true' : 'false',
        force720p: videoPrefs.force720p ? 'true' : 'false',
        skipCompression: videoPrefs.skipCompression ? 'true' : 'false',
        serialUpload: videoPrefs.serialUpload ? 'true' : 'false',
      },
    });
  };

  const injuryCount = activeProfile?.injuries?.length ?? 0;

  // Find which category a movement belongs to
  const getCategoryForMovement = (movement: string): string => {
    for (const g of movementGroups) {
      if (g.movements.includes(movement)) return g.category;
    }
    if (customMovements.includes(movement)) return 'Custom';
    return '';
  };

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>{t('setup.title')}</Text>
        <View style={{ width: 28 }} />
      </View>

      {/* Search bar */}
      <View style={styles.searchContainer}>
        <IconSymbol name="magnifyingglass" size={18} color="#666" />
        <TextInput
          style={styles.searchInput}
          placeholder={t('setup.searchMovements')}
          placeholderTextColor="#555"
          value={searchText}
          onChangeText={setSearchText}
          autoCapitalize="none"
          autoCorrect={false}
        />
        {searchText.length > 0 && (
          <TouchableOpacity onPress={() => setSearchText('')}>
            <IconSymbol name="xmark.circle.fill" size={18} color="#555" />
          </TouchableOpacity>
        )}
      </View>

      {/* Category filter tabs */}
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        contentContainerStyle={styles.filterTabsContent}
        style={styles.filterTabs}
      >
        {categoryTabs.map(tab => {
          const isActive = activeFilter === tab;
          return (
            <TouchableOpacity
              key={tab}
              style={[styles.filterTab, isActive && styles.filterTabActive]}
              onPress={() => setActiveFilter(tab)}
            >
              <Text style={[styles.filterTabText, isActive && styles.filterTabTextActive]}>
                {tab === ALL_FILTER ? t('setup.categoryAll') : tab}
              </Text>
            </TouchableOpacity>
          );
        })}
      </ScrollView>

      {/* Movement list */}
      <ScrollView
        contentContainerStyle={styles.movementListContent}
        style={styles.movementList}
      >
        {loading ? (
          <ActivityIndicator color="#00E5FF" style={{ marginTop: 40 }} />
        ) : (
          <>
            {/* Profile summary */}
            {activeProfile && (
              <TouchableOpacity
                style={styles.profileBanner}
                onPress={() => router.push(`/profile?id=${activeProfile.id}` as any)}
              >
                <View style={styles.profileBannerLeft}>
                  <View style={styles.miniAvatar}>
                    <Text style={styles.miniAvatarText}>
                      {activeProfile.name ? activeProfile.name[0].toUpperCase() : '?'}
                    </Text>
                  </View>
                  <View>
                    <Text style={styles.profileBannerName}>{activeProfile.name || t('tabs.profile')}</Text>
                    <Text style={styles.profileBannerMeta}>
                      {[
                        injuryCount > 0 ? t('setup.injuries', { count: injuryCount }) : t('setup.noInjuries'),
                        `${videoPrefs.force720p ? '720p' : videoPrefs.resolution}`,
                        videoPrefs.lowFps ? '24fps' : '30fps',
                      ].join(' · ')}
                    </Text>
                  </View>
                </View>
                <Text style={styles.chevron}>›</Text>
              </TouchableOpacity>
            )}

            {/* Add custom movement button */}
            {canAddCustom && (
              <TouchableOpacity style={styles.addCustomBtn} onPress={addCustomMovement}>
                <IconSymbol name="plus.circle.fill" size={20} color="#00E5FF" />
                <Text style={styles.addCustomText}>
                  {t('setup.addCustomMovement', { name: searchText.trim() })}
                </Text>
              </TouchableOpacity>
            )}

            {/* Movement cards by group */}
            {filteredGroups.map(group => (
              <View key={group.category} style={styles.groupSection}>
                <Text style={styles.groupHeader}>{group.category.toUpperCase()}</Text>
                {group.movements.map(movement => {
                  const isSelected = selectedMovements.includes(movement);
                  const isCustom = customMovements.includes(movement);
                  return (
                    <TouchableOpacity
                      key={movement}
                      style={[styles.movementCard, isSelected && styles.movementCardSelected]}
                      onPress={() => toggleMovement(movement)}
                    >
                      <View style={styles.movementCardIcon}>
                        <IconSymbol
                          name={getCategoryIcon(group.category) as any}
                          size={20}
                          color={isSelected ? '#00E5FF' : '#666'}
                        />
                      </View>
                      <View style={styles.movementCardInfo}>
                        <Text style={[styles.movementName, isSelected && styles.movementNameSelected]}>
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
                          hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
                        >
                          <IconSymbol name="xmark.circle" size={22} color="#666" />
                        </TouchableOpacity>
                      ) : (
                        <View style={[styles.checkbox, isSelected && styles.checkboxSelected]}>
                          {isSelected && (
                            <IconSymbol name="checkmark" size={14} color="#000" />
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
                <Text style={styles.emptyText}>{t('setup.noResults')}</Text>
              </View>
            )}

            {/* Advanced Options (collapsible) */}
            <TouchableOpacity
              style={styles.advancedHeader}
              onPress={() => setAdvancedExpanded(!advancedExpanded)}
            >
              <Text style={styles.advancedHeaderText}>{t('setup.advancedOptions')}</Text>
              <Text style={styles.advancedChevron}>{advancedExpanded ? '▼' : '▶'}</Text>
            </TouchableOpacity>

            {advancedExpanded && (
              <View style={styles.advancedSection}>
                <View style={styles.optionRow}>
                  <Text style={styles.optionLabel}>{t('setup.resolution')}</Text>
                  <View style={styles.toggleGroup}>
                    <TouchableOpacity
                      style={[styles.toggleBtn, videoPrefs.resolution === '720p' && styles.toggleActive]}
                      onPress={() => updatePref('resolution', '720p')}
                    >
                      <Text style={styles.toggleText}>720p</Text>
                    </TouchableOpacity>
                    <TouchableOpacity
                      style={[styles.toggleBtn, videoPrefs.resolution === '1080p' && styles.toggleActive]}
                      onPress={() => updatePref('resolution', '1080p')}
                    >
                      <Text style={styles.toggleText}>1080p</Text>
                    </TouchableOpacity>
                  </View>
                </View>
                <Text style={styles.hint}>{t('setup.resolutionHint')}</Text>

                <View style={[styles.optionRow, { marginTop: 16 }]}>
                  <View>
                    <Text style={styles.optionLabel}>{t('setup.skeletonOverlay')}</Text>
                    <Text style={[styles.hint, { marginTop: 2 }]}>
                      {Platform.OS === 'android' ? t('setup.skeletonAndroid') : t('setup.skeletonIos')}
                    </Text>
                  </View>
                  <Switch
                    value={videoPrefs.showSkeleton}
                    onValueChange={(v) => updatePref('showSkeleton', v)}
                    trackColor={{ false: '#767577', true: '#81b0ff' }}
                    thumbColor={videoPrefs.showSkeleton ? '#f5dd4b' : '#f4f3f4'}
                  />
                </View>
                <View style={[styles.optionRow, { marginTop: 16 }]}>
                  <View>
                    <Text style={styles.optionLabel}>{t('setup.lowFps')}</Text>
                    <Text style={[styles.hint, { marginTop: 2 }]}>{t('setup.lowFpsHint')}</Text>
                  </View>
                  <Switch
                    value={videoPrefs.lowFps}
                    onValueChange={(v) => updatePref('lowFps', v)}
                    trackColor={{ false: '#767577', true: '#81b0ff' }}
                    thumbColor={videoPrefs.lowFps ? '#f5dd4b' : '#f4f3f4'}
                  />
                </View>
                <View style={[styles.optionRow, { marginTop: 16 }]}>
                  <View>
                    <Text style={styles.optionLabel}>{t('setup.force720p')}</Text>
                    <Text style={[styles.hint, { marginTop: 2 }]}>{t('setup.force720pHint')}</Text>
                  </View>
                  <Switch
                    value={videoPrefs.force720p}
                    onValueChange={(v) => updatePref('force720p', v)}
                    trackColor={{ false: '#767577', true: '#81b0ff' }}
                    thumbColor={videoPrefs.force720p ? '#f5dd4b' : '#f4f3f4'}
                  />
                </View>
                <View style={[styles.optionRow, { marginTop: 16 }]}>
                  <View>
                    <Text style={styles.optionLabel}>{t('setup.skipCompression')}</Text>
                    <Text style={[styles.hint, { marginTop: 2 }]}>{t('setup.skipCompressionHint')}</Text>
                  </View>
                  <Switch
                    value={videoPrefs.skipCompression}
                    onValueChange={(v) => updatePref('skipCompression', v)}
                    trackColor={{ false: '#767577', true: '#81b0ff' }}
                    thumbColor={videoPrefs.skipCompression ? '#f5dd4b' : '#f4f3f4'}
                  />
                </View>
                <View style={[styles.optionRow, { marginTop: 16 }]}>
                  <View>
                    <Text style={styles.optionLabel}>{t('setup.serialUpload')}</Text>
                    <Text style={[styles.hint, { marginTop: 2 }]}>{t('setup.serialUploadHint')}</Text>
                  </View>
                  <Switch
                    value={videoPrefs.serialUpload}
                    onValueChange={(v) => updatePref('serialUpload', v)}
                    trackColor={{ false: '#767577', true: '#81b0ff' }}
                    thumbColor={videoPrefs.serialUpload ? '#f5dd4b' : '#f4f3f4'}
                  />
                </View>
              </View>
            )}
          </>
        )}
      </ScrollView>

      {/* Bottom staging bar */}
      <View style={styles.bottomBar}>
        {selectedMovements.length > 0 && (
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            contentContainerStyle={styles.stagedChipsContent}
            style={styles.stagedChipsScroll}
          >
            {selectedMovements.map(m => (
              <View key={m} style={styles.stagedChip}>
                <Text style={styles.stagedChipText}>{m}</Text>
                <TouchableOpacity
                  onPress={() => toggleMovement(m)}
                  hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}
                >
                  <IconSymbol name="xmark.circle.fill" size={14} color="#00E5FF" />
                </TouchableOpacity>
              </View>
            ))}
          </ScrollView>
        )}
        <TouchableOpacity
          style={styles.startBtn}
          onPress={handleStart}
        >
          <Text style={styles.startText}>{t('setup.startWorkout')}</Text>
        </TouchableOpacity>
        <TouchableOpacity
          style={styles.testPoseBtn}
          onPress={() => router.push('/workout/poseTestPage' as any)}
        >
          <IconSymbol name="eye" size={18} color="#888" />
          <Text style={styles.testPoseText}>Test Pose Detection</Text>
        </TouchableOpacity>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0A0E14' },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderBottomWidth: 1,
    borderBottomColor: '#1A1F28',
  },
  backBtn: { width: 28 },
  title: { fontSize: 16, fontWeight: '800', color: '#00E5FF', letterSpacing: 1.5, textTransform: 'uppercase' },

  // Search bar
  searchContainer: {
    flexDirection: 'row',
    alignItems: 'center',
    marginHorizontal: 16,
    marginTop: 12,
    marginBottom: 8,
    backgroundColor: '#141A24',
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderWidth: 1,
    borderColor: '#1E2630',
  },
  searchInput: {
    flex: 1,
    color: '#fff',
    fontSize: 15,
    marginLeft: 10,
    paddingVertical: 0,
  },

  // Filter tabs
  filterTabs: {
    maxHeight: 44,
    marginBottom: 4,
  },
  filterTabsContent: {
    paddingHorizontal: 16,
    gap: 8,
    alignItems: 'center',
  },
  filterTab: {
    paddingHorizontal: 14,
    paddingVertical: 7,
    borderRadius: 20,
    backgroundColor: '#141A24',
    borderWidth: 1,
    borderColor: '#1E2630',
  },
  filterTabActive: {
    backgroundColor: '#00303D',
    borderColor: '#00E5FF',
  },
  filterTabText: {
    color: '#667',
    fontSize: 12,
    fontWeight: '700',
    textTransform: 'uppercase',
    letterSpacing: 0.5,
  },
  filterTabTextActive: {
    color: '#00E5FF',
  },

  // Movement list
  movementList: { flex: 1 },
  movementListContent: { paddingHorizontal: 16, paddingBottom: 160 },

  // Profile banner
  profileBanner: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    backgroundColor: '#141A24',
    padding: 14,
    borderRadius: 14,
    marginBottom: 16,
    marginTop: 8,
    borderWidth: 1,
    borderColor: '#1E2630',
  },
  profileBannerLeft: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
  },
  miniAvatar: {
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: '#002B3D',
    justifyContent: 'center',
    alignItems: 'center',
  },
  miniAvatarText: {
    color: '#00E5FF',
    fontSize: 18,
    fontWeight: '800',
  },
  profileBannerName: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '700',
  },
  profileBannerMeta: {
    color: '#667',
    fontSize: 12,
    marginTop: 2,
  },
  chevron: { color: '#444', fontSize: 22, fontWeight: '300' },

  // Add custom movement
  addCustomBtn: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
    backgroundColor: '#00303D',
    paddingVertical: 14,
    paddingHorizontal: 16,
    borderRadius: 12,
    marginBottom: 16,
    borderWidth: 1,
    borderColor: '#00E5FF40',
  },
  addCustomText: {
    color: '#00E5FF',
    fontSize: 14,
    fontWeight: '700',
  },

  // Group section
  groupSection: { marginBottom: 12 },
  groupHeader: {
    color: '#4A5568',
    fontSize: 11,
    fontWeight: '800',
    letterSpacing: 1.5,
    marginBottom: 8,
    marginTop: 8,
    paddingLeft: 4,
  },

  // Movement card
  movementCard: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: '#141A24',
    borderRadius: 12,
    padding: 14,
    marginBottom: 6,
    borderWidth: 1,
    borderColor: '#1E2630',
  },
  movementCardSelected: {
    borderColor: '#00E5FF40',
    backgroundColor: '#0D1A24',
  },
  movementCardIcon: {
    width: 36,
    height: 36,
    borderRadius: 10,
    backgroundColor: '#1A2332',
    justifyContent: 'center',
    alignItems: 'center',
    marginRight: 12,
  },
  movementCardInfo: { flex: 1 },
  movementName: {
    color: '#C8D0DA',
    fontSize: 14,
    fontWeight: '700',
    letterSpacing: 0.5,
  },
  movementNameSelected: {
    color: '#fff',
  },
  movementCategory: {
    color: '#4A5568',
    fontSize: 11,
    fontWeight: '600',
    letterSpacing: 0.5,
    marginTop: 2,
  },

  // Checkbox
  checkbox: {
    width: 24,
    height: 24,
    borderRadius: 12,
    borderWidth: 2,
    borderColor: '#2D3748',
    justifyContent: 'center',
    alignItems: 'center',
  },
  checkboxSelected: {
    backgroundColor: '#00E5FF',
    borderColor: '#00E5FF',
  },

  // Empty state
  emptyState: {
    alignItems: 'center',
    paddingVertical: 40,
  },
  emptyText: {
    color: '#4A5568',
    fontSize: 14,
  },

  // Advanced options
  advancedHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingVertical: 14,
    paddingHorizontal: 4,
    marginTop: 12,
  },
  advancedHeaderText: {
    color: '#4A5568',
    fontSize: 12,
    fontWeight: '800',
    textTransform: 'uppercase',
    letterSpacing: 1,
  },
  advancedChevron: {
    color: '#4A5568',
    fontSize: 12,
  },
  advancedSection: {
    backgroundColor: '#141A24',
    borderRadius: 14,
    padding: 16,
    marginBottom: 20,
    borderWidth: 1,
    borderColor: '#1E2630',
  },
  hint: { color: '#4A5568', fontSize: 12, marginTop: 8 },

  optionRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 },
  optionLabel: { color: '#C8D0DA', fontSize: 15, fontWeight: '600' },
  toggleGroup: { flexDirection: 'row', backgroundColor: '#1A2332', borderRadius: 8, padding: 2 },
  toggleBtn: { paddingVertical: 6, paddingHorizontal: 12, borderRadius: 6 },
  toggleActive: { backgroundColor: '#2D3748' },
  toggleText: { color: '#C8D0DA', fontWeight: 'bold', fontSize: 13 },

  // Bottom bar
  bottomBar: {
    position: 'absolute',
    bottom: 0,
    left: 0,
    right: 0,
    paddingHorizontal: 20,
    paddingTop: 12,
    paddingBottom: 34,
    backgroundColor: '#0D1118',
    borderTopWidth: 1,
    borderTopColor: '#1A1F28',
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
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    backgroundColor: '#00303D',
    borderRadius: 16,
    paddingVertical: 6,
    paddingLeft: 12,
    paddingRight: 8,
    borderWidth: 1,
    borderColor: '#00E5FF30',
  },
  stagedChipText: {
    color: '#00E5FF',
    fontSize: 12,
    fontWeight: '700',
  },
  startBtn: {
    backgroundColor: '#00E5FF',
    padding: 16,
    borderRadius: 12,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 10,
  },
  startText: { color: '#000', fontSize: 16, fontWeight: '800', textTransform: 'uppercase', letterSpacing: 0.5 },
  testPoseBtn: {
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 6,
    marginTop: 10,
    paddingVertical: 8,
  },
  testPoseText: {
    color: '#4A5568',
    fontSize: 13,
    fontWeight: '600',
  },
});
