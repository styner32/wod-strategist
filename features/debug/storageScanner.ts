import {
  cacheDirectory,
  copyAsync,
  deleteAsync,
  documentDirectory,
  getInfoAsync,
  makeDirectoryAsync,
  readDirectoryAsync,
  writeAsStringAsync,
} from "expo-file-system/legacy";
import { File as FSFile } from "expo-file-system";
import { Alert, Share } from "react-native";

interface DirEntry {
  name: string;
  isDirectory: boolean;
  sizeBytes: number;
  children?: DirEntry[];
}

/**
 * Recursively scan a directory and return size info.
 * maxDepth controls how deep we go to avoid hangs on huge trees.
 */
const scanDir = async (
  path: string,
  maxDepth: number = 2,
  currentDepth: number = 0
): Promise<DirEntry[]> => {
  try {
    const items = await readDirectoryAsync(path);
    const entries: DirEntry[] = [];

    for (const item of items) {
      const fullPath = `${path}${item}`;
      try {
        const info = await getInfoAsync(fullPath);
        if (!info.exists) continue;

        const entry: DirEntry = {
          name: item,
          isDirectory: info.isDirectory ?? false,
          sizeBytes: info.size ?? 0,
        };

        // Recurse into subdirectories if within depth limit
        if (entry.isDirectory && currentDepth < maxDepth) {
          entry.children = await scanDir(
            `${fullPath}/`,
            maxDepth,
            currentDepth + 1
          );
          // Sum child sizes for directory total
          entry.sizeBytes = entry.children.reduce(
            (sum, c) => sum + c.sizeBytes,
            0
          );
        }

        entries.push(entry);
      } catch {
        entries.push({ name: item, isDirectory: false, sizeBytes: 0 });
      }
    }

    // Sort by size descending
    entries.sort((a, b) => b.sizeBytes - a.sizeBytes);
    return entries;
  } catch {
    return [];
  }
};

const formatSize = (bytes: number): string => {
  if (bytes >= 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  }
  if (bytes >= 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  if (bytes >= 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${bytes} B`;
};

const buildReport = (entries: DirEntry[], indent: string = ""): string => {
  let report = "";
  for (const entry of entries) {
    const icon = entry.isDirectory ? "📁" : "📄";
    report += `${indent}${icon} ${entry.name}  (${formatSize(entry.sizeBytes)})\n`;
    if (entry.children && entry.children.length > 0) {
      report += buildReport(entry.children, indent + "  ");
    }
  }
  return report;
};

/**
 * Derive the app container root from documentDirectory.
 * documentDirectory = file:///.../<UUID>/Documents/
 * container root   = file:///.../<UUID>/
 */
const getContainerRoot = (): string => {
  const docDir = documentDirectory!;
  // Remove trailing "Documents/" to get container root
  return docDir.replace(/Documents\/$/, "");
};

/**
 * Scan all accessible storage directories and produce a size report.
 * Scans the entire app container, not just Documents and Cache.
 */
export const scanAllStorage = async (): Promise<string> => {
  const containerRoot = getContainerRoot();

  // All known iOS app container directories
  const dirs = [
    { name: "Documents", path: `${containerRoot}Documents/` },
    { name: "Library/Caches", path: `${containerRoot}Library/Caches/` },
    { name: "Library/Application Support", path: `${containerRoot}Library/Application Support/` },
    { name: "Library/Preferences", path: `${containerRoot}Library/Preferences/` },
    { name: "Library/WebKit", path: `${containerRoot}Library/WebKit/` },
    { name: "Library/Cookies", path: `${containerRoot}Library/Cookies/` },
    { name: "Library/SplashBoard", path: `${containerRoot}Library/SplashBoard/` },
    { name: "tmp", path: `${containerRoot}tmp/` },
  ];

  let report = "=== WOD Strategist Storage Scan ===\n";
  report += `Date: ${new Date().toISOString()}\n`;
  report += `Container: ${containerRoot}\n\n`;

  let grandTotal = 0;

  for (const dir of dirs) {
    try {
      const info = await getInfoAsync(dir.path);
      if (!info.exists) continue;

      const entries = await scanDir(dir.path, 2);
      const totalSize = entries.reduce((sum, e) => sum + e.sizeBytes, 0);
      grandTotal += totalSize;
      report += `--- ${dir.name} ---\n`;
      report += `Path: ${dir.path}\n`;
      report += `Total: ${formatSize(totalSize)} (${entries.length} items)\n\n`;
      report += buildReport(entries);
      report += "\n";
    } catch {
      // Directory doesn't exist or not accessible, skip
    }
  }

  // Also scan Library root for any other subdirs we might have missed
  try {
    const libPath = `${containerRoot}Library/`;
    const libItems = await readDirectoryAsync(libPath);
    const knownSubdirs = ["Caches", "Application Support", "Preferences", "WebKit", "Cookies", "SplashBoard"];
    const unknownDirs = libItems.filter((item) => !knownSubdirs.includes(item));
    if (unknownDirs.length > 0) {
      report += `--- Library (other) ---\n`;
      for (const item of unknownDirs) {
        const fullPath = `${libPath}${item}`;
        try {
          const info = await getInfoAsync(fullPath);
          if (!info.exists) continue;
          const size = info.size ?? 0;
          const icon = info.isDirectory ? "📁" : "📄";
          grandTotal += size;
          report += `  ${icon} ${item}  (${formatSize(size)})\n`;
        } catch {}
      }
      report += "\n";
    }
  } catch {}

  report += `=== GRAND TOTAL: ${formatSize(grandTotal)} ===\n`;

  return report;
};

/**
 * Recursively copy all files from src to dest.
 */
const copyRecursive = async (
  srcDir: string,
  destDir: string,
  onProgress?: (file: string) => void
): Promise<number> => {
  const items = await readDirectoryAsync(srcDir);
  let copied = 0;

  for (const item of items) {
    const srcPath = `${srcDir}${item}`;
    const destPath = `${destDir}${item}`;

    try {
      const info = await getInfoAsync(srcPath);
      if (!info.exists) continue;

      if (info.isDirectory) {
        await makeDirectoryAsync(destPath, { intermediates: true });
        copied += await copyRecursive(`${srcPath}/`, `${destPath}/`, onProgress);
      } else {
        onProgress?.(item);
        await copyAsync({ from: srcPath, to: destPath });
        copied++;
      }
    } catch (e) {
      console.warn(`Failed to copy ${srcPath}:`, e);
    }
  }

  return copied;
};

/**
 * Copy all cache files to Documents/Hidden_Cache/ so they become
 * visible via Xcode container download.
 */
export const extractCacheToDocuments = async (
  onProgress?: (file: string) => void
): Promise<number> => {
  const destDir = `${documentDirectory!}Hidden_Cache/`;
  await makeDirectoryAsync(destDir, { intermediates: true });
  return copyRecursive(cacheDirectory!, destDir, onProgress);
};

/**
 * Copy any container subdirectory to Documents/Extracted_<name>/.
 * e.g. "tmp" → Documents/Extracted_tmp/
 */
export const extractDirToDocuments = async (
  relPath: string,
  onProgress?: (file: string) => void
): Promise<number> => {
  const safeName = relPath.replace(/\//g, "_");
  const srcDir = `${getContainerRoot()}${relPath}/`;
  const destDir = `${documentDirectory!}Extracted_${safeName}/`;
  await makeDirectoryAsync(destDir, { intermediates: true });
  return copyRecursive(srcDir, destDir, onProgress);
};

/**
 * List top-level cache items with their sizes.
 */
export interface CacheItem {
  name: string;
  isDirectory: boolean;
  sizeBytes: number;
}

/**
 * List top-level items in a directory with their sizes.
 */
const listDirItems = async (baseDir: string): Promise<CacheItem[]> => {
  const items = await readDirectoryAsync(baseDir);
  const results: CacheItem[] = [];

  for (const item of items) {
    const fullPath = `${baseDir}${item}`;
    try {
      const info = await getInfoAsync(fullPath);
      if (!info.exists) continue;

      let size = info.size ?? 0;
      if (info.isDirectory) {
        try {
          const children = await readDirectoryAsync(fullPath);
          for (const child of children) {
            try {
              const ci = await getInfoAsync(`${fullPath}/${child}`);
              if (ci.exists && ci.size) size += ci.size;
            } catch {}
          }
        } catch {}
      }

      results.push({
        name: item,
        isDirectory: info.isDirectory ?? false,
        sizeBytes: size,
      });
    } catch {
      results.push({ name: item, isDirectory: false, sizeBytes: 0 });
    }
  }

  results.sort((a, b) => b.sizeBytes - a.sizeBytes);
  return results;
};

export const listCacheItems = () => listDirItems(cacheDirectory!);
export const listDocumentItems = () => listDirItems(documentDirectory!);

/**
 * Delete a single item by name from a directory.
 * Uses the new File API which handles tmp/ paths correctly.
 */
const deleteDirItem = async (baseDir: string, name: string): Promise<void> => {
  const fullPath = `${baseDir}${name}`;
  try {
    // Strip file:// prefix for the new File API
    const nativePath = fullPath.replace(/^file:\/\//, "");
    const f = new FSFile(nativePath);
    if (f.exists) {
      f.delete();
      console.log("🗑️ Deleted (File API):", name);
      return;
    }
  } catch (e) {
    console.warn("⚠️ File API delete failed, trying legacy:", e);
  }
  // Fallback to legacy API
  await deleteAsync(fullPath, { idempotent: true });
};

export const deleteCacheItem = (name: string) => deleteDirItem(cacheDirectory!, name);
export const deleteDocumentItem = (name: string) => deleteDirItem(documentDirectory!, name);

/**
 * Delete all files in a directory. Returns the number of items deleted.
 * Uses the new File API which handles tmp/ paths correctly.
 */
const deleteAllInDir = async (
  baseDir: string,
  onProgress?: (file: string) => void
): Promise<number> => {
  const items = await readDirectoryAsync(baseDir);
  let deleted = 0;

  for (const item of items) {
    const fullPath = `${baseDir}${item}`;
    try {
      onProgress?.(item);
      // Strip file:// prefix for the new File API
      const nativePath = fullPath.replace(/^file:\/\//, "");
      const f = new FSFile(nativePath);
      if (f.exists) {
        f.delete();
        deleted++;
        continue;
      }
    } catch {
      // Fall through to legacy API
    }
    try {
      await deleteAsync(fullPath, { idempotent: true });
      deleted++;
    } catch (e) {
      console.warn(`Failed to delete ${fullPath}:`, e);
    }
  }

  return deleted;
};

export const deleteCacheFiles = (onProgress?: (file: string) => void) =>
  deleteAllInDir(cacheDirectory!, onProgress);
export const deleteDocumentFiles = (onProgress?: (file: string) => void) =>
  deleteAllInDir(documentDirectory!, onProgress);

/**
 * List/delete items in any container subdirectory by relative path.
 * e.g. "Library/Application Support" or "tmp"
 */
export const listItemsByRelPath = (relPath: string) =>
  listDirItems(`${getContainerRoot()}${relPath}/`);
export const deleteItemByRelPath = (relPath: string, name: string) =>
  deleteDirItem(`${getContainerRoot()}${relPath}/`, name);
export const deleteAllByRelPath = (relPath: string, onProgress?: (file: string) => void) =>
  deleteAllInDir(`${getContainerRoot()}${relPath}/`, onProgress);

/**
 * Get all scannable directories in the app container with existence check.
 */
export const getContainerDirs = async (): Promise<{ name: string; path: string }[]> => {
  const root = getContainerRoot();
  const candidates = [
    { name: "Documents", path: `${root}Documents/` },
    { name: "Library/Caches", path: `${root}Library/Caches/` },
    { name: "Library/Application Support", path: `${root}Library/Application Support/` },
    { name: "Library/Preferences", path: `${root}Library/Preferences/` },
    { name: "Library/WebKit", path: `${root}Library/WebKit/` },
    { name: "Library/Cookies", path: `${root}Library/Cookies/` },
    { name: "tmp", path: `${root}tmp/` },
  ];

  const existing: { name: string; path: string }[] = [];
  for (const c of candidates) {
    try {
      const info = await getInfoAsync(c.path);
      if (info.exists) existing.push(c);
    } catch {}
  }
  return existing;
};

/**
 * Run the scan and share/display the report.
 */
export const runStorageScanAndShare = async () => {
  try {
    Alert.alert("Scanning…", "This may take a minute for large storage.");
    const report = await scanAllStorage();
    console.log(report);

    // Save report to Documents for easy retrieval
    const reportPath = `${documentDirectory!}storage_report.txt`;
    await writeAsStringAsync(reportPath, report);

    // Offer to share
    await Share.share({ message: report, title: "Storage Scan Report" });
  } catch (e) {
    console.error("Storage scan failed:", e);
    Alert.alert("Error", `Scan failed: ${e}`);
  }
};
