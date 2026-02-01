# Wod Strategist

## Get started

1. Install dependencies

   ```bash
   npm install
   ```

2. Start the app

   ```bash
   npx expo start
   ```

   ```bash
   npx expo prebuild --clean
   ```

   ```bash
   npx expo run:ios --device
   ```

3. Test uploading video to API

   ```bash
   curl -X POST http://localhost:8088/api/v1/upload -F "session_id=workout-session-002" -F "file=@./tmp/wod_1.MP4"
   ```
