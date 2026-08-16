// Package media implements the M2 media index (SQLite), filesystem scanner,
// thumbnail generation/caching and the gallery HTTP API.
package media

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // driver name "sqlite", pure Go (no CGO)
)

// MetaDir is the per-root metadata directory holding the index DB and the
// thumbnail cache. The scanner always skips it.
const MetaDir = ".pocketnas"

// ThumbDir is the thumbnail cache subdirectory inside MetaDir.
const ThumbDir = "thumb"

// Media is one indexed media row.
type Media struct {
	ID            int64  `json:"-"`
	Path          string `json:"path"` // root-relative slash path, leading "/"
	Name          string `json:"name"`
	MimeType      string `json:"mimeType"`
	Size          int64  `json:"size"`
	ModifiedTime  int64  `json:"modifiedTime"`
	TakenTime     int64  `json:"takenTime"` // EXIF DateTimeOriginal preferred, mtime fallback (seconds)
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Duration      int    `json:"duration"` // video milliseconds, 0 for images
	ThumbnailPath string `json:"-"`        // file name relative to .pocketnas/thumb/
	CreatedAt     int64  `json:"-"`
}

// Store wraps the SQLite index database.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS media_index (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE,
    name TEXT, mime_type TEXT, size INTEGER, modified_time INTEGER,
    taken_time INTEGER,
    width INTEGER, height INTEGER,
    duration INTEGER,
    thumbnail_path TEXT,
    created_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_taken ON media_index(taken_time DESC);
`

// Open opens (creating if needed) the index database at
// <root>/.pocketnas/index.db in WAL mode and ensures the schema exists.
func Open(root string) (*Store, error) {
	dir := filepath.Join(root, MetaDir)
	if err := os.MkdirAll(filepath.Join(dir, ThumbDir), 0o755); err != nil {
		return nil, err
	}
	dsn := filepath.Join(dir, "index.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite is single-writer; keep one connection to avoid SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Upsert inserts or replaces the row for m.Path.
func (s *Store) Upsert(m Media) error {
	_, err := s.db.Exec(`
INSERT INTO media_index (path,name,mime_type,size,modified_time,taken_time,width,height,duration,thumbnail_path,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(path) DO UPDATE SET
  name=excluded.name, mime_type=excluded.mime_type, size=excluded.size,
  modified_time=excluded.modified_time, taken_time=excluded.taken_time,
  width=excluded.width, height=excluded.height, duration=excluded.duration,
  thumbnail_path=excluded.thumbnail_path`,
		m.Path, m.Name, m.MimeType, m.Size, m.ModifiedTime, m.TakenTime,
		m.Width, m.Height, m.Duration, m.ThumbnailPath, m.CreatedAt)
	return err
}

// DeleteMissing removes every indexed row whose path is not in seen.
func (s *Store) DeleteMissing(seen map[string]bool) error {
	rows, err := s.db.Query(`SELECT path FROM media_index`)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if !seen[p] {
			stale = append(stale, p)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range stale {
		if _, err := s.db.Exec(`DELETE FROM media_index WHERE path=?`, p); err != nil {
			return err
		}
	}
	return nil
}

// Page returns up to limit items ordered by taken_time DESC plus the total
// count. typ is "all", "image" or "video".
func (s *Store) Page(offset, limit int, typ string) ([]Media, int, error) {
	where := ""
	switch typ {
	case "image":
		where = `WHERE mime_type LIKE 'image/%'`
	case "video":
		where = `WHERE mime_type LIKE 'video/%'`
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM media_index ` + where).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`
SELECT id,path,name,mime_type,size,modified_time,taken_time,width,height,duration,thumbnail_path,created_at
FROM media_index `+where+` ORDER BY taken_time DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Media{}
	for rows.Next() {
		var m Media
		if err := rows.Scan(&m.ID, &m.Path, &m.Name, &m.MimeType, &m.Size,
			&m.ModifiedTime, &m.TakenTime, &m.Width, &m.Height, &m.Duration,
			&m.ThumbnailPath, &m.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, m)
	}
	return items, total, rows.Err()
}

// Count returns the number of indexed rows.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM media_index`).Scan(&n)
	return n, err
}

// ModifiedTimes returns path -> modified_time for every indexed row; the
// incremental scanner uses it to skip unchanged files.
func (s *Store) ModifiedTimes() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT path, modified_time FROM media_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var p string
		var mt int64
		if err := rows.Scan(&p, &mt); err != nil {
			return nil, err
		}
		out[p] = mt
	}
	return out, rows.Err()
}

// SetThumbnail records the generated thumbnail file name for path.
func (s *Store) SetThumbnail(path, thumb string) error {
	_, err := s.db.Exec(`UPDATE media_index SET thumbnail_path=? WHERE path=?`, thumb, path)
	return err
}

// Get returns the row for path, or nil if not indexed.
func (s *Store) Get(path string) (*Media, error) {
	row := s.db.QueryRow(`
SELECT id,path,name,mime_type,size,modified_time,taken_time,width,height,duration,thumbnail_path,created_at
FROM media_index WHERE path=?`, path)
	var m Media
	if err := row.Scan(&m.ID, &m.Path, &m.Name, &m.MimeType, &m.Size,
		&m.ModifiedTime, &m.TakenTime, &m.Width, &m.Height, &m.Duration,
		&m.ThumbnailPath, &m.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}
