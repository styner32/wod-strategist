package worker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
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

	"github.com/wod-strategist/api/internal/activity"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("NewMergeChunksTask", func() {

	It("creates a task with the correct payload fields, minute-truncated MergeRequestedAt, and a 1-hour timeout", func() {
		Expect(mergeChunksTaskTimeout).To(Equal(time.Hour))

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
		Entry("homogeneous compressed mobile chunks retain the original resolution policy",
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

	DescribeTable("preserves 4K dimensions when only newly audited stream details differ",
		func(difference string) {
			base := chunkStreamProfile{VideoCodec: "h264", PixFmt: "yuv420p", Width: 3840, Height: 2160, DisplayWidth: 3840, DisplayHeight: 2160, VideoTimeBase: "1/30000", VideoTimescale: 30000, HasAudio: true, AudioCodec: "aac", SampleRate: 44100, Channels: 2, AudioTimeBase: "1/44100", ChannelLayout: "stereo", VideoIndex: 0, AudioIndex: 1, VideoCount: 1, AudioCount: 1, StreamOrder: "video,audio"}
			other := base
			switch difference {
			case "audio time base":
				other.AudioTimeBase = "1/88200"
			case "stream order":
				other.VideoIndex, other.AudioIndex, other.StreamOrder = 1, 0, "audio,video"
			case "channel layout":
				other.ChannelLayout = "downmix"
			}
			profiles := []chunkStreamProfile{base, other}
			heterogeneous, _, _, _ := chunksNeedNormalization(profiles)
			Expect(heterogeneous).To(BeFalse())
			width, height, includeAudio := concatPreparationSettings(profiles)
			Expect(width).To(Equal(3840))
			Expect(height).To(Equal(2160))
			Expect(includeAudio).To(BeTrue())
		},
		Entry("audio time base", "audio time base"),
		Entry("audio/video stream order and indexes", "stream order"),
		Entry("audio channel layout", "channel layout"),
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

	DescribeTable("validates positive stream time bases",
		func(value string, valid bool) { Expect(validStreamTimeBase(value)).To(Equal(valid)) },
		Entry("ordinary audio", "1/44100", true),
		Entry("non-unit positive numerator", "1001/30000", true),
		Entry("unknown", "", false),
		Entry("zero", "0/44100", false),
		Entry("negative", "-1/44100", false),
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
		profile1, err := probeChunkStreamProfile(ctx, chunk1)
		Expect(err).NotTo(HaveOccurred())
		timeline1, err := probeVideoTimeline(ctx, chunk1)
		Expect(err).NotTo(HaveOccurred())
		trim1, err := probeLeadingBlackTrimSecs(ctx, chunk1, profile1, timeline1)
		Expect(err).NotTo(HaveOccurred())
		Expect(trim1).To(BeNumerically("~", 0.0323, 0.005))

		// Verify a genuinely dark 2.0s chunk is NOT scheduled for trimming.
		darkChunk := filepath.Join(tmpDir, "dark_only.mp4")
		outDark, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "color=c=black:size=406x720:rate=30:d=2.0",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-y", darkChunk,
		).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(outDark))
		darkProfile, err := probeChunkStreamProfile(ctx, darkChunk)
		Expect(err).NotTo(HaveOccurred())
		darkTimeline, err := probeVideoTimeline(ctx, darkChunk)
		Expect(err).NotTo(HaveOccurred())
		darkTrim, err := probeLeadingBlackTrimSecs(ctx, darkChunk, darkProfile, darkTimeline)
		Expect(err).NotTo(HaveOccurred())
		Expect(darkTrim).To(Equal(0.0))

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

	DescribeTable("canonicalizes native mobile timescales before concat when only later chunks need black trimming",
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

			// Chunk 1: the same native timescale with a leading black flash.
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

			// Both prepared chunks use the canonical timescale, including sparse trims.
			profTrimmed, err := probeChunkStreamProfile(ctx, localPaths[1])
			Expect(err).NotTo(HaveOccurred())
			Expect(profTrimmed.VideoTimescale).To(Equal(canonicalConcatVideoTimescale))

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

	It("bounds a single chunk by video duration despite a long audio tail", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		source := createMergeAuditChunk(ctx, tmpDir, "tail.mp4", "2", "10", 0, false)
		output := filepath.Join(tmpDir, "merged.mp4")
		intervals, intervalErr, err := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, []string{"tail"}, []string{source}, output)
		Expect(err).NotTo(HaveOccurred())
		Expect(intervalErr).NotTo(HaveOccurred())
		Expect(intervals).To(HaveLen(1))
		Expect(intervals[0].End).To(BeNumerically("~", 2.0, 1.0/30))
		Expect(verifyMediaEndpoints(ctx, output, 2, 2.0/30)).To(Succeed())
	})

	It("keeps delayed short audio in place and fills the remaining video with silence", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		source := createMergeAuditChunk(ctx, tmpDir, "delayed.mp4", "2", "0.5", 0.25, false)
		output := filepath.Join(tmpDir, "merged.mp4")
		_, _, err := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, []string{"delayed"}, []string{source}, output)
		Expect(err).NotTo(HaveOccurred())
		pcm, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-xerror", "-i", output, "-map", "0:a:0", "-ac", "1", "-ar", "44100", "-f", "f32le", "-").Output()
		Expect(err).NotTo(HaveOccurred())
		firstAudible := -1
		for i := 0; i+4 <= len(pcm); i += 4 {
			if math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:i+4])))) > 0.01 {
				firstAudible = i / 4
				break
			}
		}
		Expect(firstAudible).To(BeNumerically(">", int(0.20*44100)))
		Expect(firstAudible).To(BeNumerically("<", int(0.29*44100)))
		Expect(verifyMediaEndpoints(ctx, output, 2, 2.0/30)).To(Succeed())
	})

	It("produces bounded silence when existing audio lies entirely outside the video", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		source := createMergeAuditChunk(ctx, tmpDir, "outside.mp4", "2", "0.5", 3, false)
		output := filepath.Join(tmpDir, "merged.mp4")
		_, _, err := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, []string{"outside"}, []string{source}, output)
		Expect(err).NotTo(HaveOccurred())
		Expect(verifyMediaEndpoints(ctx, output, 2, 2.0/30)).To(Succeed())
		pcm, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-xerror", "-i", output, "-map", "0:a:0", "-ac", "1", "-ar", "44100", "-f", "f32le", "-").Output()
		Expect(err).NotTo(HaveOccurred())
		Expect(len(pcm)).To(BeNumerically(">=", 2*44100*4))
		peak := 0.0
		for i := 0; i+4 <= len(pcm); i += 4 {
			peak = max(peak, math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:i+4])))))
		}
		Expect(peak).To(BeNumerically("<", 0.0001))
	})

	It("maps reversed video/audio stream order before concat without losing either chunk", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		first := createMergeAuditChunk(ctx, tmpDir, "first.mp4", "2", "2", 0, false)
		second := createMergeAuditChunk(ctx, tmpDir, "second.mp4", "2", "2", 0, true)
		profile, err := probeChunkStreamProfile(ctx, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(profile.StreamOrder).To(Equal("audio,video"))
		Expect(profile.VideoIndex).To(Equal(1))
		Expect(profile.AudioTimeBase).To(Equal("1/44100"))
		output := filepath.Join(tmpDir, "merged.mp4")
		intervals, intervalErr, err := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, []string{"first", "second"}, []string{first, second}, output)
		Expect(err).NotTo(HaveOccurred())
		Expect(intervalErr).NotTo(HaveOccurred())
		Expect(intervals[1].End).To(BeNumerically("~", 4.0, 1.0/30))
	})

	It("normalizes different audio time bases even when AAC sample rates match", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		first := createMergeAuditChunk(ctx, tmpDir, "first.mp4", "2", "2", 0, false)
		second := createMergeAuditChunk(ctx, tmpDir, "second.mp4", "2", "2", 0, false)
		doubleMergeTestAudioClock(second)
		profile, err := probeChunkStreamProfile(ctx, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(profile.SampleRate).To(Equal(44100))
		Expect(profile.AudioTimeBase).To(Equal("1/88200"))
		output := filepath.Join(tmpDir, "merged.mp4")
		intervals, _, err := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, []string{"first", "second"}, []string{first, second}, output)
		Expect(err).NotTo(HaveOccurred())
		Expect(intervals[1].End).To(BeNumerically("~", 4.0, 1.0/30))
		Expect(verifyMediaEndpoints(ctx, output, 4, 2.0/30)).To(Succeed())
	})

	It("fills a missing audio track while preserving both video intervals", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		first := createMergeAuditChunk(ctx, tmpDir, "silent.mp4", "1.1", "", 0, false)
		second := createMergeAuditChunk(ctx, tmpDir, "audible.mp4", "1.1", "1.1", 0, false)
		output := filepath.Join(tmpDir, "merged.mp4")
		intervals, _, err := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, []string{"silent", "audible"}, []string{first, second}, output)
		Expect(err).NotTo(HaveOccurred())
		Expect(intervals[1].End).To(BeNumerically("~", 2.2, 1.0/30))
		profile, err := probeChunkStreamProfile(ctx, output)
		Expect(err).NotTo(HaveOccurred())
		Expect(profile.HasAudio).To(BeTrue())
	})

	DescribeTable("preserves the video span of 29.97fps, VFR, and B-frame sources",
		func(rate, selection string) {
			if !hasFfmpeg() {
				Skip("ffmpeg unavailable")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			tmpDir := GinkgoT().TempDir()
			source := filepath.Join(tmpDir, "source.mp4")
			args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=" + rate + ":duration=2"}
			if selection != "" {
				args = append(args, "-vf", selection, "-fps_mode", "vfr")
			}
			args = append(args, "-c:v", "libx264", "-preset", "medium", "-bf", "2", "-an", "-video_track_timescale", "30000", "-y", source)
			out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))
			original, err := probeVideoTimeline(ctx, source)
			Expect(err).NotTo(HaveOccurred())
			output := filepath.Join(tmpDir, "merged.mp4")
			_, _, err = mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, []string{"source"}, []string{source}, output)
			Expect(err).NotTo(HaveOccurred())
			Expect(verifyVideoDuration(ctx, output, original.Duration(), 1.0/30+original.Tick)).To(Succeed())
		},
		Entry("29.97fps with B-frames", "30000/1001", ""),
		Entry("variable frame durations with B-frames", "30", "select=not(eq(mod(n\\,5)\\,1))"),
	)

	It("stops before writing merged output when an input cannot be probed", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg unavailable")
		}
		tmpDir := GinkgoT().TempDir()
		source := filepath.Join(tmpDir, "corrupt.mp4")
		Expect(os.WriteFile(source, []byte("not an MP4"), 0600)).To(Succeed())
		output := filepath.Join(tmpDir, "merged.mp4")
		_, _, err := mergeDownloadedChunks(context.Background(), zap.NewNop(), tmpDir, []string{"corrupt"}, []string{source}, output)
		Expect(err).To(HaveOccurred())
		_, statErr := os.Stat(output)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		_, statErr = os.Stat(source)
		Expect(statErr).NotTo(HaveOccurred())
	})

	It("rejects ambiguous multiple audio streams", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		source := createMergeAuditChunk(ctx, tmpDir, "single.mp4", "1.1", "1.1", 0, false)
		multiple := filepath.Join(tmpDir, "multiple.mp4")
		out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", source, "-map", "0:v:0", "-map", "0:a:0", "-map", "0:a:0", "-c", "copy", "-y", multiple).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		_, err = probeChunkStreamProfile(ctx, multiple)
		Expect(err).To(MatchError(ContainSubstring("multiple audio")))
	})

	It("reconciles AAC priming over 200 seams without changing any video frame", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		tmpDir := GinkgoT().TempDir()
		source := createMergeAuditChunk(ctx, tmpDir, "canonical.mp4", "1.1", "1.1", 0, false)
		signature, err := readVideoFrameSignature(ctx, source)
		Expect(err).NotTo(HaveOccurred())
		duration := signature.End - signature.Start
		var entries []string
		var expected []videoFrameSignature
		for i := 0; i < 200; i++ {
			entries = append(entries, fmt.Sprintf("file '%s'", source), fmt.Sprintf("duration %.9f", duration))
			expected = append(expected, signature)
		}
		manifest := filepath.Join(tmpDir, "list.txt")
		Expect(os.WriteFile(manifest, []byte(strings.Join(entries, "\n")), 0600)).To(Succeed())
		output := filepath.Join(tmpDir, "merged.mp4")
		Expect(runFFmpegStreamCopyConcat(ctx, zap.NewNop(), manifest, output, duration*200, true)).To(Succeed())
		Expect(verifyVideoFrameSequence(ctx, output, expected)).To(Succeed())
		Expect(verifyMediaEndpoints(ctx, output, duration*200, 2.0/30+1.0/30000)).To(Succeed())
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

	It("prepares multiple distinct chunks concurrently while preserving exact chunk order, cumulative intervals, and source cleanup", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg not installed, skipping")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		tmpDir := GinkgoT().TempDir()
		specs := []struct {
			color    string
			duration float64
			freq     int
		}{
			{"red", 0.8, 330},
			{"green", 1.2, 440},
			{"blue", 0.9, 550},
			{"yellow", 1.4, 660},
			{"cyan", 1.0, 770},
			{"magenta", 1.1, 880},
		}

		objects := make([]string, len(specs))
		rawPaths := make([]string, len(specs))
		localPaths := make([]string, len(specs))
		for i, sp := range specs {
			chunkPath := filepath.Join(tmpDir, fmt.Sprintf("chunk_%03d.mp4", i))
			out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin",
				"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:size=160x120:rate=30:d=%.1f", sp.color, sp.duration),
				"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:sample_rate=44100:d=%.1f", sp.freq, sp.duration),
				"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
				"-c:a", "aac", "-ar", "44100", "-ac", "2",
				"-y", chunkPath,
			).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))
			objects[i] = fmt.Sprintf("gs://b/videos/1/s/chunk_%03d.mp4", i)
			rawPaths[i] = chunkPath
			localPaths[i] = chunkPath
		}

		mergedPath := filepath.Join(tmpDir, "merged.mp4")
		intervals, intervalErr, mergeErr := mergeDownloadedChunks(ctx, zap.NewNop(), tmpDir, objects, localPaths, mergedPath)
		Expect(mergeErr).NotTo(HaveOccurred())
		Expect(intervalErr).NotTo(HaveOccurred())
		Expect(intervals).To(HaveLen(len(specs)))

		expectedSignatures := make([]videoFrameSignature, len(specs))
		cursor := 0.0
		for i, sp := range specs {
			Expect(intervals[i].FilePath).To(Equal(objects[i]))
			Expect(intervals[i].Start).To(Equal(cursor))
			Expect(intervals[i].End - intervals[i].Start).To(BeNumerically("~", sp.duration, 2.0/30))
			cursor = intervals[i].End

			_, rawStatErr := os.Stat(rawPaths[i])
			Expect(os.IsNotExist(rawStatErr)).To(BeTrue(), "raw source chunk %d should be removed after normalization", i)
			Expect(filepath.Base(localPaths[i])).To(Equal(fmt.Sprintf("norm_chunk_%03d.mp4", i)))

			sig, sigErr := readVideoFrameSignature(ctx, localPaths[i])
			Expect(sigErr).NotTo(HaveOccurred())
			expectedSignatures[i] = sig
		}

		Expect(verifyVideoFrameSequence(ctx, mergedPath, expectedSignatures)).To(Succeed())

		swapped := append([]videoFrameSignature(nil), expectedSignatures...)
		swapped[0], swapped[len(swapped)-1] = swapped[len(swapped)-1], swapped[0]
		Expect(verifyVideoFrameSequence(ctx, mergedPath, swapped)).To(HaveOccurred())
	})

	It("stops before writing merged output when one chunk in a multi-chunk batch cannot be probed", func() {
		if !hasFfmpeg() {
			Skip("ffmpeg unavailable")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tmpDir := GinkgoT().TempDir()
		good0 := createMergeAuditChunk(ctx, tmpDir, "chunk_000.mp4", "1.0", "1.0", 0, false)
		corrupt := filepath.Join(tmpDir, "chunk_001.mp4")
		Expect(os.WriteFile(corrupt, []byte("not an MP4"), 0600)).To(Succeed())
		good2 := createMergeAuditChunk(ctx, tmpDir, "chunk_002.mp4", "1.0", "1.0", 0, false)

		output := filepath.Join(tmpDir, "merged.mp4")
		_, _, err := mergeDownloadedChunks(
			ctx,
			zap.NewNop(),
			tmpDir,
			[]string{"chunk_000", "chunk_001", "chunk_002"},
			[]string{good0, corrupt, good2},
			output,
		)
		Expect(err).To(MatchError(ContainSubstring("chunk 1:")))
		_, statErr := os.Stat(output)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		for _, src := range []string{good0, corrupt, good2} {
			_, srcErr := os.Stat(src)
			Expect(srcErr).NotTo(HaveOccurred())
		}
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
		testhelpers.FlushRedis()

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

	It("enqueues merge:chunks tasks with a 1-hour execution timeout", func() {
		task, err := NewMergeChunksTask(
			"sess-merge-timeout",
			"gs://test-bucket/videos/sess-merge-timeout",
			WorkoutTypeWOD, nil, nil, profileID,
			false,
			"",
		)
		Expect(err).NotTo(HaveOccurred())

		_, err = w.QueueClient.Enqueue(task)
		Expect(err).NotTo(HaveOccurred())

		pending, err := inspector.ListPendingTasks("default")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Type).To(Equal(TypeMergeChunks))
		Expect(pending[0].Timeout).To(Equal(time.Hour))
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

		It("does not publish, enqueue, or delete originals when a downloaded chunk is corrupt", func() {
			const sessionID = "sess-merge-corrupt-source"
			const chunkObject = "videos/sess-merge-corrupt-source/chunk_001.mp4"
			const chunkURI = "gs://test-bucket/videos/sess-merge-corrupt-source/chunk_001.mp4"
			testhelpers.CreateSession(dbConn, &db.Session{SessionID: sessionID, ProfileID: profileID, WorkoutType: WorkoutTypeWOD})
			start, end := 0.0, 1.0
			original := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				SessionID: sessionID, ProfileID: profileID, FilePath: chunkURI, Status: "COMPLETED", Output: "retained original analysis", StartSecs: &start, EndSecs: &end,
			})
			testhelpers.MockGCSListObjects(storageTransport, "test-bucket", "videos/"+sessionID+"/", []string{chunkObject, "videos/" + sessionID + "/merged.mp4"})
			testhelpers.MockGCSDownloadWithBody(storageTransport, chunkURI, []byte("corrupt MP4 without moov or media tracks"))
			task, err := NewMergeChunksTask(sessionID, "gs://test-bucket/videos/"+sessionID, WorkoutTypeWOD, nil, nil, profileID, true, "")
			Expect(err).NotTo(HaveOccurred())
			err = w.HandleMergeChunksTask(context.Background(), task)
			Expect(err).To(MatchError(ContainSubstring("probe concat source profiles")))
			Expect(storageTransport.Verify()).To(Succeed())
			requests := storageTransport.Requests()
			Expect(requests).To(HaveLen(2), "only the source list and source download may be requested")
			for _, request := range requests {
				Expect(request.Method).To(Equal("GET"), "no media upload, overwrite, or original deletion is allowed")
			}
			queues, err := inspector.Queues()
			Expect(err).NotTo(HaveOccurred())
			for _, name := range queues {
				info, queueErr := inspector.GetQueueInfo(name)
				Expect(queueErr).NotTo(HaveOccurred())
				Expect(info.Pending).To(BeZero())
				Expect(info.Scheduled).To(BeZero())
				Expect(info.Retry).To(BeZero())
			}
			var retained db.ChunkAnalysisResult
			Expect(dbConn.First(&retained, original.ID).Error).To(Succeed())
			Expect(retained.FilePath).To(Equal(chunkURI))
			Expect(retained.Status).To(Equal("COMPLETED"))
			Expect(retained.Output).To(Equal("retained original analysis"))
			Expect(retained.MediaStartSecs).To(BeNil())
			Expect(retained.MediaEndSecs).To(BeNil())
			var published int64
			Expect(dbConn.Model(&db.AnalysisResult{}).Where("session_id = ? AND profile_id = ?", sessionID, profileID).Count(&published).Error).To(Succeed())
			Expect(published).To(BeZero())
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

		It("uploads merged.mp4 for playback but skips analysis re-encode, activity:review, and video:analysis when merged duration exceeds maxAnalysisVideoDuration", func() {
			origMaxDuration := maxAnalysisVideoDuration
			maxAnalysisVideoDuration = 1500 * time.Millisecond
			DeferCleanup(func() {
				maxAnalysisVideoDuration = origMaxDuration
			})

			w.ActivityCountingEnabled = true

			tmpFile := createTinyMP4(GinkgoT()) // 1.0s MP4
			mp4Bytes, readErr := os.ReadFile(tmpFile)
			Expect(readErr).NotTo(HaveOccurred())

			const sessionID = "WOD-20261029-01JMERGEOVERLONG000000000"
			testhelpers.CreateSession(dbConn, &db.Session{
				SessionID:   sessionID,
				ProfileID:   profileID,
				WorkoutType: WorkoutTypeWOD,
			})

			obsRaw, obsErr := json.Marshal(activity.Observations{
				DurationSecs:  1.0,
				Version:       activity.Version,
				TargetState:   "identified",
				ActivityState: "exercise",
				Events: []activity.Observation{{
					Movement: "Burpee",
					Unit:     "reps",
					Start:    0.1,
					End:      0.8,
					Complete: true,
					Evidence: "visible complete burpee",
				}},
				Unassessed: []activity.Interval{},
			})
			Expect(obsErr).NotTo(HaveOccurred())
			start0, end0 := 0.0, 1.0
			start1, end1 := 1.0, 2.0
			chunk1URI := fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_001.mp4", profileID, sessionID)
			chunk2URI := fmt.Sprintf("gs://test-bucket/videos/%d/%s/chunk_002.mp4", profileID, sessionID)
			mergedObject := fmt.Sprintf("videos/%d/%s/merged.mp4", profileID, sessionID)

			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID:            sessionID,
				ProfileID:            profileID,
				FilePath:             chunk1URI,
				Status:               "COMPLETED",
				ExerciseType:         "Burpee",
				StartSecs:            &start0,
				EndSecs:              &end0,
				MovementObservations: obsRaw,
			}).Error).NotTo(HaveOccurred())
			Expect(dbConn.Create(&db.ChunkAnalysisResult{
				SessionID:            sessionID,
				ProfileID:            profileID,
				FilePath:             chunk2URI,
				Status:               "COMPLETED",
				ExerciseType:         "Burpee",
				StartSecs:            &start1,
				EndSecs:              &end1,
				MovementObservations: obsRaw,
			}).Error).NotTo(HaveOccurred())

			ffmpegTransport := testhelpers.NewMockTransport()
			ffmpegStorageClient, sErr := testhelpers.NewStorageClient("test-bucket", ffmpegTransport)
			Expect(sErr).NotTo(HaveOccurred())
			w.StorageClient = ffmpegStorageClient

			prefix := fmt.Sprintf("videos/%d/%s/", profileID, sessionID)
			testhelpers.MockGCSListObjects(ffmpegTransport, "test-bucket", prefix, []string{
				prefix + "chunk_001.mp4",
				prefix + "chunk_002.mp4",
			})
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, chunk1URI, mp4Bytes)
			testhelpers.MockGCSDownloadWithBody(ffmpegTransport, chunk2URI, mp4Bytes)
			testhelpers.MockGCSUpload(ffmpegTransport, "test-bucket", "merged")
			ffmpegTransport.New(testhelpers.GCSBaseURL).
				Get("/storage/v1/b/test-bucket/o/" + mergedObject).
				Reply(200).
				JSON(map[string]any{"name": mergedObject, "bucket": "test-bucket", "generation": "21"})

			task, err := NewMergeChunksTask(
				sessionID,
				"gs://test-bucket/"+strings.TrimSuffix(prefix, "/"),
				WorkoutTypeWOD,
				[]string{"Burpee"},
				nil,
				profileID,
				false,
				"",
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(w.HandleMergeChunksTask(context.Background(), task)).To(Succeed())
			Expect(ffmpegTransport.Verify()).To(Succeed())

			// Neither video:analysis nor activity:review may be enqueued
			pending, err := inspector.ListPendingTasks("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(pending).To(BeEmpty())

			// Media offsets are still persisted for playback timeline integrity
			var chunks []db.ChunkAnalysisResult
			Expect(dbConn.Where("session_id = ?", sessionID).Order("id").Find(&chunks).Error).To(Succeed())
			Expect(chunks).To(HaveLen(2))
			Expect(chunks[0].MediaEndSecs).NotTo(BeNil())
			Expect(chunks[1].MediaEndSecs).NotTo(BeNil())
			Expect(*chunks[1].MediaEndSecs).To(BeNumerically(">", 1.5))

			// AnalysisResult is marked FAILED with user-facing 90m message and merged playback video
			var result db.AnalysisResult
			Expect(dbConn.Where("session_id = ? AND profile_id = ?", sessionID, profileID).First(&result).Error).To(Succeed())
			Expect(result.Status).To(Equal("FAILED"))
			Expect(result.Output).To(Equal(videoTooLongUserMessage))
			Expect([]string(result.AvailableVideos)).To(Equal([]string{"merged"}))

			// Activity summary transitions to partial without calling Gemini, retaining live chunk counts
			var updatedSession db.Session
			Expect(dbConn.Where("session_id = ? AND profile_id = ?", sessionID, profileID).First(&updatedSession).Error).To(Succeed())
			var summary activity.Summary
			Expect(json.Unmarshal(updatedSession.ActivitySummary, &summary)).To(Succeed())
			Expect(summary.ReviewState).To(Equal("partial"))
			Expect(summary.Movements).To(HaveLen(1))
			Expect(summary.Movements[0].Count).To(Equal(2))
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

func createMergeAuditChunk(ctx context.Context, directory, name, videoDuration, audioDuration string, audioOffset float64, reverse bool) string {
	if !hasFfmpeg() {
		Skip("ffmpeg unavailable")
	}
	path := filepath.Join(directory, name)
	args := []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=30:duration=" + videoDuration}
	if audioDuration != "" {
		args = append(args, "-itsoffset", fmt.Sprintf("%.9f", audioOffset), "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration="+audioDuration)
		if reverse {
			args = append(args, "-map", "1:a:0", "-map", "0:v:0")
		} else {
			args = append(args, "-map", "0:v:0", "-map", "1:a:0")
		}
		args = append(args, "-c:a", "aac", "-ar", "44100", "-ac", "2")
	} else {
		args = append(args, "-map", "0:v:0", "-an")
	}
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-video_track_timescale", "30000", "-y", path)
	out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	return path
}

// Keep AAC media identical and its physical duration unchanged while changing
// only its MP4 time base, a property independent of codec sample_rate.
func doubleMergeTestAudioClock(path string) {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	type atom struct {
		start, end int
		kind       string
	}
	children := func(start, end int) []atom {
		var result []atom
		for start+8 <= end {
			size := int(binary.BigEndian.Uint32(data[start : start+4]))
			Expect(size).To(BeNumerically(">=", 8))
			Expect(start + size).To(BeNumerically("<=", end))
			result = append(result, atom{start, start + size, string(data[start+4 : start+8])})
			start += size
		}
		return result
	}
	child := func(parent atom, kind string) atom {
		for _, a := range children(parent.start+8, parent.end) {
			if a.kind == kind {
				return a
			}
		}
		Fail("missing MP4 atom " + kind)
		return atom{}
	}
	moov := child(atom{-8, len(data), "root"}, "moov")
	for _, track := range children(moov.start+8, moov.end) {
		if track.kind != "trak" {
			continue
		}
		mdia := child(track, "mdia")
		hdlr := child(mdia, "hdlr")
		if string(data[hdlr.start+16:hdlr.start+20]) != "soun" {
			continue
		}
		var walk func(int, int)
		walk = func(start, end int) {
			for _, a := range children(start, end) {
				double := func(pos int) { binary.BigEndian.PutUint32(data[pos:pos+4], binary.BigEndian.Uint32(data[pos:pos+4])*2) }
				switch a.kind {
				case "mdhd":
					Expect(data[a.start+8]).To(Equal(byte(0)))
					double(a.start + 20)
					double(a.start + 24)
				case "stts":
					count := int(binary.BigEndian.Uint32(data[a.start+12 : a.start+16]))
					for i := 0; i < count; i++ {
						double(a.start + 20 + i*8)
					}
				case "elst":
					Expect(data[a.start+8]).To(Equal(byte(0)))
					count := int(binary.BigEndian.Uint32(data[a.start+12 : a.start+16]))
					for i := 0; i < count; i++ {
						pos := a.start + 20 + i*12
						if int32(binary.BigEndian.Uint32(data[pos:pos+4])) >= 0 {
							double(pos)
						}
					}
				case "mdia", "minf", "stbl", "edts":
					walk(a.start+8, a.end)
				}
			}
		}
		walk(track.start+8, track.end)
	}
	Expect(os.WriteFile(path, data, 0600)).To(Succeed())
}
