package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"pocket-nas/internal/files"
)

func setupHandler(t *testing.T) (http.Handler, *Handler, string) {
	t.Helper()
	root := t.TempDir()
	makeJPEGExif(t, filepath.Join(root, "exif.jpg"), 800, 600, "2020:05:01 12:30:00")
	makeJPEG(t, filepath.Join(root, "sub", "plain.jpg"), 400, 300)
	writeFile(t, filepath.Join(root, "fake.mp4"), []byte("not a video"))

	h, err := NewHandler(files.ResolveRoot(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if err := h.scanner.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Get("/api/gallery", h.Gallery)
	r.Get("/api/gallery/scan", h.GalleryScan)
	r.Get("/api/thumb/*", h.Thumb)
	r.Get("/api/media/file/*", h.MediaFile)
	return r, h, root
}

func do(t *testing.T, h http.Handler, method, url string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGalleryEndpoint(t *testing.T) {
	r, _, _ := setupHandler(t)

	rec := do(t, r, "GET", "/api/gallery", nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		Total int `json:"total"`
		Items []struct {
			Path      string `json:"path"`
			Name      string `json:"name"`
			MimeType  string `json:"mimeType"`
			TakenTime int64  `json:"takenTime"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Duration  int    `json:"duration"`
			ThumbURL  string `json:"thumbUrl"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 3 || len(resp.Items) != 3 {
		t.Fatalf("total=%d items=%d", resp.Total, len(resp.Items))
	}
	for _, it := range resp.Items {
		if !strings.HasPrefix(it.ThumbURL, "/api/thumb/") || !strings.Contains(it.ThumbURL, "?w=300&h=300") {
			t.Fatalf("bad thumbUrl %q", it.ThumbURL)
		}
	}
	// taken_time DESC: fake.mp4 and plain.jpg have ~now mtime, exif.jpg is 2020.
	if resp.Items[2].Path != "/exif.jpg" {
		t.Fatalf("oldest should be last, got %v", resp.Items[2].Path)
	}

	// Type filter.
	rec = do(t, r, "GET", "/api/gallery?type=video", nil)
	var vresp struct {
		Total int `json:"total"`
	}
	json.Unmarshal(rec.Body.Bytes(), &vresp)
	if vresp.Total != 1 {
		t.Fatalf("video total=%d", vresp.Total)
	}

	// Pagination.
	rec = do(t, r, "GET", "/api/gallery?offset=1&limit=1", nil)
	var presp struct {
		Total int `json:"total"`
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &presp)
	if presp.Total != 3 || len(presp.Items) != 1 {
		t.Fatalf("paging: %+v", presp)
	}
}

func TestGalleryScanEndpoint(t *testing.T) {
	r, _, _ := setupHandler(t)
	rec := do(t, r, "GET", "/api/gallery/scan", nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var resp struct {
		Scanning bool `json:"scanning"`
		Indexed  int  `json:"indexed"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Scanning || resp.Indexed != 3 {
		t.Fatalf("scan status: %+v", resp)
	}
}

func TestThumbEndpoint(t *testing.T) {
	r, _, _ := setupHandler(t)

	rec := do(t, r, "GET", "/api/thumb/sub/plain.jpg?w=200&h=200", nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type=%q", ct)
	}

	// Size cap: w=5000 must be clamped to 1024 (still succeeds).
	rec = do(t, r, "GET", "/api/thumb/exif.jpg?w=5000", nil)
	if rec.Code != 200 {
		t.Fatalf("clamped request status=%d", rec.Code)
	}

	// Unthumbable (fake video, no valid stream) → 302 placeholder.
	rec = do(t, r, "GET", "/api/thumb/fake.mp4", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/static/placeholder.svg" {
		t.Fatalf("Location=%q", loc)
	}

	// Path traversal → 403.
	rec = do(t, r, "GET", "/api/thumb/../../etc/passwd", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("traversal status=%d", rec.Code)
	}

	// Missing file → 404.
	rec = do(t, r, "GET", "/api/thumb/nope.jpg", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d", rec.Code)
	}
}

func TestMediaFileEndpoint(t *testing.T) {
	r, _, root := setupHandler(t)

	rec := do(t, r, "GET", "/api/media/file/sub/plain.jpg", nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type=%q", ct)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("no Accept-Ranges header")
	}

	// Range request → 206 with partial body.
	rec = do(t, r, "GET", "/api/media/file/sub/plain.jpg", map[string]string{"Range": "bytes=0-9"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status=%d", rec.Code)
	}
	if rec.Body.Len() != 10 {
		t.Fatalf("range body len=%d", rec.Body.Len())
	}
	full, _ := os.ReadFile(filepath.Join(root, "sub", "plain.jpg"))
	if rec.Body.String() != string(full[:10]) {
		t.Fatal("range body mismatch")
	}

	// Traversal → 403.
	rec = do(t, r, "GET", "/api/media/file/../../../etc/passwd", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("traversal status=%d", rec.Code)
	}
}
