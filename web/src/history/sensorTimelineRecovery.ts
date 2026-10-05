import type { QueryClient } from '@tanstack/react-query';
import type { SensorTimelineResponse } from '../api/history';

export interface SensorMetadataRefresh {
  sessionKey?: string;
  signature?: string;
  pending?: Promise<void>;
  failed?: boolean;
}

/** Refresh once per verified mapping/source, including the final poll that stops waiting. */
export async function refreshSensorVideoMetadata(
  client: QueryClient,
  response: SensorTimelineResponse,
  sessionId: string,
  profileId: number,
  refresh: SensorMetadataRefresh,
): Promise<void> {
  if (!response.timeline || !['completed', 'limited'].includes(response.status) ||
      response.video_mapping.segments.length === 0) return;
  const sessionKey = `${sessionId}:${profileId}`;
  const signature = JSON.stringify([response.timeline.source, response.video_mapping.segments]);
  if (refresh.sessionKey === sessionKey && refresh.signature === signature) {
    await refresh.pending;
    return;
  }
  refresh.sessionKey = sessionKey;
  refresh.signature = signature;
  refresh.failed = false;
  const pending = Promise.all([
    client.refetchQueries({ queryKey: ['chunks', sessionId], type: 'active' }, { cancelRefetch: false, throwOnError: true }),
    client.refetchQueries({ queryKey: ['session-analysis', sessionId], type: 'active' }, { cancelRefetch: false, throwOnError: true }),
  ]).then(() => undefined);
  refresh.pending = pending;
  try {
    await pending;
  } catch (error) {
    // A failed metadata request must remain eligible for an explicit retry.
    if (refresh.pending === pending) {
      refresh.signature = undefined;
      refresh.failed = true;
    }
    throw error;
  } finally {
    if (refresh.pending === pending) refresh.pending = undefined;
  }
}
