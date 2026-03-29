const fs = require("node:fs");
const path = require("node:path");
const dotenv = require("dotenv");

dotenv.config();
dotenv.config({ path: path.resolve(__dirname, "../api/.env") });

const defaultApiBase = process.env.EXPO_PUBLIC_API_URL;
const API_BASE = defaultApiBase.replace(/\/+$/, "");
const API_KEY = process.env.EXPO_PUBLIC_API_KEY

if (!API_KEY) {
  console.error("API key is not set. Use EXPO_PUBLIC_API_KEY, API_KEY, or API_SECRET.");
  process.exit(1);
}

const inputArg = process.argv[2] || process.env.VIDEO_PATH || path.resolve(__dirname, "../api/tmp/wod_large.mp4");
const FILE_PATH = path.isAbsolute(inputArg) ? inputArg : path.resolve(process.cwd(), inputArg);

const SESSION_ID = "WOD-2026-03-28-001";

if (!fs.existsSync(FILE_PATH)) {
  console.error(`Video file not found: ${FILE_PATH}`);
  process.exit(1);
}

async function run() {
  try {
    console.log(`Starting test upload: ${SESSION_ID}`);
    console.log(`API base: ${API_BASE}`);
    console.log(`Video path: ${FILE_PATH}`);
    const stats = fs.statSync(FILE_PATH);
    console.log(`File size: ${(stats.size / 1024 / 1024).toFixed(2)} MB`);

    console.log("\n1. Requesting upload URL...");
    const urlRes = await fetch(`${API_BASE}/upload-url`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-API-Key": API_KEY,
      },
      body: JSON.stringify({
        session_id: SESSION_ID,
        filename: "test_video.mp4",
      }),
    });

    if (!urlRes.ok)
      throw new Error(`Step 1 Failed: ${urlRes.status} ${await urlRes.text()}`);
    const { upload_url, gcs_uri } = await urlRes.json();
    console.log("Received signed URL");
    console.log("GCS URI:", gcs_uri);

    console.log("\n2. Uploading to GCS...");
    const fileStream = fs.readFileSync(FILE_PATH);

    const uploadRes = await fetch(upload_url, {
      method: "PUT",
      headers: {
        "Content-Type": "video/mp4",
        "Content-Length": stats.size.toString(),
      },
      body: fileStream,
    });

    if (!uploadRes.ok)
      throw new Error(
        `Step 2 Failed: ${uploadRes.status} ${await uploadRes.text()}`
      );
    console.log(`Upload succeeded with status ${uploadRes.status}`);

    console.log("\n3. Notifying API...");
    const completeRes = await fetch(`${API_BASE}/upload-complete`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-API-Key": API_KEY,
      },
      body: JSON.stringify({
        session_id: SESSION_ID,
        gcs_uri: gcs_uri,
        movements: [],
        injuries: [],
        workout_type: "wod",
      }),
    });

    if (!completeRes.ok)
      throw new Error(
        `Step 3 Failed: ${completeRes.status} ${await completeRes.text()}`
      );
    const result = await completeRes.json();
    console.log("Flow complete:", result);
  } catch (error) {
    console.error("\nError:", error.message);
    process.exitCode = 1;
  }
}

run();
