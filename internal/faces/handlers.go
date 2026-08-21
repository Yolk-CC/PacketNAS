package faces

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"

	"pocket-nas/internal/files"
)

// Handler exposes the /api/faces API (SPEC-M11 §3).
type Handler struct {
	svc *Service
}

// NewHandler wraps the service.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

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

// errUnavailable is the guard for every endpoint except status/download.
func (h *Handler) available(w http.ResponseWriter) bool {
	ok, reason := h.svc.Available()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "faces_unavailable", reason)
		return false
	}
	return true
}

// store returns the faces store or writes a 500 and returns nil.
func (h *Handler) store(w http.ResponseWriter) *Store {
	st := h.svc.Store()
	if st == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "faces database unavailable")
	}
	return st
}

// Status handles GET /api/faces/status.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	ok, reason := h.svc.Available()
	pending, done, scanning := h.svc.QueueProgress()
	prof := h.svc.Profile()
	var persons, facesTotal int
	if st := h.store(w); st != nil {
		persons, _ = st.PersonCount()
		facesTotal, _ = st.FaceCount()
	}
	resp := map[string]any{
		"available":  ok,
		"model":      map[string]any{"profile": prof.Name, "det": prof.DetModel, "rec": prof.RecModel, "dims": prof.Dims},
		"queue":      map[string]any{"pending": pending, "done": done, "scanning": scanning},
		"persons":    persons,
		"facesTotal": facesTotal,
		"models":     h.svc.ListModels(),
		"download":   h.svc.DownloadState(),
		"profiles":   BuiltinProfiles,
	}
	if reason != "" {
		resp["reason"] = reason
	}
	writeJSON(w, http.StatusOK, resp)
}

// DownloadModels handles POST /api/faces/models/download.
func (h *Handler) DownloadModels(w http.ResponseWriter, r *http.Request) {
	if !h.svc.StartDownload() {
		writeJSON(w, http.StatusAccepted, map[string]any{"downloading": true, "note": "already running"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloading": true})
}

// SetModels handles PUT /api/faces/models {profile?, detModel?, recModel?,
// libPath?}. Persists settings, rebuilds the engine; an embedding-dimension
// change resets the face index (reload handles it).
func (h *Handler) SetModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile  string `json:"profile"`
		DetModel string `json:"detModel"`
		RecModel string `json:"recModel"`
		LibPath  string `json:"libPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	cfg := h.svc.getCfg()
	if req.Profile != "" {
		if _, ok := BuiltinProfiles[req.Profile]; !ok {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown profile "+req.Profile)
			return
		}
		cfg.Profile = req.Profile
		// Profile switch resets explicit file overrides to the profile's.
		p := BuiltinProfiles[req.Profile]
		cfg.DetModel = ""
		cfg.RecModel = ""
		_ = p
	}
	if req.DetModel != "" {
		cfg.DetModel = req.DetModel
	}
	if req.RecModel != "" {
		cfg.RecModel = req.RecModel
	}
	if req.LibPath != "" || r.Body != nil {
		cfg.LibPath = req.LibPath
	}
	if err := h.svc.setCfg(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	h.svc.Reload()
	h.Status(w, r)
}

// Scan handles POST /api/faces/scan.
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	h.svc.TriggerScan()
	pending, done, scanning := h.svc.QueueProgress()
	writeJSON(w, http.StatusOK, map[string]any{"pending": pending, "done": done, "scanning": scanning})
}

// personJSON is the /api/faces/persons item shape.
type personJSON struct {
	ID        int64  `json:"id"`
	Name      string `json:"name,omitempty"`
	FaceCount int    `json:"faceCount"`
	CoverURL  string `json:"coverUrl"`
}

// Persons handles GET /api/faces/persons.
func (h *Handler) Persons(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	st := h.store(w)
	if st == nil {
		return
	}
	persons, err := st.Persons()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	out := make([]personJSON, 0, len(persons))
	for _, p := range persons {
		cover := ""
		if p.CoverFaceID > 0 {
			cover = "/api/faces/crop/" + strconv.FormatInt(p.CoverFaceID, 10)
		}
		out = append(out, personJSON{ID: p.ID, Name: p.Name, FaceCount: p.FaceCount, CoverURL: cover})
	}
	writeJSON(w, http.StatusOK, out)
}

// PersonPhotos handles GET /api/faces/persons/{id}/photos — gallery-format
// items for every media file containing this person.
func (h *Handler) PersonPhotos(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	st := h.store(w)
	if st == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid person id")
		return
	}
	p, err := st.PersonByID(id)
	if err != nil || p == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "person not found")
		return
	}
	faces, err := st.FacesForPerson(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	byHash := h.svc.hashIndex()
	seen := map[string]bool{}
	type item struct {
		Path        string `json:"path"`
		Name        string `json:"name"`
		MimeType    string `json:"mimeType"`
		TakenTime   int64  `json:"takenTime"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		ThumbURL    string `json:"thumbUrl"`
		IsLivePhoto bool   `json:"isLivePhoto"`
		LiveType    string `json:"liveType"`
	}
	out := []item{}
	for _, f := range faces {
		path, ok := byHash[f.FileHash]
		if !ok || seen[path] {
			continue
		}
		seen[path] = true
		m, err := h.svc.src.MediaByPath(path)
		if err != nil || m == nil {
			continue // hash known but file not indexed yet
		}
		out = append(out, item{
			Path: m.Path, Name: m.Name, MimeType: m.MimeType,
			TakenTime: m.TakenTime, Width: m.Width, Height: m.Height,
			ThumbURL:    "/api/thumb" + escapePath(m.Path) + "?w=300&h=300",
			IsLivePhoto: m.IsLivePhoto, LiveType: m.LiveType,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "items": out, "person": p})
}

func escapePath(rel string) string {
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = (&urlEscaper{}).escape(s)
	}
	return strings.Join(segs, "/")
}

type urlEscaper struct{}

func (*urlEscaper) escape(s string) string {
	r := strings.NewReplacer(" ", "%20", "#", "%23", "?", "%3F", "%", "%25")
	return r.Replace(s)
}

// RenamePerson handles PUT /api/faces/persons/{id} {name}.
func (h *Handler) RenamePerson(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	st := h.store(w)
	if st == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid person id")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	p, err := st.PersonByID(id)
	if err != nil || p == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "person not found")
		return
	}
	if err := st.SetPersonName(id, strings.TrimSpace(req.Name)); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	h.Persons(w, r)
}

// MergePersons handles POST /api/faces/persons/merge {from,to}.
func (h *Handler) MergePersons(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	st := h.store(w)
	if st == nil {
		return
	}
	var req struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.From == req.To {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid merge request")
		return
	}
	for _, id := range []int64{req.From, req.To} {
		p, err := st.PersonByID(id)
		if err != nil || p == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "person not found")
			return
		}
	}
	if err := st.MergePersons(req.From, req.To); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	h.Persons(w, r)
}

// Crop handles GET /api/faces/crop/{faceId} — cached face thumbnail cut
// from the original image. Works whenever the store has faces, even if the
// engine is unavailable (covers/migration previews).
func (h *Handler) Crop(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("faceId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid face id")
		return
	}
	cacheDir := filepath.Join(h.svc.root, ".pocketnas", "facecrop")
	cache := filepath.Join(cacheDir, strconv.FormatInt(id, 10)+".jpg")
	if st, err := os.Stat(cache); err == nil && st.Size() > 0 {
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, cache)
		return
	}
	st := h.store(w)
	if st == nil {
		return
	}
	face, err := st.FaceByID(id)
	if err != nil || face == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "face not found")
		return
	}
	path, ok := h.svc.hashIndex()[face.FileHash]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "source file not linked yet")
		return
	}
	abs, err := h.svc.src.Resolve(path)
	if err != nil {
		if errors.Is(err, files.ErrForbidden) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	img, err := decodeImage(abs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	crop := cropFace(img, face.Box)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	tmp := cache + ".tmp.jpg" // .jpg suffix: imaging infers format by extension
	if err := imaging.Save(crop, tmp, imaging.JPEGQuality(90)); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	if err := os.Rename(tmp, cache); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, cache)
}

// cropFace cuts the face box (with 20% margin, clamped) from img.
func cropFace(img image.Image, box [4]float32) image.Image {
	b := img.Bounds()
	w := box[2] - box[0]
	h := box[3] - box[1]
	mx, my := w*0.2, h*0.2
	x1 := clamp(int(box[0]-mx), b.Min.X, b.Max.X)
	y1 := clamp(int(box[1]-my), b.Min.Y, b.Max.Y)
	x2 := clamp(int(box[2]+mx+0.5), b.Min.X, b.Max.X)
	y2 := clamp(int(box[3]+my+0.5), b.Min.Y, b.Max.Y)
	if x2 <= x1 || y2 <= y1 {
		return imaging.Resize(img, 128, 0, imaging.Lanczos)
	}
	return imaging.Crop(img, image.Rect(x1, y1, x2, y2))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Export handles GET /api/faces/export — gzip JSON with persons + faces.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	st := h.store(w)
	if st == nil {
		return
	}
	prof := h.svc.Profile()
	data, err := st.Export(prof.RecModel, prof.Dims)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="faces-export.json.gz"`)
	gz := gzip.NewWriter(w)
	_ = json.NewEncoder(gz).Encode(data)
	gz.Close()
}

// Import handles POST /api/faces/import (gzip or plain JSON body). Faces
// link to media by content hash; unmatched records stay until the files
// appear. No re-identification is performed.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	st := h.store(w)
	if st == nil {
		return
	}
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" ||
		strings.HasSuffix(r.URL.Query().Get("filename"), ".gz") {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid gzip body")
			return
		}
		defer gz.Close()
		body = gz
	} else {
		// Sniff gzip magic for clients that don't set Content-Encoding.
		buf := make([]byte, 2)
		if _, err := io.ReadFull(r.Body, buf); err == nil && buf[0] == 0x1f && buf[1] == 0x8b {
			gz, err := gzip.NewReader(io.MultiReader(
				io.NopCloser(byteReader(buf)), r.Body))
			if err == nil {
				defer gz.Close()
				body = gz
			}
		} else {
			body = io.MultiReader(byteReader(buf), r.Body)
		}
	}
	var data ExportData
	if err := json.NewDecoder(body).Decode(&data); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid export payload: "+err.Error())
		return
	}
	res, err := st.Import(&data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	// Link hashes for media files not yet hashed so imported faces resolve.
	go h.svc.HashFiles(context.Background())
	writeJSON(w, http.StatusOK, res)
}

func byteReader(b []byte) io.Reader { return bytes.NewReader(b) }
