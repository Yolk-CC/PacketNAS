package livephoto

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// makeJPEGBytes encodes a tiny real JPEG.
func makeJPEGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withXMP inserts an XMP APP1 segment right after the JPEG SOI marker.
func withXMP(t *testing.T, jpg []byte, xmp string) []byte {
	t.Helper()
	if jpg[0] != 0xFF || jpg[1] != 0xD8 {
		t.Fatal("not a JPEG")
	}
	payload := append([]byte("http://ns.adobe.com/xap/1.0/\x00"), []byte(xmp)...)
	app1 := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	out := append([]byte{}, jpg[:2]...)
	out = append(out, app1...)
	out = append(out, payload...)
	out = append(out, jpg[2:]...)
	return out
}

// makeMP4 generates a real 1s MP4 with ffmpeg.
func makeMP4(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	p := filepath.Join(t.TempDir(), "v.mp4")
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x240:rate=10", "-c:v", "libx264", "-y", p).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeSample(t *testing.T, parts ...[]byte) (string, []byte) {
	t.Helper()
	var whole []byte
	for _, p := range parts {
		whole = append(whole, p...)
	}
	path := filepath.Join(t.TempDir(), "sample.jpg")
	if err := os.WriteFile(path, whole, 0o644); err != nil {
		t.Fatal(err)
	}
	head := whole
	if len(head) > HeadSize() {
		head = head[:HeadSize()]
	}
	return path, head
}

func xmpPacket(inner string) string {
	return `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:GCamera="http://ns.google.com/photos/1.0/camera/" xmlns:samsung="http://ns.samsung.com/photos/1.0/camera/" ` +
		inner + `/></rdf:RDF></x:xmpmeta>`
}

func TestParsePixelLegacy(t *testing.T) {
	mp4 := makeMP4(t)
	jpg := makeJPEGBytes(t)
	xmp := xmpPacket(`GCamera:MicroVideo="1" GCamera:MicroVideoOffset="` +
		itoa(len(mp4)) + `"`)
	photo := withXMP(t, jpg, xmp)
	path, head := writeSample(t, photo, mp4)

	info := Parse(path, head)
	if info.Type != "pixel_legacy" {
		t.Fatalf("type=%q", info.Type)
	}
	if info.VideoOffset != int64(len(photo)) || info.VideoLength != int64(len(mp4)) {
		t.Fatalf("offset=%d len=%d, want %d/%d", info.VideoOffset, info.VideoLength, len(photo), len(mp4))
	}
	// Extracted bytes must equal the embedded MP4 exactly.
	got := readRange(t, path, info.VideoOffset, info.VideoLength)
	if !bytes.Equal(got, mp4) {
		t.Fatal("extracted video bytes mismatch")
	}
}

func TestParsePixelNewFtypScan(t *testing.T) {
	mp4 := makeMP4(t)
	jpg := makeJPEGBytes(t)
	// New-style: MotionPhoto=1, no offset → must use the ftyp scan.
	xmp := xmpPacket(`GCamera:MotionPhoto="1" GCamera:MotionPhotoPresentationTimestampUs="-800000"`)
	photo := withXMP(t, jpg, xmp)
	path, head := writeSample(t, photo, mp4)

	info := Parse(path, head)
	if info.Type != "pixel" {
		t.Fatalf("type=%q", info.Type)
	}
	if info.VideoOffset != int64(len(photo)) || info.VideoLength != int64(len(mp4)) {
		t.Fatalf("offset=%d len=%d", info.VideoOffset, info.VideoLength)
	}
	if got := readRange(t, path, info.VideoOffset, info.VideoLength); !bytes.Equal(got, mp4) {
		t.Fatal("extracted video bytes mismatch")
	}
}

func TestParseSamsungMarker(t *testing.T) {
	mp4 := makeMP4(t)
	jpg := makeJPEGBytes(t)
	xmp := xmpPacket(`samsung:MotionPhoto="1"`)
	photo := withXMP(t, jpg, xmp)
	// Legacy Samsung: fixed tail marker directly before the MP4.
	path, head := writeSample(t, photo, []byte(samsungMarker), mp4)

	info := Parse(path, head)
	if info.Type != "samsung" {
		t.Fatalf("type=%q", info.Type)
	}
	wantOff := int64(len(photo) + len(samsungMarker))
	if info.VideoOffset != wantOff || info.VideoLength != int64(len(mp4)) {
		t.Fatalf("offset=%d len=%d want %d/%d", info.VideoOffset, info.VideoLength, wantOff, len(mp4))
	}
	if got := readRange(t, path, info.VideoOffset, info.VideoLength); !bytes.Equal(got, mp4) {
		t.Fatal("extracted video bytes mismatch")
	}
}

func TestParseSamsungFtypFallback(t *testing.T) {
	mp4 := makeMP4(t)
	jpg := makeJPEGBytes(t)
	xmp := xmpPacket(`samsung:MotionPhoto="1"`)
	photo := withXMP(t, jpg, xmp)
	// New Samsung: no marker, ftyp scan must find it.
	path, head := writeSample(t, photo, mp4)

	info := Parse(path, head)
	if info.Type != "samsung" {
		t.Fatalf("type=%q", info.Type)
	}
	if info.VideoOffset != int64(len(photo)) {
		t.Fatalf("offset=%d want %d", info.VideoOffset, len(photo))
	}
	if got := readRange(t, path, info.VideoOffset, info.VideoLength); !bytes.Equal(got, mp4) {
		t.Fatal("extracted video bytes mismatch")
	}
}

func TestParsePlainJPEGIsNone(t *testing.T) {
	jpg := makeJPEGBytes(t)
	path, head := writeSample(t, jpg)
	if info := Parse(path, head); info.Type != "none" {
		t.Fatalf("plain jpeg: %+v", info)
	}
}

func TestParseIOSHeicIsNoneFromSingleFile(t *testing.T) {
	// iOS Live Photos are pair-based; Parse on the .heic alone must say none.
	dir := t.TempDir()
	heic := filepath.Join(dir, "IMG_0001.heic")
	if err := os.WriteFile(heic, []byte("fake heic bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "IMG_0001.mov"), makeMP4(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if info := Parse(heic, []byte("fake heic bytes")); info.Type != "none" {
		t.Fatalf("heic alone: %+v", info)
	}
}

func TestParseCorruptXMP(t *testing.T) {
	mp4 := makeMP4(t)
	jpg := makeJPEGBytes(t)
	// Unterminated packet + truncated offset value → no valid offset; ftyp
	// scan must still rescue the pixel_legacy detection.
	xmp := `<x:xmpmeta><rdf:RDF><rdf:Description GCamera:MicroVideo="1" GCamera:MicroVideoOffset="`
	photo := withXMP(t, jpg, xmp)
	path, head := writeSample(t, photo, mp4)
	info := Parse(path, head)
	if info.Type != "pixel_legacy" || info.VideoOffset != int64(len(photo)) {
		t.Fatalf("corrupt xmp: %+v", info)
	}

	// Garbage XMP without any marker → none.
	garbage := withXMP(t, jpg, `<x:xmpmeta><rdf:RDF>%%%%not xml%%%%`)
	path2, head2 := writeSample(t, garbage)
	if info := Parse(path2, head2); info.Type != "none" {
		t.Fatalf("garbage xmp: %+v", info)
	}
}

func TestParseTruncatedFile(t *testing.T) {
	mp4 := makeMP4(t)
	jpg := makeJPEGBytes(t)
	xmp := xmpPacket(`GCamera:MicroVideo="1" GCamera:MicroVideoOffset="` + itoa(len(mp4)) + `"`)
	photo := withXMP(t, jpg, xmp)
	// Claim the full MP4 length but truncate the file so the embedded ftyp
	// box itself is incomplete: both the declared-offset check and the ftyp
	// scan's box-size legality check must reject it.
	truncated := mp4[:8] // 4-byte size + "ftyp" only; declared box size exceeds EOF
	path, head := writeSample(t, photo, truncated)
	if info := Parse(path, head); info.Type != "none" {
		t.Fatalf("truncated: %+v", info)
	}
}

func TestFtypScanRejectsFakeSignature(t *testing.T) {
	jpg := makeJPEGBytes(t)
	xmp := xmpPacket(`GCamera:MotionPhoto="1"`)
	photo := withXMP(t, jpg, xmp)
	// Embed a fake "ftypisom" in tail data whose box size is illegal
	// (points past EOF) — must NOT be accepted.
	fake := make([]byte, 64)
	copy(fake[4:], []byte("ftypisom"))
	binary.BigEndian.PutUint32(fake[:4], 1<<24) // box size way past EOF
	path, head := writeSample(t, photo, fake)
	if info := Parse(path, head); info.Type != "none" {
		t.Fatalf("fake ftyp accepted: %+v", info)
	}
}

func readRange(t *testing.T, path string, off, length int64) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, off); err != nil {
		t.Fatal(err)
	}
	return buf
}

func itoa(n int) string { return strconv.Itoa(n) }
