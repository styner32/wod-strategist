package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("NewMergeChunksTask", func() {

	It("creates a task with the correct payload fields and minute-truncated MergeRequestedAt", func() {
		task, err := NewMergeChunksTask(
			"session-1",
			"gs://bucket/videos/session-1",
			"wod",
			[]string{"Deadlift", "Pull-up"},
			[]string{"Knee"},
			42,
			false,
			"",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(task.Type()).To(Equal(TypeMergeChunks))

		var payload VideoAnalysisPayload
		Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
		Expect(payload.SessionID).To(Equal("session-1"))
		Expect(payload.FilePath).To(Equal("gs://bucket/videos/session-1"))
		Expect(payload.WorkoutType).To(Equal(WorkoutTypeWOD))
		Expect(payload.Movements).To(Equal([]string{"Deadlift", "Pull-up"}))
		Expect(payload.Injuries).To(Equal([]string{"Knee"}))
		Expect(payload.ProfileID).To(Equal(uint(42)))
		Expect(payload.MergeRequestedAt.Second()).To(Equal(0))
		Expect(payload.MergeRequestedAt.Nanosecond()).To(Equal(0))
	})
})

var _ = Describe("chunksNeedNormalization and mergeDownloadedChunks", func() {
	DescribeTable("detects stream compatibility for FFmpeg concat",
		func(profiles []chunkStreamProfile, wantNorm bool, wantW, wantH int, wantAudio bool) {
			gotNorm, gotW, gotH, gotAudio := chunksNeedNormalization(profiles)
			Expect(gotNorm).To(Equal(wantNorm))
			Expect(gotW).To(Equal(wantW))
			Expect(gotH).To(Equal(wantH))
			Expect(gotAudio).To(Equal(wantAudio))
		},
		Entry("homogeneous compressed mobile chunks skip normalization",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, DisplayWidth: 406, DisplayHeight: 720, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, DisplayWidth: 406, DisplayHeight: 720, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
			},
			false, 0, 0, true,
		),
		Entry("mixed H.264 406x720 44.1kHz stereo + rotated HEVC 1280x720 48kHz mono normalizes to 720x1280",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, Rotation: 0, DisplayWidth: 406, DisplayHeight: 720, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
				{VideoCodec: "hevc", PixFmt: "yuv420p", Width: 1280, Height: 720, Rotation: 270, DisplayWidth: 720, DisplayHeight: 1280, HasAudio: true, AudioCodec: "aac", SampleRate: 48000, Channels: 1},
			},
			true, 720, 1280, true,
		),
		Entry("missing audio on one chunk triggers normalization with silent audio fill",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, DisplayWidth: 406, DisplayHeight: 720, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, DisplayWidth: 406, DisplayHeight: 720, HasAudio: false},
			},
			true, 406, 720, true,
		),
		Entry("4K raw fallback caps normalized long edge to 1280 with even dimensions",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, DisplayWidth: 406, DisplayHeight: 720, HasAudio: false},
				{VideoCodec: "hevc", PixFmt: "yuv420p", Width: 2160, Height: 3840, DisplayWidth: 2160, DisplayHeight: 3840, HasAudio: false},
			},
			true, 720, 1280, false,
		),
		Entry("mismatched video timescales on otherwise identical H.264 chunks trigger normalization",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, DisplayWidth: 406, DisplayHeight: 720, VideoTimeBase: "1/600", VideoTimescale: 600, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, DisplayWidth: 406, DisplayHeight: 720, VideoTimeBase: "1/15360", VideoTimescale: 15360, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
			},
			true, 406, 720, true,
		),
	)

	DescribeTable("parses MP4/MOV video stream timescale",
		func(timeBase string, want int) {
			Expect(parseStreamTimescale(timeBase)).To(Equal(want))
		},
		Entry("iOS Core Media 600 timescale", "1/600", 600),
		Entry("Android MediaCodec 90000 timescale", "1/90000", 90000),
		Entry("FFmpeg default 30fps libx264 15360 timescale", "1/15360", 15360),
		Entry("canonical normalized 30000 timescale", "1/30000", 30000),
		Entry("non-unit numerator requires normalization", "1001/30000", 0),
		Entry("empty time_base", "", 0),
		Entry("malformed time_base", "invalid", 0),
	)

	DescribeTable("checks in-place leading black trim eligibility",
		func(profiles []chunkStreamProfile, want bool) {
			Expect(canTrimInPlaceForConcat(profiles)).To(Equal(want))
		},
		Entry("matching positive video timescale on even H.264/AAC chunks allows in-place trim",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, VideoTimeBase: "1/600", VideoTimescale: 600, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, VideoTimeBase: "1/600", VideoTimescale: 600, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2},
			},
			true,
		),
		Entry("unknown zero video timescale forbids in-place trim",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, VideoTimescale: 0, HasAudio: false},
			},
			false,
		),
		Entry("mismatched video timescales forbid in-place trim",
			[]chunkStreamProfile{
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, VideoTimescale: 600, HasAudio: false},
				{VideoCodec: "h264", PixFmt: "yuv420p", Width: 406, Height: 720, VideoTimescale: 90000, HasAudio: false},
			},
			false,
		),
	)

	It("merges mixed H.264/44.1kHz-stereo and rotated HEVC/48kHz-mono chunks without dropping frames or inflating duration", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not installed, skipping")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tmpDir, err := os.MkdirTemp("", "merge-heterogeneous-test")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		chunk0 := filepath.Join(tmpDir, "chunk_000.mp4")
		out0, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "testsrc2=size=406x720:rate=30",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
			"-t", "2",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-ar", "44100", "-ac", "2",
			"-y", chunk0,
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out0))

		secondCodec := "mpeg4"
		if encOut, encErr := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-encoders").Output(); encErr == nil && strings.Contains(string(encOut), "libx265") {
			secondCodec = "libx265"
		}

		rawChunk1 := filepath.Join(tmpDir, "raw_chunk_001.mp4")
		out1, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
			"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000",
			"-t", "2",
			"-c:v", secondCodec, "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-ar", "48000", "-ac", "1",
			"-y", rawChunk1,
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out1))

		chunk1 := filepath.Join(tmpDir, "chunk_001.mp4")
		rotOut, rotErr := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
			"-display_rotation", "-90", "-i", rawChunk1,
			"-c", "copy", "-y", chunk1,
		).CombinedOutput()
		if rotErr != nil {
			// Fallback for older FFmpeg builds without -display_rotation
			rotOut, rotErr = exec.CommandContext(ctx, "ffmpeg", "-v", "error",
				"-i", rawChunk1, "-c", "copy", "-metadata:s:v:0", "rotate=90", "-y", chunk1,
			).CombinedOutput()
			Expect(rotErr).NotTo(HaveOccurred(), string(rotOut))
		}
		_ = os.Remove(rawChunk1)

		objects := []string{
			"gs://b/videos/1/s/chunk_000.mp4",
			"gs://b/videos/1/s/chunk_001.mp4",
		}
		localPaths := []string{chunk0, chunk1}
		mergedPath := filepath.Join(tmpDir, "merged.mp4")

		intervals, intervalErr, mergeErr := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, objects, localPaths, mergedPath)
		Expect(mergeErr).NotTo(HaveOccurred())
		Expect(intervalErr).NotTo(HaveOccurred())
		Expect(intervals).To(HaveLen(2))
		Expect(intervals[0].Start).To(Equal(0.0))
		Expect(intervals[0].End).To(BeNumerically("~", 2.0, 0.15))
		Expect(intervals[1].Start).To(Equal(intervals[0].End))
		Expect(intervals[1].End).To(BeNumerically("~", 4.0, 0.25))

		mergedDuration := probeVideoDuration(ctx, mergedPath)
		Expect(mergedDuration).To(BeNumerically("~", intervals[1].End, 0.2))

		// Verify all video frames from BOTH chunks survived (~120 frames at 30fps across 4s).
		frameOut, err := exec.CommandContext(ctx, "ffprobe", "-v", "error",
			"-select_streams", "v:0", "-count_frames",
			"-show_entries", "stream=nb_read_frames",
			"-of", "default=noprint_wrappers=1:nokey=1",
			mergedPath,
		).Output()
		Expect(err).NotTo(HaveOccurred())
		frameCount, err := strconv.Atoi(strings.TrimSpace(string(frameOut)))
		Expect(err).NotTo(HaveOccurred())
		Expect(frameCount).To(BeNumerically(">=", 115))
	})

	DescribeTable("parses leading black trim seconds from blackdetect output",
		func(ffmpegOut string, want float64) {
			Expect(parseLeadingBlackTrimSecs(ffmpegOut)).To(BeNumerically("~", want, 0.0005))
		},
		Entry("1-frame 30fps black flash at chunk start",
			"[blackdetect @ 0x1] black_start:0 black_end:0.0333333 black_duration:0.0333333",
			0.032333,
		),
		Entry("2-frame 30fps black flash at chunk start",
			"[blackdetect @ 0x1] black_start:0 black_end:0.0666667 black_duration:0.0666667",
			0.065667,
		),
		Entry("genuinely dark scene spanning probe window is never trimmed",
			"[blackdetect @ 0x1] black_start:0 black_end:0.35 black_duration:0.35",
			0.0,
		),
		Entry("mid-chunk dark flash after bright start is never trimmed",
			"[blackdetect @ 0x1] black_start:0.10 black_end:0.133333 black_duration:0.033333",
			0.0,
		),
		Entry("no blackdetect lines",
			"frame=   11 fps=0.0 q=-0.0 Lsize=N/A time=00:00:00.35",
			0.0,
		),
	)

	It("strips a synthetic 1-frame leading black flash at a chunk seam while leaving genuinely dark chunks intact", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not installed, skipping")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tmpDir, err := os.MkdirTemp("", "merge-black-trim-test")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		chunk0 := filepath.Join(tmpDir, "chunk_000.mp4")
		out0, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "color=c=white:size=406x720:rate=30:d=2.0",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:d=2.0",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-ar", "44100", "-ac", "2",
			"-y", chunk0,
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out0))

		// Chunk 1 reproduces the 10.033333s mobile segment boundary artifact:
		// 1 digital black frame (1/30s = 0.033333s) followed by 2.0s bright video.
		chunk1 := filepath.Join(tmpDir, "chunk_001.mp4")
		out1, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "color=c=black:size=406x720:rate=30:d=0.033333",
			"-f", "lavfi", "-i", "color=c=white:size=406x720:rate=30:d=2.0",
			"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=44100:d=2.033333",
			"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]",
			"-map", "[v]", "-map", "2:a",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-ar", "44100", "-ac", "2",
			"-y", chunk1,
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out1))
		Expect(probeLeadingBlackTrimSecs(ctx, chunk1)).To(BeNumerically("~", 0.0323, 0.005))

		// Verify a genuinely dark 2.0s chunk is NOT scheduled for trimming.
		darkChunk := filepath.Join(tmpDir, "dark_only.mp4")
		outDark, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "color=c=black:size=406x720:rate=30:d=2.0",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-y", darkChunk,
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(outDark))
		Expect(probeLeadingBlackTrimSecs(ctx, darkChunk)).To(Equal(0.0))

		mergedPath := filepath.Join(tmpDir, "merged.mp4")
		intervals, intervalErr, mergeErr := mergeDownloadedChunks(
			ctx,
			zap.NewNop(),
			tmpDir,
			[]string{"gs://b/videos/1/s/chunk_000.mp4", "gs://b/videos/1/s/chunk_001.mp4"},
			[]string{chunk0, chunk1},
			mergedPath,
		)
		Expect(mergeErr).NotTo(HaveOccurred())
		Expect(intervalErr).NotTo(HaveOccurred())
		Expect(intervals).To(HaveLen(2))
		Expect(intervals[1].End - intervals[1].Start).To(BeNumerically("~", 2.0, 0.05))

		// Entire merged video must have zero black flashes across the seam.
		bdOut, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "info",
			"-i", mergedPath,
			"-vf", "blackdetect=d=0.015:pix_th=0.05:pic_th=0.98",
			"-an", "-f", "null", "-",
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(bdOut))
		Expect(string(bdOut)).NotTo(ContainSubstring("black_start:"))
	})

	DescribeTable("preserves concat duration across native mobile timescales when only later chunks are trimmed in place",
		func(sourceTimescale int) {
			if !hasFfmpeg() {
				Skip("ffmpeg not installed, skipping")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tmpDir, err := os.MkdirTemp("", "merge-timescale-trim-test")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(tmpDir)

			timescaleArg := strconv.Itoa(sourceTimescale)

			// Chunk 0: untrimmed native timescale (600 on iOS Core Media, 90000 on Android MediaCodec).
			chunk0 := filepath.Join(tmpDir, "chunk_000.mp4")
			out0, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
				"-f", "lavfi", "-i", "color=c=white:size=406x720:rate=30:d=2.0",
				"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:d=2.0",
				"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
				"-video_track_timescale", timescaleArg,
				"-c:a", "aac", "-ar", "44100", "-ac", "2",
				"-y", chunk0,
			).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out0))

			// Chunk 1: same native timescale with a leading black flash so only chunk 1 is trimmed in place.
			chunk1 := filepath.Join(tmpDir, "chunk_001.mp4")
			out1, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
				"-f", "lavfi", "-i", "color=c=black:size=406x720:rate=30:d=0.10",
				"-f", "lavfi", "-i", "color=c=white:size=406x720:rate=30:d=2.0",
				"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=44100:d=2.10",
				"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]",
				"-map", "[v]", "-map", "2:a",
				"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
				"-video_track_timescale", timescaleArg,
				"-c:a", "aac", "-ar", "44100", "-ac", "2",
				"-y", chunk1,
			).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out1))

			prof0, err := probeChunkStreamProfile(ctx, chunk0)
			Expect(err).NotTo(HaveOccurred())
			Expect(prof0.VideoTimescale).To(Equal(sourceTimescale))

			mergedPath := filepath.Join(tmpDir, "merged.mp4")
			localPaths := []string{chunk0, chunk1}
			intervals, intervalErr, mergeErr := mergeDownloadedChunks(
				ctx,
				zap.NewNop(),
				tmpDir,
				[]string{"gs://b/videos/1/s/chunk_000.mp4", "gs://b/videos/1/s/chunk_001.mp4"},
				localPaths,
				mergedPath,
			)
			Expect(mergeErr).NotTo(HaveOccurred())
			Expect(intervalErr).NotTo(HaveOccurred())
			Expect(intervals).To(HaveLen(2))
			Expect(intervals[1].End).To(BeNumerically("~", 4.0, 0.15))

			// Trimmed intermediate chunk must preserve the exact source timescale before concat.
			profTrimmed, err := probeChunkStreamProfile(ctx, localPaths[1])
			Expect(err).NotTo(HaveOccurred())
			Expect(profTrimmed.VideoTimescale).To(Equal(sourceTimescale))

			// Merged video must match cumulative intervals (~4.0s, neither 25.6x inflated nor 0.17x collapsed)
			// and carry the canonical 30000 video timescale.
			mergedDur := probeVideoDuration(ctx, mergedPath)
			Expect(mergedDur).To(BeNumerically("~", intervals[1].End, 0.15))

			mergedProf, err := probeChunkStreamProfile(ctx, mergedPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(mergedProf.VideoTimescale).To(Equal(canonicalConcatVideoTimescale))

			frameOut, err := exec.CommandContext(ctx, "ffprobe", "-v", "error",
				"-select_streams", "v:0", "-count_frames",
				"-show_entries", "stream=nb_read_frames",
				"-of", "default=noprint_wrappers=1:nokey=1",
				mergedPath,
			).Output()
			Expect(err).NotTo(HaveOccurred())
			frameCount, err := strconv.Atoi(strings.TrimSpace(string(frameOut)))
			Expect(err).NotTo(HaveOccurred())
			Expect(frameCount).To(BeNumerically(">=", 115))
			Expect(frameCount).To(BeNumerically("<=", 130))
		},
		Entry("iOS Core Media 600 timescale (prevents 25.6x duration inflation)", 600),
		Entry("Android MediaCodec 90000 timescale (prevents 0.17x fast-forward collapse)", 90000),
	)

	It("normalizes once and stream-copies when the majority of iOS 600-timescale chunks have leading black frames", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not installed, skipping")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tmpDir, err := os.MkdirTemp("", "merge-bulk-trim-test")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		objects := make([]string, 4)
		localPaths := make([]string, 4)
		for i := 0; i < 4; i++ {
			chunkPath := filepath.Join(tmpDir, "chunk_"+strconv.Itoa(i)+".mp4")
			objects[i] = "gs://b/videos/1/s/chunk_" + strconv.Itoa(i) + ".mp4"
			localPaths[i] = chunkPath
			if i == 0 {
				out, cmdErr := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
					"-f", "lavfi", "-i", "color=c=white:size=406x720:rate=30:d=1.5",
					"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:d=1.5",
					"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
					"-video_track_timescale", "600",
					"-c:a", "aac", "-ar", "44100", "-ac", "2",
					"-y", chunkPath,
				).CombinedOutput()
				Expect(cmdErr).NotTo(HaveOccurred(), string(out))
			} else {
				out, cmdErr := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
					"-f", "lavfi", "-i", "color=c=black:size=406x720:rate=30:d=0.10",
					"-f", "lavfi", "-i", "color=c=white:size=406x720:rate=30:d=1.5",
					"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=44100:d=1.60",
					"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]",
					"-map", "[v]", "-map", "2:a",
					"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
					"-video_track_timescale", "600",
					"-c:a", "aac", "-ar", "44100", "-ac", "2",
					"-y", chunkPath,
				).CombinedOutput()
				Expect(cmdErr).NotTo(HaveOccurred(), string(out))
			}
		}

		mergedPath := filepath.Join(tmpDir, "merged.mp4")
		intervals, intervalErr, mergeErr := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, objects, localPaths, mergedPath)
		Expect(mergeErr).NotTo(HaveOccurred())
		Expect(intervalErr).NotTo(HaveOccurred())
		Expect(intervals).To(HaveLen(4))
		Expect(intervals[3].End).To(BeNumerically("~", 6.0, 0.2))

		// Verify bulk trim routed through normalization (norm_chunk_*) and canonical 30000 timescale.
		Expect(filepath.Base(localPaths[0])).To(HavePrefix("norm_chunk_"))
		mergedDur := probeVideoDuration(ctx, mergedPath)
		Expect(mergedDur).To(BeNumerically("~", intervals[3].End, 0.2))

		mergedProf, err := probeChunkStreamProfile(ctx, mergedPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(mergedProf.VideoTimescale).To(Equal(canonicalConcatVideoTimescale))

		bdOut, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "info",
			"-i", mergedPath,
			"-vf", "blackdetect=d=0.015:pix_th=0.05:pic_th=0.98",
			"-an", "-f", "null", "-",
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(bdOut))
		Expect(string(bdOut)).NotTo(ContainSubstring("black_start:"))
	})

	It("rejects merged output whose probed duration diverges from cumulative chunk intervals", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not installed, skipping")
		}

		tmpFile := createTinyMP4(GinkgoT())
		ctx := context.Background()

		Expect(verifyMergedDurationMatchesIntervals(ctx, tmpFile, []concatMediaInterval{
			{FilePath: "gs://b/videos/1/s/chunk_000.mp4", Start: 0, End: 1.0},
		})).To(Succeed())

		err := verifyMergedDurationMatchesIntervals(ctx, tmpFile, []concatMediaInterval{
			{FilePath: "gs://b/videos/1/s/chunk_000.mp4", Start: 0, End: 10.0},
		})
		Expect(err).To(MatchError(ContainSubstring("diverges from cumulative chunk duration")))
	})
})

var _ = Describe("listOriginalChunks", func() {
	It("skips non-video session artifacts and retains only .mp4 and .mov files", func() {
		storageTransport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		}

		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/sess-filter-pure/", []string{
			"videos/sess-filter-pure/sensor_telemetry.ndjson",
			"videos/sess-filter-pure/metrics.json",
			"videos/sess-filter-pure/apple_ai_01.json",
			"videos/sess-filter-pure/apple_ai_01_frame_1.jpg",
			"videos/sess-filter-pure/chunk_001.mp4",
			"videos/sess-filter-pure/chunk_002.MOV",
			"videos/sess-filter-pure/merged.mp4",
			"videos/sess-filter-pure/split_chunk_001.mp4",
		})

		chunks, err := w.listOriginalChunks(context.Background(), "gs://test-bucket/videos/sess-filter-pure")
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).To(Equal([]string{
			"gs://test-bucket/videos/sess-filter-pure/chunk_001.mp4",
			"gs://test-bucket/videos/sess-filter-pure/chunk_002.MOV",
		}))
	})

	It("returns empty slice when only non-video artifacts exist", func() {
		storageTransport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		}

		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/sess-only-sensor/", []string{
			"videos/sess-only-sensor/sensor_telemetry.ndjson",
			"videos/sess-only-sensor/summary.json",
			"videos/sess-only-sensor/notes.txt",
			"videos/sess-only-sensor/unsupported.avi",
			"videos/sess-only-sensor/noextension",
		})

		chunks, err := w.listOriginalChunks(context.Background(), "gs://test-bucket/videos/sess-only-sensor")
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).To(BeEmpty())
	})

	It("handles mixed-case extensions and excludes previous merge outputs", func() {
		storageTransport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		}

		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/sess-mixed/", []string{
			"videos/sess-mixed/chunk_001.Mp4",
			"videos/sess-mixed/chunk_002.mOv",
			"videos/sess-mixed/chunk_001_merged_output.mp4",
			"videos/sess-mixed/chunk_001_hardsubbed_output.mp4",
			"videos/sess-mixed/chunk_001_encoded_output.mp4",
		})

		chunks, err := w.listOriginalChunks(context.Background(), "gs://test-bucket/videos/sess-mixed")
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).To(Equal([]string{
			"gs://test-bucket/videos/sess-mixed/chunk_001.Mp4",
			"gs://test-bucket/videos/sess-mixed/chunk_002.mOv",
		}))
	})
})

var _ = Describe("HandleMergeChunksTask", func() {
	var (
		dbConn           *gorm.DB
		storageTransport *testhelpers.MockTransport
		queueClient      *asynq.Client
		inspector        *asynq.Inspector
		w                *Worker
		profileID        uint
	)

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		queueClient = testhelpers.NewQueueClient()
		inspector = testhelpers.NewQueueInspector()
		testhelpers.CleanupQueue(inspector)

		storageTransport = testhelpers.NewMockTransport()
		storageClient, sErr := testhelpers.NewStorageClient("test-bucket", storageTransport)
		Expect(sErr).NotTo(HaveOccurred())

		p := testhelpers.CreateProfile(dbConn, &db.Profile{})
		profileID = p.ID

		w = &Worker{
			DB:             dbConn,
			StorageClient:  storageClient,
			QueueClient:    queueClient,
			QueueInspector: inspector,
			BucketName:     "test-bucket",
			logger:         zap.NewNop(),
		}
	})

	It("returns SkipRetry immediately when the GCS URI is invalid", func() {
		task, err := NewMergeChunksTask(
			"sess-merge-baduri",
			"/not/a/gcs/uri",
			WorkoutTypeWOD, nil, nil, profileID,
			false,
			"",
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleMergeChunksTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("invalid GCS URI")))

		pending, _ := inspector.ListPendingTasks("default")
		Expect(pending).To(BeEmpty())
	})

	It("returns SkipRetry when no chunk objects are found", func() {
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/sess-merge-nochunks/", nil)

		task, err := NewMergeChunksTask(
			"sess-merge-nochunks",
			"gs://test-bucket/videos/sess-merge-nochunks",
			WorkoutTypeWOD, nil, nil, profileID,
			false,
			"",
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleMergeChunksTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("no chunks found")))

		pending, _ := inspector.ListPendingTasks("default")
		Expect(pending).To(BeEmpty())
	})

	It("skips merged/hardsubbed objects when filtering", func() {
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/sess-filter-001/", []string{
			"videos/sess-filter-001/file_merged_abc.mp4",
			"videos/sess-filter-001/file_hardsubbed_abc.mp4",
		})

		task, err := NewMergeChunksTask(
			"sess-filter-001",
			"gs://test-bucket/videos/sess-filter-001",
			WorkoutTypeWOD, nil, nil, profileID,
			false,
			"",
		)
		Expect(err).NotTo(HaveOccurred())

		err = w.HandleMergeChunksTask(context.Background(), task)
		Expect(err).To(MatchError(ContainSubstring("no chunks found")))
	})

	It("skips non-video session artifacts when filtering", func() {
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/sess-filter-002/", []string{
			"videos/sess-filter-002/sensor_telemetry.ndjson",
			"videos/sess-filter-002/chunk_001.mp4",
		})

		chunks, err := w.listOriginalChunks(context.Background(), "gs://test-bucket/videos/sess-filter-002")
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).To(Equal([]string{
			"gs://test-bucket/videos/sess-filter-002/chunk_001.mp4",
		}))
	})

	It("returns waiting error when chunk analysis is still pending and retryCount is high", func() {
		sessionID := "sess-pending-wait"
		testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/"+sessionID+"/", []string{
			"videos/" + sessionID + "/chunk_001.mp4",
			"videos/" + sessionID + "/chunk_002.mp4",
		})

		start0, end0 := 0.0, 10.0
		testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID: sessionID,
			ProfileID: profileID,
			FilePath:  "gs://test-bucket/videos/" + sessionID + "/chunk_001.mp4",
			Status:    "COMPLETED",
			StartSecs: &start0,
			EndSecs:   &end0,
		})

		task, err := NewMergeChunksTask(
			sessionID,
			"gs://test-bucket/videos/"+sessionID,
			WorkoutTypeWOD, nil, nil, profileID,
			false,
			"",
		)
		Expect(err).NotTo(HaveOccurred())

		ctx := WithRetryCount(context.Background(), 20)
		err = w.HandleMergeChunksTask(ctx, task)
		Expect(err).To(MatchError(ContainSubstring("waiting for chunk analysis to complete")))
	})

	It("persists concat-relative media offsets instead of capture-clock timestamps", func() {
		captureStart0, captureEnd0 := 1_000.0, 1_010.0
		captureStart1, captureEnd1 := 2_000.0, 2_010.0
		first := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID: "sess-media-map", ProfileID: profileID,
			FilePath: "gs://test-bucket/videos/sess-media-map/chunk_001.mp4",
			Status:   "COMPLETED", StartSecs: &captureStart0, EndSecs: &captureEnd0,
		})
		second := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
			SessionID: "sess-media-map", ProfileID: profileID,
			FilePath: "gs://test-bucket/videos/sess-media-map/chunk_002.mp4",
			Status:   "COMPLETED", StartSecs: &captureStart1, EndSecs: &captureEnd1,
		})

		err := w.persistConcatMediaIntervals(context.Background(), "sess-media-map", profileID, []concatMediaInterval{
			{FilePath: first.FilePath, Start: 0, End: 1.25},
			{FilePath: second.FilePath, Start: 1.25, End: 3.0},
		})
		Expect(err).NotTo(HaveOccurred())

		var rows []db.ChunkAnalysisResult
		Expect(dbConn.Where("session_id = ?", "sess-media-map").Order("id").Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].MediaStartSecs).NotTo(BeNil())
		Expect(rows[0].MediaEndSecs).NotTo(BeNil())
		Expect(rows[1].MediaStartSecs).NotTo(BeNil())
		Expect(rows[1].MediaEndSecs).NotTo(BeNil())
		Expect(*rows[0].MediaStartSecs).To(Equal(0.0))
		Expect(*rows[0].MediaEndSecs).To(Equal(1.25))
		Expect(*rows[1].MediaStartSecs).To(Equal(1.25))
		Expect(*rows[1].MediaEndSecs).To(Equal(3.0))
		Expect(*rows[0].StartSecs).To(Equal(1_000.0))
	})

	Context("when ffmpeg is available", func() {
		BeforeEach(func() {
			if !hasFfmpeg() {
				Skip("ffmpeg not found in PATH")
			}
		})

		It("enqueues a video:analysis task after merging chunks", func() {
			tmpFile := createTinyMP4(GinkgoT())
			mp4Bytes, readErr := os.ReadFile(tmpFile)
			Expect(readErr).NotTo(HaveOccurred())

			// Seed chunk records in DB so the handler uses DB-ordered paths
			start0 := 0.0
			end0 := 1.0
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: "sess-merge-001",
				ProfileID: profileID,
				FilePath:  "gs://test-bucket/videos/sess-merge-001/chunk_001.mp4",
				Status:    "COMPLETED",
				Output:    "good form",
				StartSecs: &start0,
				EndSecs:   &end0,
			}).Error).NotTo(HaveOccurred())
			missingStart := 2.0
			missingEnd := 3.0
			missingChunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				SessionID: "sess-merge-001",
				ProfileID: profileID,
				FilePath:  "gs://test-bucket/videos/sess-merge-001/chunk_missing.mp4",
				Status:    "COMPLETED",
				StartSecs: &missingStart,
				EndSecs:   &missingEnd,
			})

			// Create a fresh transport for this test with all the GCS expectations
			ffmpegTransport := testhelpers.NewMockTransport()
			ffmpegStorageClient, sErr := testhelpers.NewStorageClient("test-bucket", ffmpegTransport)
			Expect(sErr).NotTo(HaveOccurred())
			w.StorageClient = ffmpegStorageClient

			// Mock GCS ListObjects for original chunks lookup
			testhelpers.MockGCSListObjects(ffmpegTransport, "test-bucket", "videos/sess-merge-001/", []string{
				"videos/sess-merge-001/chunk_001.mp4",
			})
			// DownloadFile for the chunk (serve real mp4 bytes so ffmpeg can process it)
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, "gs://test-bucket/videos/sess-merge-001/chunk_001.mp4", mp4Bytes)
			// UploadFromFile for the merged video
			testhelpers.MockGCSUpload(ffmpegTransport, "test-bucket", "merged")
			// UploadFromFile for the hard-subbed video (best-effort, may or may not be called)
			testhelpers.MockGCSUpload(ffmpegTransport, "test-bucket", "hardsubbed")

			task, err := NewMergeChunksTask(
				"sess-merge-001",
				"gs://test-bucket/videos/sess-merge-001",
				WorkoutTypeWOD,
				[]string{"Deadlift"},
				nil, profileID,
				false,
				"",
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(w.HandleMergeChunksTask(context.Background(), task)).To(Succeed())

			// Verify exactly one video:analysis task was enqueued to Redis
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(pending[0].Type).To(Equal(TypeVideoAnalysis))
			Expect(pending[0].Timeout).To(Equal(time.Hour))

			var enqueued VideoAnalysisPayload
			Expect(json.Unmarshal(pending[0].Payload, &enqueued)).To(Succeed())
			Expect(enqueued.SessionID).To(Equal("sess-merge-001"))
			Expect(enqueued.WorkoutType).To(Equal(WorkoutTypeWOD))
			Expect(enqueued.Movements).To(Equal([]string{"Deadlift"}))
			Expect(enqueued.FilePath).To(ContainSubstring("sess-merge-001"))

			var mapped db.ChunkAnalysisResult
			Expect(dbConn.Where("session_id = ?", "sess-merge-001").First(&mapped).Error).To(Succeed())
			Expect(mapped.MediaStartSecs).NotTo(BeNil())
			Expect(*mapped.MediaStartSecs).To(Equal(0.0))
			Expect(mapped.MediaEndSecs).NotTo(BeNil())
			Expect(*mapped.MediaEndSecs).To(BeNumerically(">", 0.0))
			var absent db.ChunkAnalysisResult
			Expect(dbConn.First(&absent, missingChunk.ID).Error).To(Succeed())
			Expect(absent.MediaStartSecs).To(BeNil(), "a DB row absent from the GCS concat manifest must not be mapped")
		})

		It("excludes split_chunk records from merge to prevent doubled content", func() {
			tmpFile := createTinyMP4(GinkgoT())
			mp4Bytes, readErr := os.ReadFile(tmpFile)
			Expect(readErr).NotTo(HaveOccurred())

			sessionID := "sess-merge-split-excl"

			// Seed: 2 original recording chunks (should be included)
			start0 := 0.0
			end0 := 10.0
			start1 := 10.0
			end1 := 20.0
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: sessionID,
				ProfileID: profileID,
				FilePath:  "gs://test-bucket/videos/" + sessionID + "/camera_chunk_001.mp4",
				Status:    "COMPLETED",
				Output:    "chunk 1",
				StartSecs: &start0,
				EndSecs:   &end0,
			}).Error).NotTo(HaveOccurred())

			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: sessionID,
				ProfileID: profileID,
				FilePath:  "gs://test-bucket/videos/" + sessionID + "/camera_chunk_002.mp4",
				Status:    "COMPLETED",
				Output:    "chunk 2",
				StartSecs: &start1,
				EndSecs:   &end1,
			}).Error).NotTo(HaveOccurred())

			// Seed: 2 split_chunk records from splitAndAnalyzeChunks (should be EXCLUDED)
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: sessionID,
				ProfileID: profileID,
				FilePath:  "gs://test-bucket/videos/" + sessionID + "/split_chunk_000.mp4",
				Status:    "COMPLETED",
				Output:    "split chunk 0",
				StartSecs: &start0,
				EndSecs:   &end0,
			}).Error).NotTo(HaveOccurred())

			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID: sessionID,
				ProfileID: profileID,
				FilePath:  "gs://test-bucket/videos/" + sessionID + "/split_chunk_001.mp4",
				Status:    "COMPLETED",
				Output:    "split chunk 1",
				StartSecs: &start1,
				EndSecs:   &end1,
			}).Error).NotTo(HaveOccurred())

			// Set up GCS mocks — only 2 downloads should happen (not 4)
			ffmpegTransport := testhelpers.NewMockTransport()
			ffmpegStorageClient, sErr := testhelpers.NewStorageClient("test-bucket", ffmpegTransport)
			Expect(sErr).NotTo(HaveOccurred())
			w.StorageClient = ffmpegStorageClient

			testhelpers.MockGCSListObjects(ffmpegTransport, "test-bucket", "videos/"+sessionID+"/", []string{
				"videos/" + sessionID + "/camera_chunk_001.mp4",
				"videos/" + sessionID + "/camera_chunk_002.mp4",
				"videos/" + sessionID + "/split_chunk_000.mp4",
				"videos/" + sessionID + "/split_chunk_001.mp4",
			})
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, "gs://test-bucket/videos/"+sessionID+"/camera_chunk_001.mp4", mp4Bytes)
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, "gs://test-bucket/videos/"+sessionID+"/camera_chunk_002.mp4", mp4Bytes)
			testhelpers.MockGCSUpload(ffmpegTransport, "test-bucket", "merged")

			task, err := NewMergeChunksTask(
				sessionID,
				"gs://test-bucket/videos/"+sessionID,
				WorkoutTypeWOD,
				[]string{"Snatch"},
				nil, profileID,
				false,
				"",
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(w.HandleMergeChunksTask(context.Background(), task)).To(Succeed())

			// Verify a video:analysis task was enqueued
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(pending[0].Type).To(Equal(TypeVideoAnalysis))
		})

		It("resolves abandoned first chunk after grace and preserves merge order", func() {
			tmpFile := createTinyMP4(GinkgoT())
			mp4Bytes, readErr := os.ReadFile(tmpFile)
			Expect(readErr).NotTo(HaveOccurred())

			sessionID := "sess-pending-autoheal"

			// Only chunk_002 is completed; chunk_001 was abandoned/failed before DB record
			start0, end0 := 1.0, 2.0
			completed := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				SessionID: sessionID,
				ProfileID: profileID,
				FilePath:  "gs://test-bucket/videos/" + sessionID + "/chunk_002.mp4",
				Status:    "COMPLETED",
				Output:    "good form",
				StartSecs: &start0,
				EndSecs:   &end0,
			})
			// Existing deployments may already contain duplicate rows.
			testhelpers.CreateChunkAnalysisResult(dbConn, &completed)

			ffmpegTransport := testhelpers.NewMockTransport()
			ffmpegStorageClient, sErr := testhelpers.NewStorageClient("test-bucket", ffmpegTransport)
			Expect(sErr).NotTo(HaveOccurred())
			w.StorageClient = ffmpegStorageClient

			testhelpers.MockGCSListObjects(ffmpegTransport, "test-bucket", "videos/"+sessionID+"/", []string{
				"videos/" + sessionID + "/chunk_002.mp4",
				"videos/" + sessionID + "/chunk_001.mp4",
			})
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, "gs://test-bucket/videos/"+sessionID+"/chunk_002.mp4", mp4Bytes)
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, "gs://test-bucket/videos/"+sessionID+"/chunk_001.mp4", mp4Bytes)
			testhelpers.MockGCSUpload(ffmpegTransport, "test-bucket", "merged")

			task, err := NewMergeChunksTask(
				sessionID,
				"gs://test-bucket/videos/"+sessionID,
				WorkoutTypeWOD,
				[]string{"Squat"},
				nil, profileID,
				false,
				"",
			)
			Expect(err).NotTo(HaveOccurred())

			var payload VideoAnalysisPayload
			Expect(json.Unmarshal(task.Payload(), &payload)).To(Succeed())
			payload.MergeRequestedAt = time.Now().Add(-orphanChunkGrace - time.Minute)
			body, marshalErr := json.Marshal(payload)
			Expect(marshalErr).NotTo(HaveOccurred())
			task = asynq.NewTask(TypeMergeChunks, body)
			ctx := WithRetryCount(context.Background(), 3)
			Expect(w.HandleMergeChunksTask(ctx, task)).To(Succeed())

			// Verify chunk_001 was marked as FAILED in DB
			var failedRec db.ChunkAnalysisResult
			Expect(dbConn.Where("session_id = ? AND file_path = ?", sessionID, "gs://test-bucket/videos/"+sessionID+"/chunk_001.mp4").First(&failedRec).Error).To(Succeed())
			Expect(failedRec.Status).To(Equal("FAILED"))
			Expect(failedRec.Output).To(ContainSubstring("archived or abandoned"))

			Expect(failedRec.MediaStartSecs).NotTo(BeNil())
			Expect(*failedRec.MediaStartSecs).To(Equal(0.0))

			// Verify video:analysis task was enqueued
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(HaveLen(1))
			Expect(pending[0].Type).To(Equal(TypeVideoAnalysis))
		})
	})
})
