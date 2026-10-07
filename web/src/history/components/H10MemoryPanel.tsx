import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ApiError } from '../../api/client';
import { onDeviceAiApi } from '../../api/onDeviceAi';
import { H10_MEMORY_FILENAME, summarizeH10Memory } from '../h10MemoryRecord';

const buttonClass = 'rounded-lg border border-border px-3 py-2 text-sm text-accent hover:bg-bg-tertiary disabled:opacity-50';
const statusLabel: Record<string, string> = { complete: '회수 완료', error: '오류', recording: '미완료(기록 중 종료)' };
const seconds = (ms: number | null) => (ms === null ? '확인 불가' : `${(ms / 1000).toFixed(2)}초`);
const orNone = (value: number | string | null) => (value === null || value === '' ? '기록 없음' : String(value));

function Row({ label, value }: { label: string; value: string }) {
  return <div className="flex min-w-0 gap-3"><dt className="w-36 shrink-0 text-text-muted">{label}</dt><dd className="min-w-0 break-all">{value}</dd></div>;
}

function H10MemoryDetail({ sessionId, profileId }: { sessionId: string; profileId: number }) {
  const query = useQuery({ queryKey: ['h10-memory', profileId, sessionId],
    queryFn: () => onDeviceAiApi.read(sessionId, profileId, H10_MEMORY_FILENAME), retry: false, staleTime: 60_000 });
  if (query.isPending) return <p role="status" className="mt-3 text-sm">기록을 불러오는 중…</p>;
  if (query.isError) {
    if (query.error instanceof ApiError && query.error.status === 404) {
      return <p className="mt-3 text-sm text-text-muted">이 세션에는 H10 내부 저장 기록이 없습니다. (토글 OFF였거나 아직 업로드되지 않음)</p>;
    }
    return <div role="alert" className="mt-3 text-sm"><p>기록을 읽을 수 없습니다.</p><p className="text-xs text-text-muted">{query.error.message}</p>
      <button className={buttonClass} onClick={() => void query.refetch()}>다시 불러오기</button></div>;
  }
  const s = summarizeH10Memory(query.data);
  return <div className="mt-3 min-w-0 space-y-3 text-sm">
    <dl className="space-y-1">
      <Row label="상태" value={statusLabel[s.status] ?? s.status} />
      <Row label="심박 샘플" value={`${s.sampleCount}개 · 간격 ${s.intervalMs === null ? '미기록' : `${s.intervalMs}ms`} · 길이 ${seconds(s.durationMs)}`} />
      <Row label="심박 범위" value={s.min === null ? '유효 샘플 없음' : `최소 ${s.min} · 최대 ${s.max} · 평균 ${s.avg} bpm`} />
      <Row label="0 값 샘플" value={`${s.zeroCount}개`} />
      <Row label="센서 오프라인 구간" value={s.offlineRanges.length ? s.offlineRanges.map(r => `${r.start}–${r.stop}`).join(', ') : '없음'} />
      <Row label="시작 시점" value={`운동 시작 기준 +${seconds(s.startAckOffsetMs)} (명령 왕복 ${seconds(s.startRoundTripMs)})`} />
      <Row label="종료 전 기록 상태" value={s.recordingOnBeforeStop === null ? '확인 불가' : s.recordingOnBeforeStop ? '기록 중이었음' : '이미 멈춰 있었음'} />
      <Row label="회수" value={`${orNone(s.entryPath)} · ${s.bytes === null ? '-' : `${s.bytes}B`} · ${seconds(s.fetchMs)} · 시도 ${orNone(s.attempts)}회`} />
      <Row label="센서 파일 목록" value={s.entries.length ? s.entries.join(', ') : '없음'} />
      <Row label="센서에서 삭제" value={s.sensorRemoved ? '삭제됨' : '삭제 안 됨'} />
      <Row label="기기 · 전송" value={`${orNone(s.deviceName)} · ${orNone(s.transport)}`} />
      {s.recoveredLater && <Row label="복구" value="다음 세션 시작 시 회수됨" />}
      {s.leftovers.length > 0 && <Row label="이전 잔여 기록" value={s.leftovers.join(', ')} />}
    </dl>
    {s.errors.length > 0 && <div className="rounded-lg border border-red-500/40 p-3">
      <h3 className="font-medium text-red-400">오류 {s.errors.length}건</h3>
      <ul className="mt-1 space-y-1">{s.errors.map((e, i) => <li key={i} className="break-all">
        <span className="font-mono">{e.stage} · {e.code}</span> <span className="text-text-muted">{e.message}</span>
        {e.atEpochMs !== null && <span className="text-xs text-text-muted"> ({new Date(e.atEpochMs).toLocaleString('ko-KR')})</span>}
      </li>)}</ul>
    </div>}
    <details className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">원본 JSON</summary>
      <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words text-xs text-text-secondary">{JSON.stringify(query.data ?? null, null, 2)}</pre>
    </details>
  </div>;
}

/** Phase 1: shows what the experimental H10 internal recording uploaded; no timeline merge. */
export function H10MemoryPanel({ sessionId, profileId }: { sessionId: string; profileId: number }) {
  const [open, setOpen] = useState(false);
  return <section className="mt-6 min-w-0 rounded-xl border border-border bg-bg-elevated p-4" aria-label="H10 내부 저장 심박 기록">
    <div className="flex flex-wrap items-center justify-between gap-3"><div><h2 className="font-semibold text-text-primary">H10 내부 저장 심박 (실험)</h2>
      <p className="mt-1 text-sm text-text-muted">Polar H10 메모리에서 회수한 심박 원본 · 그래프에는 아직 반영하지 않음</p></div>
      <button className={buttonClass} aria-expanded={open} onClick={() => setOpen(!open)}>{open ? '기록 접기' : '기록 확인'}</button></div>
    {open && <H10MemoryDetail sessionId={sessionId} profileId={profileId} />}
  </section>;
}
