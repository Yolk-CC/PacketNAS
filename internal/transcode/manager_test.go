package transcode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitStatus(t *testing.T, m *Manager, rel, res, want string, timeout time.Duration) *Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		j := m.Status(rel, res)
		if j.Status == want {
			return j
		}
		if j.Status == StatusFailed && want != StatusFailed {
			t.Fatalf("job failed: %s", j.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s, now %s", want, m.Status(rel, res).Status)
	return nil
}

func TestManagerLifecycleAndDedup(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "clip.mp4")
	genVideo(t, src, "320x240", 1, true)
	st, _ := os.Stat(src)
	mtime := st.ModTime().Unix()

	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if !m.Available() {
		t.Skip("ffmpeg not available")
	}

	// none → queued on first request.
	j1 := m.Request("/clip.mp4", "360p", mtime)
	if j1.Status != StatusQueued && j1.Status != StatusRunning {
		t.Fatalf("first request: %+v", j1)
	}
	// Dedup: second request returns the same job, no second queue entry.
	j2 := m.Request("/clip.mp4", "360p", mtime)
	if j2.Status == StatusNone {
		t.Fatal("dedup lost the job")
	}

	done := waitStatus(t, m, "/clip.mp4", "360p", StatusDone, 60*time.Second)
	if done.Progress != 100 {
		t.Fatalf("progress=%d", done.Progress)
	}
	out := m.OutputPath(done)
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if w, h := probeDims(t, out); h != 240 {
		t.Fatalf("height=%d w=%d, want 240 (source smaller than 360p tier)", w, h)
	}

	// Done + cache present → returned as done, no re-run.
	j3 := m.Request("/clip.mp4", "360p", mtime)
	if j3.Status != StatusDone {
		t.Fatalf("after done: %+v", j3)
	}

	// Only one output file for the repeated requests.
	entries, _ := os.ReadDir(m.cacheDir)
	count := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".mp4" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("cache has %d outputs, want 1 (dedup)", count)
	}

	// Source mtime change → different cache key → re-transcode.
	j4 := m.Request("/clip.mp4", "360p", mtime+100)
	if j4.Status == StatusDone {
		t.Fatal("mtime change must invalidate the done state")
	}
	waitStatus(t, m, "/clip.mp4", "360p", StatusDone, 60*time.Second)
}

func TestManagerRestartRecovery(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate crashed jobs persisted mid-flight.
	m.db.Exec(`INSERT INTO transcode_jobs (path,res,status,progress,output,error,updated_at)
		VALUES ('/a.mp4','720p','running',55,'x.mp4','',1)`)
	m.db.Exec(`INSERT INTO transcode_jobs (path,res,status,progress,output,error,updated_at)
		VALUES ('/b.mp4','360p','queued',0,'y.mp4','',1)`)
	m.db.Exec(`INSERT INTO transcode_jobs (path,res,status,progress,output,error,updated_at)
		VALUES ('/c.mp4','360p','done',100,'z.mp4','',1)`)
	m.Close()

	m2, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if j := m2.Status("/a.mp4", "720p"); j.Status != StatusNone {
		t.Fatalf("running after restart: %s", j.Status)
	}
	if j := m2.Status("/b.mp4", "360p"); j.Status != StatusNone {
		t.Fatalf("queued after restart: %s", j.Status)
	}
	if j := m2.Status("/c.mp4", "360p"); j.Status != StatusDone {
		t.Fatalf("done after restart: %s", j.Status)
	}
}

func TestCacheKey(t *testing.T) {
	a := CacheKey("/v.mp4", 100, "360p")
	if a == CacheKey("/v.mp4", 100, "720p") {
		t.Fatal("res must affect key")
	}
	if a == CacheKey("/v.mp4", 101, "360p") {
		t.Fatal("mtime must affect key")
	}
	if a == CacheKey("/w.mp4", 100, "360p") {
		t.Fatal("path must affect key")
	}
	if a != CacheKey("/v.mp4", 100, "360p") {
		t.Fatal("key not deterministic")
	}
	if filepath.Ext(a) != ".mp4" {
		t.Fatalf("key=%q", a)
	}
}

func TestEvictLRUTranscode(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, make([]byte, 100), 0o644)
		mt := base.Add(time.Duration(i) * time.Minute)
		os.Chtimes(p, mt, mt)
	}
	EvictLRU(dir, 200) // total 300 → evict to 160: removes a and b
	if _, err := os.Stat(filepath.Join(dir, "a.mp4")); !os.IsNotExist(err) {
		t.Fatal("oldest not evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "c.mp4")); err != nil {
		t.Fatal("newest evicted")
	}
}
