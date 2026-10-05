import { lazy, Suspense, useState } from 'react';
const OnDeviceComparison = lazy(() => import('./OnDeviceComparison').then(m => ({default:m.OnDeviceComparison})));
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ApiError } from '../../api/client';
import { onDeviceAiApi, onDeviceAssetPath } from '../../api/onDeviceAi';
import { arrayValue, assetName, numberValue, object, recordView, strings, textValue } from '../onDeviceRecord';

const labels: Record<string, string> = {
  camera: '촬영 상태', behavior: '인물·운동 행동', sound: '주변 소리', space: '공간·상호작용',
  weather: '지역 날씨', review: '사용자 검수', summary: '요청한 종합 설명',
  success: '완료', error: '실패', skipped: '건너뜀', cancelled: '중단', timeout: '시간 초과',
  valid: '구조화 성공', invalid: '구조화 실패', not_applicable: '해당 없음',
  correct: '맞음', incorrect: '틀림', abstract: '너무 추상적', uncertain: '판단 어려움',
  identified: '대상 확인',
};
const label = (value: unknown) => labels[textValue(value)] ?? (textValue(value) || '기록 없음');
const date = (value: unknown) => { const n = numberValue(value); return n === null ? '시각 미기록' : new Date(n).toLocaleString('ko-KR'); };
const duration = (value: unknown) => { const n = numberValue(value); return n === null ? '확인 불가' : `${(n / 1000).toFixed(2)}초`; };
const fileLabel = (name: string) => name.endsWith('_session.json') ? '기기·세션 정보' : name.endsWith('.ndjson') ? '센서·실행 기록' : name.startsWith('apple_ai_') ? '기존 자세 분석' : '환경·행동 관찰';
const httpsURL = (value: unknown) => { try { const url = new URL(textValue(value)); return url.protocol === 'https:' ? url.href : undefined; } catch { return undefined; } };
const buttonClass = 'rounded-lg border border-border px-3 py-2 text-sm text-accent hover:bg-bg-tertiary disabled:opacity-50';

function JsonDetails({ title, value }: { title: string; value: unknown }) {
  return <details className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">{title}</summary>
    <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words text-xs text-text-secondary">{JSON.stringify(value ?? null, null, 2)}</pre>
  </details>;
}

function Evidence({ item, sessionId, profileId }: { item: Record<string, unknown>; sessionId: string; profileId: number }) {
  const [failed, setFailed] = useState(false);
  const filename = assetName(item.filename)!;
  const url = `/api/v1${onDeviceAssetPath(sessionId, profileId, filename)}`;
  const offset = numberValue(item.mediaOffsetMs) ?? numberValue(item.captureOffsetMs);
  return <figure className="min-w-0 rounded-lg border border-border p-2">
    {failed ? <p className="text-sm text-amber-400">근거 파일을 불러오지 못했습니다.</p> : filename.endsWith('.jpg') ?
      <a href={url} target="_blank" rel="noreferrer"><img loading="lazy" className="max-h-80 w-full rounded object-contain" src={url} alt="AI에 전달한 입력 사진" onError={() => setFailed(true)} /></a> :
      filename.endsWith('.m4a') ? <audio controls preload="none" className="w-full" src={url} onError={() => setFailed(true)} /> : null}
    <figcaption className="mt-2 break-all text-xs text-text-muted">{offset !== null && `${item.mediaOffsetMs !== undefined ? '원본 조각 내' : '촬영 경과'} ${(offset / 1000).toFixed(1)}초 · `}{filename}</figcaption>
    <a href={url} target="_blank" rel="noreferrer" className="text-xs text-accent">원본 열기</a>
  </figure>;
}

function MeasurementDetails({ kind, value }: { kind: unknown; value: unknown }) {
  const data = object(value);
  if (kind === 'weather') {
    const attribution = object(data.attribution);
    const temperature = numberValue(data.temperatureC);
    const humidity = numberValue(data.humidityFraction);
    return <div className="rounded-lg bg-bg-tertiary p-3">
      <h4 className="font-medium">지역 실외 날씨 · 실내 측정값 아님</h4>
      <p>{temperature === null ? '온도 미기록' : `${temperature.toFixed(1)}°C`} · {humidity === null ? '습도 미기록' : `습도 ${(humidity * 100).toFixed(0)}%`} · {textValue(data.condition)}</p>
      <p className="text-xs text-text-muted">자료 시각: {date(data.observedAt)}</p>
      {httpsURL(attribution.markURL) && <img className="mt-2 h-6 max-w-40 object-contain" src={httpsURL(attribution.markURL)} alt={textValue(attribution.name) || 'WeatherKit'} />}
      {httpsURL(attribution.legalURL) && <a className="text-xs text-accent" href={httpsURL(attribution.legalURL)} target="_blank" rel="noreferrer">{textValue(attribution.name) || 'WeatherKit'} · 출처</a>}
    </div>;
  }
  if (kind === 'sound') return <div className="rounded-lg bg-bg-tertiary p-3">
    <h4 className="font-medium">소리 분류 · Apple Sound Analysis</h4>
    <p className="text-xs text-text-muted">녹음 신호 RMS {numberValue(data.rmsDbfs)?.toFixed(1) ?? '미기록'} dBFS · 실제 공간의 소음계 측정값이 아닙니다.</p>
    <ul className="mt-2 space-y-1">{arrayValue(data.classifications).map((window, i) => <li key={i}>{arrayValue(object(window).labels).map(value => {
      const item = object(value); const confidence = numberValue(item.confidence);
      return `${textValue(item.label)} (${confidence === null ? '신뢰도 미기록' : `신뢰도 ${(confidence * 100).toFixed(0)}%`})`;
    }).join(' · ')}</li>)}</ul>
  </div>;
  return null;
}

function RecordDetail({ sessionId, profileId, filename }: { sessionId: string; profileId: number; filename: string }) {
  const query = useQuery({ queryKey: ['on-device-ai-record', profileId, sessionId, filename],
    queryFn: () => onDeviceAiApi.read(sessionId, profileId, filename), retry: false, staleTime: 60_000 });
  if (query.isPending) return <p role="status">기록을 불러오는 중…</p>;
  if (query.isError) return <div role="alert"><p>기록을 읽을 수 없습니다. 업로드가 끝났는지 확인해주세요.</p><p className="text-xs text-text-muted">{query.error.message}</p><button className={buttonClass} onClick={() => void query.refetch()}>다시 불러오기</button></div>;
  const view = recordView(query.data);
  const r = view.record;
  const context = object(r.context);
  const review = object(r.review);
  return <article className="min-w-0 space-y-4 text-sm">
    <div>
      <h3 className="font-semibold text-text-primary">{view.session ? '기기·세션 정보' : view.apple ? '기존 자세 분석' : label(r.kind)}</h3>
      <p className="text-text-muted">{date(r.startedAt)}{!view.session && ` · ${label(r.outcome)}`}</p>
      <p>{view.source || '세션 메타데이터'}{textValue(r.model) && ` · ${textValue(r.model)}`}</p>
      {!view.session && <p className="text-xs text-text-muted">준비 {duration(r.preparationMs)} · 처리 {duration(view.elapsedMs)} · 프롬프트 버전 {numberValue(r.promptVersion) ?? '미기록'}{!view.apple && ` · ${r.responseFormat === 'text' ? '자연어 응답 · JSON 검사 해당 없음' : label(r.validation)}`}</p>}
      {r.parentId !== undefined && <p className="text-xs text-text-muted">재분석 · 원래 기록: {textValue(r.parentId)}</p>}
    </div>
    {view.session && <p className="text-text-muted">{r.complete ? '세션 종료 기록 있음' : '완료되지 않은 세션 기록'} · 종료 {date(r.endedAt)}</p>}
    {!view.apple && ['camera','behavior','space'].includes(textValue(r.kind)) && <Suspense fallback={null}><OnDeviceComparison value={r} sessionId={sessionId} profileId={profileId} /></Suspense>}
    <MeasurementDetails kind={r.kind} value={r.data} />
    {view.reason && <p className="rounded-lg bg-bg-tertiary p-3 text-amber-400">실패·건너뜀 사유: {view.reason}</p>}
    {view.response ? <div className="rounded-lg bg-bg-tertiary p-3"><h4 className="mb-2 font-medium">AI 응답 원문</h4><p className="whitespace-pre-wrap break-words">{view.response}</p></div> : !view.session && <p className="text-text-muted">AI 응답 원문 없음{r.source !== 'FoundationModels' && !view.apple ? ' · 측정값·검수 기록은 아래 상세에서 확인하세요.' : ''}</p>}
    {Object.keys(view.parsed).length > 0 && <div className="space-y-2">
      <p className="text-text-muted">대상: {label(view.parsed.target)}</p>
      <h4 className="font-medium">관찰 사실</h4><ul className="list-disc pl-5">{strings(view.parsed.facts).map((fact, i) => <li key={i}>{fact}</li>)}</ul>
      <p>잠정 해석: {textValue(view.parsed.interpretation) || '없음'}</p>
      <p className="text-text-muted">판단 한계: {strings(view.parsed.limitations).join(' · ') || '미기록'}</p>
    </div>}
    {Object.keys(review).length > 0 && <p className="rounded-lg bg-bg-tertiary p-3">사용자 검수: {label(review.verdict)} · {textValue(review.note) || '메모 없음'}<br /><span className="text-xs">검수 대상: {textValue(review.targetId)}</span></p>}
    {view.frames.length > 0 && <div><h4 className="mb-2 font-medium">실제 입력 근거 ({view.frames.length}개)</h4>
      <div className="grid gap-3 sm:grid-cols-3">{view.frames.map((item, i) => <Evidence key={`${filename}-${i}`} item={item} sessionId={sessionId} profileId={profileId} />)}</div>
      <p className="mt-2 text-xs text-text-muted">사진 시각은 원본 촬영 기준이며, 병합 영상의 재생 위치와 다를 수 있습니다.</p></div>}
    <details className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">질문·운동·외형 컨텍스트</summary>
      <dl className="mt-2 space-y-2 whitespace-pre-wrap break-words"><dt className="text-text-muted">WOD</dt><dd>{textValue(context.wodDescription) || '미기록'}</dd><dt className="text-text-muted">동작</dt><dd>{textValue(context.movements) || '미기록'}</dd><dt className="text-text-muted">대상 외형</dt><dd>{textValue(context.appearanceHints) || '미기록'}</dd><dt className="text-text-muted">실제 질문</dt><dd>{textValue(r.prompt) || '이 버전에는 질문 원문이 보관되지 않았습니다.'}</dd></dl>
    </details>
    {!view.apple && !view.session && <JsonDetails title="측정값·시스템 지침·검증 자료" value={r.data} />}
    {!view.session && <JsonDetails title="분석 전후 배터리·발열" value={{ before: r.powerBefore ?? null, after: r.powerAfter ?? null }} />}
    <JsonDetails title={view.session ? '기기·OS·앱 버전 및 촬영 설정' : '전체 원본 JSON (상세 메타데이터 포함)'} value={r} />
    <p className="text-xs text-text-muted">검수하지 않은 응답은 정답으로 간주하지 않습니다. 센서·날씨는 AI 추론과 별도입니다. 토큰 수·연산 장치·정확한 에너지 소모량은 제공되지 않으면 확인 불가입니다.</p>
  </article>;
}

function Journal({ sessionId, profileId }: { sessionId: string; profileId: number }) {
  const [selected, setSelected] = useState<string | null>(null);
  const query = useInfiniteQuery({ queryKey: ['on-device-ai', profileId, sessionId], initialPageParam: '',
    queryFn: ({ pageParam }) => onDeviceAiApi.list(sessionId, profileId, pageParam),
    getNextPageParam: last => last.next_cursor || undefined, retry: false, staleTime: 30_000 });
  const files = [...new Set(query.data?.pages.flatMap(page => page.files) ?? [])];
  const current = selected && files.includes(selected) ? selected : files.find(name => name.endsWith('.json'));
  return <div className="mt-4 space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-2"><p className="text-sm text-text-muted">업로드된 기록 {files.length}개{query.hasNextPage ? ' 이상' : ''}</p><button className={buttonClass} disabled={query.isFetching} onClick={() => void query.refetch()}>새로고침</button></div>
    {query.isPending && <p role="status">업로드 기록을 불러오는 중…</p>}
    {query.isError && <p role="alert" className="text-sm text-amber-400">{query.error instanceof ApiError && query.error.status === 404 ? '서버에 조회 기능이 아직 배포되지 않았습니다.' : '목록을 불러오지 못했습니다. 새로고침으로 다시 시도해주세요.'}</p>}
    {query.isSuccess && !files.length && <p className="text-sm text-text-muted">업로드된 온디바이스 AI 기록이 없습니다. 아이폰에서 해당 기능을 켜고 녹화를 마친 뒤 업로드가 완료되어야 표시됩니다.</p>}
    {files.length > 0 && <div className="grid gap-4 xl:grid-cols-[240px_minmax(0,1fr)]">
      <nav aria-label="온디바이스 AI 기록 선택" className="max-h-96 space-y-2 overflow-auto xl:max-h-[640px]">
        {files.map((name, i) => name.endsWith('.ndjson') ? <a className="block rounded-lg border border-border p-3 text-sm text-accent" key={name} target="_blank" rel="noreferrer" href={`/api/v1${onDeviceAssetPath(sessionId, profileId, name)}`}>{fileLabel(name)} · 원본 열기</a> :
          <button key={name} aria-pressed={current === name} className={`block w-full rounded-lg border p-3 text-left text-sm ${current === name ? 'border-accent bg-bg-tertiary text-accent' : 'border-border text-text-secondary'}`} onClick={() => setSelected(name)}><span>{fileLabel(name)} · {i + 1}</span><span className="mt-1 block break-all text-[10px] text-text-muted">{name}</span></button>)}
        {query.hasNextPage && <button className={buttonClass} disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>기록 더 보기</button>}
      </nav>
      {current && <RecordDetail key={current} sessionId={sessionId} profileId={profileId} filename={current} />}
    </div>}
  </div>;
}

export function OnDeviceAiPanel({ sessionId, profileId }: { sessionId: string; profileId: number }) {
  const [open, setOpen] = useState(false);
  return <section className="mt-6 min-w-0 rounded-xl border border-border bg-bg-elevated p-4" aria-label="온디바이스 AI 관찰 기록">
    <div className="flex flex-wrap items-center justify-between gap-3"><div><h2 className="font-semibold text-text-primary">온디바이스 AI 관찰 기록</h2><p className="mt-1 text-sm text-text-muted">아이폰의 자세 분석 · 환경·행동 관찰 · 입력 근거</p></div>
      <button className={buttonClass} aria-expanded={open} onClick={() => setOpen(!open)}>{open ? '기록 접기' : '기록 확인'}</button></div>
    {open && <Journal key={`${profileId}:${sessionId}`} sessionId={sessionId} profileId={profileId} />}
  </section>;
}
