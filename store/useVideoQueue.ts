import AsyncStorage from "@react-native-async-storage/async-storage";
import { File } from "expo-file-system";
import * as MediaLibrary from "expo-media-library";
import { Video } from "react-native-compressor";
import { Alert } from "react-native";
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";

import { processWorkoutVideo } from "@/features/wod/api";
import type { WorkoutType } from "@/features/wod/workoutType";

// ==========================================
// Types
// ==========================================

export type VideoStatus =
  | "RECORDED"   // raw file exists, ready to encode
  | "ENCODING"   // transient: encoding in progress
  | "ENCODED"    // compressed file exists, ready to upload
  | "UPLOADING"  // transient: upload in progress
  | "UPLOADED";  // upload complete, files can be cleaned up

export interface VideoItem {
  id: string;
  rawUri: string;
  compressedUri?: string;
  compressedSize?: number; // bytes
  sessionId: string;
  workoutType: WorkoutType;
  movements: string[];
  injuries: string[];
  status: VideoStatus;
  progress: number;
  error?: string;        // last error message (shown inline, cleared on next action)
  createdAt: number;
  profileId: number;
  gallerySaved: boolean;
}

export interface EnqueueMetadata {
  sessionId: string;
  workoutType: WorkoutType;
  movements: string[];
  injuries: string[];
  profileId: number;
}

interface VideoQueueState {
  items: VideoItem[];

  // Actions
  enqueue: (rawUri: string, metadata: EnqueueMetadata) => string;
  startEncoding: (id: string) => void;
  startUpload: (id: string) => void;
  cancelUpload: (id: string) => void;
  remove: (id: string) => void;
  dismiss: (id: string) => void;
  saveToGallery: (id: string) => Promise<boolean>;

  // Internal (used by actions)
  _updateItem: (id: string, patch: Partial<VideoItem>) => void;
}

// ==========================================
// Helpers
// ==========================================

function generateId(): string {
  return `vid_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

async function safeDelete(uri: string): Promise<void> {
  try {
    const file = new File(uri);
    if (file.exists) {
      file.delete();
      console.log("🗑️ Deleted temp file:", uri);
    }
  } catch (e) {
    console.warn("⚠️ Failed to delete temp file:", uri, e);
  }
}

// ==========================================
// Upload cancellation tracking
// ==========================================

/** Active upload cancel handles, keyed by item ID */
const activeUploadCancels = new Map<string, () => Promise<void>>();
/** Monotonic generation counter per item — incremented on each cancel/re-upload */
const uploadGeneration = new Map<string, number>();

// ==========================================
// Store
// ==========================================

export const useVideoQueue = create<VideoQueueState>()(persist((set, get) => ({
  items: [],

  _updateItem: (id, patch) => {
    set((state) => ({
      items: state.items.map((item) =>
        item.id === id ? { ...item, ...patch } : item
      ),
    }));
  },

  enqueue: (rawUri, metadata) => {
    const id = generateId();
    const item: VideoItem = {
      id,
      rawUri,
      compressedUri: undefined,
      sessionId: metadata.sessionId,
      workoutType: metadata.workoutType,
      movements: metadata.movements,
      injuries: metadata.injuries,
      status: "RECORDED",
      progress: 0,
      createdAt: Date.now(),
      profileId: metadata.profileId,
      gallerySaved: false,
    };

    set((state) => ({ items: [item, ...state.items] }));
    return id;
  },

  startEncoding: (id) => {
    const item = get().items.find((i) => i.id === id);
    console.log("🎬 [startEncoding] called", {
      id,
      found: !!item,
      status: item?.status,
      rawUri: item?.rawUri,
    });
    if (!item || (item.status !== "RECORDED" && item.status !== "ENCODED")) {
      console.warn("🎬 [startEncoding] Skipped — item not in RECORDED or ENCODED state");
      return;
    }

    // Validate raw file exists
    try {
      const rawFile = new File(item.rawUri);
      if (!rawFile.exists) {
        console.error("🎬 [startEncoding] Raw file does not exist:", item.rawUri);
        get()._updateItem(id, {
          error: "Original raw file is missing. Cannot encode.",
        });
        return;
      }
    } catch (e) {
      console.warn("🎬 [startEncoding] Could not check raw file:", e);
    }

    // Clean up old compressed file if re-encoding
    if (item.compressedUri) {
      safeDelete(item.compressedUri);
    }

    get()._updateItem(id, {
      status: "ENCODING",
      progress: 0,
      error: undefined,
      compressedUri: undefined,
      compressedSize: undefined,
    });
    _runEncoding(id, item.rawUri, get);
  },

  startUpload: (id) => {
    const item = get().items.find((i) => i.id === id);
    console.log("☁️ [startUpload] called", {
      id,
      found: !!item,
      status: item?.status,
      compressedUri: item?.compressedUri,
      sessionId: item?.sessionId,
    });
    if (!item || (item.status !== "ENCODED" && item.status !== "UPLOADED")) {
      console.warn("☁️ [startUpload] Skipped — item not in ENCODED or UPLOADED state");
      return;
    }
    if (!item.sessionId) {
      console.error("☁️ [startUpload] Skipped — sessionId is empty");
      get()._updateItem(id, {
        error: "Session ID is missing. Please re-record.",
      });
      return;
    }
    if (!item.compressedUri) {
      console.warn("☁️ [startUpload] Skipped — no compressedUri");
      get()._updateItem(id, {
        error: "No compressed file. Try encoding first.",
      });
      return;
    }

    // Validate compressed file exists
    try {
      const compressedFile = new File(item.compressedUri);
      if (!compressedFile.exists) {
        console.error("☁️ [startUpload] Compressed file does not exist:", item.compressedUri);
        get()._updateItem(id, {
          status: "RECORDED",
          error: "Compressed file missing. Please re-encode.",
          compressedUri: undefined,
          compressedSize: undefined,
        });
        return;
      }
    } catch (e) {
      console.warn("☁️ [startUpload] Could not check compressed file:", e);
    }

    // Increment generation so any previous stale upload is invalidated
    const gen = (uploadGeneration.get(id) ?? 0) + 1;
    uploadGeneration.set(id, gen);

    get()._updateItem(id, { status: "UPLOADING", progress: 0, error: undefined });

    // Re-read the item to get the freshest state
    const freshItem = get().items.find((i) => i.id === id)!;
    _runUpload(id, freshItem, get, gen);
  },

  cancelUpload: (id) => {
    const item = get().items.find((i) => i.id === id);
    if (!item || item.status !== "UPLOADING") return;

    console.log("🛑 Cancelling upload for:", id);

    // Increment generation to invalidate the in-flight _runUpload
    uploadGeneration.set(id, (uploadGeneration.get(id) ?? 0) + 1);

    const cancel = activeUploadCancels.get(id);
    if (cancel) {
      cancel().catch((e) => console.warn("⚠️ Cancel error (safe to ignore):", e));
      activeUploadCancels.delete(id);
    }

    get()._updateItem(id, { status: "ENCODED", progress: 0, error: undefined });
  },

  remove: (id) => {
    const item = get().items.find((i) => i.id === id);
    if (item) {
      if (item.rawUri) safeDelete(item.rawUri);
      if (item.compressedUri) safeDelete(item.compressedUri);
    }
    set((state) => ({
      items: state.items.filter((i) => i.id !== id),
    }));
  },

  dismiss: (id) => {
    const item = get().items.find((i) => i.id === id);
    if (item) {
      if (item.rawUri) safeDelete(item.rawUri);
      if (item.compressedUri) safeDelete(item.compressedUri);
    }
    set((state) => ({
      items: state.items.filter((i) => i.id !== id),
    }));
  },

  saveToGallery: async (id) => {
    const item = get().items.find((i) => i.id === id);
    if (!item) return false;

    const uri = item.rawUri;
    if (!uri) return false;

    try {
      await MediaLibrary.saveToLibraryAsync(uri);
      get()._updateItem(id, { gallerySaved: true });
      console.log("📱 Saved to gallery:", uri);
      return true;
    } catch (e) {
      console.warn("⚠️ Gallery save failed:", e);
      return false;
    }
  },
}),
{
  name: "video-queue",
  storage: createJSONStorage(() => AsyncStorage),
  partialize: (state) => ({ items: state.items }),
}
));

// ==========================================
// Async Workers (outside component lifecycle)
// ==========================================

async function _runEncoding(
  id: string,
  rawUri: string,
  get: () => VideoQueueState
) {
  try {
    console.log("🎬 Starting encoding for:", id, "rawUri:", rawUri);

    // Read raw file size for comparison after encoding
    let rawSize: number | undefined;
    try {
      const rawFile = new File(rawUri);
      rawSize = rawFile.size ?? undefined;
      console.log("🎬 Raw file size:", rawSize, "bytes", rawSize ? `(${(rawSize / (1024 * 1024)).toFixed(1)} MB)` : "");
    } catch (e) {
      console.warn("⚠️ Could not read raw file size:", e);
    }

    let compressedUri = await Video.compress(
      rawUri,
      {
        compressionMethod: "auto",
        maxSize: 720,
        minimumFileSizeForCompress: 0,
        progressDivider: 5,
      },
      (progress) => {
        get()._updateItem(id, { progress });
      }
    );

    // BUG FIX: react-native-compressor may return the SAME path as the input
    // when it decides no compression is needed. If we move (rename) that file,
    // we'd just rename the raw file and report the raw size as "encoded".
    const isSamePath =
      compressedUri === rawUri ||
      compressedUri.replace("file://", "") === rawUri.replace("file://", "");

    if (isSamePath) {
      console.warn(
        "⚠️ Video.compress returned same path as input — compression was a no-op!",
        { rawUri, compressedUri }
      );
    }

    // Rename/copy compressed file with _encoded suffix for easier debugging
    try {
      const srcFile = new File(compressedUri);
      const dir = compressedUri.substring(0, compressedUri.lastIndexOf("/"));
      const ext = compressedUri.substring(compressedUri.lastIndexOf("."));
      const encodedName = `${id}_encoded${ext}`;
      const destPath = `${dir}/${encodedName}`;

      if (isSamePath) {
        // Same path: COPY instead of move to preserve the raw file
        srcFile.copy(new File(destPath));
        console.log("📝 Copied raw file to encoded path (no-op compression):", destPath);
      } else {
        // Different path: safe to move the compressed file
        srcFile.move(new File(destPath));
        console.log("📝 Renamed encoded file to:", destPath);
      }
      compressedUri = destPath;
    } catch (renameErr) {
      console.warn("⚠️ Could not rename/copy encoded file, using original path", renameErr);
    }

    console.log("✅ Encoding complete:", id, "compressedUri:", compressedUri);

    // Read compressed file size so the user can see it before uploading
    let compressedSize: number | undefined;
    try {
      const compressedFile = new File(compressedUri);
      compressedSize = compressedFile.size ?? undefined;
    } catch (e) {
      console.warn("⚠️ Could not read compressed file size:", e);
    }

    // Log compression ratio for debugging
    if (rawSize && compressedSize) {
      const ratio = ((1 - compressedSize / rawSize) * 100).toFixed(1);
      console.log(
        `📊 Compression: ${(rawSize / (1024 * 1024)).toFixed(1)} MB → ${(compressedSize / (1024 * 1024)).toFixed(1)} MB (${ratio}% reduction)`
      );

      if (compressedSize >= rawSize * 0.95) {
        console.warn(
          "⚠️ Compression barely reduced file size — encoded file is ≥95% of raw. " +
          "The compressor may not have performed effective encoding."
        );
      }
    }

    get()._updateItem(id, {
      status: "ENCODED",
      compressedUri,
      compressedSize,
      progress: 1,
    });

    // NOTE: We intentionally do NOT delete the raw file here.
    // It's kept so the user can re-encode later if needed.
    // Files are cleaned up on dismiss/remove.
  } catch (e) {
    // On encoding failure, revert to RECORDED so user can try again
    const errorMsg = e instanceof Error ? e.message : String(e);
    console.error("❌ Encoding failed:", id, errorMsg);
    get()._updateItem(id, {
      status: "RECORDED",
      error: `Encoding failed: ${errorMsg}`,
    });
  }
}

async function _runUpload(
  id: string,
  item: VideoItem,
  get: () => VideoQueueState,
  generation: number
) {
  const isStale = () => uploadGeneration.get(id) !== generation;

  try {
    console.log("☁️ [UPLOAD START]", {
      id,
      generation,
      compressedUri: item.compressedUri,
      sessionId: item.sessionId,
    });

    if (!item.compressedUri) {
      get()._updateItem(id, {
        status: "ENCODED",
        error: "No compressed file URI.",
      });
      return;
    }

    console.log("☁️ [UPLOAD] Calling processWorkoutVideo...");

    await processWorkoutVideo(item.compressedUri, item.sessionId, {
      onProgress: (p) => {
        if (!isStale()) get()._updateItem(id, { progress: p });
      },
      onCancelReady: (cancel) => {
        activeUploadCancels.set(id, cancel);
      },
      movements: item.movements,
      injuries: item.injuries,
      workoutType: item.workoutType,
      profileId: item.profileId,
    });

    activeUploadCancels.delete(id);

    if (isStale()) {
      console.log("🛑 Upload stale (cancelled/superseded), ignoring result:", id);
      return;
    }

    console.log("✅ [UPLOAD] Upload complete:", id);
    get()._updateItem(id, { status: "UPLOADED", progress: 1, error: undefined });

    // NOTE: We intentionally do NOT delete files here.
    // Files are cleaned up on dismiss/remove.
  } catch (e) {
    activeUploadCancels.delete(id);

    if (isStale()) {
      console.log("🛑 Upload stale (cancelled/superseded), ignoring error:", id);
      return;
    }

    // On upload failure, revert to ENCODED so user can try again
    const errorMsg = e instanceof Error ? e.message : String(e);
    console.error("❌ [UPLOAD] Upload failed:", id, errorMsg);
    get()._updateItem(id, {
      status: "ENCODED",
      error: `Upload failed: ${errorMsg}`,
    });
  }
}

// ==========================================
// Selector Hooks
// ==========================================

/** Returns only items that are actively processing (encoding or uploading) */
export const useActiveItems = () =>
  useVideoQueue((s) =>
    s.items.filter((i) => i.status === "ENCODING" || i.status === "UPLOADING")
  );

/** Returns count of items in queue (excluding UPLOADED) */
export const useQueueCount = () =>
  useVideoQueue((s) => s.items.filter((i) => i.status !== "UPLOADED").length);

/** Returns true if any item is actively processing */
export const useIsProcessing = () =>
  useVideoQueue((s) =>
    s.items.some((i) => i.status === "ENCODING" || i.status === "UPLOADING")
  );
