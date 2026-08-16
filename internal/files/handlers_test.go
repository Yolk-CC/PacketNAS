package files_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pocket-nas/internal/config"
	"pocket-nas/internal/files"
	"pocket-nas/internal/server"
)

// setup builds a router over a temp root with a couple of seed files.
func setup(t *testing.T, password string) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.jpg"), []byte("jpegdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := files.New(root)
	return server.NewRouter(config.Config{Root: root, Password: password}, svc), root
}

func do(t *testing.T, h http.Handler, method, url, token string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, body)
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func jsonBody(t *testing.T, v any) (io.Reader, string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b), "application/json"
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body not JSON: %v (%q)", err, rec.Body.String())
	}
	return body.Error.Code
}

// --- Path traversal (SPEC DoD #1) ---

func TestPathTraversalForbidden(t *testing.T) {
	h, _ := setup(t, "")

	// chi cleans /api/download/../../etc/passwd at the router level, so the
	// meaningful traversal vectors arrive via query paths and raw dot-dots.
	rec := do(t, h, http.MethodGet, "/api/files?path=../", "", nil, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("list ../ = %d (%s), want 403", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "FORBIDDEN" {
		t.Fatalf("error code = %q", code)
	}

	rec = do(t, h, http.MethodGet, "/api/files?path=/../../etc", "", nil, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("list /../../etc = %d, want 403", rec.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/download/%2e%2e/%2e%2e/etc/passwd", "", nil, "")
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("download traversal = %d, want 403 or 404", rec.Code)
	}
}

// --- Full API chain (SPEC DoD #2) ---

func TestFullAPIChain(t *testing.T) {
	h, root := setup(t, "")

	// List root.
	rec := do(t, h, http.MethodGet, "/api/files", "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	var entries []files.FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].IsDir || entries[0].Name != "sub" || entries[1].Name != "hello.txt" {
		t.Fatalf("unexpected list: %+v", entries)
	}

	// Upload (streaming multipart) into /sub.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "up1.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("uploaded-bytes")); err != nil {
		t.Fatal(err)
	}
	fw2, err := mw.CreateFormFile("file", "up2.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw2.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	rec = do(t, h, http.MethodPost, "/api/upload?path=/sub", "", &buf, mw.FormDataContentType())
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d (%s)", rec.Code, rec.Body.String())
	}
	var up struct {
		Uploaded []string `json:"uploaded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if len(up.Uploaded) != 2 {
		t.Fatalf("uploaded = %v", up.Uploaded)
	}
	data, err := os.ReadFile(filepath.Join(root, "sub", "up1.txt"))
	if err != nil || string(data) != "uploaded-bytes" {
		t.Fatalf("uploaded file mismatch: %v %q", err, data)
	}

	// Upload into nonexistent dir -> 400/404.
	rec = do(t, h, http.MethodPost, "/api/upload?path=/nope", "", &buf, mw.FormDataContentType())
	if rec.Code == http.StatusOK {
		t.Fatal("upload to missing dir should fail")
	}

	// Download file.
	rec = do(t, h, http.MethodGet, "/api/download/hello.txt", "", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "hello world" {
		t.Fatalf("download = %d %q", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q", cd)
	}

	// Range request -> 206.
	req := httptest.NewRequest(http.MethodGet, "/api/download/hello.txt", nil)
	req.Header.Set("Range", "bytes=0-4")
	rangeRec := httptest.NewRecorder()
	h.ServeHTTP(rangeRec, req)
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("range = %d, want 206", rangeRec.Code)
	}
	if rangeRec.Body.String() != "hello" {
		t.Fatalf("range body = %q", rangeRec.Body.String())
	}
	if cr := rangeRec.Header().Get("Content-Range"); cr != "bytes 0-4/11" {
		t.Fatalf("Content-Range = %q", cr)
	}

	// Rename.
	body, ct := jsonBody(t, map[string]string{"path": "/hello.txt", "newName": "hi.txt"})
	rec = do(t, h, http.MethodPost, "/api/files/rename", "", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d (%s)", rec.Code, rec.Body.String())
	}
	// Rename onto existing -> 409.
	if err := os.WriteFile(filepath.Join(root, "exists.txt"), []byte("e"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, ct = jsonBody(t, map[string]string{"path": "/hi.txt", "newName": "exists.txt"})
	rec = do(t, h, http.MethodPost, "/api/files/rename", "", body, ct)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rename conflict = %d, want 409", rec.Code)
	}

	// Mkdir.
	body, ct = jsonBody(t, map[string]string{"dir": "/", "name": "photos"})
	rec = do(t, h, http.MethodPost, "/api/files/mkdir", "", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("mkdir = %d (%s)", rec.Code, rec.Body.String())
	}
	body, ct = jsonBody(t, map[string]string{"dir": "/", "name": "photos"})
	rec = do(t, h, http.MethodPost, "/api/files/mkdir", "", body, ct)
	if rec.Code != http.StatusConflict {
		t.Fatalf("mkdir existing = %d, want 409", rec.Code)
	}

	// Move.
	body, ct = jsonBody(t, map[string]any{"srcPaths": []string{"/hi.txt"}, "destDir": "/photos"})
	rec = do(t, h, http.MethodPost, "/api/files/move", "", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("move = %d (%s)", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "photos", "hi.txt")); err != nil {
		t.Fatal("move target missing")
	}

	// Delete (recursive).
	body, ct = jsonBody(t, map[string]any{"paths": []string{"/photos"}})
	rec = do(t, h, http.MethodDelete, "/api/files", "", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "photos")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("photos should be deleted")
	}

	// System info.
	rec = do(t, h, http.MethodGet, "/api/system/info", "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("system info = %d", rec.Code)
	}
	var info struct {
		Version   string `json:"version"`
		Root      string `json:"root"`
		GoVersion string `json:"goVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.1.0" || info.GoVersion == "" || info.Root == "" {
		t.Fatalf("bad system info: %+v", info)
	}
}

// --- Auth (SPEC DoD #3) ---

func login(t *testing.T, h http.Handler, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := jsonBody(t, map[string]string{"password": password})
	return do(t, h, http.MethodPost, "/api/auth/login", "", body, ct)
}

func TestAuth(t *testing.T) {
	h, _ := setup(t, "s3cret")

	// No token -> 401 UNAUTHORIZED.
	rec := do(t, h, http.MethodGet, "/api/files", "", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", rec.Code)
	}
	if code := decodeError(t, rec); code != "UNAUTHORIZED" {
		t.Fatalf("code = %q", code)
	}

	// Wrong password -> 403.
	rec = login(t, h, "wrong")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong password = %d, want 403", rec.Code)
	}

	// Correct password -> token, then token grants access.
	rec = login(t, h, "s3cret")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if len(tok.Token) != 64 {
		t.Fatalf("token = %q, want 64 hex chars", tok.Token)
	}
	rec = do(t, h, http.MethodGet, "/api/files", tok.Token, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("with token = %d, want 200", rec.Code)
	}

	// Bad token -> 401.
	rec = do(t, h, http.MethodGet, "/api/files", strings.Repeat("0", 64), nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token = %d, want 401", rec.Code)
	}
}

func TestAuthDisabled(t *testing.T) {
	h, _ := setup(t, "")
	// Empty password: login returns empty token, middleware passes through.
	rec := login(t, h, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil || tok.Token != "" {
		t.Fatalf("token = %q err=%v", tok.Token, err)
	}
	rec = do(t, h, http.MethodGet, "/api/files", "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("open access = %d, want 200", rec.Code)
	}
}

// --- ZIP download (SPEC DoD #4) ---

func TestZipDownload(t *testing.T) {
	h, root := setup(t, "")
	// Add a nested file to make the archive non-trivial.
	if err := os.WriteFile(filepath.Join(root, "sub", "note.md"), []byte("# note"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, http.MethodGet, "/api/download/sub", "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("zip = %d (%s)", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "sub.zip") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Fatal("streaming zip must not set Content-Length")
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip unreadable: %v", err)
	}
	contents := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[f.Name] = string(b)
	}
	if contents["sub/a.jpg"] != "jpegdata" || contents["sub/note.md"] != "# note" {
		t.Fatalf("zip contents = %v", contents)
	}

	// ?archive=zip on a directory also works.
	rec = do(t, h, http.MethodGet, "/api/download/sub?archive=zip", "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("archive=zip = %d", rec.Code)
	}
}
