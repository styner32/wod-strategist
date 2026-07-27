import { IconSymbol } from '@/components/ui/icon-symbol';
import { t } from '@/features/i18n';
import { fetchMovementGroups, parseWorkoutImage, type MovementGroup } from '@/features/wod/api';
import { WORKOUT_TYPES, type WorkoutType } from '@/features/wod/workoutType';
import { useActiveProfile } from '@/store/useProfileStore';
import AsyncStorage from '@react-native-async-storage/async-storage';
import * as ImagePicker from 'expo-image-picker';
import { router } from 'expo-router';
import React, { useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
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

type SetupStep = 'input' | 'confirm';
type InputMethod = 'text' | 'photo' | 'movements';

const VIDEO_PREFS_KEY = 'wod_video_preferences';
const ALL_FILTER = 'All';

interface VideoPreferences {
  showSkeleton: boolean;
  lowFps: boolean;
  skipCompression: boolean;
  serialUpload: boolean;
  resolution: '480p' | '720p' | '1080p' | '2160p';
  landscapeMode: boolean;
  autoRecord: boolean;
  zoomMode: boolean;
  aspectRatio: '4:3' | '16:9';
}

function getDefaultVideoPrefs(): VideoPreferences {
  const isAndroid = Platform.OS === 'android';
  return {
    showSkeleton: !isAndroid,
    lowFps: isAndroid,
    skipCompression: isAndroid,
    serialUpload: isAndroid,
    resolution: '720p',
    landscapeMode: false,
    autoRecord: true,
    zoomMode: false,
    aspectRatio: '16:9',
  };
}

// Category icons for the card list (must match icon-symbol.tsx MAPPING keys)
const CATEGORY_ICONS: Record<string, string> = {
  'Barbell': 'dumbbell.fill',
  'Dumbbell & Kettlebell': 'dumbbell.fill',
  'Gymnastics': 'figure.run',
  'Bodyweight & Plyo': 'flame.fill',
  'Cardio': 'figure.run',
  'Surfing': 'figure.run',
  'Yoga': 'figure.run',
  'Custom': 'plus.circle.fill',
};

function getCategoryIcon(category: string): string {
  return CATEGORY_ICONS[category] || 'dumbbell.fill';
}

export default function WorkoutSetup() {
  const [workoutType, setWorkoutType] = useState<WorkoutType>('wod');
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

  // Wizard state
  const [currentStep, setCurrentStep] = useState<SetupStep>('input');
  const [inputMethod, setInputMethod] = useState<InputMethod>('movements');
  const [wodDescription, setWodDescription] = useState('');
  const [rawText, setRawText] = useState('');
  const [scanning, setScanning] = useState(false);

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

  const pickImage = async (useCamera: boolean) => {
    const pickerFn = useCamera ? ImagePicker.launchCameraAsync : ImagePicker.launchImageLibraryAsync;
    const result = await pickerFn({ mediaTypes: ['images'], quality: 0.8, allowsEditing: false });
    if (result.canceled || !result.assets?.[0]) return null;
    return result.assets[0].uri;
  };

  const handleScanWhiteboard = async (useCamera: boolean) => {
    const uri = await pickImage(useCamera);
    if (!uri) return;
    setScanning(true);
    try {
      const result = await parseWorkoutImage(uri);
      setWodDescription(result.wod_description || '');
      setRawText(result.raw_text || '');
      if (result.movements?.length > 0) {
        setSelectedMovements(prev => {
          const combined = new Set([...prev, ...result.movements]);
          return Array.from(combined);
        });
      }
      setCurrentStep('confirm');
    } catch (err) {
      console.error('Whiteboard scan failed:', err);
      Alert.alert(t('common.error'), t('setup.scanFailed'));
    } finally {
      setScanning(false);
    }
  };

  const handleNext = () => setCurrentStep('confirm');

  const [sessionAppearance, setSessionAppearance] = useState('');

  const handleStart = async () => {
    try {
      await AsyncStorage.setItem(VIDEO_PREFS_KEY, JSON.stringify(videoPrefs));
    } catch (e) {
      console.warn('Failed to persist video prefs:', e);
    }
    const injuries = activeProfile?.injuries ?? [];
    const appearanceHints = sessionAppearance.trim();

    router.push({
      pathname: '/workout/visionTestPage',
      params: {
        resolution: videoPrefs.resolution,
        workoutType,
        movements: selectedMovements.join(', '),
        injuries: injuries.join(', '),
        wodDescription: wodDescription.trim(),
        ...(appearanceHints ? { appearanceHints } : {}),
        autoRecord: videoPrefs.autoRecord ? 'true' : 'false',
        showSkeleton: videoPrefs.showSkeleton ? 'true' : 'false',
        lowFps: videoPrefs.lowFps ? 'true' : 'false',
        skipCompression: videoPrefs.skipCompression ? 'true' : 'false',
        serialUpload: videoPrefs.serialUpload ? 'true' : 'false',
        landscapeMode: videoPrefs.landscapeMode ? 'true' : 'false',
        zoomMode: videoPrefs.zoomMode ? 'true' : 'false',
        aspectRatio: videoPrefs.aspectRatio,
      },
    });
  };

  const handlePreview = () => {
    router.push({
      pathname: '/workout/visionTestPage',
      params: {
        resolution: videoPrefs.resolution,
        showSkeleton: videoPrefs.showSkeleton ? 'true' : 'false',
        lowFps: videoPrefs.lowFps ? 'true' : 'false',
        landscapeMode: videoPrefs.landscapeMode ? 'true' : 'false',
        zoomMode: videoPrefs.zoomMode ? 'true' : 'false',
        aspectRatio: videoPrefs.aspectRatio,
        previewOnly: 'true',
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
        <TouchableOpacity onPress={() => currentStep === 'confirm' ? setCurrentStep('input') : router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>{currentStep === 'confirm' ? t('setup.confirmTitle') : t('setup.title')}</Text>
        <View style={{ width: 28 }} />
      </View>

      {/* ─── STEP 1: INPUT ─── */}
      {currentStep === 'input' && (<>
        <View style={styles.workoutTypeTabs}>
          {WORKOUT_TYPES.map(wt => {
            const active = workoutType === wt;
            return (<TouchableOpacity key={wt} style={[styles.workoutTypeTab, active && styles.workoutTypeTabActive]} onPress={() => setWorkoutType(wt)}><Text style={[styles.workoutTypeTabText, active && styles.workoutTypeTabTextActive]}>{t(`workoutType.${wt}`)}</Text></TouchableOpacity>);
          })}
        </View>
        <View style={styles.inputMethodTabs}>
          {(['text', 'photo', 'movements'] as InputMethod[]).map(method => {
            const active = inputMethod === method;
            const icons: Record<InputMethod, string> = { text: '📝', photo: '📷', movements: '🏋️' };
            const tKey = `inputMethod${method.charAt(0).toUpperCase() + method.slice(1)}`;
            return (<TouchableOpacity key={method} style={[styles.inputMethodTab, active && styles.inputMethodTabActive]} onPress={() => setInputMethod(method)}><Text style={[styles.inputMethodTabText, active && styles.inputMethodTabTextActive]}>{icons[method]} {t(`setup.${tKey}`)}</Text></TouchableOpacity>);
          })}
        </View>

        {inputMethod === 'text' && (
          <ScrollView contentContainerStyle={styles.movementListContent} style={styles.movementList}>
            <Text style={styles.sectionLabel}>{t('setup.wodDescriptionLabel')}</Text>
            <TextInput style={styles.wodTextInput} placeholder={t('setup.wodTextPlaceholder')} placeholderTextColor="#555" value={wodDescription} onChangeText={setWodDescription} multiline numberOfLines={5} textAlignVertical="top" />
          </ScrollView>
        )}

        {inputMethod === 'photo' && (
          <ScrollView contentContainerStyle={styles.movementListContent} style={styles.movementList}>
            {scanning ? (
              <View style={styles.scanningContainer}><ActivityIndicator color="#00E5FF" size="large" /><Text style={styles.scanningText}>{t('setup.scanningImage')}</Text></View>
            ) : (<>
              <TouchableOpacity style={styles.photoBtn} onPress={() => handleScanWhiteboard(true)}><IconSymbol name="camera.fill" size={28} color="#00E5FF" /><Text style={styles.photoBtnText}>{t('setup.takePhoto')}</Text></TouchableOpacity>
              <TouchableOpacity style={styles.photoBtn} onPress={() => handleScanWhiteboard(false)}><IconSymbol name="photo.fill" size={28} color="#00E5FF" /><Text style={styles.photoBtnText}>{t('setup.chooseFromGallery')}</Text></TouchableOpacity>
            </>)}
          </ScrollView>
        )}

        {inputMethod === 'movements' && (<>
          <View style={styles.searchContainer}>
            <IconSymbol name="magnifyingglass" size={18} color="#666" />
            <TextInput style={styles.searchInput} placeholder={t('setup.searchMovements')} placeholderTextColor="#555" value={searchText} onChangeText={setSearchText} autoCapitalize="none" autoCorrect={false} />
            {searchText.length > 0 && (<TouchableOpacity onPress={() => setSearchText('')}><IconSymbol name="xmark.circle.fill" size={18} color="#555" /></TouchableOpacity>)}
          </View>
          <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.filterTabsContent} style={styles.filterTabs}>
            {categoryTabs.map(tab => {
              const isActive = activeFilter === tab;
              return (<TouchableOpacity key={tab} style={[styles.filterTab, isActive && styles.filterTabActive]} onPress={() => setActiveFilter(tab)}><Text style={[styles.filterTabText, isActive && styles.filterTabTextActive]}>{tab === ALL_FILTER ? t('setup.categoryAll') : tab}</Text></TouchableOpacity>);
            })}
          </ScrollView>
          <ScrollView contentContainerStyle={styles.movementListContent} style={styles.movementList}>
            {loading ? (<ActivityIndicator color="#00E5FF" style={{ marginTop: 40 }} />) : (<>
              {canAddCustom && (<TouchableOpacity style={styles.addCustomBtn} onPress={addCustomMovement}><IconSymbol name="plus.circle.fill" size={20} color="#00E5FF" /><Text style={styles.addCustomText}>{t('setup.addCustomMovement', { name: searchText.trim() })}</Text></TouchableOpacity>)}
              {filteredGroups.map(group => (
                <View key={group.category} style={styles.groupSection}>
                  <Text style={styles.groupHeader}>{group.category.toUpperCase()}</Text>
                  {group.movements.map(movement => {
                    const isSelected = selectedMovements.includes(movement);
                    const isCustom = customMovements.includes(movement);
                    return (
                      <TouchableOpacity key={movement} style={[styles.movementCard, isSelected && styles.movementCardSelected]} onPress={() => toggleMovement(movement)}>
                        <View style={styles.movementCardIcon}><IconSymbol name={getCategoryIcon(group.category) as any} size={20} color={isSelected ? '#00E5FF' : '#666'} /></View>
                        <View style={styles.movementCardInfo}><Text style={[styles.movementName, isSelected && styles.movementNameSelected]}>{movement.toUpperCase()}</Text><Text style={styles.movementCategory}>{group.category.toUpperCase()}</Text></View>
                        {isCustom ? (<TouchableOpacity onPress={(e: GestureResponderEvent) => { e.stopPropagation(); removeCustomMovement(movement); }} hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}><IconSymbol name="xmark.circle" size={22} color="#666" /></TouchableOpacity>) : (<View style={[styles.checkbox, isSelected && styles.checkboxSelected]}>{isSelected && (<IconSymbol name="checkmark" size={14} color="#000" />)}</View>)}
                      </TouchableOpacity>
                    );
                  })}
                </View>
              ))}
              {filteredGroups.length === 0 && !canAddCustom && (<View style={styles.emptyState}><Text style={styles.emptyText}>{t('setup.noResults')}</Text></View>)}
            </>)}
          </ScrollView>
        </>)}

        <View style={styles.bottomBar}>
          {selectedMovements.length > 0 && (
            <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.stagedChipsContent} style={styles.stagedChipsScroll}>
              {selectedMovements.map(m => (<View key={m} style={styles.stagedChip}><Text style={styles.stagedChipText}>{m}</Text><TouchableOpacity onPress={() => toggleMovement(m)} hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}><IconSymbol name="xmark.circle.fill" size={14} color="#00E5FF" /></TouchableOpacity></View>))}
            </ScrollView>
          )}
          <TouchableOpacity style={styles.startBtn} onPress={handleNext}><Text style={styles.startText}>{t('setup.nextStep')}</Text></TouchableOpacity>
        </View>
      </>)}

      {/* ─── STEP 2: CONFIRM ─── */}
      {currentStep === 'confirm' && (<>
        <ScrollView contentContainerStyle={styles.movementListContent} style={styles.movementList}>
          <Text style={styles.confirmHint}>{t('setup.confirmHint')}</Text>
          <Text style={styles.sectionLabel}>{t('setup.wodDescriptionLabel')}</Text>
          <TextInput style={styles.wodTextInput} placeholder={t('setup.wodDescriptionPlaceholder')} placeholderTextColor="#555" value={wodDescription} onChangeText={setWodDescription} multiline numberOfLines={3} textAlignVertical="top" />
          {rawText.length > 0 && (<View style={styles.rawTextSection}><Text style={styles.rawTextLabel}>{t('setup.rawTextLabel')}</Text><Text style={styles.rawTextValue}>{rawText}</Text></View>)}
          <Text style={[styles.sectionLabel, { marginTop: 20 }]}>{t('setup.plannedMovements')}</Text>
          {selectedMovements.length > 0 ? (
            <View style={styles.confirmChipsWrap}>{selectedMovements.map(m => (<View key={m} style={styles.confirmChip}><Text style={styles.confirmChipText}>{m}</Text><TouchableOpacity onPress={() => toggleMovement(m)} hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}><IconSymbol name="xmark.circle.fill" size={14} color="#00E5FF" /></TouchableOpacity></View>))}</View>
          ) : (<Text style={styles.noMovementsText}>{t('setup.noMovementsSelected')}</Text>)}
          <TouchableOpacity style={styles.addMoreBtn} onPress={() => { setInputMethod('movements'); setCurrentStep('input'); }}><Text style={styles.addMoreBtnText}>{t('setup.addMoreMovements')}</Text></TouchableOpacity>
          {activeProfile && (
            <TouchableOpacity style={[styles.profileBanner, { marginTop: 20 }]} onPress={() => router.push(`/profile?id=${activeProfile.id}` as any)}>
              <View style={styles.profileBannerLeft}>
                <View style={styles.miniAvatar}><Text style={styles.miniAvatarText}>{activeProfile.name ? activeProfile.name[0].toUpperCase() : '?'}</Text></View>
                <View><Text style={styles.profileBannerName}>{activeProfile.name || t('tabs.profile')}</Text><Text style={styles.profileBannerMeta}>{[t(`profileEdit.${activeProfile.fitnessLevel ?? 'intermediate'}`), injuryCount > 0 ? t('setup.injuries', { count: injuryCount }) : t('setup.noInjuries')].join(' · ')}</Text></View>
              </View>
              <Text style={styles.chevron}>›</Text>
            </TouchableOpacity>
          )}
          {/* Today's Outfit & Identification Cues */}
          <View style={{ marginTop: 20, backgroundColor: '#141A22', padding: 14, borderRadius: 12, borderWidth: 1, borderColor: '#222' }}>
            <Text style={[styles.sectionLabel, { marginTop: 0 }]}>오늘의 착장 / 외형 (선택)</Text>
            <Text style={{ color: '#888', fontSize: 12, marginBottom: 10 }}>실시간 인물 식별 보조 정보</Text>
            <TextInput
              style={{ backgroundColor: '#1A222D', color: '#fff', padding: 10, borderRadius: 8, fontSize: 14 }}
              placeholder="예: 검은 반팔, 회색 반바지, 무릎보호대"
              placeholderTextColor="#555"
              value={sessionAppearance}
              onChangeText={setSessionAppearance}
            />
          </View>
          <TouchableOpacity style={styles.advancedHeader} onPress={() => setAdvancedExpanded(!advancedExpanded)}><Text style={styles.advancedHeaderText}>{t('setup.advancedOptions')}</Text><Text style={styles.advancedChevron}>{advancedExpanded ? '▼' : '▶'}</Text></TouchableOpacity>
          {advancedExpanded && (
            <View style={styles.advancedSection}>
              <View style={styles.optionRow}><Text style={styles.optionLabel}>{t('setup.resolution')}</Text><View style={styles.toggleGroup}><TouchableOpacity style={[styles.toggleBtn, videoPrefs.resolution === '720p' && styles.toggleActive]} onPress={() => updatePref('resolution', '720p')}><Text style={styles.toggleText}>720p</Text></TouchableOpacity><TouchableOpacity style={[styles.toggleBtn, videoPrefs.resolution === '1080p' && styles.toggleActive]} onPress={() => updatePref('resolution', '1080p')}><Text style={styles.toggleText}>1080p</Text></TouchableOpacity></View></View>
              <View style={[styles.optionRow, { marginTop: 16 }]}><View><Text style={styles.optionLabel}>{t('setup.skeletonOverlay')}</Text></View><Switch value={videoPrefs.showSkeleton} onValueChange={v => updatePref('showSkeleton', v)} trackColor={{ false: '#767577', true: '#81b0ff' }} thumbColor={videoPrefs.showSkeleton ? '#f5dd4b' : '#f4f3f4'} /></View>
              <View style={[styles.optionRow, { marginTop: 16 }]}><View><Text style={styles.optionLabel}>{t('setup.autoRecord')}</Text></View><Switch value={videoPrefs.autoRecord} onValueChange={v => updatePref('autoRecord', v)} trackColor={{ false: '#767577', true: '#81b0ff' }} thumbColor={videoPrefs.autoRecord ? '#f5dd4b' : '#f4f3f4'} /></View>
            </View>
          )}
        </ScrollView>
        <View style={styles.bottomBar}>
          <TouchableOpacity style={styles.startBtn} onPress={handleStart}><Text style={styles.startText}>{t('setup.startWorkout')}</Text></TouchableOpacity>
          <TouchableOpacity style={styles.previewBtn} onPress={handlePreview}><IconSymbol name="eye" size={18} color="#00E5FF" /><Text style={styles.previewBtnText}>{t('setup.previewCamera')}</Text></TouchableOpacity>
        </View>
      </>)}
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
  previewBtn: {
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 8,
    marginTop: 12,
    paddingVertical: 12,
    paddingHorizontal: 20,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: '#00E5FF40',
    backgroundColor: '#00E5FF10',
  },
  previewBtnText: {
    color: '#00E5FF',
    fontSize: 14,
    fontWeight: '700',
  },
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

  // Workout type tabs
  workoutTypeTabs: { flexDirection: 'row', paddingHorizontal: 16, paddingTop: 12, paddingBottom: 4, gap: 6 },
  workoutTypeTab: { flex: 1, paddingVertical: 8, borderRadius: 10, backgroundColor: '#141A24', borderWidth: 1, borderColor: '#1E2630', alignItems: 'center' },
  workoutTypeTabActive: { backgroundColor: '#00303D', borderColor: '#00E5FF' },
  workoutTypeTabText: { color: '#666', fontSize: 12, fontWeight: '700', textTransform: 'uppercase', letterSpacing: 0.5 },
  workoutTypeTabTextActive: { color: '#00E5FF' },

  // Input method tabs
  inputMethodTabs: { flexDirection: 'row', paddingHorizontal: 16, paddingTop: 8, paddingBottom: 4, gap: 8 },
  inputMethodTab: { flex: 1, paddingVertical: 10, borderRadius: 10, backgroundColor: '#141A24', borderWidth: 1, borderColor: '#1E2630', alignItems: 'center' },
  inputMethodTabActive: { backgroundColor: '#00303D', borderColor: '#00E5FF40' },
  inputMethodTabText: { color: '#666', fontSize: 13, fontWeight: '700' },
  inputMethodTabTextActive: { color: '#00E5FF' },

  // Text input mode
  sectionLabel: { color: '#00E5FF', fontSize: 12, fontWeight: '800', textTransform: 'uppercase' as const, letterSpacing: 1, marginBottom: 8, marginTop: 12 },
  wodTextInput: { backgroundColor: '#141A24', borderRadius: 12, padding: 14, color: '#fff', fontSize: 15, borderWidth: 1, borderColor: '#1E2630', minHeight: 100, textAlignVertical: 'top' as const },

  // Photo mode
  photoBtn: { flexDirection: 'row' as const, alignItems: 'center' as const, gap: 14, backgroundColor: '#141A24', borderRadius: 14, padding: 20, marginBottom: 12, borderWidth: 1, borderColor: '#1E2630' },
  photoBtnText: { color: '#C8D0DA', fontSize: 16, fontWeight: '600' },
  scanningContainer: { alignItems: 'center' as const, paddingVertical: 60, gap: 16 },
  scanningText: { color: '#00E5FF', fontSize: 14, fontWeight: '600' },

  // Confirm step
  confirmHint: { color: '#4A5568', fontSize: 13, marginBottom: 16, lineHeight: 18 },
  confirmChipsWrap: { flexDirection: 'row' as const, flexWrap: 'wrap' as const, gap: 8 },
  confirmChip: { flexDirection: 'row' as const, alignItems: 'center' as const, gap: 6, backgroundColor: '#00303D', borderRadius: 16, paddingVertical: 8, paddingLeft: 14, paddingRight: 10, borderWidth: 1, borderColor: '#00E5FF30' },
  confirmChipText: { color: '#00E5FF', fontSize: 13, fontWeight: '700' },
  noMovementsText: { color: '#4A5568', fontSize: 14, fontStyle: 'italic' as const },
  addMoreBtn: { marginTop: 12, paddingVertical: 10 },
  addMoreBtnText: { color: '#00E5FF', fontSize: 14, fontWeight: '700' },
  rawTextSection: { backgroundColor: '#141A24', borderRadius: 10, padding: 12, marginTop: 12, borderWidth: 1, borderColor: '#1E2630' },
  rawTextLabel: { color: '#4A5568', fontSize: 11, fontWeight: '700', textTransform: 'uppercase' as const, letterSpacing: 0.5, marginBottom: 4 },
  rawTextValue: { color: '#888', fontSize: 13, lineHeight: 18 },
});
