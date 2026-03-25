import { Directory, File, Paths } from "expo-file-system";
import * as MediaLibrary from "expo-media-library";
import { useCallback, useState } from "react";

import { useVideoQueue } from "./useVideoQueue";

// ==========================================
// Types
// ==========================================

export interface OrphanedFile {
  path: string;
  name: string;
  size: number; // bytes
}

// ==========================================
// Hook
// ==========================================

/**
 * Scans the app's cache directory for .mp4/.mov files
 * that are NOT tracked by the video queue.
 */
export function useOrphanedVideos() {
  const [orphans, setOrphans] = useState<OrphanedFile[]>([]);
  const [scanning, setScanning] = useState(false);

  const scan = useCallback(async () => {
    setScanning(true);
    try {
      const items = useVideoQueue.getState().items;
      const trackedPaths = new Set(
        items.flatMap((item) =>
          [item.rawUri, item.compressedUri].filter(Boolean) as string[]
        )
      );

      const cacheDir = new Directory(Paths.cache);
      const found: OrphanedFile[] = [];

      if (cacheDir.exists) {
        for (const entry of cacheDir.list()) {
          if (!(entry instanceof File)) continue;
          if (!/\.(mp4|mov)$/i.test(entry.name ?? "")) continue;

          const fullPath = `${Paths.cache}/${entry.name}`;
          if (trackedPaths.has(fullPath)) continue;

          found.push({
            path: fullPath,
            name: entry.name ?? "unknown",
            size: entry.size ?? 0,
          });
        }
      }

      // Sort by name (newest first since filenames contain timestamps)
      found.sort((a, b) => b.name.localeCompare(a.name));
      setOrphans(found);
    } catch (e) {
      console.warn("⚠️ Orphan scan failed:", e);
    } finally {
      setScanning(false);
    }
  }, []);

  const deleteFile = useCallback(
    async (path: string) => {
      try {
        const f = new File(path);
        if (f.exists) f.delete();
        setOrphans((prev) => prev.filter((o) => o.path !== path));
      } catch (e) {
        console.warn("⚠️ Failed to delete orphan:", path, e);
      }
    },
    []
  );

  const saveToGallery = useCallback(
    async (path: string): Promise<boolean> => {
      try {
        await MediaLibrary.saveToLibraryAsync(path);
        // Delete temp file after successful gallery save
        try {
          const f = new File(path);
          if (f.exists) f.delete();
        } catch (_) {}
        setOrphans((prev) => prev.filter((o) => o.path !== path));
        return true;
      } catch (e) {
        console.warn("⚠️ Failed to save orphan to gallery:", path, e);
        return false;
      }
    },
    []
  );

  const deleteAll = useCallback(async () => {
    for (const orphan of orphans) {
      try {
        const f = new File(orphan.path);
        if (f.exists) f.delete();
      } catch (_) {}
    }
    setOrphans([]);
  }, [orphans]);

  return { orphans, scanning, scan, deleteFile, saveToGallery, deleteAll };
}
