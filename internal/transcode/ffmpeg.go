// Package transcode implements on-demand multi-resolution video transcoding
// with ffmpeg: a job queue, disk cache and progress reporting.
package transcode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Res is one transcoding tier.
type Res struct {
	Name   string // "360p" | "720p" | "1080p"
	Height int
	VRate  string
	ARate  string
}

// Resolutions are the fixed transcoding tiers (SPEC-M4 §1.1).
var Resolutions = map[string]Res{
	"360p":  {Name: "360p", Height: 360, VRate: "800k", ARate: "96k"},
	"720p":  {Name: "720p", Height: 720, VRate: "2000k", ARate: "128k"},
	"1080p": {Name: "1080p", Height: 1080, VRate: "4000k", ARate: "128k"},
}

// ValidRes reports whether res is a requestable transcoding tier.
func ValidRes(res string) bool {
	_, ok := Resolutions[res]
	return ok
}

// buildArgs constructs the ffmpeg command line. The scale expression uses
// min(target, ih) so sources smaller than the tier are never upscaled.
// hasAudio=false drops the audio codec options entirely (sources without
// an audio stream would fail with -c:a otherwise).
func buildArgs(input, output string, res Res, hasAudio bool) []string {
	args := []string{
		"-progress", "pipe:1", "-nostats",
		"-y", "-i", input,
		"-vf", fmt.Sprintf("scale=-2:min(%d\\,ih)", res.Height),
		"-c:v", "libx264", "-preset", "veryfast", "-b:v", res.VRate,
	}
	if hasAudio {
		args = append(args, "-c:a", "aac", "-b:a", res.ARate)
	}
	args = append(args, "-movflags", "+faststart", output)
	return args
}

// probeResult is the subset of ffprobe JSON output we need.
type probeResult struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// probe returns (hasAudio, durationMs). Missing ffprobe or any failure
// yields (false, 0) and is non-fatal: transcoding then runs without audio
// options and without progress percentages.
func probe(ctx context.Context, input string) (bool, int64) {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return false, 0
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, bin,
		"-v", "quiet", "-print_format", "json",
		"-show_format", "-show_streams", input).Output()
	if err != nil {
		return false, 0
	}
	var pr probeResult
	if err := json.Unmarshal(out, &pr); err != nil {
		return false, 0
	}
	hasAudio := false
	for _, s := range pr.Streams {
		if s.CodecType == "audio" {
			hasAudio = true
			break
		}
	}
	var durMs int64
	if sec, err := strconv.ParseFloat(pr.Format.Duration, 64); err == nil {
		durMs = int64(sec * 1000)
	}
	return hasAudio, durMs
}

// cappedWriter discards all writes after a small buffer, so a chatty stderr
// can never block the child process.
type cappedWriter struct {
	buf []byte
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if len(w.buf) < w.max {
		remain := w.max - len(w.buf)
		if len(p) < remain {
			remain = len(p)
		}
		w.buf = append(w.buf, p[:remain]...)
	}
	return len(p), nil
}

// run transcodes input to output (a .tmp path renamed by the caller),
// reporting progress percentages via onProgress. durationMs=0 disables
// percentage reporting.
func run(ctx context.Context, ffmpegBin, input, output string, res Res,
	hasAudio bool, durationMs int64, onProgress func(int)) error {

	args := buildArgs(input, output, res, hasAudio)
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &cappedWriter{max: 64 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	// Parse "-progress pipe:1" key=value lines. out_time_ms is in
	// microseconds despite its name (AV_TIME_BASE units).
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "out_time_ms=") || durationMs <= 0 {
			continue
		}
		us, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_ms="), 10, 64)
		if err != nil {
			continue
		}
		pct := int(us / 1000 * 100 / durationMs)
		if pct > 99 {
			pct = 99 // 100 is reserved for a finished+renamed output
		}
		if pct >= 0 && onProgress != nil {
			onProgress(pct)
		}
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(string(stderr.buf))
		if msg == "" {
			msg = err.Error()
		} else if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return fmt.Errorf("ffmpeg: %s", msg)
	}
	if onProgress != nil {
		onProgress(100)
	}
	return nil
}
