import { useMutation, useQueryClient } from "@tanstack/react-query";
import { historyApi, type AnalysisResult } from "../../api/history";
import { cancelArchiveQueries, refreshArchiveQueries } from "../historyArchive";

export function HistoryArchiveAction({ result }: { result: AnalysisResult }) {
  const client = useQueryClient();
  const archived = Boolean(result.archived_at);
  const mutation = useMutation({
    mutationFn: (nextArchived: boolean) => nextArchived ? historyApi.archive(result.id) : historyApi.unarchive(result.id),
    onMutate: () => cancelArchiveQueries(client),
    onSuccess: (_response, nextArchived) => refreshArchiveQueries(client, result, nextArchived),
  });
  const submit = () => {
    if (!archived && !window.confirm("이 운동 기록을 보관하시겠습니까?\n\n피로도·훈련 부하·과거 운동 비교에서 제외하고 진행 중인 분석을 중단합니다. 영상과 저장된 결과, 스트레칭은 보존합니다. 복원해도 중단한 분석은 자동 재개되지 않습니다.")) return;
    mutation.mutate(!archived);
  };
  return <div className="shrink-0">
    <button type="button" onClick={submit} disabled={mutation.isPending}
      aria-label={`${result.session_id} ${archived ? "복원" : "보관"}`}
      className="rounded-lg border border-border bg-bg-secondary px-3 py-2 text-sm font-medium text-text-secondary hover:bg-bg-tertiary disabled:opacity-50">
      {mutation.isPending ? "처리 중…" : archived ? "복원" : "보관"}
    </button>
    {mutation.isError && <p role="alert" className="mt-2 max-w-sm text-xs text-error">{archived ? "복원" : "보관"}하지 못했습니다. 다시 시도해주세요.</p>}
  </div>;
}
