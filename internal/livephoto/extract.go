package livephoto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"pocket-nas/internal/files"
)

// LiveCacheDir is the extraction cache subdirectory inside .pocketnas.
// (The meta dir name is duplicated here to avoid an import cycle: media
// imports livephoto for Parse.)
const LiveCacheDir = "livecache"

const metaDir = ".pocketnas"

// Meta is the index-side information the handler needs about one file.
type Meta struct {
	Live      bool
	Type      string // pixel | pixel_legacy | samsung | ios
	Offset    int64  // embedded video start
	Length    int64  // embedded video length
	Companion string // iOS: root-relative .mov path
}

// Lookup resolves a root-relative path to its index metadata. Provided by
// the media package (which owns the DB).
type Lookup func(rel string) (*Meta, error)

// Handler serves GET /api/livephoto/<path...>: the video part of a Live
// Photo, extracted to a disk cache so Range requests work.
type Handler struct {
	root      string // resolved storage root
	cacheDir  string
	lookup    Lookup
	resolveFn func(rel string) (string, error) // nil → files.Resolve(root, rel)
}

// SetResolver makes the handler resolve virtual paths (shared mode,
// SPEC-M7) via fn instead of the legacy single-root resolution.
func (h *Handler) SetResolver(fn func(rel string) (string, error)) { h.resolveFn = fn }

func (h *Handler) resolve(rel string) (string, error) {
	if h.resolveFn != nil {
		return h.resolveFn(rel)
	}
	return files.Resolve(h.root, rel)
}

// NewHandler creates a Handler for the resolved root.
func NewHandler(root string, lookup Lookup) (*Handler, error) {
	dir := filepath.Join(root, metaDir, LiveCacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Handler{root: root, cacheDir: dir, lookup: lookup}, nil
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	var body errorBody
	body.Error.Code = code
	body.Error.Message = msg
	_ = json.NewEncoder(w).Encode(body)
}

// cacheName keys the extraction cache by sha256(path + mtime).
func cacheName(rel string, mtime int64) string {
	sum := sha256.Sum256([]byte(rel + ":" + strconv.FormatInt(mtime, 10)))
	return hex.EncodeToString(sum[:]) + ".mp4"
}

// ServeHTTP handles GET /api/livephoto/<path...>.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rel := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	abs, err := h.resolve(rel)
	if err != nil {
		if errors.Is(err, files.ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "media not found")
		return
	}
	meta, err := h.lookup(rel)
	if err != nil || meta == nil || !meta.Live {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not a live photo")
		return
	}

	if meta.Type == "ios" {
		// Serve the companion .mov directly.
		cAbs, err := h.resolve(meta.Companion)
		if err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
		f, err := os.Open(cAbs)
		if err != nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "companion video not found")
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "companion video not found")
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, st.Name(), st.ModTime(), f)
		return
	}

	// Embedded video: extract to cache (keyed by path+mtime), then serve.
	cached := filepath.Join(h.cacheDir, cacheName(rel, info.ModTime().Unix()))
	if st, err := os.Stat(cached); err != nil || st.Size() != meta.Length {
		if err := h.extractTo(abs, meta.Offset, meta.Length, cached); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "extraction failed: "+err.Error())
			return
		}
	}
	f, err := os.Open(cached)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, "livephoto.mp4", st.ModTime(), f)
}

// extractTo copies length bytes starting at offset from src to dst
// (atomically, via a temp file + rename).
func (h *Handler) extractTo(src string, offset, length int64, dst string) error {
	if offset < 0 || length <= 0 {
		return errors.New("invalid video range")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if offset+length > st.Size() {
		return errors.New("video range exceeds file size")
	}
	if _, err := in.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(out, in, length)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dst)
}
