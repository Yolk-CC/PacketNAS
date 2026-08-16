package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"pocket-nas/internal/files"
)

// buildTestRoot creates a directory tree covering EXIF JPEG, plain JPEG,
// PNG, hidden dirs, .pocketnas and a fake mp4.
func buildTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	makeJPEGExif(t, filepath.Join(root, "exif.jpg"), 800, 600, "2020:05:01 12:30:00")
	makeJPEG(t, filepath.Join(root, "plain.jpg"), 400, 300)
	makePNG(t, filepath.Join(root, "sub", "pic.png"), 120, 90)
	writeFile(t, filepath.Join(root, "fake.mp4"), []byte("not really a video"))
	writeFile(t, filepath.Join(root, ".hidden", "secret.jpg"), []byte("x"))
	writeFile(t, filepath.Join(root, MetaDir, "junk.jpg"), []byte("x"))
	writeFile(t, filepath.Join(root, "notes.txt"), []byte("not media"))
	return root
}

func openScanner(t *testing.T, root string) (*Store, *Scanner) {
	t.Helper()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, NewScanner(st, files.ResolveRoot(root))
}

func TestFullScanIndexesMedia(t *testing.T) {
	root := buildTestRoot(t)
	st, sc := openScanner(t, root)

	progress := make(chan int, 64)
	if err := sc.Full(context.Background(), progress); err != nil {
		t.Fatal(err)
	}

	items, total, err := st.Page(0, 100, "all")
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("total=%d items=%v", total, items)
	}
	byPath := map[string]Media{}
	for _, m := range items {
		byPath[m.Path] = m
	}

	exif := byPath["/exif.jpg"]
	if exif.Width != 800 || exif.Height != 600 {
		t.Fatalf("exif.jpg dims: %+v", exif)
	}
	// goexif parses EXIF datetimes (no zone info) in time.Local.
	wantTaken := time.Date(2020, 5, 1, 12, 30, 0, 0, time.Local).Unix()
	if exif.TakenTime != wantTaken {
		t.Fatalf("EXIF taken=%d want %d", exif.TakenTime, wantTaken)
	}

	plain := byPath["/plain.jpg"]
	if plain.Width != 400 || plain.Height != 300 {
		t.Fatalf("plain.jpg dims: %+v", plain)
	}
	st1, _ := os.Stat(filepath.Join(root, "plain.jpg"))
	if plain.TakenTime != st1.ModTime().Unix() {
		t.Fatalf("no-EXIF taken should fall back to mtime: %d vs %d", plain.TakenTime, st1.ModTime().Unix())
	}

	if byPath["/sub/pic.png"].Width != 120 || byPath["/sub/pic.png"].Height != 90 {
		t.Fatalf("png dims: %+v", byPath["/sub/pic.png"])
	}

	// Fake mp4: ffprobe fails, must be indexed with zero metadata.
	fake := byPath["/fake.mp4"]
	if fake.MimeType != "video/mp4" || fake.Width != 0 || fake.Duration != 0 {
		t.Fatalf("fake.mp4: %+v", fake)
	}

	// Hidden / meta dirs and non-media files excluded.
	if _, ok := byPath["/.hidden/secret.jpg"]; ok {
		t.Fatal("hidden dir was scanned")
	}
	if _, ok := byPath["/notes.txt"]; ok {
		t.Fatal("non-media file was scanned")
	}

	// EXIF-first ordering: exif.jpg (2020) should rank above fake.mp4 (now)? No:
	// fake.mp4 falls back to mtime (now) which is newer — check DESC order.
	if items[0].TakenTime < items[len(items)-1].TakenTime {
		t.Fatal("not ordered by taken_time DESC")
	}
}

func TestFullScanRealVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	mp4 := filepath.Join(root, "clip.mp4")
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "testsrc=duration=2:size=320x240:rate=10", "-y", mp4).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg generate: %v %s", err, out)
	}
	st, sc := openScanner(t, root)
	if err := sc.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	m, err := st.Get("/clip.mp4")
	if err != nil || m == nil {
		t.Fatalf("clip.mp4 not indexed: %v", err)
	}
	if m.Width != 320 || m.Height != 240 {
		t.Fatalf("video dims: %+v", m)
	}
	if m.Duration < 1500 || m.Duration > 3000 {
		t.Fatalf("video duration ms: %d", m.Duration)
	}
}

func TestIncrementalScan(t *testing.T) {
	root := buildTestRoot(t)
	st, sc := openScanner(t, root)
	if err := sc.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	// Touch one file: only it should be re-extracted; add a new one.
	makeJPEG(t, filepath.Join(root, "new.jpg"), 50, 40)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "plain.jpg"), past, past); err != nil {
		t.Fatal(err)
	}
	// Delete one: DeleteMissing should drop its row.
	if err := os.Remove(filepath.Join(root, "fake.mp4")); err != nil {
		t.Fatal(err)
	}

	if err := sc.Incremental(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.Get("/new.jpg"); m == nil || m.Width != 50 {
		t.Fatalf("new.jpg not indexed: %+v", m)
	}
	if m, _ := st.Get("/fake.mp4"); m != nil {
		t.Fatal("deleted file still indexed")
	}
	if m, _ := st.Get("/plain.jpg"); m == nil || m.ModifiedTime != past.Unix() {
		t.Fatalf("plain.jpg not re-extracted: %+v", m)
	}
	if _, total, _ := st.Page(0, 100, "all"); total != 4 {
		t.Fatalf("total=%d after incremental", total)
	}
}

func TestScanCancellation(t *testing.T) {
	root := buildTestRoot(t)
	_, sc := openScanner(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sc.Full(ctx, nil); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestScanConcurrentGuard(t *testing.T) {
	root := buildTestRoot(t)
	_, sc := openScanner(t, root)
	sc.scanning.Store(true)
	if err := sc.Full(context.Background(), nil); err == nil {
		t.Fatal("expected 'already in progress' error")
	}
}
