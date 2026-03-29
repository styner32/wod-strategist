const fs = require("node:fs");
const path = require("node:path");
const { execFile } = require("node:child_process");
const { promisify } = require("node:util");

const execFileAsync = promisify(execFile);
const repoTmpDir = path.resolve(__dirname, "../api/tmp");

const defaultApiBase = process.env.EXPO_PUBLIC_API_URL;
const API_BASE = defaultApiBase.replace(/\/+$/, "");
const API_KEY = process.env.EXPO_PUBLIC_API_KEY
const WORKOUT_TYPE = 'wod';
const CHUNK_SECONDS = 10;
const MOVEMENTS = []
const INJURIES = []
const AUTO_MERGE = true;
const KEEP_CHUNKS = false;
const SESSION_ID = "wod-2025-03-29-004" // update

const inputArg = './tmp/wod_2.MP4' // update

if (!inputArg) {
  throw new Error("No input video path provided.")
}

const INPUT_VIDEO = path.isAbsolute(inputArg) ? inputArg : path.resolve(process.cwd(), inputArg);

function parseOptionalUint(value) {
  if (value === undefined || value === "") {
    return undefined;
  }

  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 0) {
    throw new Error(`PROFILE_ID must be a non-negative integer, got ${value}`);
  }

  return parsed;
}

function roundSeconds(value) {
  return Math.round(value * 1000) / 1000;
}

async function ensureBinaryExists(name) {
  try {
    await execFileAsync(name, ["-version"]);
  } catch (error) {
    throw new Error(`${name} is required for this script. Install it first and rerun.`);
  }
}

async function probeDuration(filePath) {
  const { stdout } = await execFileAsync("ffprobe", [
    "-v",
    "error",
    "-show_entries",
    "format=duration",
    "-of",
    "default=noprint_wrappers=1:nokey=1",
    filePath,
  ]);

  const duration = Number(stdout.trim());
  if (!Number.isFinite(duration) || duration <= 0) {
    throw new Error(`Could not determine duration for ${filePath}`);
  }

  return duration;
}

async function splitVideoIntoChunks(inputPath, chunkDir) {
  const outputPattern = path.join(chunkDir, "chunk_%04d.mp4");

  await execFileAsync("ffmpeg", [
    "-v",
    "error",
    "-y",
    "-i",
    inputPath,
    "-map",
    "0:v:0",
    "-map",
    "0:a:0?",
    "-c:v",
    "libx264",
    "-preset",
    "veryfast",
    "-crf",
    "22",
    "-sc_threshold",
    "0",
    "-force_key_frames",
    `expr:gte(t,n_forced*${CHUNK_SECONDS})`,
    "-c:a",
    "aac",
    "-b:a",
    "128k",
    "-f",
    "segment",
    "-segment_time",
    String(CHUNK_SECONDS),
    "-reset_timestamps",
    "1",
    outputPattern,
  ]);

  const chunkFiles = fs
    .readdirSync(chunkDir)
    .filter((name) => /^chunk_\d+\.mp4$/.test(name))
    .sort()
    .map((name) => path.join(chunkDir, name));

  if (chunkFiles.length === 0) {
    throw new Error("ffmpeg did not produce any chunk files");
  }

  return chunkFiles;
}

async function postJSON(endpoint, body) {
  const response = await fetch(`${API_BASE}${endpoint}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": API_KEY,
    },
    body: JSON.stringify(body),
  });

  if (!response.ok) {
    throw new Error(`${endpoint} failed: ${response.status} ${await response.text()}`);
  }

  return response.json();
}

async function uploadChunkFile(uploadURL, filePath) {
  const body = fs.readFileSync(filePath);
  const response = await fetch(uploadURL, {
    method: "PUT",
    headers: {
      "Content-Type": "video/mp4",
      "Content-Length": String(body.length),
    },
    body,
  });

  if (!response.ok) {
    throw new Error(`chunk PUT failed: ${response.status} ${await response.text()}`);
  }
}

async function run() {
  if (!globalThis.fetch) {
    throw new Error("This script requires Node.js 18+ because it uses the built-in fetch API.");
  }
  if (!API_KEY) {
    throw new Error("API key is not set. Use EXPO_PUBLIC_API_KEY, API_KEY, or API_SECRET.");
  }
  if (!fs.existsSync(INPUT_VIDEO)) {
    throw new Error(`Video file not found: ${INPUT_VIDEO}`);
  }

  const profileId = parseOptionalUint(process.env.PROFILE_ID);

  await ensureBinaryExists("ffmpeg");
  await ensureBinaryExists("ffprobe");

  fs.mkdirSync(repoTmpDir, { recursive: true });
  const chunkDir = fs.mkdtempSync(path.join(repoTmpDir, `${SESSION_ID}-chunks-`));
  let keepChunkDir = KEEP_CHUNKS;

  try {
    console.log(`Session ID: ${SESSION_ID}`);
    console.log(`API base: ${API_BASE}`);
    console.log(`Video path: ${INPUT_VIDEO}`);
    console.log(`Chunk seconds: ${CHUNK_SECONDS}`);
    console.log(`Movements: ${MOVEMENTS.join(", ") || "(none)"}`);
    console.log(`Injuries: ${INJURIES.join(", ") || "(none)"}`);
    console.log(`Auto merge: ${AUTO_MERGE}`);
    console.log(`Chunk workspace: ${chunkDir}`);

    const sourceDuration = await probeDuration(INPUT_VIDEO);
    console.log(`Source duration: ${roundSeconds(sourceDuration)}s`);

    console.log("\n1. Splitting source video into uploadable chunks...");
    const chunkFiles = await splitVideoIntoChunks(INPUT_VIDEO, chunkDir);
    console.log(`Created ${chunkFiles.length} chunk file(s).`);

    console.log("\n2. Uploading chunks through /upload-url and /chunk-complete...");
    let cursor = 0;
    for (let index = 0; index < chunkFiles.length; index += 1) {
      const chunkPath = chunkFiles[index];
      const filename = path.basename(chunkPath);
      const chunkDuration = roundSeconds(await probeDuration(chunkPath));
      const startSecs = roundSeconds(cursor);
      const endSecs = roundSeconds(cursor + chunkDuration);

      console.log(
        `Uploading chunk ${index + 1}/${chunkFiles.length}: ${filename} (${startSecs}s -> ${endSecs}s)`
      );

      const { upload_url, gcs_uri } = await postJSON("/upload-url", {
        session_id: SESSION_ID,
        filename,
      });

      await uploadChunkFile(upload_url, chunkPath);
      await postJSON("/chunk-complete", {
        session_id: SESSION_ID,
        gcs_uri,
        movements: MOVEMENTS,
        injuries: INJURIES,
        workout_type: WORKOUT_TYPE,
        ...(profileId !== undefined ? { profile_id: profileId } : {}),
        start_secs: startSecs,
        end_secs: endSecs,
      });

      cursor = endSecs;
    }

    if (AUTO_MERGE) {
      console.log("\n3. Requesting /merge-chunks...");
      const mergeResult = await postJSON("/merge-chunks", {
        session_id: SESSION_ID,
        workout_type: WORKOUT_TYPE,
        movements: MOVEMENTS,
        injuries: INJURIES,
        ...(profileId !== undefined ? { profile_id: profileId } : {}),
      });
      console.log("Merge enqueued:", mergeResult);
    } else {
      console.log("\n3. Skipping merge because AUTO_MERGE=0.");
    }

    console.log("\nReplay complete.");
    console.log(`Chunk analysis endpoint: ${API_BASE}/chunk-analysis/${SESSION_ID}`);
    console.log(`Full analysis endpoint: ${API_BASE}/analysis/${SESSION_ID}`);
  } catch (error) {
    keepChunkDir = true;
    throw error;
  } finally {
    if (keepChunkDir) {
      console.log(`Chunk files kept at ${chunkDir}`);
    } else {
      fs.rmSync(chunkDir, { recursive: true, force: true });
    }
  }
}

run().catch((error) => {
  console.error(`\nError: ${error.message}`);
  process.exitCode = 1;
});
