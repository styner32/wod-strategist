package worker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// Media clocks are verified independently of format.duration, which can include
// an audio tail or an edit that is unrelated to the displayed video duration.
type videoTimeline struct {
	Start, End, Tick float64
	FrameCount       int
}

func (v videoTimeline) Duration() float64 { return v.End - v.Start }

type frameTiming struct{ pts, duration int64 }

type videoFrameSignature struct {
	FrameCount int
	Digest     string
	Start, End float64
	timings    []frameTiming
}

// Keep only diagnostic tails: full decode/probe output must never accumulate
// in CombinedOutput alongside a long workout's media on Cloud Run's tmpfs.
type mediaErrorTail struct{ data []byte }

func (b *mediaErrorTail) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 16 * 1024
	if len(p) >= limit {
		b.data = append(b.data[:0], p[len(p)-limit:]...)
	} else {
		if excess := len(b.data) + len(p) - limit; excess > 0 {
			copy(b.data, b.data[excess:])
			b.data = b.data[:len(b.data)-excess]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func consumeMediaOutput(cmd *exec.Cmd, consume func(io.Reader) error) error {
	var stderr mediaErrorTail
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	readErr := consume(stdout)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return fmt.Errorf("read media output: %w (%s)", readErr, strings.TrimSpace(string(stderr.data)))
	}
	if waitErr != nil {
		return fmt.Errorf("media command: %w (%s)", waitErr, strings.TrimSpace(string(stderr.data)))
	}
	// ffprobe can report decoding errors and still return exit code zero.
	// All callers use -v error, so any diagnostic here invalidates the evidence.
	if len(bytes.TrimSpace(stderr.data)) != 0 {
		return fmt.Errorf("media decoding error: %s", strings.TrimSpace(string(stderr.data)))
	}
	return nil
}

func mediaSeconds(raw string) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid media time %q", raw)
	}
	return v, nil
}

func mediaTimeBase(raw string) (float64, error) {
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid media time base %q", raw)
	}
	num, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || num <= 0 {
		return 0, fmt.Errorf("invalid media time base %q", raw)
	}
	den, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || den <= 0 {
		return 0, fmt.Errorf("invalid media time base %q", raw)
	}
	return float64(num) / float64(den), nil
}

func probeVideoTimeline(ctx context.Context, path string) (videoTimeline, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "V:0",
		"-show_frames", "-show_entries", "frame=pts_time,best_effort_timestamp_time,duration_time,pkt_duration_time:stream=time_base,start_time,duration",
		"-of", "json", path)
	var result videoTimeline
	lastPTS, lastDuration := 0.0, 0.0
	var streamEnd float64
	hasStreamEnd := false
	err := consumeMediaOutput(cmd, func(r io.Reader) error {
		dec := json.NewDecoder(r)
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return fmt.Errorf("invalid frame document: %v", err)
		}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			switch tok {
			case "frames":
				if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
					return fmt.Errorf("invalid frames: %v", err)
				}
				for dec.More() {
					var frame struct {
						PTS      string `json:"pts_time"`
						BestPTS  string `json:"best_effort_timestamp_time"`
						Duration string `json:"duration_time"`
						Packet   string `json:"pkt_duration_time"`
					}
					if err := dec.Decode(&frame); err != nil {
						return err
					}
					rawPTS := frame.PTS
					if rawPTS == "" {
						rawPTS = frame.BestPTS
					}
					pts, err := mediaSeconds(rawPTS)
					if err != nil {
						return err
					}
					if result.FrameCount > 0 && pts <= lastPTS {
						return fmt.Errorf("video presentation times are not increasing at frame %d", result.FrameCount)
					}
					if result.FrameCount == 0 {
						result.Start = pts
					}
					rawDuration := frame.Duration
					if rawDuration == "" {
						rawDuration = frame.Packet
					}
					lastDuration = 0
					if rawDuration != "" {
						lastDuration, err = mediaSeconds(rawDuration)
						if err != nil || lastDuration < 0 {
							return fmt.Errorf("invalid frame duration %q", rawDuration)
						}
					}
					lastPTS = pts
					result.FrameCount++
				}
				if _, err := dec.Token(); err != nil {
					return err
				}
			case "streams":
				var streams []struct {
					TimeBase string `json:"time_base"`
					Start    string `json:"start_time"`
					Duration string `json:"duration"`
				}
				if err := dec.Decode(&streams); err != nil {
					return err
				}
				if len(streams) != 1 {
					return fmt.Errorf("expected one displayed video stream")
				}
				result.Tick, err = mediaTimeBase(streams[0].TimeBase)
				if err != nil {
					return err
				}
				if streams[0].Start != "" && streams[0].Duration != "" {
					start, err := mediaSeconds(streams[0].Start)
					if err != nil {
						return err
					}
					duration, err := mediaSeconds(streams[0].Duration)
					if err != nil || duration <= 0 {
						return fmt.Errorf("invalid video stream duration %q", streams[0].Duration)
					}
					streamEnd, hasStreamEnd = start+duration, true
				}
			default:
				var ignored json.RawMessage
				if err := dec.Decode(&ignored); err != nil {
					return err
				}
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return videoTimeline{}, fmt.Errorf("probe displayed video %s: %w", path, err)
	}
	if result.FrameCount == 0 || lastDuration <= 0 || result.Tick <= 0 {
		return videoTimeline{}, fmt.Errorf("video has no verifiable frames, final frame duration or time base: %s", path)
	}
	result.End = lastPTS + lastDuration
	if hasStreamEnd {
		// With reordered VFR frames, the decoder can attach a decode-order
		// packet duration to the final presentation frame. The video track's
		// edit bounds may shorten that frame; the container's audio-inclusive
		// duration cannot. A bound before the last displayed frame is invalid.
		if streamEnd <= lastPTS {
			return videoTimeline{}, fmt.Errorf("video track ends before its final displayed frame: %s", path)
		}
		result.End = min(result.End, streamEnd)
	}
	if result.Duration() <= 0 || math.IsNaN(result.Duration()) || math.IsInf(result.Duration(), 0) {
		return videoTimeline{}, fmt.Errorf("video has no positive displayed duration: %s", path)
	}
	return result, nil
}

func verifyVideoDuration(ctx context.Context, path string, expected, tolerance float64) error {
	if expected <= 0 || tolerance < 0 || math.IsNaN(expected) || math.IsInf(expected, 0) || math.IsNaN(tolerance) || math.IsInf(tolerance, 0) {
		return fmt.Errorf("invalid expected video duration or tolerance")
	}
	timeline, err := probeVideoTimeline(ctx, path)
	if err != nil {
		return err
	}
	if math.Abs(timeline.Duration()-expected) > tolerance+1e-6 {
		return fmt.Errorf("displayed video duration %.6fs differs from expected %.6fs (tolerance %.6fs)", timeline.Duration(), expected, tolerance)
	}
	return nil
}

func scanVideoFrames(ctx context.Context, path string, visit func(frameTiming, []byte) error) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-xerror", "-copyts", "-noautorotate", "-i", path,
		"-map", "0:V:0", "-an", "-sn", "-dn", "-c:v", "rawvideo", "-pix_fmt", "yuv420p",
		"-fps_mode:v", "passthrough", "-enc_time_base:v", "1:30000", "-f", "framemd5", "-")
	return consumeMediaOutput(cmd, func(r io.Reader) error {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Split(line, ",")
			if len(fields) != 6 {
				return fmt.Errorf("invalid decoded frame record %q", line)
			}
			pts, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
			if err != nil {
				return err
			}
			duration, err := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64)
			if err != nil || duration <= 0 {
				return fmt.Errorf("invalid decoded frame duration %q", fields[3])
			}
			digest, err := hex.DecodeString(strings.TrimSpace(fields[5]))
			if err != nil || len(digest) != 16 {
				return fmt.Errorf("invalid decoded frame digest")
			}
			if err := visit(frameTiming{pts, duration}, digest); err != nil {
				return err
			}
		}
		return scanner.Err()
	})
}

func readVideoFrameSignature(ctx context.Context, path string) (videoFrameSignature, error) {
	var result videoFrameSignature
	digest := sha256.New()
	err := scanVideoFrames(ctx, path, func(t frameTiming, frameHash []byte) error {
		if result.FrameCount > 0 && t.pts <= result.timings[len(result.timings)-1].pts {
			return fmt.Errorf("decoded frame timestamps are not increasing")
		}
		if result.FrameCount == 0 {
			result.Start = float64(t.pts) / 30000
		}
		result.End = float64(t.pts+t.duration) / 30000
		result.FrameCount++
		result.timings = append(result.timings, t)
		_, _ = digest.Write(frameHash)
		return nil
	})
	if err != nil {
		return videoFrameSignature{}, err
	}
	if result.FrameCount == 0 {
		return videoFrameSignature{}, fmt.Errorf("video contains no decoded frames: %s", path)
	}
	result.Digest = hex.EncodeToString(digest.Sum(nil))
	return result, nil
}

func verifyVideoFrameSequence(ctx context.Context, path string, expected []videoFrameSignature) error {
	if len(expected) == 0 {
		return fmt.Errorf("no expected video frame sequence")
	}
	for _, signature := range expected {
		if signature.FrameCount <= 0 || len(signature.timings) != signature.FrameCount || signature.Digest == "" {
			return fmt.Errorf("incomplete expected video frame signature")
		}
	}
	chunk, frame := 0, 0
	cursor := int64(0)
	var digest hash.Hash = sha256.New()
	err := scanVideoFrames(ctx, path, func(t frameTiming, frameHash []byte) error {
		if chunk >= len(expected) {
			return fmt.Errorf("merged video contains extra frames")
		}
		signature := expected[chunk]
		source := signature.timings[frame]
		wantPTS := cursor + source.pts - signature.timings[0].pts
		if math.Abs(float64(t.pts-wantPTS)) > 1 || math.Abs(float64(t.duration-source.duration)) > 1 {
			return fmt.Errorf("video timestamp mismatch at chunk %d frame %d: PTS %d expected %d", chunk, frame, t.pts, wantPTS)
		}
		_, _ = digest.Write(frameHash)
		frame++
		if frame == signature.FrameCount {
			if hex.EncodeToString(digest.Sum(nil)) != signature.Digest {
				return fmt.Errorf("video frame content/order mismatch at chunk %d", chunk)
			}
			cursor += signature.timings[len(signature.timings)-1].pts + signature.timings[len(signature.timings)-1].duration - signature.timings[0].pts
			chunk++
			frame = 0
			digest.Reset()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if chunk != len(expected) || frame != 0 {
		return fmt.Errorf("merged video omitted frames at chunk %d frame %d", chunk, frame)
	}
	return nil
}

func verifyMediaEndpoints(ctx context.Context, path string, expected, tolerance float64) error {
	if err := verifyVideoDuration(ctx, path, expected, tolerance); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries",
		"stream=codec_type,start_time,duration:format=start_time,duration", "-of", "json", path)
	var doc struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Start    string `json:"start_time"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Start    string `json:"start_time"`
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := consumeMediaOutput(cmd, func(r io.Reader) error { return json.NewDecoder(r).Decode(&doc) }); err != nil {
		return err
	}
	check := func(label, rawStart, rawDuration string) error {
		start, err := mediaSeconds(rawStart)
		if err != nil {
			return err
		}
		duration, err := mediaSeconds(rawDuration)
		if err != nil || duration <= 0 {
			return fmt.Errorf("invalid %s duration %q", label, rawDuration)
		}
		if math.Abs(start) > tolerance+1e-6 || math.Abs(start+duration-expected) > tolerance+1e-6 {
			return fmt.Errorf("%s endpoints [%.6f, %.6f] diverge from [0, %.6f] (tolerance %.6f)", label, start, start+duration, expected, tolerance)
		}
		return nil
	}
	videoCount, audioCount := 0, 0
	for _, stream := range doc.Streams {
		if stream.Type != "video" && stream.Type != "audio" {
			continue
		}
		if stream.Type == "video" {
			videoCount++
		} else {
			audioCount++
		}
		if err := check(stream.Type, stream.Start, stream.Duration); err != nil {
			return err
		}
	}
	if videoCount != 1 {
		return fmt.Errorf("expected exactly one output video stream")
	}
	if audioCount > 1 {
		return fmt.Errorf("expected at most one output audio stream")
	}
	return check("container", doc.Format.Start, doc.Format.Duration)
}
