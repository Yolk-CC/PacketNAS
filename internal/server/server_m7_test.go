package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pocket-nas/internal/config"
	"pocket-nas/internal/files"
)

func newTestRouter(t *testing.T, root string) http.Handler {
	t.Helper()
	cfg := config.Config{Root: root, Addr: "127.0.0.1", Port: 0}
	return NewRouter(cfg, files.New(root))
}

func TestBrowseEndpoint(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"alpha", "beta", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestRouter(t, t.TempDir())

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/system/browse?path="+base, nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Path string `json:"path"`
		Dirs []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"dirs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Dirs) != 2 || resp.Dirs[0].Name != "alpha" || resp.Dirs[1].Name != "beta" {
		t.Fatalf("dirs: %+v", resp.Dirs)
	}
	if resp.Dirs[0].Path != filepath.Join(base, "alpha") {
		t.Fatalf("path: %q", resp.Dirs[0].Path)
	}

	// Non-directory → 400.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/system/browse?path="+filepath.Join(base, "file.txt"), nil))
	if rec.Code != 400 {
		t.Fatalf("file browse status %d", rec.Code)
	}

	// Omitted path → system root, must succeed.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/system/browse", nil))
	if rec.Code != 200 {
		t.Fatalf("root browse status %d", rec.Code)
	}
}

func TestSharesEndpoints(t *testing.T) {
	root := t.TempDir()
	shareA := t.TempDir()
	shareB := t.TempDir()
	if err := os.WriteFile(filepath.Join(shareA, "pic.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestRouter(t, root)

	// Initially legacy mode.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/settings/shares", nil))
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	var get1 struct {
		Shares []any `json:"shares"`
		Legacy bool  `json:"legacy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &get1); err != nil {
		t.Fatal(err)
	}
	if !get1.Legacy || len(get1.Shares) != 0 {
		t.Fatalf("initial: %+v", get1)
	}

	// PUT two shares.
	body := `{"shares":[{"name":"photos","path":"` + shareA + `"},{"name":"docs","path":"` + shareB + `"}]}`
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings/shares", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	var put1 struct {
		Shares []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"shares"`
		Legacy bool `json:"legacy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &put1); err != nil {
		t.Fatal(err)
	}
	if put1.Legacy || len(put1.Shares) != 2 || put1.Shares[0].Name != "photos" {
		t.Fatalf("after PUT: %+v", put1)
	}

	// Files list at "/" now shows only share pseudo-dirs.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/files?path=/", nil))
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	var list []struct {
		Name  string `json:"name"`
		IsDir bool   `json:"isDir"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "docs" || list[1].Name != "photos" || !list[0].IsDir {
		t.Fatalf("shared root list: %+v", list)
	}

	// A path outside shares → 404.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/files?path=/etc", nil))
	if rec.Code != 404 {
		t.Fatalf("outside share status %d: %s", rec.Code, rec.Body)
	}

	// Invalid share (bad name) → 400 invalid_share envelope.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings/shares",
		strings.NewReader(`{"shares":[{"name":"a/b","path":"`+shareA+`"}]}`)))
	if rec.Code != 400 {
		t.Fatalf("bad share status %d", rec.Code)
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil || errBody.Error.Code != "invalid_share" {
		t.Fatalf("error body: %s", rec.Body)
	}

	// PUT empty array → back to legacy mode; root listing restored.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings/shares", strings.NewReader(`{"shares":[]}`)))
	if rec.Code != 200 {
		t.Fatalf("clear PUT: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/settings/shares", nil))
	var get2 struct {
		Legacy bool `json:"legacy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &get2); err != nil || !get2.Legacy {
		t.Fatalf("after clear: %s", rec.Body)
	}
}
