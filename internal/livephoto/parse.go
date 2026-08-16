// Package livephoto detects Live Photo / Motion Photo files (JPEG or HEIC
// with an embedded or companion MP4 video) and extracts the video part.
//
// Supported formats (see SPEC-M3 §0):
//   - Pixel legacy MicroVideo: XMP GCamera:MicroVideo=1 + MicroVideoOffset
//     (offset counted back from the end of file)
//   - Pixel MotionPhoto: XMP GCamera:MotionPhoto=1, video located via an
//     ftyp box scan
//   - Samsung: XMP samsung:MotionPhoto=1, or the fixed tail marker
//     "MotionPhoto_Data" right before the MP4; ftyp scan as fallback
//   - iOS Live Photo: same-name .heic/.jpg + .mov pairing (handled by the
//     media scanner, not by Parse)
package livephoto

import (
	"encoding/binary"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Info describes a detected Live Photo.
type Info struct {
	Type        string // "pixel" | "pixel_legacy" | "samsung" | "ios" | "none"
	VideoOffset int64  // video start, counted from the file head (0 for iOS)
	VideoLength int64  // video byte length (companion file size for iOS)
	Companion   string // iOS: .mov root-relative path; "" otherwise
}

// None is returned when no Live Photo structure is detected.
var None = Info{Type: "none"}

// headSize is how many bytes callers must supply in data (XMP always lives
// in an APP1 segment right after SOI, 128KB is plenty).
const headSize = 128 << 10

// HeadSize is the number of head bytes Parse expects in data.
func HeadSize() int { return headSize }

// samsungMarker precedes the embedded MP4 in older Samsung Motion Photos.
const samsungMarker = "MotionPhoto_Data"

// validBrands are the MP4 major brands accepted during the ftyp scan.
var validBrands = []string{"isom", "iso2", "mp41", "mp42", "avc1", "qt  "}

var (
	reMicroVideo       = regexp.MustCompile(`GCamera:MicroVideo\s*=\s*["']1["']|<GCamera:MicroVideo>\s*1\s*</GCamera:MicroVideo>`)
	reMicroVideoOffAtt = regexp.MustCompile(`GCamera:MicroVideoOffset\s*=\s*["'](\d+)["']`)
	reMicroVideoOffEle = regexp.MustCompile(`<GCamera:MicroVideoOffset>\s*(\d+)\s*</GCamera:MicroVideoOffset>`)
	reMotionPhoto      = regexp.MustCompile(`GCamera:MotionPhoto\s*=\s*["']1["']|<GCamera:MotionPhoto>\s*1\s*</GCamera:MotionPhoto>`)
	reSamsung          = regexp.MustCompile(`samsung:MotionPhoto\s*=\s*["']1["']|<samsung:MotionPhoto>\s*1\s*</samsung:MotionPhoto>`)
)

// Parse inspects a photo file for an embedded video. data must hold (at
// least) the first 128KB of the file; when a full ftyp scan is needed Parse
// re-opens path itself. Returns None for unrecognized files. iOS Live
// Photos are not detectable from a single file and always yield None here.
func Parse(path string, data []byte) Info {
	st, err := os.Stat(path)
	if err != nil {
		return None
	}
	size := st.Size()
	xmp := extractXMP(data)

	// Samsung legacy: the fixed tail marker "MotionPhoto_Data" directly
	// precedes the MP4 and is a sufficient signature on its own.
	if off, ok := findTailMarker(path, size); ok {
		if validBoxAt(path, off, size) {
			return Info{Type: "samsung", VideoOffset: off, VideoLength: size - off}
		}
	}

	if xmp == "" {
		return None
	}

	// Pixel legacy MicroVideo with explicit offset from the tail.
	if reMicroVideo.MatchString(xmp) {
		if n, ok := parseOffset(xmp); ok && n > 0 && n < size {
			off := size - n
			if validBoxAt(path, off, size) {
				return Info{Type: "pixel_legacy", VideoOffset: off, VideoLength: n}
			}
		}
		// Offset missing/invalid: fall through to the ftyp scan.
		if off, ok := scanFtyp(path, size); ok {
			return Info{Type: "pixel_legacy", VideoOffset: off, VideoLength: size - off}
		}
		return None
	}

	// Pixel new MotionPhoto: no offset, ftyp scan.
	if reMotionPhoto.MatchString(xmp) {
		if off, ok := scanFtyp(path, size); ok {
			return Info{Type: "pixel", VideoOffset: off, VideoLength: size - off}
		}
		return None
	}

	// Samsung new: ftyp scan (old marker handled above).
	if reSamsung.MatchString(xmp) {
		if off, ok := scanFtyp(path, size); ok {
			return Info{Type: "samsung", VideoOffset: off, VideoLength: size - off}
		}
	}
	return None
}

// extractXMP returns the XMP packet text contained in the head bytes, or ""
// if there is none.
func extractXMP(data []byte) string {
	s := string(data)
	start := strings.Index(s, "<x:xmpmeta")
	if start == -1 {
		start = strings.Index(s, "<rdf:RDF")
	}
	if start == -1 {
		return ""
	}
	end := strings.Index(s[start:], "</x:xmpmeta>")
	if end == -1 {
		// Truncated/corrupt packet: use what we have.
		return s[start:]
	}
	return s[start : start+end+len("</x:xmpmeta>")]
}

// parseOffset reads GCamera:MicroVideoOffset in attribute or element form.
func parseOffset(xmp string) (int64, bool) {
	if m := reMicroVideoOffAtt.FindStringSubmatch(xmp); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		return n, err == nil
	}
	if m := reMicroVideoOffEle.FindStringSubmatch(xmp); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		return n, err == nil
	}
	return 0, false
}

// findTailMarker locates "MotionPhoto_Data" near the end of the file and
// returns the offset just past the marker.
func findTailMarker(path string, size int64) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	// The marker (16 bytes) directly precedes the MP4; scanning the last
	// 1MB window in overlapping chunks is enough and cheap.
	window := int64(1 << 20)
	if size < window {
		window = size
	}
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, size-window); err != nil && window == size {
		// short read tolerated below via partial buffer
	}
	idx := strings.LastIndex(string(buf), samsungMarker)
	if idx == -1 {
		return 0, false
	}
	off := size - window + int64(idx) + int64(len(samsungMarker))
	if off >= size {
		return 0, false
	}
	return off, true
}

// validBoxAt reports whether a well-formed MP4 box (size>8, fits in the
// file, plausible type) starts at off.
func validBoxAt(path string, off, size int64) bool {
	if off < 0 || off+8 > size {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [16]byte
	if _, err := f.ReadAt(hdr[:], off); err != nil {
		return false
	}
	boxSize := int64(binary.BigEndian.Uint32(hdr[:4]))
	typ := string(hdr[4:8])
	if boxSize == 1 { // 64-bit largesize
		boxSize = int64(binary.BigEndian.Uint64(hdr[8:16]))
	}
	if boxSize < 8 || off+boxSize > size {
		return false
	}
	return isKnownBoxType(typ)
}

func isKnownBoxType(typ string) bool {
	switch typ {
	case "ftyp", "moov", "mdat", "free", "skip", "styp", "wide":
		return true
	}
	return false
}

// scanFtyp finds the last valid MP4 ftyp box signature in the file and
// returns its offset. A candidate is: 4-byte big-endian length + "ftyp" +
// one of the known major brands, with a legal box size (>8, within file)
// — this rejects accidental matches inside JPEG entropy data.
func scanFtyp(path string, size int64) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	const chunk = 4 << 20 // 4MB chunks, overlap 16 bytes for boundary matches
	var (
		off     int64
		last    int64 = -1
		overlap [16]byte
		overN   int
	)
	buf := make([]byte, chunk)
	for off < size {
		n, err := f.ReadAt(buf, off)
		if n == 0 {
			break
		}
		// Search window = overlap + current chunk.
		win := make([]byte, 0, overN+n)
		win = append(win, overlap[:overN]...)
		win = append(win, buf[:n]...)
		base := off - int64(overN)
		s := string(win)
		searchFrom := 0
		for {
			i := strings.Index(s[searchFrom:], "ftyp")
			if i == -1 {
				break
			}
			pos := searchFrom + i // index of 'f' in win
			searchFrom = pos + 1
			if pos < 4 || pos+8 > len(win) {
				continue
			}
			boxSize := int64(binary.BigEndian.Uint32(win[pos-4 : pos]))
			brand := string(win[pos+4 : pos+8])
			absPos := base + int64(pos) - 4
			if boxSize < 8 || absPos < 0 || absPos+boxSize > size {
				continue
			}
			if !isValidBrand(brand) {
				continue
			}
			// Only accept if this candidate is fully inside what we have
			// read so far for the size check (absPos+boxSize <= off+n or
			// <= size is fine since boxSize legality is vs file size).
			if absPos > last {
				last = absPos
			}
		}
		if err != nil {
			break
		}
		overN = copy(overlap[:], buf[max(0, n-16):n])
		off += int64(n)
	}
	if last == -1 {
		return 0, false
	}
	// Final verification: parse the box chain from the candidate — the
	// first box must be legal, which we already checked; accept.
	if !validBoxAt(path, last, size) {
		return 0, false
	}
	return last, true
}

func isValidBrand(b string) bool {
	for _, v := range validBrands {
		if b == v {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
