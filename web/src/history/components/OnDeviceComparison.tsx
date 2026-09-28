import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import ko from '../../../../features/i18n/locales/ko.json';
import en from '../../../../features/i18n/locales/en.json';
import { onDeviceAiApi, readEnvironmentRecords } from '../../api/onDeviceAi';
import { latestRecordReview, object, recordView, strings, textValue } from '../onDeviceRecord';

function message(language: string, key: string): string {
  let value: unknown = (language === 'ko' ? ko : en).environment;
  for (const part of key.split('.')) value = object(value)[part];
  return textValue(value) || key;
}
export function ObservationQuality({ value, language }: { value: unknown; language: string }) {
  const v = recordView(value);
  const t = (key: string) => message(language, key);
  return <div className="space-y-1 text-xs text-text-muted">
    <p>{t('requestOutcome')}: {t(`outcome.${textValue(v.record.outcome)}`)}</p>
    <p>{t('validation')}: {t(`format.${v.responseFormat}`)}</p>
    {v.questionId && <p>{t('question')}: {t(`questions.${v.questionId}`)} · {v.questionId}</p>}
    <p>{t('automaticCheck')}: {v.quality.flags.length ? v.quality.flags.map(f => t(`quality.${f}`)).join(' · ') : t('quality.unchecked')}</p>
    <p>{textValue(object(v.record.context).appearanceHints).trim() ? t('targetProvided') : t('targetMissing')}</p>
  </div>;
}
function Review({ value, records, language }: { value: unknown; records?: unknown[]; language: string }) {
  const t = (key: string) => message(language, key);
  const review = records ? latestRecordReview(records, textValue(object(value).id)) : null;
  return <div className="text-sm">
    <p>{t('userReview')}: {!records ? t('reviewsNotLoaded') : review ? t(`verdict.${textValue(review.verdict)}`) : t('reviewNeeded')}</p>
    {review && <>
      <p>{textValue(review.note)}</p>
      {review.targetConfirmed === true && <p>{t('confirmTarget')} · {strings(review.confirmedBehaviors).map(b => t(`behavior.${b}`)).join(' · ')}</p>}
      {textValue(review.correctedObservation) && <p>{t('correctedObservation')}: {textValue(review.correctedObservation)}</p>}
    </>}
  </div>;
}
function Result({ value, records, language }: { value: unknown; records?: unknown[]; language: string }) {
  const v = recordView(value);
  return <div className="min-w-0 space-y-3 rounded border border-border p-3">
    <p className="break-all text-xs">{textValue(v.record.id)} · v{String(v.record.promptVersion ?? '?')} · {v.elapsedMs ?? '?'} ms</p>
    <ObservationQuality value={value} language={language} />
    <p className="whitespace-pre-wrap break-words text-xs">{textValue(v.record.prompt)}</p>
    <p className="whitespace-pre-wrap break-words">{v.response}</p>
    <Review value={value} records={records} language={language} />
  </div>;
}

export function OnDeviceComparison({ value, sessionId, profileId }: { value: unknown; sessionId: string; profileId: number }) {
  const [language, setLanguage] = useState(navigator.language.startsWith('ko') ? 'ko' : 'en');
  const [selected, setSelected] = useState('');
  const v = recordView(value);
  const t = (key: string) => message(language, key);
  const records = useQuery({ queryKey: ['environment-comparisons', profileId, sessionId],
    queryFn: () => readEnvironmentRecords(sessionId, profileId), enabled: false, retry: false, staleTime: 0 });
  // A child archive already identifies its original; only that JSON is read automatically.
  const parent = useQuery({ queryKey: ['on-device-ai-record', profileId, sessionId, v.parentFilename],
    queryFn: () => onDeviceAiApi.read(sessionId, profileId, v.parentFilename!), enabled: !!v.parentFilename, retry: false });
  const loaded = records.isSuccess ? records.data : undefined;
  const candidates = (loaded ?? []).map(object).filter(r => ['camera','behavior','space'].includes(textValue(r.kind)) && (r.parentId === v.record.id || r.id === v.record.parentId));
  const comparison = selected ? candidates.find(r => r.id === selected) : parent.data;
  const comparisonIsChild = object(comparison).parentId === v.record.id;
  return <section className="space-y-3 rounded-lg border border-border p-3">
    <select aria-label="Language / 언어" value={language} onChange={e => setLanguage(e.target.value)} className="rounded bg-bg-tertiary p-2"><option value="ko">한국어</option><option value="en">English</option></select>
    <ObservationQuality value={value} language={language} />
    <Review value={value} records={loaded} language={language} />
    <button className="rounded border border-border p-2 text-accent disabled:opacity-50" disabled={records.isFetching} onClick={() => void records.refetch()}>
      {t(records.isFetching ? 'loadingComparison' : 'loadComparison')}
    </button>
    <p className="text-xs text-text-muted">{t('comparisonHelp')}</p>
    {records.isError && <p role="alert">{t('comparisonError')}</p>}
    {parent.isError && <p role="alert">{t('originalMissing')}</p>}
    {loaded && <label className="block">{t('selectComparison')}
      <select className="ml-2 max-w-full rounded bg-bg-tertiary p-2" value={selected} onChange={e => setSelected(e.target.value)}>
        <option value="">{t(v.parentFilename ? 'originalComparison' : 'noneComparison')}</option>
        {candidates.map(r => <option key={textValue(r.id)} value={textValue(r.id)}>
          {r.parentId === v.record.id ? '↳ ' : ''}{textValue(r.questionId) || textValue(r.kind)} · {new Date(Number(r.startedAt)).toLocaleTimeString()} · {textValue(r.id)}
        </option>)}
      </select>
    </label>}
    {comparison != null && <div className="grid gap-3 lg:grid-cols-2">
      <div><h4 className="mb-2 font-medium">{t('originalComparison')}</h4><Result value={comparisonIsChild ? value : comparison} records={loaded} language={language} /></div>
      <div><h4 className="mb-2 font-medium">{t('reanalysisComparison')}</h4><Result value={comparisonIsChild ? comparison : value} records={loaded} language={language} /></div>
    </div>}
  </section>;
}
