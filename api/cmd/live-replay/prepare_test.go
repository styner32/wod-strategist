package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("Live replay fixture preparation", func() {
	DescribeTable("retains exact source sensor generations",
		func(document string, expected int64) {
			object, generation, err := sourceSensorReference([]byte(document), 1, "source")
			Expect(err).NotTo(HaveOccurred())
			Expect(object).To(Equal("videos/1/source/sensor_telemetry_v1.ndjson"))
			Expect(generation).To(Equal(expected))
		},
		Entry("legacy numeric generation", `{"object_name":"videos/1/source/sensor_telemetry_v1.ndjson","target_generation":9007199254740993}`, int64(9007199254740993)),
		Entry("string generation", `{"object_name":"videos/1/source/sensor_telemetry_v1.ndjson","target_generation":"9007199254740993"}`, int64(9007199254740993)),
		Entry("missing generation pins current metadata", `{"object_name":"videos/1/source/sensor_telemetry_v1.ndjson","target_generation":null}`, int64(0)),
	)

	DescribeTable("rejects sensor paths outside the selected session",
		func(document string) {
			_, _, err := sourceSensorReference([]byte(document), 1, "source")
			Expect(err).To(HaveOccurred())
		},
		Entry("different profile", `{"object_name":"videos/10/source/sensor.ndjson"}`),
		Entry("different session", `{"object_name":"videos/1/other/sensor.ndjson"}`),
		Entry("parent component", `{"object_name":"videos/1/source/../sensor.ndjson"}`),
		Entry("bad generation", `{"object_name":"videos/1/source/sensor.ndjson","target_generation":"1.5"}`),
	)

	It("downloads the recorded GCS generation even when the current object is newer", func() {
		dir, err := os.MkdirTemp("", "live-replay-pinned-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		transport := testhelpers.NewMockTransport()
		object := "videos/1/source/sensor.ndjson"
		transport.New(testhelpers.GCSBaseURL).Get("/storage/v1/b/test-bucket/o/" + url.PathEscape(object)).Reply(200).JSON(map[string]any{"name": object, "generation": "101", "size": "3"})
		transport.New(testhelpers.GCSBaseURL).Get("/test-bucket/" + object + "?generation=100").Reply(200).Body([]byte("historical"))
		client, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		filename := filepath.Join(dir, "sensor.ndjson")
		reference, err := downloadPinnedObject(context.Background(), client, object, 100, filename, 1024)
		Expect(err).NotTo(HaveOccurred())
		Expect(reference.Generation).To(Equal("100"))
		Expect(reference.SHA256).To(Equal(digest([]byte("historical"))))
		Expect(reference.SizeBytes).To(Equal(int64(10)))
		Expect(transport.Verify()).To(Succeed())
	})

	It("uses opposite sides of a source pause for chunk starts and ends", func() {
		mapping := []MappingSegment{
			{CaptureStartMs: 0, CaptureEndMs: 10000, MediaStartMs: 0, MediaEndMs: 10000},
			{CaptureStartMs: 12000, CaptureEndMs: 22000, MediaStartMs: 10000, MediaEndMs: 20000},
		}
		end, err := prepareCaptureEnd(mapping, 10000)
		Expect(err).NotTo(HaveOccurred())
		Expect(end).To(Equal(10000.0))
		start, err := captureAtMedia(mapping, 10000)
		Expect(err).NotTo(HaveOccurred())
		Expect(start).To(Equal(12000.0))
	})

	It("does not overwrite an existing fixture or connect before validating arguments", func() {
		dir, err := os.MkdirTemp("", "live-replay-existing-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		filename := filepath.Join(dir, "manifest.json")
		Expect(os.WriteFile(filename, []byte("preserved"), 0600)).To(Succeed())
		err = prepareFixture(context.Background(), PrepareOptions{DatabaseURL: "intentionally-invalid", Bucket: "bucket", OutputDir: dir, SourceSessionID: "source", MediaEndSecs: 300, ChunkSeconds: 10})
		Expect(err).To(MatchError(ContainSubstring("already exists")))
		data, err := os.ReadFile(filename)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("preserved"))
	})

	It("preserves movement and legacy appearance input without copying derived AI output", func() {
		movements, err := decodeReplayMovements([]byte(`[" Power Clean ","power clean","Pull-up",""]`))
		Expect(err).NotTo(HaveOccurred())
		Expect(movements).To(Equal([]string{"Power Clean", "Pull-up"}))
		appearance, err := decodeReplayAppearance([]byte(`{"session":{"top":"black shirt","bottom":"blue shorts"},"persistent":{"hair":"short"},"removable":["gloves"]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(appearance).To(Equal("black shirt, blue shorts, short, gloves"))
	})

	It("rejects identity mismatch and a corrupt cropped sensor footer", func() {
		_, err := telemetryBaseEpoch([]byte(telemetrySourceFixture), 10, "source")
		Expect(err).To(HaveOccurred())
		cropped, _, err := cropTelemetry([]byte(telemetrySourceFixture), 1000, 3000, "source", 1, 1700000001000)
		Expect(err).NotTo(HaveOccurred())
		Expect(validatePreparedTelemetry(cropped, 1, "source")).To(Succeed())
		corrupt := strings.ReplaceAll(string(cropped), `"k":"end"`, `"k":"incomplete"`)
		Expect(validatePreparedTelemetry([]byte(corrupt), 1, "source")).NotTo(Succeed())
	})

	It("prepares real video chunks without AAC padding, retaining FPS and half-open HR peaks", func() {
		for _, binary := range []string{"ffmpeg", "ffprobe"} {
			if _, err := exec.LookPath(binary); err != nil {
				Skip(binary + " is unavailable")
			}
		}
		dir, err := os.MkdirTemp("", "live-replay-media-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		ctx := context.Background()
		source := filepath.Join(dir, "source.mp4")
		output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25", "-f", "lavfi", "-i", "sine=frequency=440", "-t", "3", "-c:v", "libx264", "-c:a", "aac", source).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		_, fps, err := probeVideo(ctx, source)
		Expect(err).NotTo(HaveOccurred())
		Expect(fps).To(Equal(25.0))
		telemetry := []byte(`{"k":"meta","schema_version":"2.0.0","clock_source":"capture_clock"}
{"k":"hr","t":0,"bpm":140,"contact":true}
{"k":"hr","t":900,"bpm":145,"contact":true}
{"k":"hr","t":1000,"bpm":160,"contact":true}
{"k":"hr","t":1900,"bpm":165,"contact":true}
{"k":"end","t":2000,"pause_intervals":[],"summary":{}}
`)
		mapping := []MappingSegment{{CaptureStartMs: 10000, CaptureEndMs: 13000, MediaStartMs: 0, MediaEndMs: 3000}}
		chunks, err := prepareVideoChunks(ctx, source, dir, PrepareOptions{MediaStartSecs: 0.5, MediaEndSecs: 2.5, ChunkSeconds: 1}, mapping, 10500, telemetry, fps)
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).To(HaveLen(2))
		Expect(chunks[0].HeartRateBPM).To(Equal(145))
		Expect(chunks[1].HeartRateBPM).To(Equal(165))
		Expect(chunks[0].CaptureStartSecs).To(Equal(0.0))
		Expect(chunks[0].CaptureEndSecs).To(Equal(1.0))
		Expect(chunks[1].CaptureStartSecs).To(Equal(1.0))
		Expect(chunks[1].MediaEndSecs).To(Equal(2.0))
		for _, chunk := range chunks {
			data, err := checkedFile(dir, chunk.File, chunk.SHA256, chunk.SizeBytes)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).NotTo(BeEmpty())
			filename, err := fixturePath(dir, chunk.File)
			Expect(err).NotTo(HaveOccurred())
			duration, preparedFPS, err := probeVideo(ctx, filename)
			Expect(err).NotTo(HaveOccurred())
			// A cut at 0.5s lies between 25 FPS frames; the final segment may
			// differ by one frame, as allowed by the prepared-video contract.
			Expect(duration).To(BeNumerically("~", 1, 1/fps+0.001))
			Expect(preparedFPS).To(Equal(25.0))
			output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,width,height", "-of", "json", filename).Output()
			Expect(err).NotTo(HaveOccurred())
			var probe struct {
				Streams []struct {
					Type          string `json:"codec_type"`
					Width, Height int
				} `json:"streams"`
			}
			Expect(json.Unmarshal(output, &probe)).To(Succeed())
			Expect(probe.Streams).To(HaveLen(1))
			Expect(probe.Streams[0].Type).To(Equal("video"))
			Expect(probe.Streams[0].Width).To(BeNumerically("<=", 1280))
			Expect(probe.Streams[0].Height).To(BeNumerically("<=", 720))
		}
	})

	It("keeps ten-second chunk boundaries when a shifted 30 FPS source puts keyframes before muxer thresholds", func() {
		for _, binary := range []string{"ffmpeg", "ffprobe"} {
			if _, err := exec.LookPath(binary); err != nil {
				Skip(binary + " is unavailable")
			}
		}
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		original := filepath.Join(dir, "original.mp4")
		output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=30", "-t", "40", "-an", "-c:v", "libx264", "-preset", "ultrafast", original).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		// This 10-tick MP4 timestamp shift reproduces the retained session's
		// first forced keyframe at DTS 9.999349. With a zero segment tolerance
		// the old command missed the 10s boundary and produced 18.333s/10s/1.667s.
		source := filepath.Join(dir, "shifted-source.mp4")
		output, err = exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", original, "-c", "copy", "-output_ts_offset", "0.000651", source).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		probe, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=start_time", "-of", "json", source).Output()
		Expect(err).NotTo(HaveOccurred())
		var sourceTiming struct {
			Format struct {
				Start string `json:"start_time"`
			} `json:"format"`
		}
		Expect(json.Unmarshal(probe, &sourceTiming)).To(Succeed())
		Expect(sourceTiming.Format.Start).To(Equal("0.000651"))
		_, fps, err := probeVideo(ctx, source)
		Expect(err).NotTo(HaveOccurred())
		Expect(fps).To(Equal(30.0))
		var telemetry strings.Builder
		encoder := json.NewEncoder(&telemetry)
		Expect(encoder.Encode(map[string]any{"k": "meta", "schema_version": "2.0.0", "clock_source": "capture_clock"})).To(Succeed())
		for second := range 30 {
			Expect(encoder.Encode(map[string]any{"k": "hr", "t": second * 1000, "bpm": 150, "contact": true})).To(Succeed())
		}
		Expect(encoder.Encode(map[string]any{"k": "end", "t": 30000, "pause_intervals": []any{}, "summary": map[string]any{"hr_samples": 30, "acc_samples": 0}})).To(Succeed())
		mapping := []MappingSegment{{MediaStartMs: 0, MediaEndMs: 40000, CaptureStartMs: 10000, CaptureEndMs: 50000}}
		chunks, err := prepareVideoChunks(ctx, source, dir, PrepareOptions{MediaStartSecs: 5, MediaEndSecs: 35, ChunkSeconds: 10}, mapping, 15000, []byte(telemetry.String()), fps)
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).To(HaveLen(3))
		totalDuration := 0.0
		for index, chunk := range chunks {
			filename, err := fixturePath(dir, chunk.File)
			Expect(err).NotTo(HaveOccurred())
			duration, preparedFPS, err := probeVideo(ctx, filename)
			Expect(err).NotTo(HaveOccurred())
			Expect(duration).To(BeNumerically("<=", 10+1/fps+0.001), "chunk %d exceeded the recording cadence", index)
			Expect(duration).To(BeNumerically("~", 10, 1/fps+0.001), "chunk %d lost the intended boundary", index)
			Expect(preparedFPS).To(Equal(fps))
			Expect(chunk.MediaStartSecs).To(BeNumerically("~", float64(index*10), 1/fps+0.001))
			Expect(chunk.MediaEndSecs).To(BeNumerically("~", float64((index+1)*10), 1/fps+0.001))
			Expect(chunk.CaptureStartSecs).To(BeNumerically("~", float64(index*10), 1/fps+0.001))
			Expect(chunk.CaptureEndSecs).To(BeNumerically("~", float64((index+1)*10), 1/fps+0.001))
			totalDuration += duration
		}
		Expect(totalDuration).To(BeNumerically("~", 30, 1/fps+0.001))
	})
})
