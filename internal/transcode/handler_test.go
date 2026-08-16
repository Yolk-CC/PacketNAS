package transcode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"pocket-nas/internal/files"
)

func setupHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	genVideo(t, filepath.Join(root, "clip.mp4"), "320x240", 1, true)
	writeFile(t, filepath.Join(root, "broken.mp4"), []byte("not a video"))
	writeFile(t, filepath.Join(root, "doc.txt"), []byte("hello"))

	h, err := NewHandler(files.ResolveRoot(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if !h.mgr.Available() {
		t.Skip("ffmpeg not available")
	}
	r := chi.NewRouter()
	r.Get("/api/video/status/*", h.Status)
	r.Get("/api/video/*", h.Video)
	return r, root
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, h http.Handler, url string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestVideoEndpointFlow(t *testing.T) {
	r, root := setupHandler(t)

	// 1. First transcode-tier request → 202 queued/running.
	rec := get(t, r, "/api/video/clip.mp4?res=360p", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	var st struct {
		Status   string `json:"status"`
		Progress int    `json:"progress"`
	}
	json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Status != StatusQueued && st.Status != StatusRunning {
		t.Fatalf("status=%q", st.Status)
	}

	// 2. Poll status until done.
	deadline := time.Now().Add(60 * time.Second)
	for {
		rec := get(t, r, "/api/video/status/clip.mp4?res=360p", nil)
		json.Unmarshal(rec.Body.Bytes(), &st)
		if st.Status == StatusDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never done, last=%+v", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st.Progress != 100 {
		t.Fatalf("done progress=%d", st.Progress)
	}

	// 3. Now GET returns the transcoded file, Range-capable.
	rec = get(t, r, "/api/video/clip.mp4?res=360p", nil)
	if rec.Code != 200 {
		t.Fatalf("done get: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("ct=%q", ct)
	}
	outPath := filepath.Join(root, metaDirName, CacheDirName)
	entries, _ := os.ReadDir(outPath)
	if len(entries) != 1 {
		t.Fatalf("cache entries=%d", len(entries))
	}
	cached, _ := os.ReadFile(filepath.Join(outPath, entries[0].Name()))
	if rec.Body.Len() != len(cached) {
		t.Fatal("body != cached output")
	}
	rec = get(t, r, "/api/video/clip.mp4?res=360p", map[string]string{"Range": "bytes=0-99"})
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 100 {
		t.Fatalf("range: %d len=%d", rec.Code, rec.Body.Len())
	}

	// 4. Original is byte-identical to the source.
	rec = get(t, r, "/api/video/clip.mp4?res=original", nil)
	src, _ := os.ReadFile(filepath.Join(root, "clip.mp4"))
	if rec.Code != 200 || rec.Body.Len() != len(src) {
		t.Fatalf("original: %d len=%d want %d", rec.Code, rec.Body.Len(), len(src))
	}
	for i := range src {
		if rec.Body.Bytes()[i] != src[i] {
			t.Fatal("original bytes differ")
		}
	}
	// default res behaves as original
	rec = get(t, r, "/api/video/clip.mp4", nil)
	if rec.Code != 200 || rec.Body.Len() != len(src) {
		t.Fatal("default res should be original")
	}

	// 5. Non-video → 400; invalid res → 400; broken video → 409 eventually.
	rec = get(t, r, "/api/video/doc.txt?res=720p", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-video: %d", rec.Code)
	}
	rec = get(t, r, "/api/video/clip.mp4?res=4k", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad res: %d", rec.Code)
	}
	// Broken video: accepted first, then the job fails → 409.
	get(t, r, "/api/video/broken.mp4?res=360p", nil)
	failDeadline := time.Now().Add(30 * time.Second)
	for {
		rec = get(t, r, "/api/video/broken.mp4?res=360p", nil)
		if rec.Code == http.StatusConflict {
			break
		}
		if time.Now().After(failDeadline) {
			t.Fatalf("broken video never failed, last=%d %s", rec.Code, rec.Body)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 6. Traversal → 403.
	rec = get(t, r, "/api/video/../../../etc/passwd?res=360p", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("traversal: %d", rec.Code)
	}
}

// TestConcurrentDedup fires concurrent first requests; only one output may
// ever be produced.
func TestConcurrentDedup(t *testing.T) {
	r, root := setupHandler(t)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = get(t, r, "/api/video/clip.mp4?res=720p", nil).Code
		}(i)
	}
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusAccepted && c != 200 {
			t.Fatalf("unexpected code %d", c)
		}
	}
	// Wait for completion, then count outputs.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		rec := get(t, r, "/api/video/status/clip.mp4?res=720p", nil)
		var st struct {
			Status string `json:"status"`
		}
		json.Unmarshal(rec.Body.Bytes(), &st)
		if st.Status == StatusDone {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	entries, _ := os.ReadDir(filepath.Join(root, metaDirName, CacheDirName))
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".mp4" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d outputs for concurrent identical requests, want 1", n)
	}
}
