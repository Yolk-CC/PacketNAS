package files

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
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

	_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
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
		defer f.Close()
		_, err = io.Copy(entry, f)
		return err
	})
}
