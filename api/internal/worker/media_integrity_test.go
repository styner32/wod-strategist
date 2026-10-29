package worker

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("media integrity verification", func() {
	var ctx context.Context
	var directory string

	BeforeEach(func() {
		if !hasFfmpeg() {
			Skip("ffmpeg is required for media integrity fixtures")
		}
		if _, err := exec.LookPath("ffprobe"); err != nil {
			Skip("ffprobe is required for media integrity verification")
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 45*time.Second)
		DeferCleanup(cancel)
		var err error
		directory, err = os.MkdirTemp("", "media-integrity-test-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, directory)
	})

	It("measures displayed B-frame timestamps rather than the decode-order endpoints", func() {
		path := integrityTestVideo(ctx, directory, "bframes", "testsrc2=size=96x64:rate=30:duration=2",
			"-preset", "medium", "-crf", "18", "-bf", "2", "-g", "30")
		bFrames, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "V:0",
			"-show_entries", "stream=has_b_frames", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(bFrames))).To(Equal("2"), "the fixture must actually contain B-frames")
		timeline, err := probeVideoTimeline(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		Expect(timeline.Start).To(BeNumerically("~", 0, 0.000001))
		Expect(timeline.End).To(BeNumerically("~", 2, 0.000001))
		Expect(timeline.Duration()).To(BeNumerically("~", 2, 0.000001))
		Expect(timeline.FrameCount).To(Equal(60))
		Expect(timeline.Tick).To(BeNumerically("~", 1.0/30000, 0.000000001))
		Expect(verifyVideoDuration(ctx, path, 2, 0.001)).To(Succeed())
	})

	It("preserves variable presentation intervals and fractional 29.97 fps endpoints", func() {
		vfr := integrityTestVideo(ctx, directory, "vfr", "testsrc2=size=96x64:rate=30:duration=1",
			"-vf", "select='not(eq(mod(n,3),1))'", "-fps_mode", "passthrough")
		timeline, err := probeVideoTimeline(ctx, vfr)
		Expect(err).NotTo(HaveOccurred())
		Expect(timeline.FrameCount).To(Equal(20))
		Expect(timeline.Start).To(BeNumerically("~", 0, 0.000001))
		Expect(timeline.End).To(BeNumerically("~", 1, 0.000001))
		vfrSignature, err := readVideoFrameSignature(ctx, vfr)
		Expect(err).NotTo(HaveOccurred())
		Expect(verifyVideoFrameSequence(ctx, vfr, []videoFrameSignature{vfrSignature})).To(Succeed())

		fractional := integrityTestVideo(ctx, directory, "fractional", "testsrc2=size=96x64:rate=30000/1001", "-frames:v", "60")
		timeline, err = probeVideoTimeline(ctx, fractional)
		Expect(err).NotTo(HaveOccurred())
		Expect(timeline.FrameCount).To(Equal(60))
		Expect(timeline.Duration()).To(BeNumerically("~", 2.002, 0.000001))
		Expect(verifyVideoDuration(ctx, fractional, 2.002, 0.0001)).To(Succeed())
		fractionalSignature, err := readVideoFrameSignature(ctx, fractional)
		Expect(err).NotTo(HaveOccurred())
		Expect(verifyVideoFrameSequence(ctx, fractional, []videoFrameSignature{fractionalSignature})).To(Succeed())
	})

	It("clips the final decoded B-frame duration to the verified VFR stream endpoint", func() {
		path := integrityTestVideo(ctx, directory, "vfr-bframes", "testsrc2=size=96x64:rate=30:duration=1",
			"-vf", "select='not(eq(mod(n,3),1))'", "-fps_mode", "passthrough",
			"-preset", "medium", "-crf", "18", "-bf", "2", "-g", "30")
		bFrames, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "V:0",
			"-show_entries", "stream=has_b_frames", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(bFrames))).To(Equal("2"))
		// ffprobe can attribute a 2/30-second decode duration to the final
		// displayed frame at 29/30, although the verified video stream ends at
		// one second. Summing those fields would invent another video frame.
		timeline, err := probeVideoTimeline(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		Expect(timeline.FrameCount).To(Equal(20))
		Expect(timeline.Start).To(BeNumerically("~", 0, 0.000001))
		Expect(timeline.End).To(BeNumerically("~", 1, 0.000001))
		Expect(timeline.Duration()).To(BeNumerically("~", 1, 0.000001))
		Expect(verifyVideoDuration(ctx, path, 1, 0.001)).To(Succeed())
		Expect(verifyMediaEndpoints(ctx, path, 1, 0.001)).To(Succeed())
	})

	It("retains a nonzero displayed start without mistaking an absolute endpoint for duration", func() {
		path := integrityTestVideo(ctx, directory, "nonzero", "testsrc2=size=96x64:rate=30:duration=1", "-output_ts_offset", "2")
		timeline, err := probeVideoTimeline(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		Expect(timeline.Start).To(BeNumerically("~", 2, 0.000001))
		Expect(timeline.End).To(BeNumerically("~", 3, 0.000001))
		Expect(timeline.Duration()).To(BeNumerically("~", 1, 0.000001))
		Expect(verifyVideoDuration(ctx, path, 1, 0.001)).To(Succeed())
		Expect(verifyVideoDuration(ctx, path, 3, 0.001)).To(HaveOccurred())
		Expect(verifyMediaEndpoints(ctx, path, 1, 0.001)).To(HaveOccurred())
	})

	It("measures video independently when audio and container duration continue after its last frame", func() {
		path := integrityTestAV(ctx, directory, "long-audio", "2", "0")
		timeline, err := probeVideoTimeline(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		Expect(timeline.FrameCount).To(Equal(30))
		Expect(timeline.Duration()).To(BeNumerically("~", 1, 0.000001))
		Expect(verifyVideoDuration(ctx, path, 1, 0.001)).To(Succeed())
		Expect(verifyMediaEndpoints(ctx, path, 1, 0.05)).To(HaveOccurred())
	})

	It("compares decoded frames in order and rejects same-duration swapped, duplicated or missing chunks", func() {
		first := integrityTestVideo(ctx, directory, "first", "color=c=red:size=96x64:rate=30:duration=1")
		second := integrityTestVideo(ctx, directory, "second", "color=c=blue:size=96x64:rate=30:duration=1")
		firstSignature, err := readVideoFrameSignature(ctx, first)
		Expect(err).NotTo(HaveOccurred())
		secondSignature, err := readVideoFrameSignature(ctx, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(firstSignature.FrameCount).To(Equal(30))
		Expect(firstSignature.Start).To(BeNumerically("~", 0, 0.000001))
		Expect(firstSignature.End).To(BeNumerically("~", 1, 0.000001))
		Expect(firstSignature.Digest).NotTo(BeEmpty())
		Expect(firstSignature.Digest).NotTo(Equal(secondSignature.Digest))
		expected := []videoFrameSignature{firstSignature, secondSignature}

		good := integrityTestConcat(ctx, directory, "good", first, second)
		Expect(verifyVideoFrameSequence(ctx, good, expected)).To(Succeed())
		for name, sources := range map[string][]string{
			"swapped":    {second, first},
			"duplicated": {first, first},
		} {
			By(name)
			path := integrityTestConcat(ctx, directory, name, sources...)
			Expect(verifyVideoDuration(ctx, path, 2, 0.001)).To(Succeed())
			Expect(verifyVideoFrameSequence(ctx, path, expected)).To(HaveOccurred())
		}
		// A low-rate replacement has the same two-second duration but only half
		// the required frames; endpoint checks alone cannot detect the loss.
		dropped := integrityTestVideo(ctx, directory, "dropped", "color=c=red:size=96x64:rate=15:duration=2")
		Expect(verifyVideoDuration(ctx, dropped, 2, 0.001)).To(Succeed())
		Expect(verifyVideoFrameSequence(ctx, dropped, expected)).To(HaveOccurred())
	})

	It("rejects shifted presentation timestamps even when decoded frames and frame count are identical", func() {
		original := integrityTestVideo(ctx, directory, "original", "testsrc2=size=96x64:rate=30:duration=1")
		shifted := filepath.Join(directory, "shifted.mp4")
		integrityTestFFmpeg(ctx, "-i", original, "-map", "0:v:0", "-c", "copy", "-output_ts_offset", "0.5", shifted)
		signature, err := readVideoFrameSignature(ctx, original)
		Expect(err).NotTo(HaveOccurred())
		shiftedSignature, err := readVideoFrameSignature(ctx, shifted)
		Expect(err).NotTo(HaveOccurred())
		Expect(shiftedSignature.FrameCount).To(Equal(signature.FrameCount))
		Expect(shiftedSignature.Digest).To(Equal(signature.Digest))
		Expect(verifyVideoDuration(ctx, shifted, 1, 0.001)).To(Succeed())
		Expect(verifyVideoFrameSequence(ctx, shifted, []videoFrameSignature{signature})).To(HaveOccurred())

		// Keep every pixel, the zero start and the one-second endpoint, while
		// moving a single interior frame by 10 ms. Only per-frame timing can
		// detect this corruption after counts, hashes and total duration pass.
		warped := filepath.Join(directory, "warped.mp4")
		integrityTestFFmpeg(ctx, "-i", original, "-vf", "setpts='PTS+if(eq(N,15),0.01/TB,0)'", "-an",
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "0", "-bf", "0", "-pix_fmt", "yuv420p",
			"-fps_mode", "passthrough", "-enc_time_base", "1:30000", "-video_track_timescale", "30000", warped)
		warpedSignature, err := readVideoFrameSignature(ctx, warped)
		Expect(err).NotTo(HaveOccurred())
		Expect(warpedSignature.FrameCount).To(Equal(signature.FrameCount))
		Expect(warpedSignature.Digest).To(Equal(signature.Digest))
		Expect(verifyVideoDuration(ctx, warped, 1, 0.001)).To(Succeed())
		Expect(verifyVideoFrameSequence(ctx, warped, []videoFrameSignature{signature})).To(HaveOccurred())
	})

	It("requires zero-based video, audio and container endpoints within the supplied tolerance", func() {
		videoOnly := integrityTestVideo(ctx, directory, "video-only", "testsrc2=size=96x64:rate=30:duration=1")
		Expect(verifyMediaEndpoints(ctx, videoOnly, 1, 0.05)).To(Succeed())
		aligned := integrityTestAV(ctx, directory, "aligned", "1", "0")
		Expect(verifyMediaEndpoints(ctx, aligned, 1, 0.05)).To(Succeed())

		shortAudio := integrityTestAV(ctx, directory, "short-audio", "0.5", "0")
		Expect(verifyMediaEndpoints(ctx, shortAudio, 1, 0.05)).To(HaveOccurred())
		delayedAudio := integrityTestAV(ctx, directory, "delayed-audio", "0.75", "0.25")
		Expect(verifyVideoDuration(ctx, delayedAudio, 1, 0.001)).To(Succeed())
		Expect(verifyMediaEndpoints(ctx, delayedAudio, 1, 0.05)).To(HaveOccurred())
		Expect(verifyVideoDuration(ctx, aligned, 1.02, 0.05)).To(Succeed())
		Expect(verifyVideoDuration(ctx, aligned, 1.2, 0.05)).To(HaveOccurred())
	})

	It("fails closed for missing, malformed and audio-only media instead of inferring a duration", func() {
		corrupt := filepath.Join(directory, "corrupt.mp4")
		Expect(os.WriteFile(corrupt, []byte("not an MP4 or a usable video stream"), 0o600)).To(Succeed())
		audioOnly := filepath.Join(directory, "audio-only.m4a")
		integrityTestFFmpeg(ctx, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1", "-c:a", "aac", audioOnly)
		for _, path := range []string{filepath.Join(directory, "missing.mp4"), corrupt, audioOnly} {
			By(filepath.Base(path))
			_, err := probeVideoTimeline(ctx, path)
			Expect(err).To(HaveOccurred())
			_, err = readVideoFrameSignature(ctx, path)
			Expect(err).To(HaveOccurred())
			Expect(verifyVideoDuration(ctx, path, 1, 0.05)).To(HaveOccurred())
			Expect(verifyMediaEndpoints(ctx, path, 1, 0.05)).To(HaveOccurred())
		}
	})

	It("honors canceled contexts for probes and decoded-frame verification", func() {
		path := integrityTestVideo(ctx, directory, "cancel", "testsrc2=size=96x64:rate=30:duration=1")
		signature, err := readVideoFrameSignature(ctx, path)
		Expect(err).NotTo(HaveOccurred())
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = probeVideoTimeline(canceled, path)
		Expect(err).To(HaveOccurred())
		_, err = readVideoFrameSignature(canceled, path)
		Expect(err).To(HaveOccurred())
		Expect(verifyVideoDuration(canceled, path, 1, 0.05)).To(HaveOccurred())
		Expect(verifyVideoFrameSequence(canceled, path, []videoFrameSignature{signature})).To(HaveOccurred())
		Expect(verifyMediaEndpoints(canceled, path, 1, 0.05)).To(HaveOccurred())
	})

	It("rejects nonfinite or invalid expectations that would otherwise bypass numeric comparisons", func() {
		path := integrityTestVideo(ctx, directory, "expectations", "testsrc2=size=96x64:rate=30:duration=1")
		for _, expected := range []float64{0, -1, math.NaN(), math.Inf(1)} {
			Expect(verifyVideoDuration(ctx, path, expected, 0.05)).To(HaveOccurred())
			Expect(verifyMediaEndpoints(ctx, path, expected, 0.05)).To(HaveOccurred())
		}
		for _, tolerance := range []float64{-1, math.NaN(), math.Inf(1)} {
			Expect(verifyVideoDuration(ctx, path, 1, tolerance)).To(HaveOccurred())
			Expect(verifyMediaEndpoints(ctx, path, 1, tolerance)).To(HaveOccurred())
		}
	})
})

func integrityTestFFmpeg(ctx context.Context, args ...string) {
	GinkgoHelper()
	args = append([]string{"-v", "error", "-nostdin", "-y"}, args...)
	output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "ffmpeg fixture: %s", string(output))
}

func integrityTestVideo(ctx context.Context, directory, name, source string, options ...string) string {
	GinkgoHelper()
	path := filepath.Join(directory, name+".mp4")
	args := []string{"-f", "lavfi", "-i", source, "-an", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "0",
		"-bf", "0", "-pix_fmt", "yuv420p", "-video_track_timescale", "30000"}
	args = append(args, options...)
	integrityTestFFmpeg(ctx, append(args, path)...)
	return path
}

func integrityTestAV(ctx context.Context, directory, name, audioDuration, audioOffset string) string {
	GinkgoHelper()
	path := filepath.Join(directory, name+".mp4")
	integrityTestFFmpeg(ctx, "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=30:duration=1",
		"-itsoffset", audioOffset, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration="+audioDuration,
		"-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "0",
		"-bf", "0", "-pix_fmt", "yuv420p", "-video_track_timescale", "30000", "-c:a", "aac", path)
	return path
}

func integrityTestConcat(ctx context.Context, directory, name string, sources ...string) string {
	GinkgoHelper()
	var list strings.Builder
	for _, source := range sources {
		fmt.Fprintf(&list, "file '%s'\n", filepath.Base(source))
	}
	listPath := filepath.Join(directory, name+".txt")
	Expect(os.WriteFile(listPath, []byte(list.String()), 0o600)).To(Succeed())
	path := filepath.Join(directory, name+".mp4")
	integrityTestFFmpeg(ctx, "-f", "concat", "-safe", "0", "-i", listPath, "-map", "0:v:0", "-an", "-c:v", "copy",
		"-video_track_timescale", "30000", path)
	return path
}
