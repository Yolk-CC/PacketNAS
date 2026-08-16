package livephoto

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"pocket-nas/internal/files"
)

// setupExtract builds a root with a pixel_legacy motion photo, an iOS pair
// and a plain photo, and returns a chi router + lookup backed by a map.
func setupExtract(t *testing.T) (http.Handler, string, []byte) {
	t.Helper()
	root := t.TempDir()

	mp4 := makeMP4(t)
	jpg := makeJPEGBytes(t)
	xmp := xmpPacket(`GCamera:MicroVideo="1" GCamera:MicroVideoOffset="` + itoa(len(mp4)) + `"`)
	photo := withXMP(t, jpg, xmp)
	if err := os.WriteFile(filepath.Join(root, "motion.jpg"), append(photo, mp4...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "IMG_1.heic"), []byte("fake heic"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "IMG_1.mov"), mp4, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain.jpg"), jpg, 0o644); err != nil {
		t.Fatal(err)
	}

	metas := map[string]*Meta{
		"/motion.jpg": {Live: true, Type: "pixel_legacy", Offset: int64(len(photo)), Length: int64(len(mp4))},
		"/IMG_1.heic": {Live: true, Type: "ios", Companion: "/IMG_1.mov", Length: int64(len(mp4))},
	}
	lookup := func(rel string) (*Meta, error) { return metas[rel], nil }

	h, err := NewHandler(files.ResolveRoot(root), lookup)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Get("/api/livephoto/*", h.ServeHTTP)
	return r, root, mp4
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

func TestExtractEmbeddedBytesExact(t *testing.T) {
	r, _, mp4 := setupExtract(t)
	rec := get(t, r, "/api/livephoto/motion.jpg", nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("content-type=%q", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), mp4) {
		t.Fatalf("extracted %d bytes, want %d (embedded mp4)", rec.Body.Len(), len(mp4))
	}

	// Second request must hit the cache and still support Range.
	rec = get(t, r, "/api/livephoto/motion.jpg", map[string]string{"Range": "bytes=0-99"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status=%d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), mp4[:100]) {
		t.Fatal("range body mismatch")
	}
}

func TestExtractIOSCompanion(t *testing.T) {
	r, _, mp4 := setupExtract(t)
	rec := get(t, r, "/api/livephoto/IMG_1.heic", nil)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), mp4) {
		t.Fatal("ios companion bytes mismatch")
	}
	// Range on the companion file too.
	rec = get(t, r, "/api/livephoto/IMG_1.heic", map[string]string{"Range": "bytes=10-19"})
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 10 {
		t.Fatalf("ios range: %d len=%d", rec.Code, rec.Body.Len())
	}
}

func TestExtractNonLiveIs404(t *testing.T) {
	r, _, _ := setupExtract(t)
	rec := get(t, r, "/api/livephoto/plain.jpg", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	var body errorBody
	if err := jsonUnmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "NOT_FOUND" {
		t.Fatalf("error body: %+v", body)
	}
}

func TestExtractTraversal(t *testing.T) {
	r, _, _ := setupExtract(t)
	rec := get(t, r, "/api/livephoto/../../../etc/passwd", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

func jsonUnmarshal(b []byte, v any) error {
	return json.NewDecoder(bytes.NewReader(b)).Decode(v)
}
