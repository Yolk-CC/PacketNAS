package media

import (
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"pocket-nas/internal/files"
)

func TestThumbnailGenerationAndCache(t *testing.T) {
	root := t.TempDir()
	makeJPEG(t, filepath.Join(root, "photo.jpg"), 600, 400)
	th, err := NewThumber(files.ResolveRoot(root))
	if err != nil {
		t.Fatal(err)
	}

	p1, err := th.Get(context.Background(), filepath.Join(root, "photo.jpg"), "/photo.jpg", 300, 300)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p1)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 300 || cfg.Height != 200 { // Fit keeps aspect ratio
		t.Fatalf("thumb dims %dx%d", cfg.Width, cfg.Height)
	}

	// Cache hit: second call returns the same path without regeneration.
	st1, _ := os.Stat(p1)
	p2, err := th.Get(context.Background(), filepath.Join(root, "photo.jpg"), "/photo.jpg", 300, 300)
	if err != nil || p1 != p2 {
		t.Fatalf("cache path mismatch: %q vs %q err=%v", p1, p2, err)
	}
	st2, _ := os.Stat(p2)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("cache was regenerated on hit")
	}

	// Corrupt image fails generation (caller redirects to placeholder).
	writeFile(t, filepath.Join(root, "broken.jpg"), []byte("garbage"))
	if _, err := th.Get(context.Background(), filepath.Join(root, "broken.jpg"), "/broken.jpg", 300, 300); err == nil {
		t.Fatal("expected error for corrupt image")
	}
}

func TestCacheNameKeyIncludesSizeAndMtime(t *testing.T) {
	base := cacheName("/photo.jpg", 300, 300, 1000)
	if cacheName("/photo.jpg", 300, 300, 1000) != base {
		t.Fatal("cache key not deterministic")
	}
	if cacheName("/photo.jpg", 640, 480, 1000) == base {
		t.Fatal("different sizes must not share a cache key")
	}
	if cacheName("/photo.jpg", 300, 300, 2000) == base {
		t.Fatal("different mtimes must not share a cache key")
	}
	if cacheName("/other.jpg", 300, 300, 1000) == base {
		t.Fatal("different paths must not share a cache key")
	}
}

func TestThumbnailCacheInvalidation(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "photo.jpg")
	makeJPEG(t, abs, 600, 400)
	th, err := NewThumber(files.ResolveRoot(root))
	if err != nil {
		t.Fatal(err)
	}

	p300, err := th.Get(context.Background(), abs, "/photo.jpg", 300, 300)
	if err != nil {
		t.Fatal(err)
	}
	// Same path, different requested size -> different cache entry.
	p640, err := th.Get(context.Background(), abs, "/photo.jpg", 640, 640)
	if err != nil {
		t.Fatal(err)
	}
	if p300 == p640 {
		t.Fatal("different sizes produced the same cache file")
	}
	// Cache hit for the original size still works.
	again, err := th.Get(context.Background(), abs, "/photo.jpg", 300, 300)
	if err != nil || again != p300 {
		t.Fatalf("expected cache hit at %q, got %q err=%v", p300, again, err)
	}
	// Source updated -> old cache entry must not be served again.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, future, future); err != nil {
		t.Fatal(err)
	}
	pNew, err := th.Get(context.Background(), abs, "/photo.jpg", 300, 300)
	if err != nil {
		t.Fatal(err)
	}
	if pNew == p300 {
		t.Fatal("stale cache served after source mtime changed")
	}
}

func TestThumbnailFromVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	mp4 := filepath.Join(root, "clip.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "testsrc=duration=2:size=640x360:rate=10", "-y", mp4).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	th, err := NewThumber(files.ResolveRoot(root))
	if err != nil {
		t.Fatal(err)
	}
	p, err := th.Get(context.Background(), mp4, "/clip.mp4", 300, 300)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(p)
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 300 || cfg.Height > 300 {
		t.Fatalf("video thumb too large: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestEvictLRU(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Minute) // a oldest, c newest
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	// total 300 bytes, limit 200 -> evict down to 160 bytes: deletes a,b.
	EvictLRU(dir, 200)
	if _, err := os.Stat(filepath.Join(dir, "a.jpg")); !os.IsNotExist(err) {
		t.Fatal("oldest file not evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "c.jpg")); err != nil {
		t.Fatal("newest file wrongly evicted")
	}

	// Under limit: no-op.
	EvictLRU(dir, 1<<20)
	if _, err := os.Stat(filepath.Join(dir, "c.jpg")); err != nil {
		t.Fatal("evicted while under limit")
	}
}
