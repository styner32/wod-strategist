import { Buffer } from "buffer";

// In-memory file storage: path -> Uint8Array
const memoryFiles = new Map<string, Uint8Array>();
const memoryDirs = new Set<string>();

export function __resetMockFileSystem(): void {
  memoryFiles.clear();
  memoryDirs.clear();
  memoryDirs.add("file:///mock/docs/");
  memoryDirs.add("file:///mock/cache/");
}

export function __getMockFileContent(uri: string): string | null {
  const data = memoryFiles.get(normalizeUri(uri));
  if (!data) return null;
  return Buffer.from(data).toString("utf8");
}

function normalizeUri(pathOrUri: string): string {
  if (pathOrUri.startsWith("file://")) {
    return pathOrUri;
  }
  return `file://${pathOrUri.startsWith("/") ? "" : "/"}${pathOrUri}`;
}

function joinPaths(...parts: (string | { uri: string })[]): string {
  const strings = parts.map((p) => (typeof p === "string" ? p : p.uri));
  let joined = strings.join("/");
  // replace duplicate slashes except after file://
  joined = joined.replace(/([^:])\/\/+/g, "$1/");
  return normalizeUri(joined);
}

export const Paths = {
  get document(): Directory {
    return new Directory("file:///mock/docs");
  },
  get cache(): Directory {
    return new Directory("file:///mock/cache");
  },
  get bundle(): Directory {
    return new Directory("file:///mock/bundle");
  },
};

export class Directory {
  uri: string;

  constructor(...parts: (string | Directory | File)[]) {
    this.uri = joinPaths(...parts.map((p) => (typeof p === "string" ? p : p.uri)));
    if (!this.uri.endsWith("/")) {
      this.uri += "/";
    }
  }

  get exists(): boolean {
    return memoryDirs.has(this.uri);
  }

  create(options?: { intermediates?: boolean; idempotent?: boolean }): void {
    memoryDirs.add(this.uri);
  }

  delete(): void {
    memoryDirs.delete(this.uri);
    for (const key of memoryFiles.keys()) {
      if (key.startsWith(this.uri)) {
        memoryFiles.delete(key);
      }
    }
  }
}

export class FileHandle {
  private fileUri: string;
  offset: number | null = 0;
  private isClosed: boolean = false;

  constructor(fileUri: string) {
    this.fileUri = fileUri;
    const current = memoryFiles.get(this.fileUri) ?? new Uint8Array(0);
    this.offset = current.length;
  }

  get size(): number | null {
    const current = memoryFiles.get(this.fileUri);
    return current ? current.length : 0;
  }

  writeBytes(bytes: Uint8Array): void {
    if (this.isClosed) {
      throw new Error("Cannot write to closed FileHandle");
    }
    const current = memoryFiles.get(this.fileUri) ?? new Uint8Array(0);
    const writePos = this.offset ?? current.length;
    const newLen = Math.max(current.length, writePos + bytes.length);
    const newBuf = new Uint8Array(newLen);

    newBuf.set(current);
    newBuf.set(bytes, writePos);

    memoryFiles.set(this.fileUri, newBuf);
    this.offset = writePos + bytes.length;
  }

  readBytes(length: number): Uint8Array {
    if (this.isClosed) {
      throw new Error("Cannot read from closed FileHandle");
    }
    const current = memoryFiles.get(this.fileUri) ?? new Uint8Array(0);
    const readPos = this.offset ?? 0;
    const slice = current.subarray(readPos, readPos + length);
    this.offset = readPos + slice.length;
    return slice;
  }

  close(): void {
    this.isClosed = true;
  }
}

export class File {
  uri: string;

  constructor(...parts: (string | Directory | File)[]) {
    this.uri = joinPaths(...parts.map((p) => (typeof p === "string" ? p : p.uri)));
    if (this.uri.endsWith("/")) {
      this.uri = this.uri.slice(0, -1);
    }
  }

  get exists(): boolean {
    return memoryFiles.has(this.uri);
  }

  get size(): number {
    const data = memoryFiles.get(this.uri);
    return data ? data.length : 0;
  }

  create(options?: { intermediates?: boolean; overwrite?: boolean }): void {
    if (!this.exists || options?.overwrite) {
      memoryFiles.set(this.uri, new Uint8Array(0));
    }
  }

  open(): FileHandle {
    if (!this.exists) {
      this.create();
    }
    return new FileHandle(this.uri);
  }

  delete(): void {
    memoryFiles.delete(this.uri);
  }

  textSync(): string {
    const data = memoryFiles.get(this.uri);
    return data ? Buffer.from(data).toString("utf8") : "";
  }

  async text(): Promise<string> {
    return this.textSync();
  }

  async bytes(): Promise<Uint8Array> {
    return memoryFiles.get(this.uri) ?? new Uint8Array(0);
  }
}
