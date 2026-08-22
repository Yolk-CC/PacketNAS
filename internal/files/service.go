// Package files implements safe filesystem operations confined to a root
// directory, plus the HTTP handlers exposing them per the SPEC API contract.
package files

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"pocket-nas/internal/settings"
)

// ErrForbidden is returned when a resolved path escapes the storage root.
var ErrForbidden = errors.New("path outside root")

// ErrNotFound is returned when a path does not exist.
var ErrNotFound = errors.New("path not found")

// ErrConflict is returned when a create/rename target already exists.
var ErrConflict = errors.New("target already exists")

// ErrBadRequest is returned for invalid client input.
var ErrBadRequest = errors.New("bad request")

// MetaDirName is the internal per-root metadata directory (media index,
// caches). It is hidden from directory listings.
const MetaDirName = ".pocketnas"

// Service performs filesystem operations confined to root (legacy mode)
// or to the configured shares (shared mode, SPEC-M7 §2).
type Service struct {
	root string // resolved (symlink-evaluated) absolute root

	mu     sync.RWMutex
	shares []settings.Share // non-empty → shared mode
}

// SetShares switches the service to shared mode (len(shares)>0) or back to
// legacy mode (empty). Paths are re-normalized defensively.
func (s *Service) SetShares(shares []settings.Share) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(shares) == 0 {
		s.shares = nil
		return
	}
	out := make([]settings.Share, len(shares))
	for i, sh := range shares {
		out[i] = settings.Share{Name: sh.Name, Path: ResolveRoot(sh.Path)}
	}
	s.shares = out
}

// Shares returns a copy of the configured shares (nil in legacy mode), for
// the media scanner and the settings endpoints.
func (s *Service) Shares() []settings.Share {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.shares == nil {
		return nil
	}
	out := make([]settings.Share, len(s.shares))
	copy(out, s.shares)
	return out
}

// New creates a Service for the given root directory. The root is resolved
// with filepath.Abs + EvalSymlinks so resolve() prefix checks are reliable.
func New(root string) *Service {
	return &Service{root: ResolveRoot(root)}
}

// ResolveRoot normalizes a root directory with filepath.Abs +
// filepath.EvalSymlinks so Resolve prefix checks are reliable.
func ResolveRoot(root string) string {
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root
}

// Resolve converts a client-supplied root-relative slash path ("/sub/a.jpg")
// into an absolute filesystem path guaranteed to stay inside root. It is the
// exported form of Service.resolve so other packages (e.g. media) can apply
// the exact same path-safety rules. root should be normalized with
// ResolveRoot first (as New does).
func Resolve(root, rel string) (string, error) {
	return resolve(root, rel)
}

// Root returns the resolved root directory.
func (s *Service) Root() string { return s.root }

// resolve converts a client-supplied root-relative slash path ("/sub/a.jpg")
// into an absolute filesystem path guaranteed to stay inside root.
//
// Rules: filepath.Clean → Join with root → EvalSymlinks → result must equal
// root or be prefixed with root+separator, else ErrForbidden. For paths that
// do not exist yet (upload, mkdir, rename target) EvalSymlinks degrades to
// evaluating the deepest existing ancestor.
func (s *Service) resolve(rel string) (string, error) {
	s.mu.RLock()
	shares := s.shares
	s.mu.RUnlock()
	if len(shares) == 0 {
		return resolve(s.root, rel)
	}
	// Shared mode: the first path segment selects a share by name, the rest
	// is resolved inside that share with the same traversal rules.
	if rel == "" {
		rel = "/"
	}
	for _, seg := range strings.FieldsFunc(filepath.ToSlash(rel), func(r rune) bool { return r == '/' }) {
		if seg == ".." {
			return "", ErrForbidden
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if cleaned == "" {
		// The virtual root has no filesystem counterpart.
		return "", ErrNotFound
	}
	name, rest := cleaned, ""
	if i := strings.Index(cleaned, "/"); i >= 0 {
		name, rest = cleaned[:i], cleaned[i+1:]
	}
	for _, sh := range shares {
		if sh.Name == name {
			return resolve(sh.Path, rest)
		}
	}
	return "", fmt.Errorf("%w: no such share %q", ErrNotFound, name)
}

// Resolve is the exported form of Service.resolve so other packages
// (media, livephoto, transcode) apply the exact same path-safety rules,
// including shared-mode share-name resolution.
func (s *Service) Resolve(rel string) (string, error) {
	return s.resolve(rel)
}

// isVirtualRootAbs reports whether abs is the service root (legacy mode)
// or one of the share root directories (shared mode); such paths cannot be
// renamed/moved/deleted.
func (s *Service) isVirtualRootAbs(abs string) bool {
	if abs == s.root {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sh := range s.shares {
		if abs == sh.Path {
			return true
		}
	}
	return false
}

func resolve(root, rel string) (string, error) {
	if rel == "" {
		rel = "/"
	}
	// Reject any explicit ".." segment before cleaning so traversal attempts
	// yield 403 instead of silently collapsing into the root.
	for _, seg := range strings.FieldsFunc(filepath.ToSlash(rel), func(r rune) bool { return r == '/' }) {
		if seg == ".." {
			return "", ErrForbidden
		}
	}
	// Treat as slash path from the client; Clean and strip leading separators
	// so it can never be absolute.
	cleaned := path.Clean("/" + rel)
	cleaned = strings.TrimPrefix(cleaned, "/")
	joined := filepath.Join(root, filepath.FromSlash(cleaned))

	// Evaluate symlinks on the deepest existing ancestor to support
	// not-yet-existing paths (upload/mkdir/rename targets).
	evalTarget := joined
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(evalTarget)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
				return "", ErrForbidden
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(evalTarget)
		if parent == evalTarget {
			return "", ErrForbidden
		}
		suffix = append(suffix, filepath.Base(evalTarget))
		evalTarget = parent
	}
}

// FileInfo is one entry in a directory listing.
type FileInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"` // root-relative slash path, leading "/"
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	IsDir    bool   `json:"isDir"`
	MimeType string `json:"mimeType"`
}

var imageExts = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".svg": "image/svg+xml", ".heic": "image/heic", ".heif": "image/heif",
	".avif": "image/avif", ".tiff": "image/tiff", ".tif": "image/tiff",
	".ico": "image/x-icon",
}

var videoExts = map[string]string{
	".mp4": "video/mp4", ".mov": "video/quicktime", ".mkv": "video/x-matroska",
	".avi": "video/x-msvideo", ".webm": "video/webm", ".m4v": "video/x-m4v",
	".3gp": "video/3gpp", ".ts": "video/mp2t", ".wmv": "video/x-ms-wmv",
	".flv": "video/x-flv",
}

// MimeType returns the mime type for a name per the SPEC extension table.
func MimeType(name string, isDir bool) string {
	if isDir {
		return "inode/directory"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if m, ok := imageExts[ext]; ok {
		return m
	}
	if m, ok := videoExts[ext]; ok {
		return m
	}
	return "application/octet-stream"
}

func mimeCategory(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	default:
		return ""
	}
}

// relPath converts an absolute path inside root back to a root-relative
// slash path with a leading "/".
func (s *Service) relPath(abs string) string {
	s.mu.RLock()
	shares := s.shares
	s.mu.RUnlock()
	for _, sh := range shares {
		if abs == sh.Path {
			return "/" + sh.Name
		}
		if strings.HasPrefix(abs, sh.Path+string(os.PathSeparator)) {
			rel := strings.TrimPrefix(abs, sh.Path+string(os.PathSeparator))
			return "/" + sh.Name + "/" + filepath.ToSlash(rel)
		}
	}
	if abs == s.root {
		return "/"
	}
	rel := strings.TrimPrefix(abs, s.root+string(os.PathSeparator))
	return "/" + filepath.ToSlash(rel)
}

// List returns the directory entries of rel, directories first, then sorted
// by name. typ filters non-directory entries: "all", "image" or "video".
func (s *Service) List(rel, typ string) ([]FileInfo, error) {
	if (rel == "" || rel == "/") && len(s.Shares()) > 0 {
		return s.listShares(), nil
	}
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		// Never expose the internal metadata directory (media index DB,
		// thumbnail/transcode caches) in listings, at any depth.
		if e.Name() == MetaDirName {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		isDir := e.IsDir()
		mime := MimeType(e.Name(), isDir)
		if !isDir && typ != "" && typ != "all" && mimeCategory(mime) != typ {
			continue
		}
		out = append(out, FileInfo{
			Name:     e.Name(),
			Path:     s.relPath(filepath.Join(abs, e.Name())),
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
			IsDir:    isDir,
			MimeType: mime,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// listShares returns the pseudo-directory entries of the virtual root in
// shared mode: one directory entry per configured share.
func (s *Service) listShares() []FileInfo {
	shares := s.Shares()
	out := make([]FileInfo, 0, len(shares))
	for _, sh := range shares {
		var mod int64
		if info, err := os.Stat(sh.Path); err == nil {
			mod = info.ModTime().Unix()
		}
		out = append(out, FileInfo{
			Name:     sh.Name,
			Path:     "/" + sh.Name,
			Modified: mod,
			IsDir:    true,
			MimeType: "inode/directory",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Open resolves rel and opens it for reading (used by download).
func (s *Service) Open(rel string) (*os.File, os.FileInfo, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// Stat resolves rel and stats it.
func (s *Service) Stat(rel string) (os.FileInfo, string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	return info, abs, nil
}

// SaveUpload streams one uploaded multipart file into dirRel (must be an
// existing directory), overwriting same-name files.
func (s *Service) SaveUpload(dirRel string, fh *multipart.FileHeader) error {
	dirAbs, err := s.resolve(dirRel)
	if err != nil {
		return err
	}
	info, err := os.Stat(dirAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: target is not a directory", ErrBadRequest)
	}
	name := path.Base(filepath.ToSlash(fh.Filename))
	if name == "." || name == "/" || name == "" {
		return fmt.Errorf("%w: invalid file name", ErrBadRequest)
	}
	dstAbs, err := s.resolve(s.relPath(filepath.Join(dirAbs, name)))
	if err != nil {
		return err
	}
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstAbs)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src) // stream to disk, never buffer in memory
	return err
}

// Rename renames rel to newName within the same directory.
func (s *Service) Rename(rel, newName string) error {
	if newName == "" || strings.ContainsAny(newName, `/\`) {
		return fmt.Errorf("%w: invalid new name", ErrBadRequest)
	}
	abs, err := s.resolve(rel)
	if err != nil {
		return err
	}
	if s.isVirtualRootAbs(abs) {
		return fmt.Errorf("%w: cannot rename root", ErrBadRequest)
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	target := filepath.Join(filepath.Dir(abs), newName)
	if _, err := os.Stat(target); err == nil {
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(abs, target)
}

// Move moves each srcRel into destDirRel (must be a directory). Uses
// os.Rename when possible (same volume), falling back to copy+delete.
func (s *Service) Move(srcRels []string, destDirRel string) error {
	if len(srcRels) == 0 {
		return fmt.Errorf("%w: empty srcPaths", ErrBadRequest)
	}
	destAbs, err := s.resolve(destDirRel)
	if err != nil {
		return err
	}
	dinfo, err := os.Stat(destAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if !dinfo.IsDir() {
		return fmt.Errorf("%w: destDir is not a directory", ErrBadRequest)
	}
	for _, srcRel := range srcRels {
		srcAbs, err := s.resolve(srcRel)
		if err != nil {
			return err
		}
		if s.isVirtualRootAbs(srcAbs) {
			return fmt.Errorf("%w: cannot move root", ErrBadRequest)
		}
		srcInfo, err := os.Stat(srcAbs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrNotFound
			}
			return err
		}
		// Reject moving a directory into itself or one of its own
		// subdirectories (would otherwise nest it recursively).
		if srcInfo.IsDir() &&
			(destAbs == srcAbs || strings.HasPrefix(destAbs, srcAbs+string(os.PathSeparator))) {
			return fmt.Errorf("%w: cannot move a directory into itself", ErrBadRequest)
		}
		target := filepath.Join(destAbs, filepath.Base(srcAbs))
		if target == srcAbs {
			continue
		}
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("%w: %s exists in destination", ErrConflict, filepath.Base(srcAbs))
		}
		if err := os.Rename(srcAbs, target); err == nil {
			continue
		}
		if err := copyTree(srcAbs, target); err != nil {
			return err
		}
		if err := os.RemoveAll(srcAbs); err != nil {
			return err
		}
	}
	return nil
}

// Mkdir creates name inside dirRel.
func (s *Service) Mkdir(dirRel, name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: invalid name", ErrBadRequest)
	}
	dirAbs, err := s.resolve(dirRel)
	if err != nil {
		return err
	}
	info, err := os.Stat(dirAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: dir is not a directory", ErrBadRequest)
	}
	target, err := s.resolve(s.relPath(filepath.Join(dirAbs, name)))
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Mkdir(target, 0o755)
}

// Delete removes each rel recursively. Deleting the root itself is rejected.
func (s *Service) Delete(rels []string) error {
	if len(rels) == 0 {
		return fmt.Errorf("%w: empty paths", ErrBadRequest)
	}
	for _, rel := range rels {
		abs, err := s.resolve(rel)
		if err != nil {
			return err
		}
		if s.isVirtualRootAbs(abs) {
			return fmt.Errorf("%w: cannot delete root", ErrBadRequest)
		}
		if _, err := os.Lstat(abs); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrNotFound
			}
			return err
		}
		if err := os.RemoveAll(abs); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies a file or directory tree (cross-volume move fallback).
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode())
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
