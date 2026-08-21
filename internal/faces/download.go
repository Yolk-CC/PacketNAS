package faces

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Download URLs (SPEC-M11 §1: built-in constants, direct links).
const (
	ortReleaseVersion = "1.17.3"
	ortReleaseBase    = "https://github.com/microsoft/onnxruntime/releases/download/v" + ortReleaseVersion
	insightfaceBase   = "https://github.com/deepinsight/insightface/releases/download/v0.7"
)

// ortArchive describes the platform runtime download: archive URL, the
// member path inside, and the file name it is saved as in the models dir.
type ortArchive struct {
	url    string
	member string // path suffix inside the archive
	saveAs string
	zip    bool
}

func ortDownloadFor(goos, goarch string) (*ortArchive, error) {
	base := ortReleaseBase
	switch {
	case goos == "linux" && goarch == "amd64":
		return &ortArchive{base + "/onnxruntime-linux-x64-1.17.3.tgz", "lib/libonnxruntime.so." + ortReleaseVersion, "libonnxruntime.so", false}, nil
	case goos == "linux" && goarch == "arm64":
		return &ortArchive{base + "/onnxruntime-linux-aarch64-1.17.3.tgz", "lib/libonnxruntime.so." + ortReleaseVersion, "libonnxruntime.so", false}, nil
	case goos == "darwin" && goarch == "arm64":
		return &ortArchive{base + "/onnxruntime-osx-arm64-1.17.3.tgz", "lib/libonnxruntime." + ortReleaseVersion + ".dylib", "libonnxruntime.dylib", false}, nil
	case goos == "darwin" && goarch == "amd64":
		return &ortArchive{base + "/onnxruntime-osx-x86_64-1.17.3.tgz", "lib/libonnxruntime." + ortReleaseVersion + ".dylib", "libonnxruntime.dylib", false}, nil
	case goos == "windows" && goarch == "amd64":
		return &ortArchive{base + "/onnxruntime-win-x64-1.17.3.zip", "lib/onnxruntime.dll", "onnxruntime.dll", true}, nil
	}
	return nil, fmt.Errorf("no onnxruntime download for %s/%s", goos, goarch)
}

// modelPackURL is the zip containing a profile's det+rec models.
func modelPackURL(profile string) (string, []string, error) {
	switch profile {
	case "buffalo_l":
		return insightfaceBase + "/buffalo_l.zip", []string{"det_10g.onnx", "w600k_r50.onnx"}, nil
	case "buffalo_s", "mobilefacenet":
		// buffalo_s ships the SCRFD det_500m detector; mobilefacenet users
		// still need to drop mobilefacenet.onnx manually (not in the zip).
		return insightfaceBase + "/buffalo_s.zip", []string{"det_500m.onnx", "w600k_mbf.onnx"}, nil
	}
	return "", nil, fmt.Errorf("no download pack for profile %q", profile)
}

// DownloadState is exposed in /api/faces/status.
type DownloadState struct {
	Downloading bool   `json:"downloading"`
	File        string `json:"file,omitempty"`
	Bytes       int64  `json:"bytes"`
	Total       int64  `json:"total"`
	Error       string `json:"error,omitempty"`
}

// DownloadState returns the current download progress.
func (s *Service) DownloadState() DownloadState {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	return DownloadState{
		Downloading: s.downloading,
		File:        s.downloadFile,
		Bytes:       s.downloadBytes,
		Total:       s.downloadTotal,
		Error:       s.downloadErr,
	}
}

// StartDownload kicks off background download of the onnxruntime library
// and the current profile's model pack into the models dir, then reloads
// the engine. Returns false if a download is already running.
func (s *Service) StartDownload() bool {
	s.dlMu.Lock()
	if s.downloading {
		s.dlMu.Unlock()
		return false
	}
	if err := os.MkdirAll(s.modelsDir, 0o755); err != nil {
		s.dlMu.Unlock()
		return false
	}
	s.downloading = true
	s.downloadErr = ""
	s.dlMu.Unlock()
	go func() {
		err := s.downloadAll()
		s.dlMu.Lock()
		s.downloading = false
		if err != nil {
			s.downloadErr = err.Error()
		}
		s.dlMu.Unlock()
		if err == nil {
			s.Reload()
		}
	}()
	return true
}

func (s *Service) setDl(file string, total int64) {
	s.dlMu.Lock()
	s.downloadFile = file
	s.downloadTotal = total
	s.downloadBytes = 0
	s.dlMu.Unlock()
}

func (s *Service) addDl(n int64) {
	s.dlMu.Lock()
	s.downloadBytes += n
	s.dlMu.Unlock()
}

func (s *Service) downloadAll() error {
	if arc, err := ortDownloadFor(runtime.GOOS, runtime.GOARCH); err == nil {
		dst := filepath.Join(s.modelsDir, arc.saveAs)
		if _, err := os.Stat(dst); err != nil {
			if err := s.downloadArchiveMember(arc, dst); err != nil {
				return fmt.Errorf("runtime: %w", err)
			}
			_ = os.Chmod(dst, 0o755)
		}
	}
	prof := s.currentProfile()
	url, members, err := modelPackURL(prof.Name)
	if err != nil {
		return err
	}
	missing := []string{}
	for _, m := range members {
		if _, err := os.Stat(filepath.Join(s.modelsDir, m)); err != nil {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		if err := s.downloadZipMembers(url, missing); err != nil {
			return fmt.Errorf("models: %w", err)
		}
	}
	return nil
}

// countingReader reports progress.
type countingReader struct {
	r io.Reader
	f func(int64)
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.f(int64(n))
	}
	return n, err
}

func (s *Service) httpGet(url string) (io.ReadCloser, int64, error) {
	client := &http.Client{Timeout: 30 * time.Minute}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "pocket-nas")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

// downloadArchiveMember streams an archive and extracts one member to dst.
func (s *Service) downloadArchiveMember(arc *ortArchive, dst string) error {
	body, total, err := s.httpGet(arc.url)
	if err != nil {
		return err
	}
	defer body.Close()
	s.setDl(arc.saveAs, total)
	cr := countingReader{body, s.addDl}
	tmp := dst + ".part"
	defer os.Remove(tmp)
	if arc.zip {
		return s.extractFromZipStream(cr, arc.member, tmp, total)
	}
	gz, err := gzip.NewReader(cr)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in archive", arc.member)
		}
		if err != nil {
			return err
		}
		if strings.HasSuffix(hdr.Name, arc.member) {
			return writeStream(tmp, tr)
		}
	}
}

// extractFromZipStream must buffer the whole archive (zip needs a ReaderAt).
func (s *Service) extractFromZipStream(r io.Reader, member, dst string, total int64) error {
	tmp, err := os.CreateTemp("", "ort-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, r); err != nil {
		return err
	}
	zr, err := zip.NewReader(tmp, total)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, member) {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return writeStream(dst, rc)
		}
	}
	return fmt.Errorf("%s not found in zip", member)
}

// downloadZipMembers fetches a model pack zip and extracts wanted members
// into the models dir.
func (s *Service) downloadZipMembers(url string, members []string) error {
	body, total, err := s.httpGet(url)
	if err != nil {
		return err
	}
	defer body.Close()
	s.setDl(filepath.Base(url), total)
	tmp, err := os.CreateTemp("", "models-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, countingReader{body, s.addDl}); err != nil {
		return err
	}
	zr, err := zip.NewReader(tmp, total)
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		for _, m := range members {
			if base != m {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = func() error {
				defer rc.Close()
				return writeStream(filepath.Join(s.modelsDir, m+".part"), rc)
			}()
			if err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(s.modelsDir, m+".part"), filepath.Join(s.modelsDir, m)); err != nil {
				return err
			}
			found[m] = true
		}
	}
	for _, m := range members {
		if !found[m] {
			return fmt.Errorf("%s not found in model pack", m)
		}
	}
	return nil
}

func writeStream(dst string, r io.Reader) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

var errDownloadRunning = errors.New("download already in progress")
