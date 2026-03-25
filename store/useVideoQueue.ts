import AsyncStorage from "@react-native-async-storage/async-storage";
import { File } from "expo-file-system";
import * as MediaLibrary from "expo-media-library";
import { Video } from "react-native-compressor";
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";

import { processWorkoutVideo } from "@/features/wod/api";
import type { WorkoutType } from "@/features/wod/workoutType";

// ==========================================
// Types
// ==========================================

export type VideoStatus =
  | "RECORDED"
  | "ENCODING"
  | "READY"
  | "UPLOADING"
  | "DONE"
  | "ERROR";

export interface VideoItem {
  id: string;
  rawUri: string;
  compressedUri?: string;
  sessionId: string;
  workoutType: WorkoutType;
  movements: string[];
  injuries: string[];
  status: VideoStatus;
  progress: number;
  error?: string;
  errorStep?: "encode" | "upload";
  createdAt: number;
  profileId?: number;
  gallerySaved: boolean;
}

export interface EnqueueMetadata {
  sessionId: string;
  workoutType: WorkoutType;
  movements: string[];
  injuries: string[];
  profileId?: number;
}

interface VideoQueueState {
  items: VideoItem[];

  // Actions
  enqueue: (rawUri: string, metadata: EnqueueMetadata) => string;
  startEncoding: (id: string) => void;
  startUpload: (id: string) => void;
  retry: (id: string) => void;
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
    if (!item || item.status !== "RECORDED") return;

    get()._updateItem(id, { status: "ENCODING", progress: 0 });
    _runEncoding(id, item.rawUri, get);
  },

  startUpload: (id) => {
    const item = get().items.find((i) => i.id === id);
    if (!item || item.status !== "READY" || !item.compressedUri) return;

    console.log("☁️ Upload will use compressedUri:", item.compressedUri);
    console.log("☁️ (rawUri was:", item.rawUri, ")");

    get()._updateItem(id, { status: "UPLOADING", progress: 0, error: undefined });

    // Re-read the item to get the freshest state (compressedUri from encoding)
    const freshItem = get().items.find((i) => i.id === id)!;
    _runUpload(id, freshItem, get);
  },

  retry: (id) => {
    const item = get().items.find((i) => i.id === id);
    if (!item || item.status !== "ERROR") return;

    if (item.errorStep === "encode") {
      get()._updateItem(id, { status: "ENCODING", progress: 0, error: undefined });
      _runEncoding(id, item.rawUri, get);
    } else if (item.errorStep === "upload" && item.compressedUri) {
      get()._updateItem(id, { status: "UPLOADING", progress: 0, error: undefined });
      _runUpload(id, item, get);
    }
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
    set((state) => ({
      items: state.items.filter((i) => i.id !== id),
    }));
  },

  saveToGallery: async (id) => {
    const item = get().items.find((i) => i.id === id);
    if (!item) return false;

    // Use rawUri (preferred for debugging quality)
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

    let compressedUri = await Video.compress(rawUri, {
      compressionMethod: "auto",
      maxSize: 720,
      progressDivider: 5,
    });

    // Rename compressed file with _encoded suffix for easier debugging
    try {
      const srcFile = new File(compressedUri);
      const dir = compressedUri.substring(0, compressedUri.lastIndexOf("/"));
      const ext = compressedUri.substring(compressedUri.lastIndexOf("."));
      const encodedName = `${id}_encoded${ext}`;
      const destPath = `${dir}/${encodedName}`;
      srcFile.move(new File(destPath));
      compressedUri = destPath;
      console.log("📝 Renamed encoded file to:", compressedUri);
    } catch (renameErr) {
      console.warn("⚠️ Could not rename encoded file, using original path", renameErr);
    }

    console.log("✅ Encoding complete:", id, "compressedUri:", compressedUri);

    get()._updateItem(id, {
      status: "READY",
      compressedUri,
      progress: 1,
    });

    // Delete raw temp file (gallery copy is separate)
    await safeDelete(rawUri);
  } catch (e) {
    console.error("❌ Encoding failed:", id, e);
    get()._updateItem(id, {
      status: "ERROR",
      error: String(e),
      errorStep: "encode",
    });
  }
}

async function _runUpload(
  id: string,
  item: VideoItem,
  get: () => VideoQueueState
) {
  try {
    console.log("☁️ Starting upload for:", id);

    await processWorkoutVideo(item.compressedUri!, item.sessionId, {
      onProgress: (p) => get()._updateItem(id, { progress: p }),
      movements: item.movements,
      injuries: item.injuries,
      workoutType: item.workoutType,
      profileId: item.profileId,
    });

    console.log("✅ Upload complete:", id);

    get()._updateItem(id, { status: "DONE", progress: 1 });

    // Delete compressed temp file
    if (item.compressedUri) {
      await safeDelete(item.compressedUri);
    }
  } catch (e) {
    console.error("❌ Upload failed:", id, e);
    get()._updateItem(id, {
      status: "ERROR",
      error: String(e),
      errorStep: "upload",
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

/** Returns count of items in queue (excluding DONE) */
export const useQueueCount = () =>
  useVideoQueue((s) => s.items.filter((i) => i.status !== "DONE").length);

/** Returns true if any item is actively processing */
export const useIsProcessing = () =>
  useVideoQueue((s) =>
    s.items.some((i) => i.status === "ENCODING" || i.status === "UPLOADING")
  );
