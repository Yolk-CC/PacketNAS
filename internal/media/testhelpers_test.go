package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makeJPEG encodes a w×h solid-color JPEG.
func makeJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exifSegment builds a minimal EXIF APP1 payload carrying
// DateTimeOriginal = dt (format "2006:01:02:15:04:05").
func exifSegment(dt string) []byte {
	buf := new(bytes.Buffer)
	buf.WriteString("Exif\x00\x00")
	buf.WriteString("II") // little endian
	_ = binary.Write(buf, binary.LittleEndian, uint16(42))
	_ = binary.Write(buf, binary.LittleEndian, uint32(8)) // IFD0 offset
	// IFD0: one entry, ExifIFD pointer (0x8769) -> offset 26
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(0x8769))
	_ = binary.Write(buf, binary.LittleEndian, uint16(4)) // LONG
	_ = binary.Write(buf, binary.LittleEndian, uint32(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(26))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0)) // next IFD
	// ExifIFD at 26: one entry, DateTimeOriginal (0x9003) ASCII -> offset 44
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(0x9003))
	_ = binary.Write(buf, binary.LittleEndian, uint16(2)) // ASCII
	_ = binary.Write(buf, binary.LittleEndian, uint32(20))
	_ = binary.Write(buf, binary.LittleEndian, uint32(44))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0)) // next IFD
	// string data at 44
	buf.WriteString(dt)
	buf.WriteByte(0)
	return buf.Bytes()
}

// makeJPEGExif writes a JPEG containing an EXIF DateTimeOriginal.
func makeJPEGExif(t *testing.T, path string, w, h int, dt string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{200, uint8(x % 255), 50, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if raw[0] != 0xFF || raw[1] != 0xD8 {
		t.Fatal("not a JPEG")
	}
	payload := exifSegment(dt)
	app1 := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte((len(payload) + 2) & 0xFF)}
	out := append([]byte{}, raw[:2]...)
	out = append(out, app1...)
	out = append(out, payload...)
	out = append(out, raw[2:]...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// makePNG writes a w×h PNG.
func makePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
