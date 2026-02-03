// scripts/test-upload.js
const fs = require("fs");
const path = require("path");
const dotenv = require("dotenv");
dotenv.config();

const API_BASE = process.env.EXPO_PUBLIC_API_URL;
const API_KEY = process.env.EXPO_PUBLIC_API_KEY || "";

if (API_BASE === undefined || API_KEY === undefined) {
  console.error("API_BASE or API_KEY is not set");
  process.exit(1);
}

const FILE_PATH = path.join(process.cwd(), "./api/tmp/wod_large.mp4"); // Change this to the path of the video you want to upload
console.log("FILE_PATH:", FILE_PATH);

const SESSION_ID = "test-session-" + Date.now();

// Ensure dummy file exists
if (!fs.existsSync(FILE_PATH)) {
  console.log("Creating dummy video file...");
  // Create a 5MB dummy file to test "size" slightly better than empty
  const buffer = Buffer.alloc(5 * 1024 * 1024, "a");
  const dir = path.dirname(FILE_PATH);
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(FILE_PATH, buffer);
}

async function run() {
  try {
    console.log(`🚀 Starting Test Upload: ${SESSION_ID}`);
    const stats = fs.statSync(FILE_PATH);
    console.log(`📁 File Size: ${(stats.size / 1024 / 1024).toFixed(2)} MB`);

    // 1. Get Signed URL
    console.log("\n1️⃣ Requesting Upload URL...");
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
    console.log("✅ Got URL:", upload_url.substring(0, 50) + "...");
    console.log("✅ GCS URI:", gcs_uri);

    // 2. Upload to GCS
    console.log("\n2️⃣ Uploading to GCS...");
    const fileStream = fs.readFileSync(FILE_PATH);

    const uploadRes = await fetch(upload_url, {
      method: "PUT",
      headers: {
        "Content-Type": "video/mp4",
        // 'Content-Length': stats.size.toString() // fetch handles this usually
      },
      body: fileStream,
    });

    if (!uploadRes.ok)
      throw new Error(
        `Step 2 Failed: ${uploadRes.status} ${await uploadRes.text()}`
      );
    console.log("✅ Upload Success (Status 200)");

    // 3. Notify Complete
    console.log("\n3️⃣ Notifying API...");
    const completeRes = await fetch(`${API_BASE}/upload-complete`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-API-Key": API_KEY,
      },
      body: JSON.stringify({
        session_id: SESSION_ID,
        gcs_uri: gcs_uri,
      }),
    });

    if (!completeRes.ok)
      throw new Error(
        `Step 3 Failed: ${completeRes.status} ${await completeRes.text()}`
      );
    const result = await completeRes.json();
    console.log("✅ Flow Complete:", result);
  } catch (error) {
    console.error("\n❌ Error:", error.message);
  }
}

run();
