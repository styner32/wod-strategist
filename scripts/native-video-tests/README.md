# Experimental iOS continuous recording checks

Run `scripts/native-video-tests/run-segmented-smoke.sh` on a Mac with Xcode and local AVFoundation codec services available. A restrictive execution sandbox can reject AAC even for an ordinary file writer. The tests use synthetic buffers and create their results in a printed `/private/tmp/wod-segmented-smoke-*` directory; no camera, microphone, network, or gallery permission is used.

The harness compiles the actual patched `SegmentedRecordingSession.swift` and verifies:

- Audio startup waits for delivered PCM, derives AAC settings when MP4 recommendations are missing, preserves 48kHz mono/44.1kHz stereo settings, and handles stop-before-start, timeout, duplicate/late callbacks and retry. Recording fixtures encode using that same missing-recommendation fallback. Synthetic tests do not exercise a physical microphone or iOS capture-session configuration.
- PCM contains a continuous deterministic chirp (220 Hz + 30 Hz/s); a second 3.1-second run uses 660 Hz + 30 Hz/s for audible and measurable run-boundary validation.
- A 21.1-second H264/AAC run produces three standalone analysis MP4s, with a short final tail and frame extraction at exactly zero.
- Original video sample count equals the sum of all analysis samples; their compressed SHA-256 payloads also match in order. Analysis packaging therefore adds no video reencoding in this fixture.
- An analysis packaging failure preserves the original and emits failure metadata with capture timestamps.
- Missing post-stop buffers reach a bounded tail timeout, preserve received samples, and explicitly mark the capture incomplete.
- A 24 fps sample with unavailable duration uses the configured native capture interval for the last frame, rather than assuming 30 fps.
- Source finalization releases capture/audio before deferred analysis completion; the old completion cannot clear a resumed run.
- Native legacy `RecordingSession` receives duplicate stop requests with zero or ten source frames; completion occurs once, and the nonempty recording remains readable. The test compiles its actual writer/track/timeline sources with minimal unrelated UI glue.
- JS terminal replay delivers any missing segment exactly once before finished, and ignores stale events/flash cleanup.

The recorder uses Apple HLS profile with a positive media timestamp origin to preserve AAC priming. CMAF rejects multiplexed audio/video on the tested runtime. AVURLAsset normalizes isolated fragment timestamps; analysis remux uses the asset track range, while external capture metadata uses native segment PTS with the media origin removed.

The original `.mp4` is written once from the encoder output. Its adjacent `.mp4.segments.json` records committed byte ranges and the capture clock mapping. Analysis MP4s are derivatives under `analysis/`. Source finalization can occur before their preparation is drained; `VideoFile.segments` is the terminal delivery fallback. Each entry in terminal `VideoFile.segments` includes `isLast`; live preparation events may omit it. Callback replay still deduplicates by index, so marking the last fragment does not enqueue it again.

This synthetic validation does **not** establish real iPhone continuity, stabilization delay, frame-processor pressure, audio interruptions, HDR/HEVC compatibility, thermal behavior, 30-minute memory/disk behavior, sensor timing, or Photos saving. Keep the feature disabled until device testing covers those conditions and compares original frame/sample timelines across every segment boundary. `droppedVideoFrames`, `droppedAudioBuffers`, and `tailDrainTimedOut` must all be checked; unexpected drops, absent media, or a timeout require preserving the original for review instead of labeling it complete.
