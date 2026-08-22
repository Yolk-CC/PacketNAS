package faces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/disintegration/imaging"

	"pocket-nas/internal/media"
)

// Source adapts the media index to the faces service.
type Source interface {
	// Images lists every indexed image.
	Images() ([]media.Media, error)
	// MediaByPath returns one indexed row by virtual path (nil if missing).
	MediaByPath(path string) (*media.Media, error)
	// Resolve maps a virtual path to an absolute filesystem path.
	Resolve(rel string) (string, error)
}

// Config carries the persisted faces settings (settings.json "faces").
type Config struct {
	Profile  string `json:"profile"`  // builtin profile name
	DetModel string `json:"detModel"` // .onnx file names inside models dir
	RecModel string `json:"recModel"`
	LibPath  string `json:"libPath"` // onnxruntime shared library override
}

// Service owns the faces engine lifecycle, the recognition queue and the
// faces.db store. The zero engine state is "unavailable" with a reason.
type Service struct {
	root      string
	modelsDir string
	src       Source

	// getCfg/setCfg bridge to the settings store (avoids import cycle).
	getCfg func() Config
	setCfg func(Config) error

	// onnxLibPath is an extra library path injected by the host
	// (Android nativeLibraryDir). May be empty.
	onnxLibPath string

	mu      sync.Mutex
	engine  Engine
	closeFn func() // releases the native engine (if any)
	reason  string // why unavailable
	profile Profile

	storeOnce sync.Once
	store     *Store
	storeErr  error

	scanning atomic.Bool
	queued   atomic.Bool // a scan was requested while one was running
	pending  atomic.Int64
	done     atomic.Int64

	dlMu          sync.Mutex
	downloading   bool
	downloadFile  string
	downloadBytes int64
	downloadTotal int64
	downloadErr   string
}

// NewService wires the service over root. It never fails hard on missing
// native deps — the service just stays unavailable. faces.db and the models
// dir are created lazily on first real use so merely scanning a media
// library never touches the disk when face recognition was never set up.
func NewService(root string, src Source, getCfg func() Config, setCfg func(Config) error, onnxLibPath string) (*Service, error) {
	s := &Service{
		root:        root,
		modelsDir:   filepath.Join(root, ".pocketnas", "models"),
		src:         src,
		getCfg:      getCfg,
		setCfg:      setCfg,
		onnxLibPath: onnxLibPath,
		reason:      "engine not initialized",
	}
	s.Reload()
	return s, nil
}

// Store returns the faces store, opening faces.db on first use.
// Returns nil (with a logged error) if the DB cannot be opened.
func (s *Service) Store() *Store {
	s.storeOnce.Do(func() {
		s.store, s.storeErr = OpenStore(s.root)
		if s.storeErr != nil {
			log.Printf("faces: open store: %v", s.storeErr)
		}
	})
	return s.store
}

// ModelsDir is where model files and the runtime library live.
func (s *Service) ModelsDir() string { return s.modelsDir }

// Close shuts the engine and store.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closeFn != nil {
		s.closeFn()
		s.closeFn = nil
	}
	s.engine = nil
	if st := s.Store(); st != nil {
		st.Close()
	}
}

// currentProfile resolves the effective profile: builtin defaults overridden
// by user-selected det/rec file names.
func (s *Service) currentProfile() Profile {
	cfg := s.getCfg()
	prof, ok := BuiltinProfiles[cfg.Profile]
	if !ok {
		prof = BuiltinProfiles[DefaultProfile]
	}
	if cfg.DetModel != "" {
		prof.DetModel = cfg.DetModel
	}
	if cfg.RecModel != "" {
		prof.RecModel = cfg.RecModel
	}
	return prof
}

// libCandidates orders the search for the onnxruntime shared library.
func (s *Service) libCandidates() []string {
	var c []string
	if cfg := s.getCfg(); cfg.LibPath != "" {
		c = append(c, cfg.LibPath)
	}
	if s.onnxLibPath != "" {
		c = append(c, s.onnxLibPath)
	}
	names := []string{"libonnxruntime.so"}
	if runtime.GOOS == "windows" {
		names = []string{"onnxruntime.dll"}
	} else if runtime.GOOS == "darwin" {
		names = []string{"libonnxruntime.dylib"}
	}
	for _, n := range names {
		c = append(c, filepath.Join(s.modelsDir, n))
	}
	if exe, err := os.Executable(); err == nil {
		for _, n := range names {
			c = append(c, filepath.Join(filepath.Dir(exe), n))
		}
	}
	return c
}

// Reload (re)builds the engine from current settings and model files.
// Failures leave the service unavailable with a human-readable reason.
func (s *Service) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closeFn != nil {
		s.closeFn()
		s.closeFn = nil
	}
	s.engine = nil

	prof := s.currentProfile()
	var libPath string
	for _, c := range s.libCandidates() {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			libPath = c
			break
		}
	}
	if libPath == "" {
		s.reason = "onnxruntime library not found (download it from the recognition center: library.html?tab=faces)"
		return
	}
	for _, m := range []string{prof.DetModel, prof.RecModel} {
		if st, err := os.Stat(filepath.Join(s.modelsDir, m)); err != nil || st.IsDir() {
			s.reason = fmt.Sprintf("model file %s not found in %s", m, s.modelsDir)
			return
		}
	}
	eng, err := NewOnnxEngine(libPath, s.modelsDir, prof)
	if err != nil {
		s.reason = err.Error()
		return
	}
	s.engine = eng
	s.closeFn = eng.Close
	s.profile = prof
	s.reason = ""
	// Model identity for export + rebuild detection.
	if st := s.Store(); st != nil {
		prevDims, _ := st.GetMeta("dims")
		prevModel, _ := st.GetMeta("modelRec")
		if prevDims != "" && prevDims != fmt.Sprint(prof.Dims) {
			// Dimension change: old embeddings are meaningless.
			log.Printf("faces: embedding dims changed %s→%d, rebuilding index", prevDims, prof.Dims)
			_ = st.Reset()
		}
		_ = st.SetMeta("dims", fmt.Sprint(prof.Dims))
		_ = st.SetMeta("modelRec", prof.RecModel)
		_ = prevModel
	}
}

// Available reports engine state (engine, reason).
func (s *Service) Available() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine != nil, s.reason
}

// SetEngine injects an engine (tests / future backends).
func (s *Service) SetEngine(e Engine, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine = e
	s.reason = reason
	if e != nil {
		s.profile = BuiltinProfiles[e.ProfileName()]
		if s.profile.Name == "" {
			s.profile = Profile{Name: e.ProfileName(), Dims: e.Dims(), Threshold: 0.5}
		}
	}
}

// Profile returns the active profile (zero when unavailable).
func (s *Service) Profile() Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.profile
}

// NotifyScanDone is the media-scanner hook: enqueue a face scan after every
// completed gallery scan. Non-blocking; coalesces repeat notifications.
func (s *Service) NotifyScanDone() {
	s.TriggerScan()
}

// TriggerScan starts the recognition queue if idle; otherwise marks it to
// re-run after the current pass.
func (s *Service) TriggerScan() {
	if ok, _ := s.Available(); !ok {
		return
	}
	if s.scanning.CompareAndSwap(false, true) {
		go s.scanLoop()
		return
	}
	s.queued.Store(true)
}

func (s *Service) scanLoop() {
	for {
		s.queued.Store(false)
		s.scanOnce()
		if !s.queued.CompareAndSwap(true, false) {
			s.scanning.Store(false)
			return
		}
	}
}

// hashFile computes the content sha256 used as the migration-stable key.
func hashFile(abs string) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// decodeImage mirrors the thumbnail decode path (imaging.Open) so face
// boxes align with the pixels users see.
func decodeImage(abs string) (image.Image, error) {
	return imaging.Open(abs)
}

// scanOnce processes every indexed image that is new or changed.
func (s *Service) scanOnce() {
	if s.Store() == nil {
		return
	}
	images, err := s.src.Images()
	if err != nil {
		log.Printf("faces: list images: %v", err)
		return
	}
	processed, err := s.Store().ProcessedMap()
	if err != nil {
		log.Printf("faces: processed map: %v", err)
		return
	}
	var todo []media.Media
	for _, m := range images {
		if p, ok := processed[m.Path]; ok && p[0] == fmt.Sprint(m.ModifiedTime) {
			continue
		}
		todo = append(todo, m)
	}
	s.pending.Store(int64(len(todo)))
	s.done.Store(0)

	clusters, err := s.Store().Clusters()
	if err != nil {
		clusters = nil
	}
	nextCluster, _ := s.Store().NextClusterID()

	for _, m := range todo {
		func() {
			defer s.done.Add(1)
			defer s.pending.Add(-1)
			abs, err := s.src.Resolve(m.Path)
			if err != nil {
				return
			}
			hash, err := hashFile(abs)
			if err != nil {
				return
			}
			img, err := decodeImage(abs)
			if err != nil {
				_ = s.Store().MarkProcessed(m.Path, m.ModifiedTime, hash)
				_ = s.Store().PutHash(m.Path, m.ModifiedTime, hash)
				return // undecodable: don't retry every scan
			}
			eng, _ := s.Available()
			if !eng {
				return
			}
			s.mu.Lock()
			engine := s.engine
			s.mu.Unlock()
			faces, err := engine.Detect(img)
			if err != nil {
				return // transient: retried next scan
			}
			for _, f := range faces {
				emb, err := engine.Embed(img, f)
				if err != nil {
					continue
				}
				clusterID, _, ok := AssignCluster(emb, clusters, s.Profile().Threshold)
				var personID int64
				if !ok {
					clusterID = nextCluster
					nextCluster++
					personID, err = s.Store().CreatePerson()
					if err != nil {
						continue
					}
					clusters = append(clusters, ClusterInfo{ID: clusterID, PersonID: personID, Centroid: emb, Count: 1})
				} else {
					personID = clusterForPerson(clusters, clusterID)
					// Incremental centroid update.
					for i := range clusters {
						if clusters[i].ID == clusterID {
							c := &clusters[i]
							for j := range c.Centroid {
								c.Centroid[j] = (c.Centroid[j]*float32(c.Count) + emb[j]) / float32(c.Count+1)
							}
							c.Count++
							c.Centroid = l2normalize(c.Centroid)
						}
					}
				}
				faceID, err := s.Store().InsertFace(FaceRow{
					FileHash: hash, Box: f.Box, Embedding: emb,
					PersonID: personID, ClusterID: clusterID,
				})
				if err != nil {
					continue
				}
				if p, _ := s.Store().PersonByID(personID); p != nil && p.CoverFaceID == 0 {
					_ = s.Store().SetPersonCover(personID, faceID)
				}
			}
			_ = s.Store().MarkProcessed(m.Path, m.ModifiedTime, hash)
			_ = s.Store().PutHash(m.Path, m.ModifiedTime, hash)
		}()
	}
}

// QueueProgress returns pending/done/scanning for status.
func (s *Service) QueueProgress() (pending, done int, scanning bool) {
	return int(s.pending.Load()), int(s.done.Load()), s.scanning.Load()
}

// ListModels returns .onnx files present in the models dir.
func (s *Service) ListModels() []string {
	des, err := os.ReadDir(s.modelsDir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, de := range des {
		if !de.IsDir() && strings.HasSuffix(strings.ToLower(de.Name()), ".onnx") {
			out = append(out, de.Name())
		}
	}
	sort.Strings(out)
	return out
}

// HashFiles fills the path→hash cache for images missing from it (used by
// import to link existing media without re-identification). Runs inline;
// call from a goroutine for large libraries.
func (s *Service) HashFiles(ctx context.Context) {
	if s.Store() == nil {
		return
	}
	images, err := s.src.Images()
	if err != nil {
		return
	}
	for _, m := range images {
		if ctx.Err() != nil {
			return
		}
		mt, _, ok, err := s.Store().HashEntry(m.Path)
		if err == nil && ok && mt == m.ModifiedTime {
			continue
		}
		abs, err := s.src.Resolve(m.Path)
		if err != nil {
			continue
		}
		hash, err := hashFile(abs)
		if err != nil {
			continue
		}
		_ = s.Store().PutHash(m.Path, m.ModifiedTime, hash)
	}
}

// hashIndex maps file_hash → virtual path for known files.
func (s *Service) hashIndex() map[string]string {
	st := s.Store()
	if st == nil {
		return map[string]string{}
	}
	m, err := st.HashMap()
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for p, h := range m {
		out[h] = p
	}
	return out
}
