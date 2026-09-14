# Chunk finalization and local merge

- `react-native-vision-camera` iOS `stopRecording()` resolves after requesting a stop, before `finishWriting` calls `onRecordingFinished`. Do not use `isChunkRecordingActive === false` as proof that the last file has arrived.
- Each recording owns `createChunkRecordingCompletion()`. Timer-stop and workout stop/pause share its promise and issue one native stop. Keep `pendingChunk` until `onRecordingFinished` or `onRecordingError`; register the file path and upload counters before resolving the completion.
- A timer-stopped chunk is still the final legitimate chunk if the user stops before its callback. Set `finalChunkPending` for any pending completion, even after native stop was requested.
- The completion keeps the existing 5,000 ms safety limit, measured from the first stop request. A missing/late callback beyond that limit is not proof of a complete recording. Recording errors and deliberately discarded micro chunks resolve with `null`.
- Preserve existing micro-chunk filtering: media duration below 0.2 seconds or capture wall time below 200 ms is discarded before local merge/upload. Zero-byte filtering and native stream validation address different cases from callback ordering.

## Verification (2026-09-09)

- Deterministic Jest regression withholds the final callback after timer-triggered native stop resolves, requests user stop, and verifies merge remains blocked until the final path is registered. It also verifies one native stop, error/discard completion, and the timeout boundary.
- `npm test -- --runInBand features/wod/__tests__/chunkRecordingCompletion.test.ts features/wod/__tests__/mergeChunksLocal.test.ts`: 14 tests passed. `npm run typecheck`: passed.
- Original phone file `7313D478-397F-484B-900B-2B975F33D1B4.mov` contains two HEVC frames (ffprobe duration 0.066667 seconds), but AVFoundation reports zero asset and video-track duration. It is nonempty and decodes with ffmpeg, so a zero-byte check alone cannot reject it.
- On macOS, a harness extracted from the exact `d7ea80d` native merge implementation fails with `AVFoundationErrorDomain -11800` / underlying `NSOSStatusErrorDomain -12780` for that partial alone and for a normal chunk followed by it. The normal chunk alone succeeds. The working-tree implementation skips the zero-duration partial and successfully merges the same pair; its exported video decodes without errors.
- The micro-chunk/native filters were already uncommitted at the start of this investigation; the final-callback coordination is an additional fix. Native reproduction on macOS and Jest wrapper tests do not establish rebuilt iPhone acceptance. The 5-second late-callback limit also remains.
