import { deleteAsync } from "expo-file-system";
import { Video } from "react-native-compressor";
import { create } from "zustand";

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
}

export interface EnqueueMetadata {
  sessionId: string;
  workoutType: WorkoutType;
  movements: string[];
  injuries: string[];
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
    await deleteAsync(uri, { idempotent: true });
    console.log("🗑️ Deleted temp file:", uri);
  } catch (e) {
    console.warn("⚠️ Failed to delete temp file:", uri, e);
  }
}

// ==========================================
// Store
// ==========================================

export const useVideoQueue = create<VideoQueueState>((set, get) => ({
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

    get()._updateItem(id, { status: "UPLOADING", progress: 0, error: undefined });

    // Fire-and-forget upload
    _runUpload(id, item, get);
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
}));

// ==========================================
// Async Workers (outside component lifecycle)
// ==========================================

async function _runEncoding(
  id: string,
  rawUri: string,
  get: () => VideoQueueState
) {
  try {
    console.log("🎬 Starting encoding for:", id);

    const compressedUri = await Video.compress(rawUri, {
      compressionMethod: "auto",
      maxSize: 720,
      progressDivider: 5,
    });

    console.log("✅ Encoding complete:", id);

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
