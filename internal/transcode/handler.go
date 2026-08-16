package transcode

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"pocket-nas/internal/files"
)

// Handler serves GET /api/video/<path>?res= and /api/video/status/<path>?res=.
type Handler struct {
	root string // resolved storage root
	mgr  *Manager
}

// NewHandler creates a Handler with its own Manager (queue + workers).
func NewHandler(root string) (*Handler, error) {
	mgr, err := NewManager(root)
	if err != nil {
		return nil, err
	}
	return &Handler{root: root, mgr: mgr}, nil
}

// Close stops the manager.
func (h *Handler) Close() error { return h.mgr.Close() }

// Manager exposes the job manager (for tests).
func (h *Handler) Manager() *Manager { return h.mgr }

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

// resolveVideo validates the wildcard path: inside root, exists, is a video.
func (h *Handler) resolveVideo(w http.ResponseWriter, r *http.Request) (rel, abs string, info os.FileInfo, ok bool) {
	rel = "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	abs, err := files.Resolve(h.root, rel)
	if err != nil {
		if errors.Is(err, files.ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
		} else {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		}
		return "", "", nil, false
	}
	info, err = os.Stat(abs)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "video not found")
		return "", "", nil, false
	}
	if !strings.HasPrefix(files.MimeType(info.Name(), false), "video/") {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "not a video file")
		return "", "", nil, false
	}
	return rel, abs, info, true
}

// serveFile streams f with Range support.
func serveFile(w http.ResponseWriter, r *http.Request, path, name, contentType string) {
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "output not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// Video handles GET /api/video/<path...>?res=original|360p|720p|1080p.
func (h *Handler) Video(w http.ResponseWriter, r *http.Request) {
	rel, abs, info, ok := h.resolveVideo(w, r)
	if !ok {
		return
	}
	res := r.URL.Query().Get("res")
	if res == "" || res == "original" {
		// Original is always served straight from the source file.
		serveFile(w, r, abs, info.Name(), files.MimeType(info.Name(), false))
		return
	}
	if !ValidRes(res) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid res: "+res)
		return
	}
	j := h.mgr.Request(rel, res, info.ModTime().Unix())
	switch j.Status {
	case StatusDone:
		serveFile(w, r, h.mgr.OutputPath(j), strings.TrimSuffix(info.Name(), fileExt(info.Name()))+"_"+res+".mp4", "video/mp4")
	case StatusFailed:
		writeError(w, http.StatusConflict, "CONFLICT", "transcode failed: "+j.Error)
	default: // queued / running
		writeJSON(w, http.StatusAccepted, map[string]any{"status": j.Status, "progress": j.Progress})
	}
}

func fileExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// Status handles GET /api/video/status/<path...>?res=<tier>.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.resolveVideo(w, r)
	if !ok {
		return
	}
	res := r.URL.Query().Get("res")
	if res == "" || res == "original" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "done", "progress": 100})
		return
	}
	if !ValidRes(res) {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid res: "+res)
		return
	}
	rel := "/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	j := h.mgr.Status(rel, res)
	resp := map[string]any{"status": j.Status, "progress": j.Progress}
	if j.Error != "" {
		resp["error"] = j.Error
	}
	writeJSON(w, http.StatusOK, resp)
}
