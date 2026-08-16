package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// genVideo creates a small test video with ffmpeg. withAudio controls
// whether an audio track is included.
func genVideo(t *testing.T, path string, size string, secs int, withAudio bool) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc=duration=" + itoa(secs) + ":size=" + size + ":rate=10"}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration="+itoa(secs))
	}
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-shortest", "-y", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg gen: %v %s", err, out)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestBuildArgs(t *testing.T) {
	res := Resolutions["720p"]

	args := buildArgs("/in.mp4", "/out.tmp", res, true)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-c:v libx264", "-preset veryfast", "-b:v 2000k",
		"-c:a aac", "-b:a 128k", "+faststart", "-progress pipe:1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %s", want, joined)
		}
	}
	// Scale expression caps at source height (never upscale).
	if !strings.Contains(joined, "scale=-2:min(720") {
		t.Fatalf("scale expr: %s", joined)
	}

	// No audio stream → audio options dropped.
	args = buildArgs("/in.mp4", "/out.tmp", res, false)
	joined = strings.Join(args, " ")
	if strings.Contains(joined, "-c:a") {
		t.Fatalf("audio options present for silent source: %s", joined)
	}
}

func TestProbe(t *testing.T) {
	dir := t.TempDir()
	withAudio := filepath.Join(dir, "a.mp4")
	genVideo(t, withAudio, "160x120", 1, true)
	ha, dur := probe(context.Background(), withAudio)
	if !ha {
		t.Fatal("expected audio stream detected")
	}
	if dur < 500 || dur > 2000 {
		t.Fatalf("duration=%d", dur)
	}

	silent := filepath.Join(dir, "s.mp4")
	genVideo(t, silent, "160x120", 1, false)
	ha, _ = probe(context.Background(), silent)
	if ha {
		t.Fatal("silent video reported audio")
	}

	// Nonexistent input: non-fatal zeros.
	ha, dur = probe(context.Background(), filepath.Join(dir, "nope.mp4"))
	if ha || dur != 0 {
		t.Fatal("probe should fail soft")
	}
}

func TestRunTranscodesAndReportsProgress(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	genVideo(t, src, "320x240", 1, true)     // 240p source, 720p tier
	out := filepath.Join(dir, "out.tmp.mp4") // .mp4 suffix required for muxer inference

	hasAudio, durMs := probe(context.Background(), src)
	var lastPct int
	calls := 0
	err = run(context.Background(), bin, src, out, Resolutions["720p"], hasAudio, durMs,
		func(pct int) {
			calls++
			if pct < lastPct {
				t.Errorf("progress went backwards: %d < %d", pct, lastPct)
			}
			lastPct = pct
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || lastPct != 100 {
		t.Fatalf("progress calls=%d last=%d", calls, lastPct)
	}

	// Output height must stay 240 (min(720, ih)).
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("output missing: %v", err)
	}
	w, h := probeDims(t, out)
	if w != 320 || h != 240 {
		t.Fatalf("dims %dx%d, want 320x240 (no upscale)", w, h)
	}
}

func probeDims(t *testing.T, path string) (int, int) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json",
		"-show_streams", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	var w, h int
	if i := strings.Index(s, `"width": `); i >= 0 {
		w = atoiLoose(s[i+9:])
	}
	if i := strings.Index(s, `"height": `); i >= 0 {
		h = atoiLoose(s[i+10:])
	}
	return w, h
}

func atoiLoose(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
