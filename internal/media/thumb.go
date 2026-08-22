package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/disintegration/imaging"
)

const (
	// ThumbCacheLimit is the total thumbnail cache budget; startup eviction
	// trims to 80% of it once exceeded.
	ThumbCacheLimit = 500 << 20
	thumbEvictTo    = ThumbCacheLimit * 80 / 100
	thumbWorkers    = 2 // generation concurrency limit (OOM guard)
)

// ErrNotThumbable marks media kinds we cannot make a thumbnail for.
var ErrNotThumbable = errors.New("media type not supported for thumbnails")

// Thumber generates and caches thumbnails on disk under
// <root>/.pocketnas/thumb/, keyed by sha256 of the root-relative path,
// requested size and source mtime (so size variants coexist and edits
// invalidate stale entries).
type Thumber struct {
	root     string // resolved root
	thumbDir string
	sem      chan struct{}
}

// NewThumber creates a Thumber and runs startup LRU eviction.
func NewThumber(root string) (*Thumber, error) {
	dir := filepath.Join(root, MetaDir, ThumbDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	t := &Thumber{root: root, thumbDir: dir, sem: make(chan struct{}, thumbWorkers)}
	EvictLRU(dir, ThumbCacheLimit)
	return t, nil
}

// cacheName returns the cache file name for a root-relative path at the
// requested size w×h and source modification time mtime (unix seconds).
func cacheName(rel string, w, h int, mtime int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%dx%d|%d", rel, w, h, mtime)))
	return hex.EncodeToString(sum[:]) + ".jpg"
}

// Get returns the absolute path of the cached thumbnail for abs/rel,
// generating it on a cache miss. Generation is limited to thumbWorkers
// concurrent jobs.
func (t *Thumber) Get(ctx context.Context, abs, rel string, w, h int) (string, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(t.thumbDir, cacheName(rel, w, h, info.ModTime().Unix()))
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil // cache hit
	}
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	// Re-check: another request may have generated it while we waited.
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}
	tmp := dst + ".tmp.jpg" // keep .jpg suffix: imaging/ffmpeg infer format by extension
	defer os.Remove(tmp)
	if err := t.generate(ctx, abs, tmp, w, h); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func (t *Thumber) generate(ctx context.Context, abs, dst string, w, h int) error {
	ext := strings.ToLower(filepath.Ext(abs))
	switch {
	case ext == ".heic" || ext == ".heif":
		return t.ffmpegFrame(ctx, abs, dst, w, h, "")
	case isVideo(abs):
		return t.ffmpegFrame(ctx, abs, dst, w, h, "1") // first-second frame
	case isImage(abs):
		src, err := imaging.Open(abs)
		if err != nil {
			return err
		}
		return imaging.Save(imaging.Fit(src, w, h, imaging.Lanczos), dst, imaging.JPEGQuality(85))
	default:
		return ErrNotThumbable
	}
}

// ffmpegFrame extracts one frame via ffmpeg, scaled to fit w×h keeping
// aspect. seek (seconds, "" = none) fast-seeks before decoding for videos.
func (t *Thumber) ffmpegFrame(ctx context.Context, abs, dst string, w, h int, seek string) error {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return err
	}
	args := []string{"-v", "error"}
	if seek != "" {
		args = append(args, "-ss", seek)
	}
	args = append(args, "-i", abs, "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", w, h),
		"-y", dst)
	pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return exec.CommandContext(pctx, bin, args...).Run()
}

// EvictLRU trims dir to 80% of limit by deleting least-recently-modified
// files first. Called once at startup.
func EvictLRU(dir string, limit int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name  string
		size  int64
		mtime time.Time
	}
	var items []item
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		items = append(items, item{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= limit {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime.Before(items[j].mtime) })
	target := limit * 80 / 100
	for _, it := range items {
		if total <= target {
			break
		}
		if err := os.Remove(filepath.Join(dir, it.name)); err == nil {
			total -= it.size
		}
	}
}
