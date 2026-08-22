package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"

	"github.com/go-chi/chi/v5"
)

// Version is reported by /api/system/info. It is a var so release builds can
// inject it via -ldflags "-X pocket-nas/internal/files.Version=vX.Y.Z".
var Version = "0.1.0"

// APILevel is the current API level reported by /api/system/info and the LAN
// discovery reply, for client capability negotiation (SPEC-M8 §1).
const APILevel = 2

// Handler exposes Service over HTTP per the SPEC API contract.
type Handler struct {
	svc        *Service
	serverName string // reported by /api/system/info (SPEC-M8)
}

// NewHandler creates a Handler for svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// SetServerName sets the server name reported by /api/system/info.
func (h *Handler) SetServerName(name string) { h.serverName = name }

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

// writeServiceError maps service errors to the SPEC error envelope.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ErrBadRequest):
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		// Unexpected errors may carry absolute paths; log them and answer
		// with a generic message instead.
		log.Printf("files: internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ListFiles handles GET /api/files?path=&type=all|image|video.
func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		rel = "/"
	}
	typ := r.URL.Query().Get("type")
	infos, err := h.svc.List(rel, typ)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, infos)
}

// Upload handles POST /api/upload?path=<dir>, multipart field "file".
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	dirRel := r.URL.Query().Get("path")
	if dirRel == "" {
		dirRel = "/"
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid multipart form")
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) == 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "no file field")
		return
	}
	uploaded := make([]string, 0)
	for _, fh := range r.MultipartForm.File["file"] {
		if err := h.svc.SaveUpload(dirRel, fh); err != nil {
			writeServiceError(w, err)
			return
		}
		uploaded = append(uploaded, fh.Filename)
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": uploaded})
}

// Download handles GET /api/download/<path...>[?archive=zip].
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	rel := chi.URLParam(r, "*")
	if rel == "" {
		rel = "/"
	}
	info, abs, err := h.svc.Stat(rel)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if info.IsDir() || r.URL.Query().Get("archive") == "zip" {
		if !info.IsDir() {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "archive=zip requires a directory")
			return
		}
		h.svc.StreamZip(w, abs, info.Name())
		return
	}
	f, finfo, err := h.svc.Open(rel)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", finfo.Name()))
	// ServeContent provides Range support (206) automatically.
	http.ServeContent(w, r, finfo.Name(), finfo.ModTime(), f)
}

type renameRequest struct {
	Path    string `json:"path"`
	NewName string `json:"newName"`
}

// Rename handles POST /api/files/rename.
func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" || req.NewName == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if err := h.svc.Rename(req.Path, req.NewName); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type moveRequest struct {
	SrcPaths []string `json:"srcPaths"`
	DestDir  string   `json:"destDir"`
}

// Move handles POST /api/files/move.
func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.SrcPaths) == 0 || req.DestDir == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if err := h.svc.Move(req.SrcPaths, req.DestDir); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type mkdirRequest struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
}

// Mkdir handles POST /api/files/mkdir.
func (h *Handler) Mkdir(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if req.Dir == "" {
		req.Dir = "/"
	}
	if err := h.svc.Mkdir(req.Dir, req.Name); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type deleteRequest struct {
	Paths []string `json:"paths"`
}

// Delete handles DELETE /api/files.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	var req deleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if err := h.svc.Delete(req.Paths); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SystemInfo handles GET /api/system/info.
func (h *Handler) SystemInfo(w http.ResponseWriter, r *http.Request) {
	free, total := diskStat(h.svc.Root())
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    Version,
		"root":       h.svc.Root(),
		"diskFree":   free,
		"diskTotal":  total,
		"goVersion":  runtime.Version(),
		"serverName": h.serverName,
		"apiLevel":   APILevel,
	})
}
