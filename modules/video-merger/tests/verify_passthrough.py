"""macOS AVFoundation/ffmpeg integration checks; outputs stay in a temporary directory.
Run: python3 modules/video-merger/tests/verify_passthrough.py
This is not iPhone/Android device acceptance.
"""
import hashlib
import argparse
import json
import pathlib
import subprocess
import struct
import tempfile
import urllib.parse

ROOT = pathlib.Path(__file__).resolve().parents[3]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--native-source", action="append", default=[], type=pathlib.Path,
                    help="Native segmented-recorder original; repeat in capture order to verify pause/run seams")
options = parser.parse_args()


def run(*args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"{args[0]} failed: {result.stderr}")
    return result.stdout


def hashes(path, stream):
    # AAC edit-list preroll can make ffprobe present the same packet more than
    # once. Inspect raw stored samples to compare the compressed payload bytes.
    data = json.loads(run("ffprobe", "-v", "error", "-ignore_editlist", "1", "-select_streams", stream,
                          "-show_packets", "-show_data_hash", "sha256", "-of", "json", str(path)))
    return [packet["data_hash"] for packet in data.get("packets", [])]


with tempfile.TemporaryDirectory(prefix="wod-merger-") as temporary:
    work = pathlib.Path(temporary)
    main = work / "main.swift"
    main.write_text('''import Foundation
 do {
 let result = try PassthroughVideoMerger.merge(inputPaths: Array(CommandLine.arguments.dropFirst(2)), outputPath: CommandLine.arguments[1])
 print(String(data: try JSONSerialization.data(withJSONObject: result), encoding: .utf8)!)
 } catch { fputs("\\(error)\\n", stderr); exit(1) }
''')
    binary = work / "merge"
    run("swiftc", "-suppress-warnings", "-module-cache-path", "/private/tmp/wod-merger-swift-cache",
        str(ROOT / "modules/video-merger/ios/PassthroughVideoMerger.swift"),
        str(ROOT / "modules/video-merger/ios/PassthroughMovieTimeline.swift"), str(main), "-o", str(binary))

    def fixture(name, audio=True, duration="0.5", extras=(), audio_codec="aac", audio_duration=None):
        dest = work / name
        command = ["ffmpeg", "-v", "error", "-f", "lavfi", "-i", f"testsrc2=size=160x120:rate=30:duration={duration}"]
        if audio:
            command += ["-f", "lavfi", "-i", f"sine=frequency=440:sample_rate=48000:duration={audio_duration or duration}", "-c:a", audio_codec]
        command += ["-c:v", "libx264", "-preset", "ultrafast", *extras, "-y", str(dest)]
        run(*command)
        return dest

    av = fixture("normal.mp4")
    legacy_mov = fixture("legacy.mov")
    fragmented = fixture("fragmented.mp4", extras=("-movflags", "frag_keyframe+empty_moov"))
    video = fixture("video.mp4", audio=False)
    tail = fixture("tail.mp4", audio=False, duration="0.033333")
    pcm = fixture("pcm.mov", audio_codec="pcm_s16le")
    subtick = fixture("subtick.mov", audio_duration="0.5000625", extras=("-movie_timescale", "48000"))
    delayed = work / "delayed.mov"
    run("ffmpeg", "-v", "error", "-i", str(pcm), "-itsoffset", "0.1", "-i", str(pcm),
        "-map", "0:v", "-map", "1:a", "-c", "copy", "-y", str(delayed))
    rotated = work / "rotated.mp4"
    run("ffmpeg", "-v", "error", "-display_rotation:v:0", "90", "-i", str(av), "-c", "copy", "-y", str(rotated))
    rotation = json.loads(run("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream_side_data", "-of", "json", str(rotated)))
    assert any(abs(entry.get("rotation", 0)) == 90 for entry in rotation["streams"][0].get("side_data_list", [])), rotation
    corrupt = work / "corrupt.mp4"
    corrupt.write_bytes(b"not a movie" * 100)
    empty = work / "empty.mp4"
    empty.touch()
    source_hashes = {p: hashlib.sha256(p.read_bytes()).digest() for p in [av, legacy_mov, fragmented, video, tail, pcm, delayed, rotated, corrupt, empty, subtick]}
    source_hashes.update({p: hashlib.sha256(p.read_bytes()).digest() for p in options.native_source})

    def merge(name, sources, expected_suffix=".mp4"):
        result = json.loads(run(str(binary), str(work / f"result-{name}.mp4"), *map(str, sources)))
        output = pathlib.Path(urllib.parse.unquote(urllib.parse.urlparse(result["outputPath"]).path))
        assert result["inputCount"] == len(sources) and output.suffix == expected_suffix, result
        pcm_audio = sources[0] in [pcm, delayed]
        for stream in ["v:0"] if pcm_audio else ["v:0", "a:0"]:
            assert hashes(output, stream) == sum((hashes(source, stream) for source in sources), []), (name, stream, "compressed packets changed")
        if sources[0] == pcm:
            # LPCM packet grouping is a container choice, not an audio change.
            def waveform(path):
                return subprocess.check_output(["ffmpeg", "-v", "error", "-copyts", "-i", str(path), "-map", "0:a:0",
                                                "-af", "aresample=async=1:first_pts=0",
                                                "-c:a", "pcm_s16le", "-f", "s16le", "-"])
            assert waveform(output) == b"".join(waveform(source) for source in sources), "LPCM samples changed"
        if sources[0] == delayed:
            # AVFoundation's canonical PCM validation includes edit-list silence.
            # ffmpeg's raw sink applies a different gap-filling policy, so its
            # raw byte stream is not a reference for this delayed-audio case.
            assert abs(result["durationSeconds"] - 1.2) < 1e-9
        run("ffmpeg", "-v", "error", "-i", str(output), "-f", "null", "-")
        print(f"PASS {name}: exact video/audio payloads, presented PCM validation, decode, {result['durationSeconds']:.6f}s")
        return output

    good = merge("normal", [av, av])
    merge("legacy-mov", [legacy_mov, legacy_mov], ".mov")
    merge("micro-tail", [video, tail])
    merge("mov-fallback", [pcm, pcm], ".mov")
    merge("nonzero-audio-start", [delayed, delayed], ".mov")
    merge("rotation-preserved", [rotated, rotated])
    merge("no-audio-first", [video, av])
    merge("no-audio-middle", [av, video, av])
    merge("no-audio-last", [av, video])
    for index, source in enumerate(options.native_source):
        merge(f"native-run-{index}", [source])
    if len(options.native_source) > 1:
        merge("native-pause-seam", options.native_source)
    for bad in [work / "missing.mp4", empty, corrupt, rotated]:
        before = good.read_bytes()
        result = subprocess.run([str(binary), str(good), str(av), str(bad)], capture_output=True)
        assert result.returncode != 0, f"Accepted invalid input: {bad.name}"
        assert good.read_bytes() == before, f"Previous result modified: {bad.name}"
        print(f"PASS rejected {bad.name}; previous output retained")
    # This ffmpeg empty_moov fixture exposes inconsistent AAC edit/priming
    # ranges to AVFoundation. Neither supported passthrough container preserves
    # its presented waveform, so success would be data loss.
    before = good.read_bytes()
    unsupported = subprocess.run([str(binary), str(good), str(fragmented), str(fragmented)], capture_output=True)
    assert unsupported.returncode != 0 and good.read_bytes() == before
    assert b"audio" in unsupported.stderr, unsupported.stderr
    print("PASS inconsistent fragmented AAC rejected; previous output retained")
    unsupported = subprocess.run([str(binary), str(good), str(subtick), str(subtick)], capture_output=True)
    assert unsupported.returncode != 0 and good.read_bytes() == before
    print("PASS unsupported coalesced sub-tick edits rejected; previous output retained")
    for path, digest in source_hashes.items():
        assert path.exists() and hashlib.sha256(path.read_bytes()).digest() == digest, path
    assert not list(work.glob(".merge-*")), "Temporary merge files leaked"
    print("PASS every source retained byte-for-byte; no temporary artifacts")

    # Exercise the exact metadata shape observed on the iPhone: two media
    # edits with a 62.5us gap omitted by the exporter's 600-tick movie clock.
    main.write_text('''import AVFoundation
import Foundation
do {
 let asset = AVURLAsset(url: URL(fileURLWithPath: CommandLine.arguments[1]))
 let source = asset.tracks(withMediaType: .video)[0]
 let composition = AVMutableComposition()
 let track = composition.addMutableTrack(withMediaType: .video, preferredTrackID: kCMPersistentTrackID_Invalid)!
 try track.insertTimeRange(source.timeRange, of: source, at: .zero)
 try track.insertTimeRange(source.timeRange, of: source, at: source.timeRange.duration + CMTime(value: 1, timescale: 16000))
 try PassthroughMovieTimeline.preserve(composition, in: URL(fileURLWithPath: CommandLine.arguments[2]))
} catch { fputs("\\(error)\\n", stderr); exit(1) }
''')
    unit = work / "timeline"
    run("swiftc", "-suppress-warnings", "-module-cache-path", "/private/tmp/wod-merger-swift-cache",
        str(ROOT / "modules/video-merger/ios/PassthroughMovieTimeline.swift"), str(main), "-o", str(unit))

    def atom(kind, body):
        return struct.pack(">I4s", len(body) + 8, kind) + body

    def children(data):
        position = 0
        while position < len(data):
            size, kind = struct.unpack_from(">I4s", data, position)
            assert 8 <= size <= len(data) - position
            yield kind, data[position + 8:position + size]
            position += size

    edits = b"\0" * 4 + struct.pack(">I", 2) + b"".join(struct.pack(">IiI", 300, start, 65536) for start in [0, 300])
    track_box = atom(b"trak", atom(b"tkhd", b"\0" * 20 + struct.pack(">I", 600)) +
                     atom(b"mdia", atom(b"hdlr", b"\0" * 8 + b"vide")) + atom(b"edts", atom(b"elst", edits)))
    movie = atom(b"moov", atom(b"mvhd", b"\0" * 12 + struct.pack(">II", 600, 600)) + track_box)
    media = atom(b"mdat", b"encoded-payload-sentinel")
    for movie_first in [False, True]:
        timeline = work / f"timeline-{movie_first}.mov"
        original = movie + media if movie_first else media + movie
        timeline.write_bytes(original)
        run(str(unit), str(video), str(timeline))
        corrected = timeline.read_bytes()
        assert corrected.index(media) == original.index(media), "Media bytes/offset changed"
        boxes = dict(children(corrected))
        movie_boxes = dict(children(boxes[b"moov"]))
        assert struct.unpack_from(">II", movie_boxes[b"mvhd"], 12) == (16000, 16001)
        track_boxes = dict(children(movie_boxes[b"trak"]))
        edit_list = dict(children(track_boxes[b"edts"]))[b"elst"]
        assert struct.unpack_from(">I", edit_list, 4)[0] == 3
        assert [struct.unpack_from(">IiI", edit_list, 8 + 12*i) for i in range(3)] == [
            (8000, 0, 65536), (1, -1, 65536), (8000, 300, 65536)]
    print("PASS omitted sub-tick gap restored; media bytes/offsets unchanged with leading/trailing moov")
