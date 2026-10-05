# Original-preserving local merge checks

Run the small macOS corpus with Xcode, ffmpeg, and AVFoundation codec services available:

```sh
python3 modules/video-merger/tests/verify_passthrough.py
```

A restrictive execution sandbox may block Apple's audio decoder. Run the same test with access to the local media services; no camera, microphone, network, or Photos permission is used.

For actual output from the continuous recorder, first run `scripts/native-video-tests/run-segmented-smoke.sh`. It prints a temporary fixture directory containing a 21.1-second chirp run and a distinct 3.1-second chirp run. Supply both paths in recording order:

```sh
python3 modules/video-merger/tests/verify_passthrough.py \
  --native-source /private/tmp/wod-segmented-smoke-EXAMPLE/original.mp4 \
  --native-source /private/tmp/wod-segmented-smoke-EXAMPLE/second-run/original.mp4
```

The corpus checks:

- Normal H264/AAC MP4 and legacy MOV chunks, valid single-frame video tails, same-orientation rotated inputs, PCM MOV fallback, and delayed PCM audio gaps.
- Every successful video has the exact compressed video payloads in order. AAC fixtures also compare raw compressed packet hashes, including priming packets. LPCM packet boundaries may change, so ordinary PCM fixtures compare the complete decoded sample stream instead.
- Production validation separately checks codec descriptions, dimensions, transform, first/end track timing, video sample coverage, and canonical decoded float PCM against the source composition. It hashes streaming buffers and timeline silence; only IEEE +0/-0 is normalized. There is no amplitude tolerance, resampling, or output encoding.
- The native two-run fixture exercises nonzero audio and the pause boundary. Silence-only fixtures cannot establish waveform alignment. The validated chirp pair has 726 video and 1,140 AAC packets, retained exactly, with 1,163,136 presented audio frames at 48 kHz (24.232 seconds).
- Missing, zero-byte, corrupt and differently oriented inputs reject the entire merge. Audio-free inputs at the start, middle or end retain their video and leave the corresponding audio interval silent. The ffmpeg `empty_moov` AAC and unsupported coalesced sub-tick edge fixtures reject when preservation cannot be verified. Each rejection retains the prior output, and all source files remain byte-identical after every test.
- All temporary sibling artifacts are removed. A validated artifact replaces the prior result only by a same-directory atomic rename. The wrapper uses the returned actual MP4/MOV URI.

`AVAssetExportSession` can round movie/edit-list boundaries to its chosen clock (600 ticks in the native HLS case), losing audio tail samples or moving the next run. `PassthroughMovieTimeline` preserves exact durations and restores verified sub-tick empty edits that disappeared during export. Growing metadata is relocated to EOF while the old movie atom becomes free space; media bytes and offsets remain fixed. The metadata regression covers both leading and trailing `moov`, reconstructing a 62.5us gap and checking media-offset stability. Unsupported or ambiguous mappings still reject, and resulting media must pass validation before publication.

For camera MOVs with preroll/edit lists, compare AVFoundation-readable compressed samples and decoded frames against the source composition. Counting every raw container packet or using a different player's edit-list policy can give a different total. The iPhone follow-up compared all 1,111 reference-composition/output NV12 frames and timestamps exactly; the ordered 1,133 compressed video samples and presented PCM also matched. These private device recordings are not committed as fixtures.

AAC encoded packet counts alone are not a waveform-preservation test: packets may contain priming/padding, and dropping one can alter the decoded start even when duration is unchanged. The output paths never remove AAC trim metadata to force packet counts. The compressed-reader/writer fallback is accepted only when the complete presented waveform and timing remain exact; otherwise the previous output and all sources are retained.

For the Android timeline unit checks, compile `android/src/main/java/expo/modules/videomerger/MergeTimeline.kt` together with `android/tests/MergeTimelineTest.kt` using Kotlin and run the resulting jar. These cover repeated chunk boundaries, nonzero timestamps, variable-frame-rate and one-frame tails, and invalid/overflow durations. Android's final media sample uses an explicit EOS endpoint instead of `lastPTS + 1ms`.

These are synthetic macOS and pure timing checks. They do not establish iPhone/Android device behavior, Photos compatibility, long-session resource use, HDR/HEVC coverage, background interruptions, or sensor synchronization. Run the corresponding device acceptance checks before enabling continuous capture.
