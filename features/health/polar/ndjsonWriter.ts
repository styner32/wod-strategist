import { Buffer } from "buffer";
import { Directory, File, FileHandle, Paths } from "expo-file-system";

/**
 * High-performance streaming NDJSON writer for mobile storage.
 *
 * Uses the modern Expo FileSystem (Directory, File, FileHandle) API to append
 * lines without rewriting or loading the whole file into JS heap memory.
 */
export class NdjsonWriter {
  private file: File;
  private handle: FileHandle | null = null;
  private buffer: string[] = [];
  private flushTimer: ReturnType<typeof setInterval> | null = null;
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
  }

  /**
   * Write and flush immediately (used for critical lifecycle events).
   */
  writeImmediate(line: object | string): void {
    this.write(line);
    this.flush();
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
   */
  flush(): void {
    if (this.buffer.length === 0 || !this.handle) return;
    const lines = this.buffer.splice(0, this.buffer.length);
    const text = lines.join("\n") + "\n";
    const bytes = new Uint8Array(Buffer.from(text, "utf8"));
    try {
      this.handle.offset = this.file.size;
      this.handle.writeBytes(bytes);
    } catch (err) {
      console.warn("⚠️ Error flushing NDJSON buffer to disk:", err);
    }
  }

  /**
   * Flushes remaining lines, closes the file handle, and returns the file path.
   */
  close(): string {
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
    return this.filePath;
  }
}
