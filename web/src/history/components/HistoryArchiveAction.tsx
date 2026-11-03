import { useMutation, useQueryClient } from "@tanstack/react-query";
import { historyApi, type AnalysisResult } from "../../api/history";
import { cancelArchiveQueries, refreshArchiveQueries } from "../historyArchive";

const TERMINAL_STATUSES = new Set(["COMPLETED", "FAILED", "CANCELLED"]);
const ACTIVE_ENRICHMENT_STATUSES = new Set(["pending", "preparing", "running"]);

function isSessionProcessing(result: AnalysisResult): boolean {
  const status = (result.status || "").trim().toUpperCase();
  if (!TERMINAL_STATUSES.has(status)) return true;
  if (result.sensor_state === "PENDING" || result.sensor_state === "RUNNING") return true;
  if (result.analysis_summary?.status && ACTIVE_ENRICHMENT_STATUSES.has(result.analysis_summary.status)) return true;
  return false;
}

export function HistoryArchiveAction({ result }: { result: AnalysisResult }) {
  const client = useQueryClient();
  const archived = Boolean(result.archived_at);
  const processing = !archived && isSessionProcessing(result);
  const mutation = useMutation({
    mutationFn: (nextArchived: boolean) => nextArchived ? historyApi.archive(result.id) : historyApi.unarchive(result.id),
    onMutate: () => cancelArchiveQueries(client),
    onSuccess: (_response, nextArchived) => refreshArchiveQueries(client, result, nextArchived),
  });
  const submit = () => {
    if (processing) return;
    if (!archived && !window.confirm("이 운동 기록을 보관하시겠습니까?\n\n피로도·훈련 부하·과거 운동 비교에서 제외합니다. 영상과 저장된 결과, 스트레칭은 보존합니다.")) return;
    mutation.mutate(!archived);
  };
  const conflictError = !archived && (mutation.error as { status?: number } | null)?.status === 409;
  return <div className="shrink-0">
    <button type="button" onClick={submit} disabled={mutation.isPending || processing}
      title={processing ? "분석이 완료된 후 보관할 수 있습니다." : undefined}
      aria-label={`${result.session_id} ${archived ? "복원" : "보관"}`}
      className="rounded-lg border border-border bg-bg-secondary px-3 py-2 text-sm font-medium text-text-secondary hover:bg-bg-tertiary disabled:opacity-50">
      {mutation.isPending ? "처리 중…" : archived ? "복원" : processing ? "분석 중" : "보관"}
    </button>
    {mutation.isError && <p role="alert" className="mt-2 max-w-sm text-xs text-error">
      {conflictError ? "진행 중인 분석이 완료된 후 보관할 수 있습니다." : `${archived ? "복원" : "보관"}하지 못했습니다. 다시 시도해주세요.`}
    </p>}
  </div>;
}
