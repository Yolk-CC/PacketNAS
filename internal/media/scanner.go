package media

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/bmp"
	"golang.org/x/image/webp"

	"pocket-nas/internal/files"
	"pocket-nas/internal/livephoto"
)

const scanWorkers = 4

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".heic": true, ".heif": true, ".bmp": true,
}

var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".webm": true,
	".avi": true, ".m4v": true,
}

func isImage(name string) bool { return imageExts[strings.ToLower(filepath.Ext(name))] }
func isVideo(name string) bool { return videoExts[strings.ToLower(filepath.Ext(name))] }
func isMedia(name string) bool { return isImage(name) || isVideo(name) }

// ScanRoot is one directory tree to scan (SPEC-M7 §4). Prefix is the
// virtual path prefix prepended to indexed paths: "" in legacy mode,
// "/<shareName>" in shared mode.
type ScanRoot struct {
	Prefix string
	Dir    string // resolved absolute directory
}

// Scanner walks the storage root(s) and keeps the index up to date.
type Scanner struct {
	store     *Store
	root      string                           // resolved root (files.ResolveRoot)
	rootsFn   func() []ScanRoot                // nil → legacy single root
	resolveFn func(rel string) (string, error) // nil → files.Resolve(sc.root, rel)
	scanning  atomic.Bool
	progress  atomic.Int64 // items processed in the current/last scan
	onDone    atomic.Value // func(), M11 hook fired after each completed scan
}

// SetOnDone registers a hook (e.g. the faces recognizer) invoked after
// every scan that ran to completion (not cancelled).
func (sc *Scanner) SetOnDone(fn func()) { sc.onDone.Store(fn) }

// NewScanner creates a Scanner for root (should be files.ResolveRoot output).
func NewScanner(store *Store, root string) *Scanner {
	return &Scanner{store: store, root: root}
}

// SetRootsFn makes the scanner traverse the roots returned by fn on every
// scan (shared mode: one ScanRoot per share). fn is consulted per scan, so
// share changes take effect on the next scan. resolveFn (may be nil)
// resolves virtual paths back to absolute paths (e.g. for iOS Live Photo
// companion lookup).
func (sc *Scanner) SetRootsFn(fn func() []ScanRoot, resolveFn func(rel string) (string, error)) {
	sc.rootsFn = fn
	sc.resolveFn = resolveFn
}

// resolve maps a virtual path to an absolute filesystem path.
func (sc *Scanner) resolve(rel string) (string, error) {
	if sc.resolveFn != nil {
		return sc.resolveFn(rel)
	}
	return files.Resolve(sc.root, rel)
}

// roots returns the current scan roots (legacy: just sc.root).
func (sc *Scanner) roots() []ScanRoot {
	if sc.rootsFn != nil {
		if roots := sc.rootsFn(); len(roots) > 0 {
			return roots
		}
	}
	return []ScanRoot{{Prefix: "", Dir: sc.root}}
}

// Scanning reports whether a scan is currently running.
func (sc *Scanner) Scanning() bool { return sc.scanning.Load() }

// Progress returns the number of items processed by the current/last scan.
func (sc *Scanner) Progress() int { return int(sc.progress.Load()) }

// fileJob is one discovered media file awaiting metadata extraction.
type fileJob struct {
	abs string
	rel string
}

// walk collects all media files under every scan root, skipping .pocketnas
// and hidden directories, honoring ctx cancellation. rel is the virtual
// path (scan-root prefix + path inside the root).
func (sc *Scanner) walk(ctx context.Context, fn func(abs, rel string, info fs.FileInfo) bool) error {
	for _, root := range sc.roots() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := sc.walkRoot(ctx, root, fn); err != nil {
			return err
		}
	}
	return nil
}

func (sc *Scanner) walkRoot(ctx context.Context, root ScanRoot, fn func(abs, rel string, info fs.FileInfo) bool) error {
	return filepath.WalkDir(root.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep scanning
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p == root.Dir {
				return nil
			}
			name := d.Name()
			if name == MetaDir || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isMedia(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel := root.Prefix + "/" + filepath.ToSlash(strings.TrimPrefix(p, root.Dir+string(os.PathSeparator)))
		if !fn(p, rel, info) {
			return context.Canceled
		}
		return nil
	})
}

// processPool fans jobs out to scanWorkers goroutines; each worker extracts
// metadata and upserts the row. Returns the number of processed items.
func (sc *Scanner) processPool(ctx context.Context, jobs <-chan fileJob, mtimes map[string]int64, progress chan<- int) (int, error) {
	var (
		wg        sync.WaitGroup
		processed atomic.Int64
		firstErr  atomic.Value
	)
	report := func() {
		n := int(processed.Add(1))
		sc.progress.Add(1)
		if progress != nil {
			select {
			case progress <- n:
			default: // never block the scan on a slow consumer
			}
		}
	}
	for i := 0; i < scanWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				info, err := os.Stat(j.abs)
				if err != nil {
					continue
				}
				mtime := info.ModTime().Unix()
				if mtimes != nil {
					if old, ok := mtimes[j.rel]; ok && old == mtime {
						report() // unchanged, keep existing row
						continue
					}
				}
				m := extract(ctx, j.abs, j.rel, info)
				if err := sc.store.Upsert(m); err != nil && firstErr.Load() == nil {
					firstErr.Store(err)
				}
				report()
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return int(processed.Load()), err
	}
	if v := firstErr.Load(); v != nil {
		return int(processed.Load()), v.(error)
	}
	return int(processed.Load()), nil
}

// Full re-extracts metadata for every media file under root and removes
// index rows for files that no longer exist. progress (optional) receives
// running processed counts.
func (sc *Scanner) Full(ctx context.Context, progress chan<- int) error {
	return sc.scan(ctx, nil, progress)
}

// Incremental re-extracts only files whose mtime changed since they were
// indexed, and removes rows for deleted files.
func (sc *Scanner) Incremental(ctx context.Context) error {
	mtimes, err := sc.store.ModifiedTimes()
	if err != nil {
		return err
	}
	return sc.scan(ctx, mtimes, nil)
}

func (sc *Scanner) scan(ctx context.Context, mtimes map[string]int64, progress chan<- int) error {
	if !sc.scanning.CompareAndSwap(false, true) {
		return errors.New("scan already in progress")
	}
	defer sc.scanning.Store(false)
	sc.progress.Store(0)

	jobs := make(chan fileJob, scanWorkers*2)
	seen := make(map[string]bool)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(jobs)
		_ = sc.walk(ctx, func(abs, rel string, _ fs.FileInfo) bool {
			seen[rel] = true
			select {
			case jobs <- fileJob{abs: abs, rel: rel}:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()

	_, err := sc.processPool(ctx, jobs, mtimes, progress)
	wg.Wait()
	if err != nil && errors.Is(err, context.Canceled) {
		return err
	}
	if derr := sc.store.DeleteMissing(seen); derr != nil && err == nil {
		err = derr
	}
	// M3: iOS Live Photo pairing runs after every scan so both new pairs
	// and removed companions are reflected.
	if perr := sc.pairIOSLivePhotos(ctx); perr != nil && err == nil {
		err = perr
	}
	if v := sc.onDone.Load(); v != nil {
		v.(func())()
	}
	return err
}

// parseLivePhoto reads the file head and runs the livephoto parser.
// Failures yield "none" and never break the scan.
func parseLivePhoto(abs string) livephoto.Info {
	f, err := os.Open(abs)
	if err != nil {
		return livephoto.None
	}
	defer f.Close()
	head := make([]byte, livephoto.HeadSize())
	n, _ := io.ReadFull(f, head)
	return livephoto.Parse(abs, head[:n])
}

// pairIOSLivePhotos marks .heic/.jpg images having a same-name .mov
// companion in the same directory as iOS Live Photos, and clears the flag
// when the companion disappeared.
func (sc *Scanner) pairIOSLivePhotos(ctx context.Context) error {
	images, err := sc.store.ImageMedias()
	if err != nil {
		return err
	}
	for _, m := range images {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ext := strings.ToLower(filepath.Ext(m.Name))
		if ext != ".heic" && ext != ".heif" && ext != ".jpg" && ext != ".jpeg" {
			continue
		}
		companion, size, ok := sc.findCompanion(m)
		switch {
		case ok:
			if !m.IsLivePhoto || m.LiveType != "ios" || m.CompanionPath != companion {
				if err := sc.store.SetLiveInfo(m.Path, true, "ios", companion, 0, size); err != nil {
					return err
				}
			}
		case m.IsLivePhoto && m.LiveType == "ios":
			// Companion deleted between scans: clear the pairing.
			if err := sc.store.SetLiveInfo(m.Path, false, "", "", 0, 0); err != nil {
				return err
			}
		}
	}
	return nil
}

// findCompanion looks for a same-name .mov next to the image. Same-name
// match wins; among multiple case variants the one whose mtime is within
// 5s of the image's is preferred.
func (sc *Scanner) findCompanion(m Media) (string, int64, bool) {
	dir := filepath.Dir(strings.TrimPrefix(m.Path, "/"))
	base := strings.TrimSuffix(m.Name, filepath.Ext(m.Name))
	type cand struct {
		rel   string
		mtime int64
		size  int64
	}
	var cands []cand
	for _, movExt := range []string{".mov", ".MOV"} {
		rel := "/" + filepath.ToSlash(filepath.Join(dir, base+movExt))
		abs, err := sc.resolve(rel)
		if err != nil {
			continue
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			continue
		}
		cands = append(cands, cand{rel, st.ModTime().Unix(), st.Size()})
	}
	if len(cands) == 0 {
		return "", 0, false
	}
	best := cands[0]
	for _, c := range cands[1:] {
		dBest := best.mtime - m.ModifiedTime
		if dBest < 0 {
			dBest = -dBest
		}
		dC := c.mtime - m.ModifiedTime
		if dC < 0 {
			dC = -dC
		}
		if dC < 5 && dC < dBest {
			best = c
		}
	}
	return best.rel, best.size, true
}

// extract builds a Media row for one file. Failures are non-fatal: unknown
// fields stay zero and taken_time falls back to mtime.
func extract(ctx context.Context, abs, rel string, info fs.FileInfo) Media {
	mtime := info.ModTime().Unix()
	m := Media{
		Path:         rel,
		Name:         filepath.Base(abs),
		MimeType:     files.MimeType(filepath.Base(abs), false),
		Size:         info.Size(),
		ModifiedTime: mtime,
		TakenTime:    mtime,
		CreatedAt:    time.Now().Unix(),
	}
	ext := strings.ToLower(filepath.Ext(abs))
	switch {
	case isImage(abs):
		if ext != ".heic" && ext != ".heif" {
			m.Width, m.Height = imageSize(abs)
			if t, ok := exifTaken(abs); ok {
				m.TakenTime = t
			}
		}
		// M3: detect embedded Motion Photo video from the head bytes.
		if li := parseLivePhoto(abs); li.Type != "none" {
			m.IsLivePhoto = true
			m.LiveType = li.Type
			m.VideoOffset = li.VideoOffset
			m.VideoLength = li.VideoLength
		}
	case isVideo(abs):
		w, h, dur := probeVideo(ctx, abs)
		m.Width, m.Height, m.Duration = w, h, dur
	}
	return m
}

// imageSize reads only the image header to learn dimensions.
func imageSize(abs string) (int, int) {
	f, err := os.Open(abs)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(abs)) {
	case ".webp":
		cfg, err := webp.DecodeConfig(f)
		if err != nil {
			return 0, 0
		}
		return cfg.Width, cfg.Height
	case ".bmp":
		cfg, err := bmp.DecodeConfig(f)
		if err != nil {
			return 0, 0
		}
		return cfg.Width, cfg.Height
	default: // jpeg, png, gif via registered stdlib decoders
		cfg, _, err := image.DecodeConfig(f)
		if err != nil {
			return 0, 0
		}
		return cfg.Width, cfg.Height
	}
}

// exifTaken extracts EXIF DateTimeOriginal (JPEG only); ok=false on any
// failure so the caller falls back to mtime.
func exifTaken(abs string) (int64, bool) {
	ext := strings.ToLower(filepath.Ext(abs))
	if ext != ".jpg" && ext != ".jpeg" {
		return 0, false
	}
	f, err := os.Open(abs)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return 0, false
	}
	t, err := x.DateTime()
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// ffprobeJSON is the subset of ffprobe -print_format json output we need.
type ffprobeJSON struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// probeVideo runs ffprobe and returns width/height/duration(ms) of the first
// video stream. Missing ffprobe or any failure yields zeros (non-fatal).
func probeVideo(ctx context.Context, abs string) (int, int, int) {
	bin, err := exec.LookPath("ffprobe") // resolves ffprobe.exe on Windows
	if err != nil {
		return 0, 0, 0
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, bin,
		"-v", "quiet", "-print_format", "json",
		"-show_format", "-show_streams", abs).Output()
	if err != nil {
		return 0, 0, 0
	}
	var meta ffprobeJSON
	if err := json.Unmarshal(out, &meta); err != nil {
		return 0, 0, 0
	}
	var w, h int
	for _, s := range meta.Streams {
		if s.CodecType == "video" {
			w, h = s.Width, s.Height
			break
		}
	}
	var durMs int
	if sec, err := strconv.ParseFloat(meta.Format.Duration, 64); err == nil {
		durMs = int(sec * 1000)
	}
	return w, h, durMs
}
