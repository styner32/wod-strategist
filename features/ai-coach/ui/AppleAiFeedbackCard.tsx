import React from 'react';
import { ScrollView, StyleSheet, Text, View } from 'react-native';
import { t, useLocale } from '../../i18n';
import type { AppleAiFeedbackResult } from '../useAppleAiFeedback';

const statusKeys = new Set(['off', 'checking', 'idle', 'paused', 'collecting', 'analyzing', 'waiting', 'error',
  'power', 'thermal', 'recovering', 'unsupported_os', 'unsupported_device', 'disabled', 'model_not_ready',
  'vision_unsupported', 'language_unsupported', 'module_missing', 'unavailable']);
const errorKeys = new Set(['refused', 'timeout', 'context_too_large', 'rate_limited', 'thermal', 'busy',
  'cancelled', 'empty_response', 'invalid_frames', 'analysis_failed']);

export function parseAppleAiFeedback(raw: string): string {
  try {
    const trimmed = raw.trim();
    if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
      const parsed = JSON.parse(trimmed);
      if (typeof parsed.posture_feedback === 'string' && parsed.posture_feedback.trim()) {
        return parsed.posture_feedback.trim();
      }
      if (typeof parsed.feedback === 'string' && parsed.feedback.trim()) {
        return parsed.feedback.trim();
      }
    }
  } catch {
    // fallback to raw text if not valid JSON
  }
  return raw;
}

export function AppleAiFeedbackCard({ status, error, result, archiveError }: {
  status: string; error: string | null; result: AppleAiFeedbackResult | null; archiveError?: boolean;
}) {
  const locale = useLocale();
  const time = (epoch: number) => new Date(epoch).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  const displayFeedback = result ? parseAppleAiFeedback(result.feedback) : '';
  return (
    <View style={styles.card}>
      <View style={styles.headerRow}>
        <Text style={styles.title}>{t('appleAi.title')}</Text>
        <Text style={styles.statusBadge}>{t(`appleAi.status.${statusKeys.has(status) ? status : 'unavailable'}`)}</Text>
      </View>
      {archiveError && <Text style={styles.error}>{t('appleAi.archiveError')}</Text>}
      {status === 'error' && error && <Text style={styles.error}>{t(`appleAi.errors.${errorKeys.has(error) ? error : 'analysis_failed'}`)}</Text>}
      {result && <>
        <Text style={styles.meta}>{t('appleAi.timing', {
          start: time(result.capturedAt), end: time(result.lastCapturedAt), seconds: (result.elapsedMs / 1000).toFixed(1),
        })}</Text>
        <ScrollView style={styles.feedbackScroll} nestedScrollEnabled>
          <Text selectable style={styles.feedback}>{displayFeedback}</Text>
        </ScrollView>
      </>}
    </View>
  );
}

const styles = StyleSheet.create({
  card: { backgroundColor: 'rgba(12, 23, 38, 0.92)', borderRadius: 8, padding: 8, marginTop: 4, borderWidth: 1, borderColor: '#547ea3' },
  headerRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center' },
  title: { color: '#a4d7ff', fontWeight: '700', fontSize: 12 },
  statusBadge: { color: '#88b8e0', fontSize: 10, fontWeight: '600' },
  meta: { color: '#b8c8d8', fontSize: 10, marginTop: 2 },
  error: { color: '#ffd28a', fontSize: 11, marginTop: 2 },
  feedback: { color: '#fff', fontSize: 13, lineHeight: 18, marginTop: 4 },
  feedbackScroll: { maxHeight: 85 },
});
