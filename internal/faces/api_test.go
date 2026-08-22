package faces

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pocket-nas/internal/media"
)

// fakeSource implements Source over plain files in a dir.
type fakeSource struct {
	root   string
	images []media.Media
}

func (f *fakeSource) Images() ([]media.Media, error) { return f.images, nil }
func (f *fakeSource) MediaByPath(p string) (*media.Media, error) {
	for _, m := range f.images {
		if m.Path == p {
			cp := m
			return &cp, nil
		}
	}
	return nil, nil
}
func (f *fakeSource) Resolve(rel string) (string, error) {
	return filepath.Join(f.root, filepath.FromSlash(rel)), nil
}

// fakeEngine emits one face per image and hands out embeddings in sequence.
type fakeEngine struct {
	seq    int
	embeds [][]float32
	dims   int
}

func (e *fakeEngine) Detect(img image.Image) ([]Face, error) {
	return []Face{{Box: [4]float32{10, 10, 60, 60}, Score: 0.99}}, nil
}
func (e *fakeEngine) Embed(img image.Image, f Face) ([]float32, error) {
	v := e.embeds[e.seq%len(e.embeds)]
	e.seq++
	out := make([]float32, len(v))
	copy(out, v)
	return out, nil
}
func (e *fakeEngine) Dims() int           { return e.dims }
func (e *fakeEngine) ProfileName() string { return "fake" }

// writePNG creates a solid-color PNG.
func writePNG(t *testing.T, path string, c color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// newTestService builds a Service over 3 images with a fake engine whose
// embeddings make images 0+1 the same person and 2 another.
func newTestService(t *testing.T) (*Service, *fakeSource) {
	t.Helper()
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "a.png"), color.NRGBA{200, 10, 10, 255})
	writePNG(t, filepath.Join(root, "b.png"), color.NRGBA{210, 20, 20, 255})
	writePNG(t, filepath.Join(root, "c.png"), color.NRGBA{10, 10, 200, 255})
	src := &fakeSource{root: root}
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		st, _ := os.Stat(filepath.Join(root, name))
		src.images = append(src.images, media.Media{
			Path: "/" + name, Name: name, MimeType: "image/png",
			ModifiedTime: st.ModTime().Unix(),
		})
	}
	cfg := Config{}
	svc, err := NewService(root, src, func() Config { return cfg }, func(Config) error { return nil }, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	// v1 ≈ v1' (same person), v2 far away.
	emb := [][]float32{
		append([]float32{1, 0, 0}, make([]float32, 509)...),
		append([]float32{0.999, 0.01, 0}, make([]float32, 509)...),
		append([]float32{0, 0, 1}, make([]float32, 509)...),
	}
	svc.SetEngine(&fakeEngine{embeds: emb, dims: 512}, "")
	return svc, src
}

func TestServiceScanClusters(t *testing.T) {
	svc, _ := newTestService(t)
	svc.scanOnce()
	persons, err := svc.Store().Persons()
	if err != nil {
		t.Fatal(err)
	}
	if len(persons) != 2 {
		t.Fatalf("want 2 persons, got %d: %+v", len(persons), persons)
	}
	counts := map[int]bool{}
	for _, p := range persons {
		counts[p.FaceCount] = true
	}
	if !counts[1] || !counts[2] {
		t.Fatalf("face counts: %+v", persons)
	}
	// Second scan is a no-op (processed bookkeeping).
	svc.scanOnce()
	n, _ := svc.Store().FaceCount()
	if n != 3 {
		t.Fatalf("rescan duplicated faces: %d", n)
	}
}

func TestAPIDegraded(t *testing.T) {
	root := t.TempDir()
	src := &fakeSource{root: root}
	cfg := Config{}
	svc, err := NewService(root, src, func() Config { return cfg }, func(Config) error { return nil }, "")
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	h := NewHandler(svc)

	rec := httptest.NewRecorder()
	h.Status(rec, httptest.NewRequest("GET", "/api/faces/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	var st map[string]any
	json.NewDecoder(rec.Body).Decode(&st)
	if st["available"] != false || st["reason"] == nil {
		t.Fatalf("degraded status: %v", st)
	}

	for _, ep := range []struct{ m, p string }{
		{"POST", "/api/faces/scan"},
		{"GET", "/api/faces/persons"},
		{"GET", "/api/faces/export"},
		{"POST", "/api/faces/import"},
	} {
		rec := httptest.NewRecorder()
		switch ep.p {
		case "/api/faces/scan":
			h.Scan(rec, httptest.NewRequest(ep.m, ep.p, nil))
		case "/api/faces/persons":
			h.Persons(rec, httptest.NewRequest(ep.m, ep.p, nil))
		case "/api/faces/export":
			h.Export(rec, httptest.NewRequest(ep.m, ep.p, nil))
		case "/api/faces/import":
			h.Import(rec, httptest.NewRequest(ep.m, ep.p, bytes.NewReader([]byte("{}"))))
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: got %d, want 503", ep.p, rec.Code)
		}
		var body map[string]map[string]string
		json.NewDecoder(rec.Body).Decode(&body)
		if body["error"]["code"] != "faces_unavailable" {
			t.Fatalf("%s: body %v", ep.p, body)
		}
	}
}

func TestAPIEndToEnd(t *testing.T) {
	svc, _ := newTestService(t)
	h := NewHandler(svc)

	svc.scanOnce() // run inline for determinism

	// Scan via API (queue empty now, but endpoint must accept).
	rec := httptest.NewRecorder()
	h.Scan(rec, httptest.NewRequest("POST", "/api/faces/scan", nil))
	if rec.Code != 200 {
		t.Fatalf("scan: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.Persons(rec, httptest.NewRequest("GET", "/api/faces/persons", nil))
	var persons []personJSON
	json.NewDecoder(rec.Body).Decode(&persons)
	if len(persons) != 2 {
		t.Fatalf("persons: %+v", persons)
	}

	// Photos for person 0.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/faces/persons/1/photos", nil)
	req.SetPathValue("id", "1")
	h.PersonPhotos(rec, req)
	var photos struct {
		Total int `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&photos)
	if photos.Total != 2 {
		t.Fatalf("person 1 photos: %d (%s)", photos.Total, rec.Body.String())
	}

	// Rename.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/faces/persons/1",
		bytes.NewReader([]byte(`{"name":"Alice"}`)))
	req.SetPathValue("id", "1")
	h.RenamePerson(rec, req)
	if rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}

	// Export.
	rec = httptest.NewRecorder()
	h.Export(rec, httptest.NewRequest("GET", "/api/faces/export", nil))
	if rec.Code != 200 {
		t.Fatalf("export: %d", rec.Code)
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var data ExportData
	if err := json.NewDecoder(gz).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.Persons) != 2 || len(data.Faces) != 3 {
		t.Fatalf("export: %d persons %d faces", len(data.Persons), len(data.Faces))
	}

	// Import into a fresh service over the SAME files: relations preserved
	// by content hash, no re-identification.
	root2svc, _ := newTestService(t)
	root2svc.Store().Reset()
	res, err := root2svc.Store().Import(&data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Faces != 3 {
		t.Fatalf("import: %+v", res)
	}
	root2svc.HashFiles(context.Background()) // link existing media
	h2 := NewHandler(root2svc)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/faces/persons/999/photos", nil)
	// find Alice's person id in the new store
	list, _ := root2svc.Store().Persons()
	var aliceID int64
	for _, p := range list {
		if p.Name == "Alice" {
			aliceID = p.ID
		}
	}
	if aliceID == 0 {
		t.Fatalf("Alice not imported: %+v", list)
	}
	req.SetPathValue("id", itoa(aliceID))
	h2.PersonPhotos(rec, req)
	var photos2 struct {
		Total int `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&photos2)
	if photos2.Total != 2 {
		t.Fatalf("imported photos: %d (%s)", photos2.Total, rec.Body.String())
	}

	// Merge the two persons in svc.
	rec = httptest.NewRecorder()
	h.MergePersons(rec, httptest.NewRequest("POST", "/api/faces/persons/merge",
		bytes.NewReader([]byte(`{"from":2,"to":1}`))))
	if rec.Code != 200 {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.Persons(rec, httptest.NewRequest("GET", "/api/faces/persons", nil))
	persons = nil
	json.NewDecoder(rec.Body).Decode(&persons)
	if len(persons) != 1 || persons[0].FaceCount != 3 {
		t.Fatalf("after merge: %+v", persons)
	}

	// Crop endpoint serves a jpeg for a known face.
	facesAll, _ := svc.Store().AllFaces()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/faces/crop/x", nil)
	req.SetPathValue("faceId", itoa(facesAll[0].ID))
	h.Crop(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("crop: %d %v", rec.Code, rec.Header())
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func TestSetModelsKeepsLibPathWhenEmpty(t *testing.T) {
	root := t.TempDir()
	src := &fakeSource{root: root}
	cfg := Config{LibPath: "/custom/libonnxruntime.so"}
	var saved Config
	svc, err := NewService(root, src,
		func() Config { return cfg },
		func(c Config) error { saved = c; return nil }, "")
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	h := NewHandler(svc)

	// A PUT without libPath must not wipe the configured library path.
	rec := httptest.NewRecorder()
	h.SetModels(rec, httptest.NewRequest("PUT", "/api/faces/models", bytes.NewReader([]byte(`{"profile":"buffalo_s"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("SetModels: %d body=%s", rec.Code, rec.Body.String())
	}
	if saved.LibPath != "/custom/libonnxruntime.so" {
		t.Fatalf("LibPath overwritten: %q", saved.LibPath)
	}

	// An explicit libPath is applied.
	rec = httptest.NewRecorder()
	h.SetModels(rec, httptest.NewRequest("PUT", "/api/faces/models", bytes.NewReader([]byte(`{"libPath":"/other/lib.so"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("SetModels: %d body=%s", rec.Code, rec.Body.String())
	}
	if saved.LibPath != "/other/lib.so" {
		t.Fatalf("LibPath not applied: %q", saved.LibPath)
	}
}

func TestUnavailableReasonRedactsPaths(t *testing.T) {
	root := t.TempDir()
	src := &fakeSource{root: root}
	cfg := Config{}
	svc, err := NewService(root, src, func() Config { return cfg }, func(Config) error { return nil }, "")
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// Simulate an engine failure whose reason embeds absolute paths.
	svc.SetEngine(nil, "model file det.onnx not found in "+filepath.Join(root, ".pocketnas", "models"))

	if r := svc.PublicReason(); strings.Contains(r, root) {
		t.Fatalf("PublicReason leaks path: %q", r)
	}

	h := NewHandler(svc)
	rec := httptest.NewRecorder()
	h.Scan(rec, httptest.NewRequest("POST", "/api/faces/scan", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("scan: got %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), root) {
		t.Fatalf("503 body leaks absolute path: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.Status(rec, httptest.NewRequest("GET", "/api/faces/status", nil))
	if strings.Contains(rec.Body.String(), root) {
		t.Fatalf("status body leaks absolute path: %s", rec.Body.String())
	}
}
