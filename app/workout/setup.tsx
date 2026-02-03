import { IconSymbol } from '@/components/ui/icon-symbol';
import { fetchMovements } from '@/features/wod/api';
import { router } from 'expo-router';
import React, { useEffect, useState } from 'react';
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';

export default function WorkoutSetup() {
  const [resolution, setResolution] = useState<'720p' | '1080p'>('720p');
  const [movementOptions, setMovementOptions] = useState<string[]>([]);
  const [selectedMovements, setSelectedMovements] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchMovements()
      .then(setMovementOptions)
      .finally(() => setLoading(false));
  }, []);

  const toggleMovement = (m: string) => {
    setSelectedMovements(prev => 
      prev.includes(m) ? prev.filter(x => x !== m) : [...prev, m]
    );
  };

  const handleStart = () => {
    router.push({
      pathname: '/workout/visionTestPage',
      params: {
        resolution,
        movements: selectedMovements.join(', '),
      },
    });
  };

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
          <Text style={styles.hint}>Used to guide the AI analysis.</Text>
        </View>

        <TouchableOpacity 
          style={[styles.startBtn, selectedMovements.length === 0 && styles.disabledBtn]} 
          onPress={handleStart}
          disabled={selectedMovements.length === 0}
        >
          <Text style={styles.startText}>Start Workout</Text>
          <IconSymbol name="figure.run" size={24} color="#000" />
        </TouchableOpacity>

      </ScrollView>
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
  content: { padding: 20 },
  section: { marginBottom: 30 },
  sectionTitle: { color: '#007AFF', fontSize: 14, fontWeight: 'bold', textTransform: 'uppercase', marginBottom: 15 },
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
  startBtn: {
    backgroundColor: '#fff',
    padding: 18,
    borderRadius: 12,
    flexDirection: 'row',
    justifyContent: 'center',
    alignItems: 'center',
    gap: 10,
    marginTop: 20,
  },
  disabledBtn: { opacity: 0.5 },
  startText: { color: '#000', fontSize: 18, fontWeight: 'bold' },
});
