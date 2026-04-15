import { IconSymbol } from '@/components/ui/icon-symbol';
import { t } from '@/features/i18n';
import { fetchMovements } from '@/features/wod/api';
import { useActiveProfile } from '@/store/useProfileStore';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { router } from 'expo-router';
import React, { useEffect, useState } from 'react';
import {
  ActivityIndicator,
  Platform,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';

const VIDEO_PREFS_KEY = 'wod_video_preferences';

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

export default function WorkoutSetup() {
  const workoutType = 'wod';
  const activeProfile = useActiveProfile();

  // Video preferences (persisted to AsyncStorage)
  const [videoPrefs, setVideoPrefs] = useState<VideoPreferences>(getDefaultVideoPrefs());
  const [prefsLoaded, setPrefsLoaded] = useState(false);
  const [advancedExpanded, setAdvancedExpanded] = useState(false);

  // Movements
  const [movementOptions, setMovementOptions] = useState<string[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

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

  // Load movements
  useEffect(() => {
    fetchMovements()
      .then(setMovementOptions)
      .catch((error) => {
        console.error("Failed to load movements", error);
      })
      .finally(() => setLoading(false));
  }, []);

  const toggleMovement = (m: string) => {
    setSelectedMovements(prev => 
      prev.includes(m) ? prev.filter(x => x !== m) : [...prev, m]
    );
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

  const canStart = true;
  const injuryCount = activeProfile?.injuries?.length ?? 0;

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>{t('setup.title')}</Text>
      </View>

      <ScrollView contentContainerStyle={styles.content}>
        {/* Profile Summary Banner */}
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

        {/* Movements Config */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t('setup.plannedMovements')}</Text>
          <Text style={styles.label}>{t('setup.selectMovements')}</Text>
          
          {loading ? (
            <ActivityIndicator color="#007AFF" />
          ) : (
            <View style={styles.chipContainer}>
              {movementOptions.map(m => {
                const isSelected = selectedMovements.includes(m);
                return (
                  <TouchableOpacity
                    key={m}
                    onPress={() => toggleMovement(m)}
                    style={[styles.chip, isSelected && styles.chipActive]}
                  >
                    <Text style={[styles.chipText, isSelected && styles.chipTextActive]}>
                      {m}
                    </Text>
                  </TouchableOpacity>
                );
              })}
            </View>
          )}
          <Text style={styles.hint}>
            {t('setup.movementsHint')}
          </Text>
        </View>

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

      </ScrollView>

      <View style={styles.bottomBar}>
        <TouchableOpacity 
          style={[styles.startBtn, !canStart && styles.disabledBtn]} 
          onPress={handleStart}
          disabled={!canStart}
        >
          <Text style={styles.startText}>{t('setup.startWorkout')}</Text>
          <IconSymbol name="figure.run" size={24} color="#000" />
        </TouchableOpacity>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#000' },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    padding: 20,
    borderBottomWidth: 1,
    borderBottomColor: '#222',
  },
  backBtn: { marginRight: 15 },
  title: { fontSize: 20, fontWeight: 'bold', color: '#fff' },
  content: { padding: 20, paddingBottom: 100 },

  profileBanner: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    backgroundColor: '#111',
    padding: 14,
    borderRadius: 14,
    marginBottom: 24,
    borderWidth: 1,
    borderColor: '#222',
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
    color: '#888',
    fontSize: 12,
    marginTop: 2,
  },
  chevron: { color: '#444', fontSize: 22, fontWeight: '300' },

  section: { marginBottom: 30 },
  sectionTitle: { color: '#007AFF', fontSize: 14, fontWeight: 'bold', textTransform: 'uppercase', marginBottom: 15 },
  label: { color: '#fff', fontSize: 16, marginBottom: 10 },
  chipContainer: { flexDirection: 'row', flexWrap: 'wrap', gap: 10 },
  chip: {
    paddingVertical: 8,
    paddingHorizontal: 16,
    borderRadius: 20,
    borderWidth: 1,
    borderColor: '#333',
    backgroundColor: '#111',
  },
  chipActive: {
    backgroundColor: '#007AFF',
    borderColor: '#007AFF',
  },
  chipText: { color: '#888', fontSize: 14 },
  chipTextActive: { color: '#fff', fontWeight: 'bold' },
  hint: { color: '#666', fontSize: 12, marginTop: 12 },

  advancedHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingVertical: 14,
    paddingHorizontal: 4,
    marginBottom: 4,
  },
  advancedHeaderText: {
    color: '#888',
    fontSize: 14,
    fontWeight: 'bold',
    textTransform: 'uppercase',
    letterSpacing: 0.5,
  },
  advancedChevron: {
    color: '#555',
    fontSize: 12,
  },
  advancedSection: {
    backgroundColor: '#0A0A0A',
    borderRadius: 14,
    padding: 16,
    marginBottom: 20,
    borderWidth: 1,
    borderColor: '#1A1A1A',
  },

  optionRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 },
  optionLabel: { color: '#fff', fontSize: 16 },
  toggleGroup: { flexDirection: 'row', backgroundColor: '#222', borderRadius: 8, padding: 2 },
  toggleBtn: { paddingVertical: 6, paddingHorizontal: 12, borderRadius: 6 },
  toggleActive: { backgroundColor: '#444' },
  toggleText: { color: '#fff', fontWeight: 'bold' },

  bottomBar: {
    position: 'absolute',
    bottom: 0,
    left: 0,
    right: 0,
    padding: 20,
    paddingBottom: 34,
    backgroundColor: 'rgba(0,0,0,0.9)',
    borderTopWidth: 1,
    borderTopColor: '#222',
  },
  startBtn: {
    backgroundColor: '#fff',
    padding: 18,
    borderRadius: 12,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 10,
  },
  disabledBtn: { opacity: 0.5 },
  startText: { color: '#000', fontSize: 18, fontWeight: 'bold' },
});
