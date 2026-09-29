import React, { useState } from 'react';
import { ActivityIndicator, Alert, Image, Linking, ScrollView, StyleSheet, Text, TextInput, TouchableOpacity, View } from 'react-native';
import { t, useLocale } from '../i18n';
import { useProfileId } from '../../store/useProfileStore';
import { aggregateHabits, checkObservation, latestReview } from './observation';
import { deleteEnvironmentSession, evidencePath, listSessions, readRecords } from './store';
import { reanalyzeObservation, reviewObservation, summarizeHabits } from './review';
import { behaviors, type Behavior, type EnvironmentRecord, type EnvironmentSession, type Verdict } from './types';

export function EnvironmentLiveCard({ status, record }: { status: string; record?: EnvironmentRecord }) {
  useLocale();
  return <View style={styles.liveCard}>
    <View style={styles.liveHeaderRow}>
      <Text style={styles.liveTitle}>{t('environment.title')}</Text>
      <Text style={styles.liveStatus}>{t(`environment.status.${status}`, { defaultValue: status })}</Text>
    </View>
    {record && <Text numberOfLines={2} style={styles.liveText}>{t(`environment.kind.${record.kind}`)} · {new Date(record.completedAt).toLocaleTimeString()}{'\n'}
      {record.parsed?.facts.join(' ') || (record.kind === 'sound' ? t('environment.soundSaved') : record.raw || '')}</Text>}
    {record?.questionId && <Text style={styles.liveText}>{t(`environment.questions.${record.questionId}`)} · {t('environment.reviewNeeded')}</Text>}
  </View>;
}
export function EnvironmentHistoryCard({ sessionId, profileId }: { sessionId: string; profileId?: number }) {
  useLocale();
  const activeProfile = useProfileId();
  const [session, setSession] = useState<EnvironmentSession | null>(null);
  const [records, setRecords] = useState<EnvironmentRecord[] | null>(null);
  const [allRecords, setAllRecords] = useState<EnvironmentRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [note, setNote] = useState('');
  const [targetConfirmed, setTargetConfirmed] = useState(false);
  const [confirmedBehaviors, setConfirmedBehaviors] = useState<Behavior[]>([]);
  const [correctedObservation, setCorrectedObservation] = useState('');
  const [visibleCount, setVisibleCount] = useState(30);
  const [habitEvidence, setHabitEvidence] = useState<EnvironmentRecord | null>(null);
  const load = async () => {
    const pid = profileId ?? activeProfile;
    if (!pid) return;
    const sessions = (await listSessions(pid)).filter(s => !s.deleting).slice(0,30);
    const current = sessions.find(s => s.sessionId === sessionId) ?? (await listSessions(pid)).find(s => s.sessionId === sessionId);
    const history: EnvironmentRecord[] = [];
    for (const s of sessions) history.push(...await readRecords(s));
    setAllRecords(history); setSession(current ?? null);
    setRecords(current ? await readRecords(current) : []);
  };
  const action = async (fn: () => Promise<unknown>) => {
    setBusy(true); setError(false);
    try { await fn(); } catch { setError(true); } finally { setBusy(false); }
  };
  const button = (label: string, onPress: () => void) => <TouchableOpacity disabled={busy} onPress={onPress} style={styles.button}><Text style={styles.link}>{label}</Text></TouchableOpacity>;
  const observations = (records ?? []).filter(r => r.kind !== 'review');
  const ai = observations.filter(r => ['FoundationModels','SoundAnalysis'].includes(r.source) && r.outcome !== 'skipped');
  return <View style={styles.card}>
    {button(t('environment.history'), () => { if (records) setRecords(null); else void action(load); })}
    {busy && <ActivityIndicator />}
    {error && <Text style={styles.text}>{t('environment.actionError')}</Text>}
    {records && <>
      <Text style={styles.text}>{t('environment.localScope')}</Text>
      {!session && <Text style={styles.text}>{t('environment.noData')}</Text>}
      <Text style={styles.text}>{t('environment.execution', { count: ai.length, success: ai.filter(r => r.outcome === 'success').length,
        ms: ai.length ? Math.round(ai.reduce((sum,r) => sum+r.inferenceMs,0)/ai.length) : 0,
        reviewed: records.filter(r => r.review).length })}</Text>
      {aggregateHabits(allRecords).map(h => <View key={h.behavior}>
        <Text style={styles.text}>{t(`environment.behavior.${h.behavior}`)} · {t(h.repeated ? 'environment.repeated' : 'environment.observed', { count: h.evidence.length })}</Text>
        <View style={{ flexDirection:'row', flexWrap:'wrap' }}>{h.evidence.map(e => <React.Fragment key={e.recordId}>
          {button(new Date(e.at).toLocaleDateString(), () => setHabitEvidence(allRecords.find(r => r.id === e.recordId) ?? null))}
        </React.Fragment>)}</View>
      </View>)}
      {habitEvidence && <View>
        {button(t('environment.closeEvidence'), () => setHabitEvidence(null))}
        <Text style={styles.text}>{habitEvidence.sessionId}{'\n'}{latestReview(allRecords, habitEvidence.id)?.correctedObservation || habitEvidence.parsed?.facts.join(' ') || habitEvidence.raw}</Text>
        {habitEvidence.evidence.filter(e => e.mime === 'image/jpeg').map(e => <Image key={e.filename}
          source={{ uri: evidencePath(habitEvidence.bundleId,e.filename) }} style={{ width:'100%',height:160 }} resizeMode="contain" />)}
      </View>}
      {session && button(t('environment.summarize'), () => void action(async () => { await summarizeHabits(session, allRecords); await load(); }))}
      <ScrollView style={{ maxHeight: 520 }} nestedScrollEnabled>
        {observations.slice().reverse().slice(0,visibleCount).map(r => <View key={r.id} style={styles.entry}>
          {button(`${t(`environment.kind.${r.kind}`)} · ${new Date(r.startedAt).toLocaleTimeString()}`, () => { setSelected(selected === r.id ? null : r.id); setNote(''); setTargetConfirmed(false); setConfirmedBehaviors([]); setCorrectedObservation(''); })}
          <Text style={styles.text}>{r.parsed?.facts.join(' ') || (r.kind === 'weather' ? t('environment.weather') : r.reason ? t(`environment.status.${r.reason}`, { defaultValue: r.reason }) : r.raw || t('environment.openDetails'))}</Text>
          {selected === r.id && <>
            <ObservationStatus record={r} records={records} />
            {r.parentId && <View style={styles.card}>
              <Text style={styles.title}>{t('environment.originalComparison')}</Text>
              {records.find(v => v.id === r.parentId) ? <ObservationComparison record={records.find(v => v.id === r.parentId)!} records={records} />
                : <Text style={styles.text}>{t('environment.originalMissing')}</Text>}
              <Text style={styles.title}>{t('environment.reanalysisComparison')}</Text>
              <ObservationComparison record={r} records={records} />
            </View>}
            {records.filter(v => v.parentId === r.id).map(v => <React.Fragment key={v.id}>
              {button(t('environment.compareReanalysis') + ' · ' + new Date(v.startedAt).toLocaleTimeString(), () => { setSelected(v.id); setVisibleCount(observations.length); setNote(''); setTargetConfirmed(false); setConfirmedBehaviors([]); setCorrectedObservation(''); })}
            </React.Fragment>)}
            <Text selectable style={styles.text}>{t('environment.method')}: {r.source} / {r.model ?? t('environment.unavailable')} / v{r.promptVersion}{'\n'}
              {t('environment.timing', { prepare: r.preparationMs, infer: r.inferenceMs })}{'\n'}
              {t('environment.validation')}: {t(`environment.format.${r.responseFormat === 'text' ? 'text' : r.validation}`)}{'\n'}
              {t('environment.unknownMetrics')}{'\n'}
              {t('environment.context')}: {JSON.stringify(r.context)}{'\n'}
              {t('environment.power')}: {JSON.stringify({ before: r.powerBefore, after: r.powerAfter })}</Text>
            {r.evidence.map(e => e.mime === 'image/jpeg'
              ? <View key={e.filename}><Image source={{ uri: evidencePath(r.bundleId,e.filename) }} style={{ width: '100%', height: 200 }} resizeMode="contain" /><Text style={styles.text}>{t('environment.mediaOffset', { ms: e.mediaOffsetMs ?? 0 })}</Text></View>
              : <AudioEvidence key={e.filename} uri={evidencePath(r.bundleId,e.filename)} />)}
            {r.prompt && <Text selectable style={styles.text}>{t('environment.prompt')}{'\n'}{String(r.data?.systemInstructions ?? '')}{'\n'}{r.prompt}</Text>}
            {r.raw && <Text selectable style={styles.text}>{t('environment.raw')}{'\n'}{r.raw}</Text>}
            {r.parsed && <Text style={styles.text}>{t('environment.interpretation')}: {r.parsed.interpretation}{'\n'}{t('environment.limits')}: {r.parsed.limitations.join(' ')}</Text>}
            {r.kind === 'weather' && <WeatherEvidence data={r.data} />}
            {r.kind !== 'weather' && session && <>
              {['behavior','space'].includes(r.kind) && <>
                <Text style={styles.text}>{t('environment.confirmHelp')}</Text>
                {button((targetConfirmed ? '✓ ' : '○ ') + t('environment.confirmTarget'), () => setTargetConfirmed(v => !v))}
                <View style={{ flexDirection: 'row', flexWrap: 'wrap' }}>{behaviors.map(b => <React.Fragment key={b}>
                  {button((confirmedBehaviors.includes(b) ? '✓ ' : '○ ') + t(`environment.behavior.${b}`), () => setConfirmedBehaviors(current => current.includes(b) ? current.filter(v => v !== b) : [...current, b]))}
                </React.Fragment>)}</View>
                <TextInput style={styles.input} value={correctedObservation} onChangeText={setCorrectedObservation} maxLength={2000}
                  accessibilityLabel={t('environment.correctedObservation')} placeholder={t('environment.correctedObservation')} placeholderTextColor="#aaa" multiline />
              </>}
              <TextInput style={styles.input} value={note} onChangeText={setNote} maxLength={1000} placeholder={t('environment.note')} placeholderTextColor="#aaa" multiline />
              <View style={{ flexDirection: 'row', flexWrap: 'wrap' }}>{(['correct','incorrect','abstract','uncertain'] as Verdict[]).map(v => <React.Fragment key={v}>
                {button(t(`environment.verdict.${v}`), () => void action(async () => { await reviewObservation(session,r,v,note,{ targetConfirmed, confirmedBehaviors, correctedObservation }); await load(); }))}
              </React.Fragment>)}</View>
              {['camera','behavior','space','sound'].includes(r.kind) && r.evidence.length > 0 && button(t('environment.reanalyze'), () => void action(async () => { await reanalyzeObservation(session,r); await load(); }))}
            </>}
          </>}
        </View>)}
      </ScrollView>
      {observations.length > visibleCount && button(t('environment.more'), () => setVisibleCount(n => n+30))}
      {session && button(t('environment.delete'), () => Alert.alert(t('environment.delete'), t('environment.deleteHelp'), [
        { text:t('common.cancel'),style:'cancel' },
        { text:t('environment.delete'),style:'destructive',onPress:() => void action(async () => { await deleteEnvironmentSession(session.profileId,session.sessionId); await load(); }) },
      ]))}
    </>}
  </View>;
}
function ObservationStatus({ record, records }: { record: EnvironmentRecord; records: EnvironmentRecord[] }) {
  const review = latestReview(records, record.id);
  const visual = ['camera','behavior','space'].includes(record.kind);
  const quality = visual ? record.quality ?? checkObservation(record.raw) : null;
  return <View>
    <Text style={styles.text}>{t('environment.requestOutcome')}: {t(`environment.outcome.${record.outcome}`)}</Text>
    {record.questionId && <Text style={styles.text}>{t('environment.question')}: {t(`environment.questions.${record.questionId}`)}</Text>}
    {visual && <>
      <Text style={styles.text}>{record.context.appearanceHints.trim() ? t('environment.targetProvided') : t('environment.targetMissing')}</Text>
      <Text style={styles.text}>{t('environment.automaticCheck')}: {quality?.flags.length ? quality.flags.map(f => t(`environment.quality.${f}`)).join(' · ') : t('environment.quality.unchecked')}</Text>
    </>}
    <Text style={styles.text}>{t('environment.userReview')}: {review ? t(`environment.verdict.${review.verdict}`) : t('environment.reviewNeeded')}{review?.note ? ` · ${review.note}` : ''}</Text>
    {review?.targetConfirmed && <Text style={styles.text}>{t('environment.confirmTarget')} · {(review.confirmedBehaviors ?? []).map(b => t(`environment.behavior.${b}`)).join(' · ')}</Text>}
    {!!review?.correctedObservation && <Text style={styles.text}>{t('environment.correctedObservation')}: {review.correctedObservation}</Text>}
  </View>;
}
function ObservationComparison({ record, records }: { record: EnvironmentRecord; records: EnvironmentRecord[] }) {
  return <View>
    <Text style={styles.text}>v{record.promptVersion} · {t('environment.timing', { prepare: record.preparationMs, infer: record.inferenceMs })}</Text>
    <Text selectable style={styles.text}>{record.prompt}</Text>
    <Text selectable style={styles.text}>{record.raw}</Text>
    <ObservationStatus record={record} records={records} />
  </View>;
}
// Use the existing video player for local m4a evidence; it owns playback lifetime.
import { VideoView, useVideoPlayer } from 'expo-video';
function AudioEvidence({ uri }: { uri: string }) {
  const player = useVideoPlayer(uri);
  return <View><Text style={styles.text}>{t('environment.audio')}</Text><VideoView player={player} style={{ height: 72 }} nativeControls /></View>;
}
function WeatherEvidence({ data }: { data?: Record<string, unknown> }) {
  const a = data?.attribution as { name?: string; markURL?: string; legalURL?: string } | undefined;
  return <View>
    <Text selectable style={styles.text}>{JSON.stringify(data, null, 2)}</Text>
    {a?.markURL && <Image source={{ uri: a.markURL }} style={{ width: 120, height: 30 }} resizeMode="contain" />}
    {a?.legalURL && <TouchableOpacity onPress={() => { if (a.legalURL?.startsWith('https://')) void Linking.openURL(a.legalURL); }}><Text style={styles.link}>{a.name} · {t('environment.attribution')}</Text></TouchableOpacity>}
  </View>;
}
const styles = StyleSheet.create({
  card: { backgroundColor: '#253442', padding: 12, borderRadius: 10, marginVertical: 6 },
  liveCard: { backgroundColor: '#1c2834', padding: 8, borderRadius: 8, marginVertical: 4, borderWidth: 1, borderColor: '#3d5266' },
  liveHeaderRow: { flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center' },
  liveTitle: { color: '#cce9ff', fontWeight: '700', fontSize: 12 },
  liveStatus: { color: '#8ec5fc', fontSize: 10, fontWeight: '600' },
  liveText: { color: '#cbd5e1', fontSize: 11, lineHeight: 15, marginTop: 2 },
  title: { color: '#cce9ff', fontWeight: '600' }, text: { color: '#ddd', fontSize: 12, lineHeight: 18, marginVertical: 4 },
  link: { color: '#86ceff', fontSize: 13 }, button: { paddingVertical: 8, paddingRight: 12 },
  entry: { borderTopWidth: 1, borderColor: '#53616b', paddingVertical: 6 },
  input: { color: '#fff', borderWidth: 1, borderColor: '#64727e', padding: 8, borderRadius: 6 },
});
