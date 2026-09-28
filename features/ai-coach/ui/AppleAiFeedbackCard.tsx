import React from 'react';
import { ScrollView, StyleSheet, Text, View } from 'react-native';
import { t, useLocale } from '../../i18n';
import type { AppleAiFeedbackResult } from '../useAppleAiFeedback';

const statusKeys = new Set(['off', 'checking', 'idle', 'paused', 'collecting', 'analyzing', 'waiting', 'error',
  'power', 'thermal', 'recovering', 'unsupported_os', 'unsupported_device', 'disabled', 'model_not_ready',
  'vision_unsupported', 'language_unsupported', 'module_missing', 'unavailable']);
const errorKeys = new Set(['refused', 'timeout', 'context_too_large', 'rate_limited', 'thermal', 'busy',
  'cancelled', 'empty_response', 'invalid_frames', 'analysis_failed']);

export function AppleAiFeedbackCard({ status, error, result, archiveError }: {
  status: string; error: string | null; result: AppleAiFeedbackResult | null; archiveError?: boolean;
}) {
  const locale = useLocale();
  const time = (epoch: number) => new Date(epoch).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  return (
    <View style={styles.card}>
      <Text style={styles.title}>{t('appleAi.title')}</Text>
      <Text style={styles.meta}>{t(`appleAi.status.${statusKeys.has(status) ? status : 'unavailable'}`)}</Text>
      {archiveError && <Text style={styles.error}>{t('appleAi.archiveError')}</Text>}
      {status === 'error' && error && <Text style={styles.error}>{t(`appleAi.errors.${errorKeys.has(error) ? error : 'analysis_failed'}`)}</Text>}
      {result && <>
        <Text style={styles.meta}>{t('appleAi.timing', {
          start: time(result.capturedAt), end: time(result.lastCapturedAt), seconds: (result.elapsedMs / 1000).toFixed(1),
        })}</Text>
        <ScrollView style={styles.feedbackScroll} nestedScrollEnabled>
          <Text selectable style={styles.feedback}>{result.feedback}</Text>
        </ScrollView>
      </>}
    </View>
  );
}

const styles = StyleSheet.create({
  card: { backgroundColor: 'rgba(12, 23, 38, 0.92)', borderRadius: 8, padding: 10, marginTop: 8, borderWidth: 1, borderColor: '#547ea3' },
  title: { color: '#a4d7ff', fontWeight: '700', fontSize: 13 },
  meta: { color: '#b8c8d8', fontSize: 11, marginTop: 4 },
  error: { color: '#ffd28a', fontSize: 12, marginTop: 4 },
  feedback: { color: '#fff', fontSize: 14, lineHeight: 20, marginTop: 6 },
  feedbackScroll: { maxHeight: 110 },
});
