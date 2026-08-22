package media

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"pocket-nas/internal/files"
	"pocket-nas/internal/livephoto"
)

const (
	defaultThumbSize = 300
	maxThumbSize     = 1024
	defaultPageLimit = 200
	maxPageLimit     = 1000
)

// Handler exposes the gallery / thumbnail / media-file HTTP API.
type Handler struct {
	store     *Store
	scanner   *Scanner
	thumber   *Thumber
	root      string                           // resolved root
	resolveFn func(rel string) (string, error) // nil → files.Resolve(root, rel)
}

// NewHandler opens the index store under root and wires up the scanner and
// thumbnail cache. root must be the files.Service resolved root.
func NewHandler(root string) (*Handler, error) {
	store, err := Open(root)
	if err != nil {
		return nil, err
	}
	th, err := NewThumber(root)
	if err != nil {
		store.Close()
		return nil, err
	}
	return &Handler{
		store:   store,
		scanner: NewScanner(store, root),
		thumber: th,
		root:    root,
	}, nil
}

// Close releases the index database.
func (h *Handler) Close() error { return h.store.Close() }

// SetShares configures shared mode (SPEC-M7 §4): rootsFn supplies the scan
// roots (one per share, or the legacy root) and resolveFn maps virtual
// paths ("/shareName/sub") to absolute paths. Both are consulted
// dynamically, so share updates apply without rebuilding the handler.
func (h *Handler) SetShares(rootsFn func() []ScanRoot, resolveFn func(rel string) (string, error)) {
	h.scanner.SetRootsFn(rootsFn, resolveFn)
	h.resolveFn = resolveFn
}

// resolve maps a client virtual path to an absolute filesystem path.
func (h *Handler) resolve(rel string) (string, error) {
	if h.resolveFn != nil {
		return h.resolveFn(rel)
	}
	return files.Resolve(h.root, rel)
}

// StartBackgroundScan launches an incremental scan in a goroutine; intended
// to be called once at server startup. The gallery API stays usable while it
// runs, returning whatever is already indexed.
func (h *Handler) StartBackgroundScan() {
	go func() {
		if err := h.scanner.Incremental(context.Background()); err != nil {
			log.Printf("media: background scan: %v", err)
		}
	}()
}

// Scanner exposes the scanner (for tests and status).
func (h *Handler) Scanner() *Scanner { return h.scanner }

// Store exposes the index store (for tests).
func (h *Handler) Store() *Store { return h.store }

// LiveLookup implements livephoto.Lookup against the index store; used by
// the /api/livephoto handler.
func (h *Handler) LiveLookup(rel string) (*livephoto.Meta, error) {
	m, err := h.store.Get(rel)
	if err != nil || m == nil {
		return nil, err
	}
	return &livephoto.Meta{
		Live:      m.IsLivePhoto,
		Type:      m.LiveType,
		Offset:    m.VideoOffset,
		Length:    m.VideoLength,
		Companion: m.CompanionPath,
	}, nil
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// galleryItem is one entry of the /api/gallery response.
type galleryItem struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType"`
	TakenTime   int64  `json:"takenTime"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Duration    int    `json:"duration"`
	ThumbURL    string `json:"thumbUrl"`
	IsLivePhoto bool   `json:"isLivePhoto"` // M3
	LiveType    string `json:"liveType"`    // pixel | pixel_legacy | samsung | ios | ""
	// M4: available playback tiers for videos (always all tiers + original).
	Resolutions []string `json:"resolutions,omitempty"`
}

// Gallery handles GET /api/gallery?offset=0&limit=200&type=all|image|video.
func (h *Handler) Gallery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	typ := q.Get("type")
	if typ != "image" && typ != "video" {
		typ = "all"
	}
	items, total, err := h.store.Page(offset, limit, typ)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	out := make([]galleryItem, 0, len(items))
	for _, m := range items {
		var resolutions []string
		if strings.HasPrefix(m.MimeType, "video/") {
			resolutions = []string{"360p", "720p", "1080p", "original"}
		}
		out = append(out, galleryItem{
			Path:        m.Path,
			Name:        m.Name,
			MimeType:    m.MimeType,
			TakenTime:   m.TakenTime,
			Width:       m.Width,
			Height:      m.Height,
			Duration:    m.Duration,
			ThumbURL:    "/api/thumb" + thumbPathEscape(m.Path) + "?w=300&h=300",
			IsLivePhoto: m.IsLivePhoto,
			LiveType:    m.LiveType,
			Resolutions: resolutions,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "items": out})
}

// thumbPathEscape escapes each segment of a root-relative path for use in a
// URL path, preserving the slashes.
func thumbPathEscape(rel string) string {
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// GalleryScan handles GET /api/gallery/scan → {"scanning":bool,"indexed":N}.
func (h *Handler) GalleryScan(w http.ResponseWriter, r *http.Request) {
	indexed, err := h.store.Count()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scanning": h.scanner.Scanning(),
		"indexed":  indexed,
	})
}

// resolveParam converts the chi "*" wildcard (or query-less suffix) into an
// absolute path inside root using the shared files.Resolve rules.
func (h *Handler) resolveParam(r *http.Request) (abs, rel string, err error) {
	rel = "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	abs, err = h.resolve(rel)
	return abs, rel, err
}

func parseThumbSize(r *http.Request) (int, int) {
	q := r.URL.Query()
	parse := func(key string) int {
		n, err := strconv.Atoi(q.Get(key))
		if err != nil || n <= 0 {
			return defaultThumbSize
		}
		if n > maxThumbSize {
			return maxThumbSize
		}
		return n
	}
	return parse("w"), parse("h")
}

// Thumb handles GET /api/thumb/<path...>?w=300&h=300. Cache hits are served
// directly; generation failures redirect to the placeholder SVG.
func (h *Handler) Thumb(w http.ResponseWriter, r *http.Request) {
	abs, rel, err := h.resolveParam(r)
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
	if !isMedia(abs) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "not a media file")
		return
	}
	tw, th := parseThumbSize(r)
	thumbAbs, err := h.thumber.Get(r.Context(), abs, rel, tw, th)
	if err != nil {
		// Generation failed (corrupt file, no ffmpeg, ...): fall back to the
		// placeholder instead of an error so grids stay renderable.
		http.Redirect(w, r, "/static/placeholder.svg", http.StatusFound)
		return
	}
	if m, _ := h.store.Get(rel); m != nil && m.ThumbnailPath == "" {
		_ = h.store.SetThumbnail(rel, cacheName(rel, tw, th, info.ModTime().Unix()))
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, thumbAbs)
}

// MediaFile handles GET /api/media/file/<path...>, streaming the original
// file with http.ServeContent (Range requests supported).
func (h *Handler) MediaFile(w http.ResponseWriter, r *http.Request) {
	abs, _, err := h.resolveParam(r)
	if err != nil {
		if errors.Is(err, files.ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "media not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "media not found")
		return
	}
	if !isMedia(abs) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "not a media file")
		return
	}
	w.Header().Set("Content-Type", files.MimeType(info.Name(), false))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
