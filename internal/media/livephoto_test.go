package media

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"pocket-nas/internal/files"
)

// makeMP4File generates a real 1s MP4 via ffmpeg.
func makeMP4File(t *testing.T, path string) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x240:rate=10", "-c:v", "libx264", "-y", path).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// makeMotionPhoto writes a pixel_legacy motion photo: JPEG + XMP APP1 +
// appended MP4. Returns the embedded MP4 bytes.
func makeMotionPhoto(t *testing.T, path string) []byte {
	t.Helper()
	mp4tmp := filepath.Join(t.TempDir(), "v.mp4")
	mp4 := makeMP4File(t, mp4tmp)

	// Build a JPEG then insert the XMP APP1 segment after SOI.
	jpgTmp := filepath.Join(t.TempDir(), "p.jpg")
	makeJPEG(t, jpgTmp, 64, 48)
	jpg, err := os.ReadFile(jpgTmp)
	if err != nil {
		t.Fatal(err)
	}
	xmp := `http://ns.adobe.com/xap/1.0/` + "\x00" +
		`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF><rdf:Description xmlns:GCamera="http://ns.google.com/photos/1.0/camera/" GCamera:MicroVideo="1" GCamera:MicroVideoOffset="` +
		itoa(len(mp4)) + `"/></rdf:RDF></x:xmpmeta>`
	app1 := []byte{0xFF, 0xE1, byte((len(xmp) + 2) >> 8), byte(len(xmp) + 2)}
	out := append([]byte{}, jpg[:2]...)
	out = append(out, app1...)
	out = append(out, []byte(xmp)...)
	out = append(out, jpg[2:]...)
	out = append(out, mp4...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return mp4
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestScanDetectsEmbeddedMotionPhoto(t *testing.T) {
	root := t.TempDir()
	mp4 := makeMotionPhoto(t, filepath.Join(root, "motion.jpg"))
	st, sc := openScanner(t, root)
	if err := sc.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	m, err := st.Get("/motion.jpg")
	if err != nil || m == nil {
		t.Fatal("not indexed")
	}
	if !m.IsLivePhoto || m.LiveType != "pixel_legacy" {
		t.Fatalf("live fields: %+v", m)
	}
	st2, _ := os.Stat(filepath.Join(root, "motion.jpg"))
	if m.VideoOffset != st2.Size()-int64(len(mp4)) || m.VideoLength != int64(len(mp4)) {
		t.Fatalf("offset=%d len=%d", m.VideoOffset, m.VideoLength)
	}
}

func TestScanPairsIOSLivePhoto(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "IMG_0001.heic"), []byte("fake heic"))
	movBytes := makeMP4File(t, filepath.Join(root, "IMG_0001.mov"))
	_ = movBytes

	st, sc := openScanner(t, root)
	if err := sc.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	m, _ := st.Get("/IMG_0001.heic")
	if m == nil || !m.IsLivePhoto || m.LiveType != "ios" || m.CompanionPath != "/IMG_0001.mov" {
		t.Fatalf("pairing: %+v", m)
	}
	if m.VideoLength != int64(len(movBytes)) {
		t.Fatalf("companion size=%d want %d", m.VideoLength, len(movBytes))
	}

	// Delete the companion: incremental scan must clear the pairing.
	if err := os.Remove(filepath.Join(root, "IMG_0001.mov")); err != nil {
		t.Fatal(err)
	}
	if err := sc.Incremental(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = st.Get("/IMG_0001.heic")
	if m == nil || m.IsLivePhoto {
		t.Fatalf("pairing not cleared: %+v", m)
	}
}

func TestGalleryReportsLiveFields(t *testing.T) {
	root := t.TempDir()
	makeMotionPhoto(t, filepath.Join(root, "motion.jpg"))
	makeJPEG(t, filepath.Join(root, "plain.jpg"), 64, 48)

	h, err := NewHandler(files.ResolveRoot(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	if err := h.scanner.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/gallery", nil)
	rec := httptest.NewRecorder()
	h.Gallery(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var resp struct {
		Items []struct {
			Path        string `json:"path"`
			IsLivePhoto bool   `json:"isLivePhoto"`
			LiveType    string `json:"liveType"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]struct {
		Live bool
		Typ  string
	}{}
	for _, it := range resp.Items {
		byPath[it.Path] = struct {
			Live bool
			Typ  string
		}{it.IsLivePhoto, it.LiveType}
	}
	if !byPath["/motion.jpg"].Live || byPath["/motion.jpg"].Typ != "pixel_legacy" {
		t.Fatalf("motion.jpg: %+v", byPath["/motion.jpg"])
	}
	if byPath["/plain.jpg"].Live {
		t.Fatalf("plain.jpg wrongly flagged: %+v", byPath["/plain.jpg"])
	}
}
