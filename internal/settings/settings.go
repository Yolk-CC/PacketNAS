// Package settings persists user-configured multi-share settings in
// <root>/.pocketnas/settings.json (SPEC-M7 §1). With no shares configured
// the server stays in legacy single-root mode.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// MetaDirName duplicates files.MetaDirName to avoid an import cycle
// (files imports settings for Service.SetShares).
const MetaDirName = ".pocketnas"

// Share is one user-configured shared directory.
type Share struct {
	Name string `json:"name"`
	Path string `json:"path"` // normalized absolute path (Abs + EvalSymlinks)
}

// Faces holds the M11 face-recognition settings.
type Faces struct {
	Profile  string `json:"profile,omitempty"`  // builtin profile (buffalo_l/buffalo_s/mobilefacenet)
	DetModel string `json:"detModel,omitempty"` // .onnx file name override
	RecModel string `json:"recModel,omitempty"`
	LibPath  string `json:"libPath,omitempty"` // onnxruntime shared library path override
}

// Store holds the configured shares and persists them atomically.
type Store struct {
	mu     sync.RWMutex
	file   string
	shares []Share
	faces  Faces
}

type fileFormat struct {
	Shares []Share `json:"shares"`
	Faces  Faces   `json:"faces,omitempty"`
}

// New returns an empty in-memory Store backed by <root>/.pocketnas/
// settings.json without reading it (legacy mode until SetShares).
func New(root string) *Store {
	return &Store{file: filepath.Join(root, MetaDirName, "settings.json")}
}

// Load reads <root>/.pocketnas/settings.json. A missing file yields an
// empty Store (shares=nil → legacy mode); a corrupt file yields an error.
func Load(root string) (*Store, error) {
	s := &Store{file: filepath.Join(root, MetaDirName, "settings.json")}
	data, err := os.ReadFile(s.file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.file, err)
	}
	s.shares = f.Shares
	s.faces = f.Faces
	return s, nil
}

// Shares returns a copy of the configured shares (nil in legacy mode).
func (s *Store) Shares() []Share {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.shares == nil {
		return nil
	}
	out := make([]Share, len(s.shares))
	copy(out, s.shares)
	return out
}

// SetShares validates and atomically persists the share list. An empty
// list switches back to legacy mode (whole root exposed).
func (s *Store) SetShares(shares []Share) error {
	norm, err := validate(shares)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(fileFormat{Shares: norm, Faces: s.currentFaces()}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.file); err != nil {
		os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	s.shares = norm
	s.mu.Unlock()
	return nil
}

func (s *Store) currentFaces() Faces {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.faces
}

// Faces returns the face-recognition settings.
func (s *Store) Faces() Faces {
	return s.currentFaces()
}

// SetFaces validates and persists the faces settings (keeping shares).
func (s *Store) SetFaces(f Faces) error {
	data, err := json.MarshalIndent(fileFormat{Shares: s.Shares(), Faces: f}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.file); err != nil {
		os.Remove(tmp)
		return err
	}
	s.mu.Lock()
	s.faces = f
	s.mu.Unlock()
	return nil
}

// validate checks names and normalizes paths, returning the normalized
// list (nil when shares is empty).
func validate(shares []Share) ([]Share, error) {
	if len(shares) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(shares))
	out := make([]Share, 0, len(shares))
	for _, sh := range shares {
		name := sh.Name
		switch {
		case name == "":
			return nil, errors.New("share name must not be empty")
		case name == "." || name == ".." || name == MetaDirName:
			return nil, fmt.Errorf("invalid share name %q", name)
		case hasAny(name, `/\`):
			return nil, fmt.Errorf("share name %q must not contain slashes", name)
		case seen[name]:
			return nil, fmt.Errorf("duplicate share name %q", name)
		}
		seen[name] = true
		p := ResolveRoot(sh.Path)
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("share %q: %v", name, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("share %q: %s is not a directory", name, p)
		}
		out = append(out, Share{Name: name, Path: p})
	}
	return out, nil
}

func hasAny(s string, chars string) bool {
	for _, c := range s {
		for _, w := range chars {
			if c == w {
				return true
			}
		}
	}
	return false
}

// ResolveRoot normalizes a path with filepath.Abs + filepath.EvalSymlinks
// (duplicated from files.ResolveRoot to avoid an import cycle).
func ResolveRoot(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root
}
