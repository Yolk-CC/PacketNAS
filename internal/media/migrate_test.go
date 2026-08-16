package media

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateFromM2Database opens a database created with the M2 schema
// (no live-photo columns) and verifies Open migrates it in place while
// preserving existing rows.
func TestMigrateFromM2Database(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, MetaDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Exact M2 schema.
	if _, err := db.Exec(`
CREATE TABLE media_index (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE,
    name TEXT, mime_type TEXT, size INTEGER, modified_time INTEGER,
    taken_time INTEGER, width INTEGER, height INTEGER,
    duration INTEGER, thumbnail_path TEXT, created_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_taken ON media_index(taken_time DESC);
INSERT INTO media_index (path,name,mime_type,size,modified_time,taken_time,width,height,duration,thumbnail_path,created_at)
VALUES ('/old.jpg','old.jpg','image/jpeg',10,1,100,800,600,0,'',1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(root)
	if err != nil {
		t.Fatalf("Open on M2 db: %v", err)
	}
	defer st.Close()

	m, err := st.Get("/old.jpg")
	if err != nil || m == nil {
		t.Fatalf("old row lost: %v", err)
	}
	if m.Width != 800 || m.TakenTime != 100 || m.IsLivePhoto || m.LiveType != "" {
		t.Fatalf("migrated row: %+v", m)
	}
	// New columns work after migration.
	if err := st.SetLiveInfo("/old.jpg", true, "pixel", "", 100, 200); err != nil {
		t.Fatal(err)
	}
	m, _ = st.Get("/old.jpg")
	if !m.IsLivePhoto || m.LiveType != "pixel" || m.VideoOffset != 100 || m.VideoLength != 200 {
		t.Fatalf("after SetLiveInfo: %+v", m)
	}
	// Upsert with live fields round-trips.
	if err := st.Upsert(Media{Path: "/new.jpg", Name: "new.jpg", MimeType: "image/jpeg",
		IsLivePhoto: true, LiveType: "ios", CompanionPath: "/new.mov", VideoLength: 42}); err != nil {
		t.Fatal(err)
	}
	m, _ = st.Get("/new.jpg")
	if m == nil || !m.IsLivePhoto || m.LiveType != "ios" || m.CompanionPath != "/new.mov" || m.VideoLength != 42 {
		t.Fatalf("upsert round-trip: %+v", m)
	}
}
