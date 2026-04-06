import { IconSymbol } from '@/components/ui/icon-symbol';
import { fetchInjuries, fetchMovements } from '@/features/wod/api';
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

export default function WorkoutSetup() {
  const workoutType = 'wod';
  const [resolution, setResolution] = useState<'720p' | '1080p'>('720p');
  // Performance toggles: default to power-saving on Android, full quality on iOS
  const isAndroid = Platform.OS === 'android';
  const [showSkeleton, setShowSkeleton] = useState(!isAndroid);
  const [lowFps, setLowFps] = useState(isAndroid);
  const [force720p, setForce720p] = useState(isAndroid);
  const [skipCompression, setSkipCompression] = useState(isAndroid);
  const [serialUpload, setSerialUpload] = useState(isAndroid);
  const [movementOptions, setMovementOptions] = useState<string[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [injuryOptions, setInjuryOptions] = useState<string[]>([]);
  const [selectedInjuries, setSelectedInjuries] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    Promise.all([fetchMovements(), fetchInjuries()])
      .then(([movements, injuries]) => {
        setMovementOptions(movements);
        setInjuryOptions(injuries);
      })
      .catch((error) => {
        console.error("Failed to load workout metadata", error);
      })
      .finally(() => setLoading(false));
  }, []);

  const toggleMovement = (m: string) => {
    setSelectedMovements(prev => 
      prev.includes(m) ? prev.filter(x => x !== m) : [...prev, m]
    );
  };

  const toggleInjury = (injury: string) => {
    setSelectedInjuries(prev =>
      prev.includes(injury) ? prev.filter(x => x !== injury) : [...prev, injury]
    );
  };

  const handleStart = () => {
    router.push({
      pathname: '/workout/visionTestPage',
      params: {
        resolution: force720p ? '720p' : resolution,
        workoutType,
        movements: selectedMovements.join(', '),
        injuries: selectedInjuries.join(', '),
        autoRecord: 'true',
        showSkeleton: showSkeleton ? 'true' : 'false',
        lowFps: lowFps ? 'true' : 'false',
        force720p: force720p ? 'true' : 'false',
        skipCompression: skipCompression ? 'true' : 'false',
        serialUpload: serialUpload ? 'true' : 'false',
      },
    });
  };

  const canStart = true;

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <TouchableOpacity onPress={() => router.back()} style={styles.backBtn}>
          <IconSymbol name="chevron.left" size={28} color="#fff" />
        </TouchableOpacity>
        <Text style={styles.title}>Workout Setup</Text>
      </View>

      <ScrollView contentContainerStyle={styles.content}>
        {/* Resolution Config */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Video Quality</Text>
          <View style={styles.optionRow}>
            <Text style={styles.optionLabel}>Resolution</Text>
            <View style={styles.toggleGroup}>
              <TouchableOpacity 
                style={[styles.toggleBtn, resolution === '720p' && styles.toggleActive]}
                onPress={() => setResolution('720p')}
              >
                <Text style={styles.toggleText}>720p</Text>
              </TouchableOpacity>
              <TouchableOpacity 
                style={[styles.toggleBtn, resolution === '1080p' && styles.toggleActive]}
                onPress={() => setResolution('1080p')}
              >
                <Text style={styles.toggleText}>1080p</Text>
              </TouchableOpacity>
            </View>
          </View>
          <Text style={styles.hint}>720p is recommended for faster AI processing.</Text>
          <View style={[styles.optionRow, { marginTop: 16 }]}>
            <View>
              <Text style={styles.optionLabel}>Skeleton Overlay</Text>
              <Text style={[styles.hint, { marginTop: 2 }]}>
                {isAndroid ? 'May reduce stability on Android' : 'Real-time pose lines'}
              </Text>
            </View>
            <Switch
              value={showSkeleton}
              onValueChange={setShowSkeleton}
              trackColor={{ false: '#767577', true: '#81b0ff' }}
              thumbColor={showSkeleton ? '#f5dd4b' : '#f4f3f4'}
            />
          </View>
          <View style={[styles.optionRow, { marginTop: 16 }]}>
            <View>
              <Text style={styles.optionLabel}>Low FPS (24fps)</Text>
              <Text style={[styles.hint, { marginTop: 2 }]}>Reduces encoder + inference load</Text>
            </View>
            <Switch
              value={lowFps}
              onValueChange={setLowFps}
              trackColor={{ false: '#767577', true: '#81b0ff' }}
              thumbColor={lowFps ? '#f5dd4b' : '#f4f3f4'}
            />
          </View>
          <View style={[styles.optionRow, { marginTop: 16 }]}>
            <View>
              <Text style={styles.optionLabel}>Force 720p</Text>
              <Text style={[styles.hint, { marginTop: 2 }]}>Override resolution to 720p</Text>
            </View>
            <Switch
              value={force720p}
              onValueChange={setForce720p}
              trackColor={{ false: '#767577', true: '#81b0ff' }}
              thumbColor={force720p ? '#f5dd4b' : '#f4f3f4'}
            />
          </View>
          <View style={[styles.optionRow, { marginTop: 16 }]}>
            <View>
              <Text style={styles.optionLabel}>Skip Chunk Compression</Text>
              <Text style={[styles.hint, { marginTop: 2 }]}>Upload raw chunks (saves CPU)</Text>
            </View>
            <Switch
              value={skipCompression}
              onValueChange={setSkipCompression}
              trackColor={{ false: '#767577', true: '#81b0ff' }}
              thumbColor={skipCompression ? '#f5dd4b' : '#f4f3f4'}
            />
          </View>
          <View style={[styles.optionRow, { marginTop: 16 }]}>
            <View>
              <Text style={styles.optionLabel}>Serial Upload</Text>
              <Text style={[styles.hint, { marginTop: 2 }]}>Queue uploads 1-at-a-time (prevents OOM)</Text>
            </View>
            <Switch
              value={serialUpload}
              onValueChange={setSerialUpload}
              trackColor={{ false: '#767577', true: '#81b0ff' }}
              thumbColor={serialUpload ? '#f5dd4b' : '#f4f3f4'}
            />
          </View>
        </View>

        {/* Movements Config */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Planned Movements</Text>
          <Text style={styles.label}>Select what you will perform:</Text>
          
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
            Used to guide the AI analysis. Select at least one to start.
          </Text>
        </View>

        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Known Injuries</Text>
          <Text style={styles.label}>
            Optional: add any current limitations the coach should consider.
          </Text>

          {loading ? (
            <ActivityIndicator color="#007AFF" />
          ) : (
            <View style={styles.chipContainer}>
              {injuryOptions.map((injury) => {
                const isSelected = selectedInjuries.includes(injury);
                return (
                  <TouchableOpacity
                    key={injury}
                    onPress={() => toggleInjury(injury)}
                    style={[styles.chip, isSelected && styles.chipActive]}
                  >
                    <Text style={[styles.chipText, isSelected && styles.chipTextActive]}>
                      {injury}
                    </Text>
                  </TouchableOpacity>
                );
              })}
            </View>
          )}
          <Text style={styles.hint}>Sent with the upload as extra analysis context.</Text>
        </View>

      </ScrollView>

      <View style={styles.bottomBar}>
        <TouchableOpacity 
          style={[styles.startBtn, !canStart && styles.disabledBtn]} 
          onPress={handleStart}
          disabled={!canStart}
        >
          <Text style={styles.startText}>Start Workout</Text>
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
  section: { marginBottom: 30 },
  sectionTitle: { color: '#007AFF', fontSize: 14, fontWeight: 'bold', textTransform: 'uppercase', marginBottom: 15 },
  typeGrid: { gap: 12 },
  typeCard: {
    padding: 16,
    borderRadius: 16,
    borderWidth: 1,
    borderColor: '#333',
    backgroundColor: '#111',
  },
  typeCardActive: {
    borderColor: '#007AFF',
    backgroundColor: '#0B1A2F',
  },
  typeCardTitle: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '700',
    marginBottom: 6,
  },
  typeCardTitleActive: {
    color: '#8BC3FF',
  },
  typeCardDescription: {
    color: '#888',
    fontSize: 13,
    lineHeight: 18,
  },
  typeCardDescriptionActive: {
    color: '#D3E7FF',
  },
  optionRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 },
  optionLabel: { color: '#fff', fontSize: 16 },
  label: { color: '#fff', fontSize: 16, marginBottom: 10 },
  toggleGroup: { flexDirection: 'row', backgroundColor: '#222', borderRadius: 8, padding: 2 },
  toggleBtn: { paddingVertical: 6, paddingHorizontal: 12, borderRadius: 6 },
  toggleActive: { backgroundColor: '#444' },
  toggleText: { color: '#fff', fontWeight: 'bold' },
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
