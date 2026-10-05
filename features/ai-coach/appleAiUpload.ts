import {
  documentDirectory, copyAsync, deleteAsync, getInfoAsync, makeDirectoryAsync,
  moveAsync, readAsStringAsync, readDirectoryAsync, writeAsStringAsync,
} from 'expo-file-system/legacy';
import { ulid } from 'ulid';
import { getUploadUrl, uploadSessionAssetToGcs } from '../wod/api';
import type { AppleAiObservation } from './appleAiObservation';
import type { AppleAiPreparation } from '../../modules/apple-on-device-ai';

interface ArchivedFrame {
  filename: string;
  capturedAt: number;
  captureOffsetMs: number;
  objectName: string;
}

export interface AppleAiArchive {
  schemaVersion: 1 | 2;
  id: string;
  profileId: number;
  sessionId: string;
  sessionStartedAt: number;
  model: 'SystemLanguageModel.default';
  promptVersion: 1 | 2 | 3;
  imageTransform: string;
  clock: 'capture_epoch_ms_not_merged_video_time';
  requestId: string;
  startedAt: number;
  completedAt: number;
  inferenceStartedAt?: number;
  outcome: AppleAiObservation['outcome'];
  error: string | null;
  answer: AppleAiObservation['answer'];
  context: { wodDescription: string; movements: string; appearanceHints?: string; language: string };
  frames: ArchivedFrame[];
  preparation?: Omit<AppleAiPreparation, 'frames'> & {
    frames: (ArchivedFrame & { sourceIndex: number; width: number; height: number })[];
  };
}

const root = () => `${documentDirectory}apple-ai/`;
const isBundleId = (id: string) => /^[0-9A-HJKMNP-TV-Z]{26}$/.test(id);
const manifestName = (id: string) => `apple_ai_${id}.json`;
const frameName = (id: string, index: number) => `apple_ai_${id}_frame_${index + 1}.jpg`;
const inputName = (id: string, index: number) => `apple_ai_${id}_input_${index + 1}.jpg`;

/** Each committed directory is its own durable queue entry; no mutable queue index. */
export async function saveAppleAiObservation(
  observation: AppleAiObservation, profileId: number, sessionStartedAt: number,
): Promise<void> {
  if (!documentDirectory || !Number.isSafeInteger(profileId) || profileId <= 0 ||
      !Number.isFinite(sessionStartedAt) || sessionStartedAt <= 0 || !observation.sessionId) {
    throw new Error('Missing Apple AI recording identity');
  }
  const id = ulid();
  const dir = `${root()}${id}/`;
  const prefix = `videos/${profileId}/${observation.sessionId}/`;
  await makeDirectoryAsync(dir, { intermediates: true });
  const frames: ArchivedFrame[] = [];
  for (const [index, frame] of observation.request.frames.entries()) {
    const filename = frameName(id, index);
    await copyAsync({ from: frame.path.startsWith('file://') ? frame.path : `file://${frame.path}`, to: dir + filename });
    frames.push({ filename, capturedAt: frame.capturedAt,
      captureOffsetMs: frame.capturedAt - sessionStartedAt, objectName: prefix + filename });
  }
  let preparation: AppleAiArchive['preparation'];
  if (observation.preparation) {
    preparation = { ...observation.preparation, frames: [] };
    for (const [index, frame] of observation.preparation.frames.entries()) {
      const filename = inputName(id, index);
      await copyAsync({ from: frame.path.startsWith('file://') ? frame.path : `file://${frame.path}`, to: dir + filename });
      preparation.frames.push({ filename, capturedAt: frame.capturedAt, captureOffsetMs: frame.capturedAt - sessionStartedAt,
        objectName: prefix + filename, sourceIndex: frame.sourceIndex, width: frame.width, height: frame.height });
    }
  }
  const archive: AppleAiArchive = {
    schemaVersion: 2, id, profileId, sessionId: observation.sessionId, sessionStartedAt,
    model: 'SystemLanguageModel.default', promptVersion: 3,
    imageTransform: 'EXIF orientation, maximum 768px before shared context crop, no upscaling; exact JPEG inputs archived',
    clock: 'capture_epoch_ms_not_merged_video_time',
    requestId: observation.request.requestId, startedAt: observation.startedAt,
    completedAt: observation.completedAt, outcome: observation.outcome,
    inferenceStartedAt: observation.inferenceStartedAt,
    error: observation.error, answer: observation.answer,
    context: { wodDescription: observation.request.wodDescription,
      movements: observation.request.movements, appearanceHints: observation.request.appearanceHints,
      language: observation.request.language }, frames, preparation,
  };
  await writeAsStringAsync(dir + 'manifest.tmp', JSON.stringify(archive, null, 2));
  // Only a completely saved bundle is visible to the uploader.
  await moveAsync({ from: dir + 'manifest.tmp', to: dir + 'manifest.json' });
}

let flushing: Promise<void> | null = null;
let flushAgain = false;

/** Retry at recording stop, login or foreground. Failed bundles remain on disk. */
export function flushAppleAiUploads(): Promise<void> {
  if (!documentDirectory) return Promise.resolve();
  flushAgain = true;
  if (flushing) return flushing;
  flushing = (async () => {
    do {
      flushAgain = false;
      if (!(await getInfoAsync(root())).exists) return;
      const ids = (await readDirectoryAsync(root())).filter(isBundleId).sort();
      for (const id of ids) {
        const dir = `${root()}${id}/`;
        try {
          // A crash after remote commit must not re-upload partially deleted files.
          if ((await getInfoAsync(dir + 'uploaded.json')).exists) {
            await deleteAsync(dir, { idempotent: true });
            continue;
          }
          if (!(await getInfoAsync(dir + 'manifest.json')).exists) continue;
          const archive: AppleAiArchive = JSON.parse(await readAsStringAsync(dir + 'manifest.json'));
          if (![1, 2].includes(archive.schemaVersion) || archive.id !== id ||
              !Number.isSafeInteger(archive.profileId) || archive.profileId <= 0 ||
              !Array.isArray(archive.frames) || archive.frames.length > (archive.schemaVersion === 1 ? 3 : 6)) {
            throw new Error('Invalid Apple AI archive');
          }
          const inputs = archive.preparation?.frames ?? [];
          if (archive.preparation && (archive.schemaVersion !== 2 || !Array.isArray(inputs) || inputs.length !== 3)) {
            throw new Error('Invalid Apple AI prepared inputs');
          }
          for (const [index, input] of inputs.entries()) {
            if (input.filename !== inputName(id, index) || !Number.isInteger(input.sourceIndex) ||
                input.sourceIndex < 0 || !archive.frames[input.sourceIndex] ||
                input.capturedAt !== archive.frames[input.sourceIndex].capturedAt ||
                (index > 0 && input.sourceIndex <= inputs[index - 1].sourceIndex)) {
              throw new Error('Invalid Apple AI input reference');
            }
          }
          const upload = async (filename: string, path: string, mime: string) => {
            // Existing API checks profile ownership and signs the session-scoped path.
            const { upload_url } = await getUploadUrl(archive.sessionId, filename, archive.profileId);
            if (!upload_url) throw new Error('Missing Apple AI upload URL');
            await uploadSessionAssetToGcs(upload_url, path, mime);
          };
          for (const [index, frame] of archive.frames.entries()) {
            if (frame.filename !== frameName(id, index)) throw new Error('Invalid Apple AI frame filename');
            await upload(frame.filename, dir + frame.filename, 'image/jpeg');
          }
          for (const input of inputs) await upload(input.filename, dir + input.filename, 'image/jpeg');
          // Publish the manifest last, so it never points at images still awaiting upload.
          await upload(manifestName(id), dir + 'manifest.json', 'application/json');
          await moveAsync({ from: dir + 'manifest.json', to: dir + 'uploaded.json' });
          console.info('Apple AI evidence uploaded:', archive.sessionId, manifestName(id));
          await deleteAsync(dir, { idempotent: true });
        } catch (error) {
          console.warn('Apple AI upload retained for retry:', id, error);
        }
      }
    } while (flushAgain);
  })().finally(() => { flushing = null; });
  return flushing;
}
