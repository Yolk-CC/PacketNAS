package files

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// StreamZip streams the directory at abs as a ZIP archive directly to w
// (no Content-Length, no in-memory buffering). The download file name is
// "<dirName>.zip".
func (s *Service) StreamZip(w http.ResponseWriter, abs, dirName string) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", dirName+".zip"))
	zw := zip.NewWriter(w)
	defer zw.Close()

	// The response has already started once we stream, so callback errors
	// can only be logged, not turned into a status code.
	if err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("zip: walk %s: %v", p, err)
			return err
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip symlinks inside archives to avoid dangling entries.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(dirName, rel))
		if d.IsDir() {
			hdr.Name += "/"
		} else {
			hdr.Method = zip.Deflate
		}
		entry, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		// Explicit Close right after the copy: deferring here would keep
		// every file descriptor open until the whole archive is done.
		_, copyErr := io.Copy(entry, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		log.Printf("zip: stream %s: %v", abs, err)
	}
}
