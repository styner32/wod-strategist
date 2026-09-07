import * as FileSystem from "expo-file-system/legacy";

export type VideoKind = "merged" | "hardsubbed" | "encoded";

export interface CachedVideoItem {
  sessionId: string;
  kind: VideoKind;
  filename: string;
  uri: string;
  sizeBytes: number;
  modifiedTime: number; // Unix timestamp in ms
}

export interface VideoCacheStats {
  count: number;
  totalSizeBytes: number;
  formattedSize: string;
}

export const DEFAULT_MAX_CACHE_BYTES = 1.5 * 1024 * 1024 * 1024; // 1.5 GB
export const DEFAULT_MAX_CACHE_AGE_DAYS = 14;

/**
 * Format bytes to human readable string (B, KB, MB, GB).
 */
export function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  if (bytes >= 1024 * 1024 * 1024)
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

/**
 * Returns the dedicated directory where workout session videos are cached locally.
 */
export function getVideoCacheDir(): string {
  const base = FileSystem.cacheDirectory ?? "";
  return base.endsWith("/") ? `${base}video_cache/` : `${base}/video_cache/`;
}

/**
 * Sanitizes sessionId for safe filesystem use.
 */
function sanitizeSessionId(sessionId: string): string {
  return sessionId.replace(/[^a-zA-Z0-9_-]/g, "_");
}

/**
 * Filename format: {sessionId}_{kind}.mp4
 */
export function getVideoCacheFilename(
  sessionId: string,
  kind: VideoKind = "merged",
): string {
  return `${sanitizeSessionId(sessionId)}_${kind}.mp4`;
}

/**
 * Full file URI for a cached session video.
 */
export function getVideoCacheUri(
  sessionId: string,
  kind: VideoKind = "merged",
): string {
  return `${getVideoCacheDir()}${getVideoCacheFilename(sessionId, kind)}`;
}

/**
 * Temporary file URI while downloading.
 */
function getVideoTempDownloadUri(
  sessionId: string,
  kind: VideoKind = "merged",
): string {
  return `${getVideoCacheDir()}${sanitizeSessionId(sessionId)}_${kind}.download`;
}

/**
 * Ensures the video cache directory exists.
 */
export async function ensureCacheDirExists(): Promise<void> {
  const dir = getVideoCacheDir();
  try {
    const info = await FileSystem.getInfoAsync(dir);
    if (!info.exists) {
      await FileSystem.makeDirectoryAsync(dir, { intermediates: true });
    }
  } catch (e) {
    console.warn("[videoCacheManager] Failed to ensure cache directory:", e);
  }
}

/**
 * Checks if a session video is already fully cached and non-empty.
 */
export async function isSessionVideoCached(
  sessionId: string,
  kind: VideoKind = "merged",
): Promise<boolean> {
  try {
    const fileUri = getVideoCacheUri(sessionId, kind);
    const info = await FileSystem.getInfoAsync(fileUri);
    return Boolean(info.exists && (info.size ?? 0) > 0);
  } catch {
    return false;
  }
}

/**
 * Returns the local file URI if the video is already fully cached, or null otherwise.
 */
export async function getCachedVideoUri(
  sessionId: string,
  kind: VideoKind = "merged",
): Promise<string | null> {
  const isCached = await isSessionVideoCached(sessionId, kind);
  if (!isCached) return null;
  return getVideoCacheUri(sessionId, kind);
}

export interface DownloadTaskHandle {
  cancel: () => Promise<void>;
  promise: Promise<string | null>;
}

/**
 * Starts a progressive background download for a session video into the local cache.
 *
 * - Downloads to a .download temp file first.
 * - On 100% completion, atomically renames to .mp4.
 * - Automatically triggers LRU cache pruning in background.
 * - Provides a cancel() method to abort download if the user leaves early.
 */
export function startProgressiveVideoDownload(
  sessionId: string,
  kind: VideoKind = "merged",
  remoteUrl: string,
  onProgress?: (fraction: number) => void,
): DownloadTaskHandle {
  let isCancelled = false;
  let downloadResumable: FileSystem.DownloadResumable | null = null;
  const tempUri = getVideoTempDownloadUri(sessionId, kind);
  const targetUri = getVideoCacheUri(sessionId, kind);

  const promise = (async (): Promise<string | null> => {
    try {
      // If already cached, do not re-download
      const alreadyCached = await isSessionVideoCached(sessionId, kind);
      if (alreadyCached) {
        return targetUri;
      }

      await ensureCacheDirExists();

      // Clean up stale partial download if exists
      try {
        await FileSystem.deleteAsync(tempUri, { idempotent: true });
      } catch {}

      const progressCallback: FileSystem.DownloadProgressCallback = (data) => {
        if (isCancelled) return;
        if (data.totalBytesExpectedToWrite > 0) {
          const fraction =
            data.totalBytesWritten / data.totalBytesExpectedToWrite;
          onProgress?.(Math.min(1, Math.max(0, fraction)));
        }
      };

      downloadResumable = FileSystem.createDownloadResumable(
        remoteUrl,
        tempUri,
        {},
        progressCallback,
      );

      const result = await downloadResumable.downloadAsync();
      if (
        isCancelled ||
        !result ||
        result.status < 200 ||
        result.status >= 300
      ) {
        try {
          await FileSystem.deleteAsync(tempUri, { idempotent: true });
        } catch {}
        return null;
      }

      // Verify temp file has size > 0
      const tempInfo = await FileSystem.getInfoAsync(tempUri);
      if (!tempInfo.exists || (tempInfo.size ?? 0) === 0) {
        try {
          await FileSystem.deleteAsync(tempUri, { idempotent: true });
        } catch {}
        return null;
      }

      // Atomically move tempUri to targetUri
      await FileSystem.moveAsync({
        from: tempUri,
        to: targetUri,
      });

      // Trigger LRU cache pruning in background
      pruneVideoCacheIfNeeded().catch((e) => {
        console.warn("[videoCacheManager] Background cache pruning failed:", e);
      });

      return targetUri;
    } catch (e: any) {
      if (!isCancelled) {
        console.warn(
          "[videoCacheManager] Download failed for session:",
          sessionId,
          e?.message,
        );
      }
      try {
        await FileSystem.deleteAsync(tempUri, { idempotent: true });
      } catch {}
      return null;
    }
  })();

  const cancel = async (): Promise<void> => {
    isCancelled = true;
    if (downloadResumable) {
      try {
        await downloadResumable.cancelAsync();
      } catch {}
    }
    try {
      await FileSystem.deleteAsync(tempUri, { idempotent: true });
    } catch {}
  };

  return { cancel, promise };
}

/**
 * Lists all cached video items with their file size and modified timestamp.
 */
export async function listCachedVideos(): Promise<CachedVideoItem[]> {
  const dir = getVideoCacheDir();
  try {
    const dirInfo = await FileSystem.getInfoAsync(dir);
    if (!dirInfo.exists) {
      return [];
    }

    const files = await FileSystem.readDirectoryAsync(dir);
    const items: CachedVideoItem[] = [];

    for (const filename of files) {
      // Only match completed .mp4 video cache files
      const match = filename.match(/^(.+)_(merged|hardsubbed|encoded)\.mp4$/);
      if (!match) continue;

      const fileUri = `${dir}${filename}`;
      const info = await FileSystem.getInfoAsync(fileUri);
      if (info.exists && !info.isDirectory) {
        items.push({
          sessionId: match[1],
          kind: match[2] as VideoKind,
          filename,
          uri: fileUri,
          sizeBytes: info.size ?? 0,
          modifiedTime: (info.modificationTime ?? 0) * 1000,
        });
      }
    }

    // Sort by modified time descending (newest first)
    items.sort((a, b) => b.modifiedTime - a.modifiedTime);
    return items;
  } catch (e) {
    console.warn("[videoCacheManager] Failed to list cached videos:", e);
    return [];
  }
}

/**
 * Returns overall statistics for cached videos.
 */
export async function getVideoCacheStats(): Promise<VideoCacheStats> {
  const items = await listCachedVideos();
  const totalSizeBytes = items.reduce((sum, item) => sum + item.sizeBytes, 0);
  return {
    count: items.length,
    totalSizeBytes,
    formattedSize: formatBytes(totalSizeBytes),
  };
}

/**
 * Deletes a specific session video from cache.
 */
export async function deleteCachedVideo(
  sessionId: string,
  kind: VideoKind = "merged",
): Promise<boolean> {
  const uri = getVideoCacheUri(sessionId, kind);
  try {
    const info = await FileSystem.getInfoAsync(uri);
    if (info.exists) {
      await FileSystem.deleteAsync(uri, { idempotent: true });
      return true;
    }
  } catch (e) {
    console.warn("[videoCacheManager] Failed to delete cached video:", uri, e);
  }
  return false;
}

/**
 * Clears all cached videos and temporary download files.
 * Returns the number of bytes freed.
 */
export async function clearAllVideoCache(): Promise<number> {
  const dir = getVideoCacheDir();
  try {
    const dirInfo = await FileSystem.getInfoAsync(dir);
    if (!dirInfo.exists) return 0;

    const stats = await getVideoCacheStats();
    await FileSystem.deleteAsync(dir, { idempotent: true });
    await ensureCacheDirExists();
    return stats.totalSizeBytes;
  } catch (e) {
    console.warn("[videoCacheManager] Failed to clear video cache:", e);
    return 0;
  }
}

/**
 * Enforces cache storage limits using LRU (Least Recently Used / oldest first).
 *
 * If total cache exceeds maxBytes or videos are older than maxAgeDays,
 * deletes the oldest files until cache is within headroom limit (80% of maxBytes).
 */
export async function pruneVideoCacheIfNeeded(
  maxBytes: number = DEFAULT_MAX_CACHE_BYTES,
  maxAgeDays: number = DEFAULT_MAX_CACHE_AGE_DAYS,
): Promise<number> {
  try {
    const items = await listCachedVideos();
    if (items.length === 0) return 0;

    let totalSize = items.reduce((acc, i) => acc + i.sizeBytes, 0);
    const now = Date.now();
    const maxAgeMs = maxAgeDays * 24 * 60 * 60 * 1000;

    // Sort ascending by modifiedTime (oldest first) for LRU eviction
    const sortedOldestFirst = [...items].sort(
      (a, b) => a.modifiedTime - b.modifiedTime,
    );
    const targetSize = maxBytes * 0.8; // Retain 20% headroom
    let sizePruningStarted = false;
    let prunedCount = 0;

    for (const item of sortedOldestFirst) {
      const isExpired = maxAgeMs > 0 && now - item.modifiedTime > maxAgeMs;
      const isOverSize =
        totalSize > maxBytes || (sizePruningStarted && totalSize > targetSize);

      if (isExpired || isOverSize) {
        await FileSystem.deleteAsync(item.uri, { idempotent: true });
        totalSize -= item.sizeBytes;
        prunedCount++;
        if (isOverSize) {
          sizePruningStarted = true;
        }
      }
    }

    // Also clean up any lingering .download temp files older than 1 hour
    const dir = getVideoCacheDir();
    const allFiles = await FileSystem.readDirectoryAsync(dir).catch(() => []);
    for (const file of allFiles) {
      if (file.endsWith(".download")) {
        const fileUri = `${dir}${file}`;
        const info = await FileSystem.getInfoAsync(fileUri).catch(() => null);
        if (
          info &&
          info.exists &&
          now - (info.modificationTime ?? 0) * 1000 > 3600 * 1000
        ) {
          await FileSystem.deleteAsync(fileUri, { idempotent: true }).catch(
            () => {},
          );
        }
      }
    }

    return prunedCount;
  } catch (e) {
    console.warn("[videoCacheManager] pruneVideoCacheIfNeeded error:", e);
    return 0;
  }
}
