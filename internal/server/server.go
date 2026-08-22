// Package server assembles routes, middleware and lifecycle for PocketNAS.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"pocket-nas/internal/config"
	"pocket-nas/internal/faces"
	"pocket-nas/internal/files"
	"pocket-nas/internal/livephoto"
	"pocket-nas/internal/media"
	"pocket-nas/internal/settings"
	"pocket-nas/internal/transcode"
	"pocket-nas/web"
)

const (
	tokenTTL       = 7 * 24 * time.Hour // 7 days
	maxPortRetries = 100
)

// tokenStore keeps issued tokens in memory with lazy expiration cleanup.
type tokenStore struct {
	mu      sync.Mutex
	entries map[string]time.Time // token -> expiry
}

func newTokenStore() *tokenStore {
	return &tokenStore{entries: make(map[string]time.Time)}
}

func (s *tokenStore) issue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	s.mu.Lock()
	s.entries[tok] = time.Now().Add(tokenTTL)
	s.mu.Unlock()
	return tok, nil
}

func (s *tokenStore) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.entries[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.entries, tok) // lazy cleanup
		return false
	}
	return true
}

func writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

// authMiddleware enforces X-Auth-Token on all /api/* routes except login,
// but only when cfg.Password is non-empty.
func authMiddleware(cfg config.Config, tokens *tokenStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Password == "" {
			next.ServeHTTP(w, r)
			return
		}
		if tokens.valid(r.Header.Get("X-Auth-Token")) {
			next.ServeHTTP(w, r)
			return
		}
		writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token")
	})
}

func loginHandler(cfg config.Config, tokens *tokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Password == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": ""})
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAuthError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
			return
		}
		if subtle.ConstantTimeCompare([]byte(req.Password), []byte(cfg.Password)) != 1 {
			writeAuthError(w, http.StatusForbidden, "FORBIDDEN", "wrong password")
			return
		}
		tok, err := tokens.issue()
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "INTERNAL", "failed to issue token")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": tok})
	}
}

// writeShares responds with {"shares":[...], "legacy":bool} (SPEC-M7 §3).
func writeShares(w http.ResponseWriter, shares []settings.Share) {
	if shares == nil {
		shares = []settings.Share{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"shares": shares,
		"legacy": len(shares) == 0,
	})
}

// browseHandler implements GET /api/system/browse?path=<abs> (SPEC-M7 §3):
// lists only the sub-directories of an absolute path for the settings
// directory picker. Dot-directories are hidden. path omitted → "/"
// (Windows: list of existing drive roots).
func browseHandler() http.HandlerFunc {
	type entry struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" && runtime.GOOS == "windows" {
			var drives []entry
			for c := 'A'; c <= 'Z'; c++ {
				d := string(c) + `:\`
				if info, err := os.Stat(d); err == nil && info.IsDir() {
					drives = append(drives, entry{Name: string(c) + ":", Path: d})
				}
			}
			if drives == nil {
				drives = []entry{}
			}
			writeBrowseJSON(w, "", drives)
			return
		}
		if p == "" {
			// Android 上从 Linux 根开始浏览对用户无意义（系统目录无权限且
			// 难以找到共享存储）；默认直接落到共享存储根，用户仍可逐级
			// 返回上级浏览其他位置（如外置 SD 卡 /storage/<uuid>）。
			p = "/"
			if info, err := os.Stat("/storage/emulated/0"); err == nil && info.IsDir() {
				p = "/storage/emulated/0"
			}
		}
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			writeAuthError(w, http.StatusBadRequest, "BAD_REQUEST", "not a readable directory: "+p)
			return
		}
		des, err := os.ReadDir(p)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, "BAD_REQUEST", "not a readable directory: "+p)
			return
		}
		out := make([]entry, 0, len(des))
		for _, de := range des {
			if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
				continue
			}
			out = append(out, entry{Name: de.Name(), Path: filepath.Join(p, de.Name())})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeBrowseJSON(w, p, out)
	}
}

func writeBrowseJSON(w http.ResponseWriter, path string, dirs any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"path": path, "dirs": dirs})
}

// NewRouter builds the chi router (exported for tests).
func NewRouter(cfg config.Config, svc *files.Service) http.Handler {
	r, _ := newRouter(cfg, svc)
	return r
}

// newRouter builds the router and returns a warmup function that eagerly
// opens the media index and starts the background scan (used by Start).
// Media routes open the index lazily on first use so that routers built in
// tests (or for pure file serving) never touch the storage root.
func newRouter(cfg config.Config, svc *files.Service) (http.Handler, func()) {
	h := files.NewHandler(svc)
	h.SetServerName(cfg.Name)
	tokens := newTokenStore()

	// M7: load configured shares; failure disables shared mode (legacy
	// single-root) but never breaks file management.
	settingsStore, settingsErr := settings.Load(cfg.Root)
	if settingsErr != nil {
		log.Printf("settings: %v (falling back to legacy mode)", settingsErr)
		settingsStore = settings.New(cfg.Root)
	}
	svc.SetShares(settingsStore.Shares())

	// Media wiring for shared mode: scan roots and virtual-path resolution
	// follow the live share list of svc.
	rootsFn := func() []media.ScanRoot {
		shares := svc.Shares()
		if len(shares) == 0 {
			return []media.ScanRoot{{Prefix: "", Dir: svc.Root()}}
		}
		out := make([]media.ScanRoot, 0, len(shares))
		for _, sh := range shares {
			out = append(out, media.ScanRoot{Prefix: "/" + sh.Name, Dir: sh.Path})
		}
		return out
	}
	resolveFn := func(rel string) (string, error) { return svc.Resolve(rel) }

	// Lazy media handler: opened at most once, on first media request or
	// warmup. Failure disables media endpoints with 500 but never breaks
	// file management.
	var (
		mediaOnce sync.Once
		mediaH    *media.Handler
		liveH     *livephoto.Handler
		videoH    *transcode.Handler
		mediaErr  error

		facesOnce sync.Once
		facesSvc  *faces.Service
		facesErr  error
		getFaces  func() (*faces.Service, error)
	)
	getMedia := func() (*media.Handler, error) {
		mediaOnce.Do(func() {
			mediaH, mediaErr = media.NewHandler(svc.Root())
			if mediaErr == nil {
				mediaH.SetShares(rootsFn, resolveFn)
				liveH, mediaErr = livephoto.NewHandler(svc.Root(), mediaH.LiveLookup)
			}
			if mediaErr == nil {
				liveH.SetResolver(resolveFn)
				videoH, mediaErr = transcode.NewHandler(svc.Root())
			}
			if mediaErr == nil {
				videoH.SetResolver(resolveFn)
			}
			if mediaErr == nil {
				// M11: face scan follows each completed gallery scan.
				mediaH.Scanner().SetOnDone(func() {
					if fs, err := getFaces(); err == nil {
						fs.NotifyScanDone()
					}
				})
				mediaH.StartBackgroundScan()
			}
		})
		return mediaH, mediaErr
	}
	// getFaces lazily builds the faces service on top of the media index.
	// Native-library/model failures degrade to available=false, never fatal.
	getFaces = func() (*faces.Service, error) {
		facesOnce.Do(func() {
			if _, mediaErr := getMedia(); mediaErr != nil {
				facesErr = mediaErr
				return
			}
			src := &facesSource{mh: mediaH, svc: svc}
			facesSvc, facesErr = faces.NewService(svc.Root(), src,
				func() faces.Config {
					f := settingsStore.Faces()
					return faces.Config{Profile: f.Profile, DetModel: f.DetModel, RecModel: f.RecModel, LibPath: f.LibPath}
				},
				func(c faces.Config) error {
					return settingsStore.SetFaces(settings.Faces{Profile: c.Profile, DetModel: c.DetModel, RecModel: c.RecModel, LibPath: c.LibPath})
				},
				cfg.OnnxLibPath)
		})
		return facesSvc, facesErr
	}
	withMedia := func(fn func(*media.Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mh, err := getMedia()
			if err != nil {
				log.Printf("media: init failed: %v", err)
				writeAuthError(w, http.StatusInternalServerError, "INTERNAL", "media index unavailable")
				return
			}
			fn(mh, w, r)
		}
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Post("/auth/login", loginHandler(cfg, tokens))
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return authMiddleware(cfg, tokens, next)
			})
			r.Get("/files", h.ListFiles)
			r.Post("/files/rename", h.Rename)
			r.Post("/files/move", h.Move)
			r.Post("/files/mkdir", h.Mkdir)
			r.Delete("/files", h.Delete)
			r.Post("/upload", h.Upload)
			r.Get("/download/*", h.Download)
			r.Get("/system/info", h.SystemInfo)
			r.Get("/system/browse", browseHandler())

			// M7: multi-share settings.
			r.Get("/settings/shares", func(w http.ResponseWriter, r *http.Request) {
				writeShares(w, settingsStore.Shares())
			})
			r.Put("/settings/shares", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Shares []settings.Share `json:"shares"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					writeAuthError(w, http.StatusBadRequest, "invalid_share", "invalid request body")
					return
				}
				if err := settingsStore.SetShares(req.Shares); err != nil {
					writeAuthError(w, http.StatusBadRequest, "invalid_share", err.Error())
					return
				}
				svc.SetShares(settingsStore.Shares())
				// Async full rescan so the media index reflects the new
				// roots (and drops rows of removed shares).
				if mh, err := getMedia(); err == nil {
					go func() {
						if err := mh.Scanner().Full(context.Background(), nil); err != nil {
							log.Printf("media: rescan after shares update: %v", err)
						}
					}()
				}
				writeShares(w, settingsStore.Shares())
			})

			// M2 media routes (same auth middleware as the rest of /api).
			r.Get("/gallery", withMedia(func(mh *media.Handler, w http.ResponseWriter, r *http.Request) { mh.Gallery(w, r) }))
			r.Get("/gallery/scan", withMedia(func(mh *media.Handler, w http.ResponseWriter, r *http.Request) { mh.GalleryScan(w, r) }))
			r.Get("/thumb/*", withMedia(func(mh *media.Handler, w http.ResponseWriter, r *http.Request) { mh.Thumb(w, r) }))
			r.Get("/media/file/*", withMedia(func(mh *media.Handler, w http.ResponseWriter, r *http.Request) { mh.MediaFile(w, r) }))

			// M3: Live Photo video extraction.
			r.Get("/livephoto/*", withMedia(func(_ *media.Handler, w http.ResponseWriter, r *http.Request) { liveH.ServeHTTP(w, r) }))

			// M4: multi-resolution video streaming / transcoding.
			r.Get("/video/status/*", withMedia(func(_ *media.Handler, w http.ResponseWriter, r *http.Request) { videoH.Status(w, r) }))
			r.Get("/video/*", withMedia(func(_ *media.Handler, w http.ResponseWriter, r *http.Request) { videoH.Video(w, r) }))

			// M11: face recognition. The service degrades gracefully when
			// onnxruntime/models are missing (status stays reachable).
			withFaces := func(fn func(*faces.Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					fs, err := getFaces()
					if err != nil {
						log.Printf("faces: init failed: %v", err)
						writeAuthError(w, http.StatusServiceUnavailable, "faces_unavailable", "faces service unavailable")
						return
					}
					fn(faces.NewHandler(fs), w, r)
				}
			}
			r.Get("/faces/status", withFaces((*faces.Handler).Status))
			r.Post("/faces/models/download", withFaces((*faces.Handler).DownloadModels))
			r.Put("/faces/models", withFaces((*faces.Handler).SetModels))
			r.Post("/faces/scan", withFaces((*faces.Handler).Scan))
			r.Get("/faces/persons", withFaces((*faces.Handler).Persons))
			r.Get("/faces/persons/{id}/photos", withFaces((*faces.Handler).PersonPhotos))
			r.Put("/faces/persons/{id}", withFaces((*faces.Handler).RenamePerson))
			r.Post("/faces/persons/merge", withFaces((*faces.Handler).MergePersons))
			r.Get("/faces/crop/{faceId}", withFaces((*faces.Handler).Crop))
			r.Get("/faces/export", withFaces((*faces.Handler).Export))
			r.Post("/faces/import", withFaces((*faces.Handler).Import))
		})
	})

	// Embedded static frontend (same origin, no CORS).
	static, err := fs.Sub(web.FS, "static")
	if err != nil {
		panic(err)
	}
	// /static/* alias so absolute references like /static/placeholder.svg work.
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	r.Handle("/*", http.FileServer(http.FS(static)))
	warmup := func() {
		if _, err := getMedia(); err != nil {
			log.Printf("media index disabled: %v", err)
		}
	}
	return r, warmup
}

// facesSource adapts the media handler + files service to faces.Source.
type facesSource struct {
	mh  *media.Handler
	svc *files.Service
}

func (f *facesSource) Images() ([]media.Media, error) { return f.mh.Store().ImageMedias() }
func (f *facesSource) MediaByPath(path string) (*media.Media, error) {
	return f.mh.Store().Get(path)
}
func (f *facesSource) Resolve(rel string) (string, error) { return f.svc.Resolve(rel) }

// startServer binds the listener (port+1 retries) and serves in a
// goroutine, returning the actual address, the server and an error channel
// that receives the Serve return value.
func startServer(cfg config.Config) (addr string, srv *http.Server, errCh chan error, err error) {
	svc := files.New(cfg.Root)
	handler, warmMedia := newRouter(cfg, svc)
	warmMedia() // open the index and kick off the background incremental scan

	var listener net.Listener
	port := cfg.Port
	for attempt := 0; attempt <= maxPortRetries; attempt++ {
		l, lerr := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Addr, port))
		if lerr == nil {
			listener = l
			break
		}
		if attempt == maxPortRetries {
			return "", nil, nil, fmt.Errorf("no free port in range %d-%d: %w", cfg.Port, port, lerr)
		}
		port++
	}

	srv = &http.Server{Handler: handler}
	// LAN discovery replies with the actual bound port (SPEC-M8 §1). Failure
	// only disables discovery; shutdown of srv closes the listener.
	if ta, ok := listener.Addr().(*net.TCPAddr); ok {
		if d := startDiscovery(cfg.Name, ta.Port); d != nil {
			srv.RegisterOnShutdown(d.Close)
		}
	}
	errCh = make(chan error, 1)
	go func() {
		errCh <- srv.Serve(listener)
	}()
	return "http://" + listener.Addr().String(), srv, errCh, nil
}

// StartAsync starts the HTTP server without blocking and without installing
// signal handlers (for embedding, e.g. the Android gomobile binding).
// It returns the actual base URL and a stop function for graceful shutdown.
func StartAsync(cfg config.Config) (string, func(), error) {
	addr, srv, errCh, err := startServer(cfg)
	if err != nil {
		return "", nil, err
	}
	go func() { // surface unexpected serve failures in the log
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
		}
	}()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return addr, stop, nil
}

// Start runs the HTTP server until SIGINT/SIGTERM. If cfg.Port is occupied
// it retries with port+1 up to 100 times, then prints the actual address.
func Start(cfg config.Config) error {
	addr, srv, errCh, err := startServer(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("PocketNAS listening on %s (root: %s)\n", addr, cfg.Root)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		log.Println("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
