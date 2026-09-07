import { Buffer } from "buffer";
import { Directory, File, FileHandle, Paths } from "expo-file-system";

/**
 * Cap on lines held in memory while writes keep failing. Beyond this the
 * oldest lines are dropped and counted, so a failing disk degrades the
 * recording instead of growing the JS heap without bound.
 */
const MAX_BUFFERED_LINES = 5000;

export interface NdjsonWriterStats {
  /** Number of flushes that threw. */
  failedWrites: number;
  /** Lines discarded because the retry buffer overflowed. */
  droppedLines: number;
  /** Lines still buffered (never reached disk). */
  pendingLines: number;
}

export interface NdjsonCloseResult extends NdjsonWriterStats {
  filePath: string;
  /** True when every queued line reached disk (retried writes included). */
  complete: boolean;
}

/**
 * High-performance streaming NDJSON writer for mobile storage.
 *
 * Uses the modern Expo FileSystem (Directory, File, FileHandle) API to append
 * lines without rewriting or loading the whole file into JS heap memory.
 *
 * A failed write keeps its lines buffered for the next flush and is recorded
 * in the writer's stats — the caller must not treat a file written by a
 * failing writer as a complete recording.
 */
export class NdjsonWriter {
  private file: File;
  private handle: FileHandle | null = null;
  private buffer: string[] = [];
  private flushTimer: ReturnType<typeof setInterval> | null = null;
  private failedWrites = 0;
  private droppedLines = 0;
  readonly filePath: string;

  constructor(sessionId: string) {
    const dir = new Directory(Paths.document, "sensor");
    if (!dir.exists) {
      dir.create({ intermediates: true });
    }
    this.file = new File(dir, `${sessionId}.ndjson`);
    if (!this.file.exists) {
      this.file.create();
    }
    this.filePath = this.file.uri;
    this.handle = this.file.open();
    if (this.handle) {
      this.handle.offset = this.file.size;
    }
  }

  /**
   * Queue a line to the batch buffer.
   */
  write(line: object | string): void {
    const str = typeof line === "string" ? line : JSON.stringify(line);
    this.buffer.push(str);
    this.trimBuffer();
  }

  /**
   * Write and flush immediately (used for critical lifecycle events).
   * Returns whether the flush reached disk.
   */
  writeImmediate(line: object | string): boolean {
    this.write(line);
    return this.flush();
  }

  /**
   * Start 1-second auto-flush interval.
   */
  startAutoFlush(intervalMs: number = 1000): void {
    if (this.flushTimer) return;
    this.flushTimer = setInterval(() => {
      this.flush();
    }, intervalMs);
  }

  /**
   * Flush pending buffered lines to disk.
   *
   * The buffer is cleared only after the write succeeds, so a transient
   * failure retries on the next flush instead of silently losing samples.
   */
  flush(): boolean {
    if (this.buffer.length === 0) return true;
    if (!this.handle) {
      this.failedWrites += 1;
      return false;
    }

    const text = this.buffer.join("\n") + "\n";
    const bytes = new Uint8Array(Buffer.from(text, "utf8"));
    try {
      this.handle.offset = this.file.size;
      this.handle.writeBytes(bytes);
      this.buffer.length = 0;
      return true;
    } catch (err) {
      this.failedWrites += 1;
      console.warn(
        `⚠️ Error flushing NDJSON buffer to disk (${this.buffer.length} lines retained):`,
        err,
      );
      this.trimBuffer();
      return false;
    }
  }

  /** Write outcome so far. */
  stats(): NdjsonWriterStats {
    return {
      failedWrites: this.failedWrites,
      droppedLines: this.droppedLines,
      pendingLines: this.buffer.length,
    };
  }

  /**
   * Flushes remaining lines, closes the file handle, and reports whether the
   * file on disk is a complete record of what was written.
   */
  close(): NdjsonCloseResult {
    if (this.flushTimer) {
      clearInterval(this.flushTimer);
      this.flushTimer = null;
    }
    this.flush();
    if (this.handle) {
      try {
        this.handle.close();
      } catch (err) {
        console.warn("⚠️ Error closing FileHandle:", err);
      }
      this.handle = null;
    }
    const stats = this.stats();
    return {
      filePath: this.filePath,
      // A write that failed but was retried successfully loses nothing —
      // only dropped or never-written lines make the file incomplete.
      complete: stats.droppedLines === 0 && stats.pendingLines === 0,
      ...stats,
    };
  }

  private trimBuffer(): void {
    const overflow = this.buffer.length - MAX_BUFFERED_LINES;
    if (overflow > 0) {
      this.buffer.splice(0, overflow);
      this.droppedLines += overflow;
    }
  }
}
