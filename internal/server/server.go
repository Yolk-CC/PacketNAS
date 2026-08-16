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
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"pocket-nas/internal/config"
	"pocket-nas/internal/files"
	"pocket-nas/internal/livephoto"
	"pocket-nas/internal/media"
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
	tokens := newTokenStore()

	// Lazy media handler: opened at most once, on first media request or
	// warmup. Failure disables media endpoints with 500 but never breaks
	// file management.
	var (
		mediaOnce sync.Once
		mediaH    *media.Handler
		liveH     *livephoto.Handler
		videoH    *transcode.Handler
		mediaErr  error
	)
	getMedia := func() (*media.Handler, error) {
		mediaOnce.Do(func() {
			mediaH, mediaErr = media.NewHandler(svc.Root())
			if mediaErr == nil {
				liveH, mediaErr = livephoto.NewHandler(svc.Root(), mediaH.LiveLookup)
			}
			if mediaErr == nil {
				videoH, mediaErr = transcode.NewHandler(svc.Root())
			}
			if mediaErr == nil {
				mediaH.StartBackgroundScan()
			}
		})
		return mediaH, mediaErr
	}
	withMedia := func(fn func(*media.Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mh, err := getMedia()
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "INTERNAL", "media index unavailable: "+err.Error())
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
