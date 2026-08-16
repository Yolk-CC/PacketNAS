package transcode

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite" // driver "sqlite", pure Go
)

// Job statuses (SPEC-M4 §1.2 state machine).
const (
	StatusNone    = "none"
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Job is one (path, res) transcoding task.
type Job struct {
	Path      string `json:"-"`
	Res       string `json:"-"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"` // 0-100
	Output    string `json:"-"`        // cache file name (not absolute)
	Error     string `json:"error,omitempty"`
	UpdatedAt int64  `json:"-"`
}

// Manager owns the job queue, workers, in-memory state and its
// persistence in .pocketnas/transcode.db.
type Manager struct {
	root     string // resolved storage root
	cacheDir string
	db       *sql.DB
	ffmpeg   string // "" when ffmpeg is unavailable (degraded mode)

	mu   sync.Mutex
	jobs map[string]*Job // key: path+"\x00"+res
	q    chan string     // keys awaiting a worker

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager opens (creating if needed) the transcode DB and cache dir,
// recovers persisted state (running/queued reset to none), evicts the LRU
// cache and starts the worker pool.
func NewManager(root string) (*Manager, error) {
	dir := filepath.Join(root, metaDirName)
	if err := os.MkdirAll(filepath.Join(dir, CacheDirName), 0o755); err != nil {
		return nil, err
	}
	dsn := filepath.Join(dir, "transcode.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS transcode_jobs (
		path TEXT, res TEXT, status TEXT, progress INTEGER,
		output TEXT, error TEXT, updated_at INTEGER,
		PRIMARY KEY (path, res))`); err != nil {
		db.Close()
		return nil, err
	}
	// Crash/restart recovery: interrupted jobs become re-requestable.
	if _, err := db.Exec(`UPDATE transcode_jobs SET status=?, progress=0, updated_at=?
		WHERE status IN (?, ?)`, StatusNone, time.Now().Unix(), StatusQueued, StatusRunning); err != nil {
		db.Close()
		return nil, err
	}

	m := &Manager{
		root:     root,
		cacheDir: filepath.Join(dir, CacheDirName),
		db:       db,
		jobs:     make(map[string]*Job),
		q:        make(chan string, 4096),
	}
	if bin, err := exec.LookPath("ffmpeg"); err == nil {
		m.ffmpeg = bin
	}
	// Load terminal states (done/failed) into memory.
	rows, err := db.Query(`SELECT path,res,status,progress,output,error,updated_at FROM transcode_jobs
		WHERE status IN (?, ?)`, StatusDone, StatusFailed)
	if err != nil {
		db.Close()
		return nil, err
	}
	for rows.Next() {
		j := &Job{}
		if err := rows.Scan(&j.Path, &j.Res, &j.Status, &j.Progress, &j.Output, &j.Error, &j.UpdatedAt); err != nil {
			rows.Close()
			db.Close()
			return nil, err
		}
		m.jobs[jobKey(j.Path, j.Res)] = j
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		db.Close()
		return nil, err
	}

	EvictLRU(m.cacheDir, CacheLimit)

	workers := 1
	if v, err := strconv.Atoi(os.Getenv("POCKETNAS_TRANSCODE_WORKERS")); err == nil && v > 0 {
		workers = v
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	for i := 0; i < workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m, nil
}

// Close stops workers and closes the DB.
func (m *Manager) Close() error {
	m.cancel()
	m.wg.Wait()
	return m.db.Close()
}

// Available reports whether ffmpeg was found (transcoding enabled).
func (m *Manager) Available() bool { return m.ffmpeg != "" }

func jobKey(path, res string) string { return path + "\x00" + res }

// copyJob returns a snapshot safe for callers.
func copyJob(j *Job) *Job { c := *j; return &c }

// Status returns the current state for (rel, res); StatusNone if unknown.
func (m *Manager) Status(rel, res string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[jobKey(rel, res)]; ok {
		return copyJob(j)
	}
	return &Job{Path: rel, Res: res, Status: StatusNone}
}

// Request returns the state for (rel, res), enqueueing a new job when the
// state is none or the cached output vanished. srcMtime keys the cache.
// Deduplication: an existing queued/running/done job is returned as-is.
func (m *Manager) Request(rel, res string, srcMtime int64) *Job {
	if !ValidRes(res) {
		return &Job{Path: rel, Res: res, Status: StatusFailed, Error: "invalid resolution"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := jobKey(rel, res)
	if j, ok := m.jobs[key]; ok {
		if j.Status == StatusDone {
			// Cache file evicted or source changed → re-enqueue.
			if j.Output == CacheKey(rel, srcMtime, res) && fileExists(filepath.Join(m.cacheDir, j.Output)) {
				return copyJob(j)
			}
		} else if j.Status == StatusQueued || j.Status == StatusRunning || j.Status == StatusFailed {
			return copyJob(j)
		}
	}
	if m.ffmpeg == "" {
		j := &Job{Path: rel, Res: res, Status: StatusFailed, Error: "ffmpeg not available", UpdatedAt: time.Now().Unix()}
		m.jobs[key] = j
		m.persist(j)
		return copyJob(j)
	}
	j := &Job{Path: rel, Res: res, Status: StatusQueued, Output: CacheKey(rel, srcMtime, res), UpdatedAt: time.Now().Unix()}
	m.jobs[key] = j
	m.persist(j)
	select {
	case m.q <- key:
	default: // queue full: leave queued; a sweeper would re-enqueue (not needed at this scale)
	}
	return copyJob(j)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// persist upserts the job row (best-effort; queue keeps working without it).
func (m *Manager) persist(j *Job) {
	_, _ = m.db.Exec(`INSERT INTO transcode_jobs (path,res,status,progress,output,error,updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(path,res) DO UPDATE SET status=excluded.status, progress=excluded.progress,
		output=excluded.output, error=excluded.error, updated_at=excluded.updated_at`,
		j.Path, j.Res, j.Status, j.Progress, j.Output, j.Error, j.UpdatedAt)
}

// worker processes queued job keys one at a time.
func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case key := <-m.q:
			m.runJob(key)
		}
	}
}

func (m *Manager) runJob(key string) {
	m.mu.Lock()
	j, ok := m.jobs[key]
	if !ok || j.Status != StatusQueued {
		m.mu.Unlock()
		return
	}
	j.Status = StatusRunning
	j.Progress = 0
	j.UpdatedAt = time.Now().Unix()
	m.persist(j)
	job := *j
	m.mu.Unlock()

	abs := filepath.Join(m.root, filepath.FromSlash(job.Path[1:]))
	out := filepath.Join(m.cacheDir, job.Output)
	tmp := out + ".tmp.mp4" // keep .mp4 suffix: ffmpeg infers the muxer by extension
	defer os.Remove(tmp)

	hasAudio, durMs := probe(m.ctx, abs)
	err := run(m.ctx, m.ffmpeg, abs, tmp, Resolutions[job.Res], hasAudio, durMs,
		func(pct int) {
			m.mu.Lock()
			if cur, ok := m.jobs[key]; ok && cur.Status == StatusRunning {
				cur.Progress = pct
			}
			m.mu.Unlock()
		})

	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.jobs[key]
	if !ok {
		return
	}
	if err != nil {
		cur.Status = StatusFailed
		cur.Error = err.Error()
	} else if err := os.Rename(tmp, out); err != nil {
		cur.Status = StatusFailed
		cur.Error = fmt.Sprintf("rename: %v", err)
	} else {
		cur.Status = StatusDone
		cur.Progress = 100
		cur.Error = ""
	}
	cur.UpdatedAt = time.Now().Unix()
	m.persist(cur)
}

// OutputPath returns the absolute path of a done job's cached output.
func (m *Manager) OutputPath(j *Job) string {
	return filepath.Join(m.cacheDir, j.Output)
}
