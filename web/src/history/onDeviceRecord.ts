import { checkObservation } from '../../../features/environment/observation';
// Archives are experiment data: malformed/older records remain inspectable as raw JSON.
export const object = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
export const textValue = (value: unknown): string => typeof value === 'string' ? value : '';
export const numberValue = (value: unknown): number | null => typeof value === 'number' && Number.isFinite(value) ? value : null;
export const arrayValue = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
export const strings = (value: unknown) => arrayValue(value).filter((item): item is string => typeof item === 'string');
export const assetName = (value: unknown): string | null => typeof value === 'string' && /^(apple_ai_|environment_)[A-Za-z0-9_-]+\.(jpg|m4a|ndjson)$/.test(value) ? value : null;
export function recordView(value: unknown) {
  const record = object(value);
  const apple = record.schemaVersion === 1 || record.schemaVersion === 2;
  const session = !apple && typeof record.complete === 'boolean' && !record.kind;
  const prepared = arrayValue(object(record.preparation).frames);
  // Version 2 selected/cropped inputs are the actual model input, not all candidates.
  const inputs = apple ? (prepared.length ? prepared : arrayValue(record.frames)) : arrayValue(record.evidence);
  const frames = inputs.map(object).filter(item => assetName(item.filename));
  return {
    record, apple, session, frames,
    source: apple ? 'Apple Foundation Models' : textValue(record.source),
    response: apple ? textValue(object(record.answer).feedback) : textValue(record.raw),
    elapsedMs: apple ? numberValue(object(record.answer).elapsedMs) : numberValue(record.inferenceMs),
    reason: textValue(record.reason) || textValue(record.error) || textValue(object(record.answer).error),
    parsed: object(record.parsed),
    questionId: textValue(record.questionId),
    responseFormat: record.responseFormat === 'text' ? 'text' : textValue(record.validation) || 'not_applicable',
    quality: checkObservation(textValue(record.raw)),
    parentFilename: /^[0-9A-HJKMNP-TV-Z]{26}$/.test(textValue(record.parentId)) ? `environment_${textValue(record.parentId)}.json` : null,
  };
}

export function latestRecordReview(records: unknown[], targetId: string) {
  const latest = records.map(object).filter(r => r.outcome === 'success' && object(r.review).targetId === targetId)
    .sort((a,b) => (numberValue(b.startedAt) ?? 0) - (numberValue(a.startedAt) ?? 0) || textValue(b.id).localeCompare(textValue(a.id)))[0];
  return latest ? object(latest.review) : null;
}
