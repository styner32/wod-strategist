import type { HeartRateSummary } from "../../../../shared/heartRateSummary";
import { HEART_RATE_ZONE_COLORS, hrNumber, hrPercent } from "../../../../shared/heartRateSummary";
import ko from "../../../../features/i18n/locales/ko.json";

const labels: Record<string, string> = ko.heartRate;
const label = (key: string) => labels[key] ?? labels.unavailable;
const seconds = (n?: number) => hrNumber(n, ` ${labels.seconds}`);

export function HeartRateBadge({ summary }: { summary?: HeartRateSummary }) {
  return (
    <span className="inline-flex rounded-md bg-bg-secondary px-2 py-1 text-xs text-text-secondary">
      ♥ {label(summary?.status ?? "unavailable")}{summary?.calculation_version === 1 ? ` · ${labels.legacy}` : ""}
    </span>
  );
}

export function HeartRateSummaryPanel({ summary }: { summary?: HeartRateSummary }) {
  const rows = summary ? [
    [labels.average, hrNumber(summary.avg_bpm, " bpm")],
    [labels.peak, hrNumber(summary.peak_bpm, " bpm")],
    [labels.minimum, hrNumber(summary.min_bpm, " bpm")],
    [labels.coverage, hrPercent(summary.coverage)],
    [labels.valid, seconds(summary.valid_seconds)],
    [labels.excluded, seconds(summary.excluded_seconds)],
    [labels.unknown, seconds(summary.unknown_seconds)],
  ] : [];

  return (
    <section className="rounded-xl border border-border bg-bg-elevated p-5 mb-6" aria-label={labels.title}>
      <div className="flex flex-wrap items-center gap-3 mb-3">
        <h2 className="font-semibold text-text-primary">{summary?.device_name || labels.title}</h2>
        <HeartRateBadge summary={summary} />
      </div>
      {summary && summary.status !== "none" && (
        <>
          {summary.coverage !== undefined && (
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-4">
              {rows.map(([name, value]) => (
                <div key={name}>
                  <div className="text-xs text-text-secondary">{name}</div>
                  <div className="text-lg text-text-primary">{value}</div>
                </div>
              ))}
            </div>
          )}
          <p className="text-sm text-text-secondary">{label(summary.application_reason)}</p>
          {summary.cardio_delta !== undefined && (
            <>
              <p className="text-sm text-text-primary mt-2">
                {labels.cardio}: {hrNumber(summary.cardio_before)} → {hrNumber(summary.cardio_after)} (+{summary.cardio_delta})
              </p>
              <p className="text-xs text-text-secondary">{labels.cardioNote}</p>
            </>
          )}
          {summary.coverage !== undefined && (
            <div className="text-sm text-text-secondary space-y-2 mt-4">
              <p>{summary.contact_coverage ? `${labels.contactCoverage}: ${hrPercent(summary.contact_coverage)}` : labels.contactUnknown}</p>
              {Object.entries(summary.excluded_by_reason ?? {})
                .filter(([, time]) => time > 0)
                .map(([reason, time]) => <p key={reason}>{label(reason)}: {seconds(time)}</p>)}
              {!!summary.low_bpm_seconds && <p>{labels.lowDuration}: {seconds(summary.low_bpm_seconds)}</p>}
              {summary.zones?.length ? (
                <>
                  <h3 className="font-medium">{labels.zones}</h3>
                  <div className="flex h-4 rounded overflow-hidden bg-bg-secondary">
                    {summary.zones.map(z => (
                      <div key={z.zone} style={{ width: `${Math.max(0, z.ratio) * 100}%`, backgroundColor: HEART_RATE_ZONE_COLORS[z.zone - 1] }} />
                    ))}
                  </div>
                  <div className="flex flex-wrap gap-4">
                    {summary.zones.map(z => (
                      <span key={z.zone}>
                        <span aria-hidden="true" style={{ color: HEART_RATE_ZONE_COLORS[z.zone - 1] }}>● </span>
                        {labels.zone} {z.zone}: {seconds(z.seconds)} · {hrPercent(z.ratio)}
                      </span>
                    ))}
                  </div>
                  <p className="text-xs">
                    {labels.maxEstimate}: {hrNumber(summary.max_bpm)} bpm · {label(summary.max_bpm_source === "estimated_220_minus_age" ? "estimated_220_minus_age" : "unknownEstimate")}
                  </p>
                </>
              ) : summary.max_bpm === undefined ? <p>{labels.zoneUnavailable}</p> : null}
            </div>
          )}
        </>
      )}
    </section>
  );
}
