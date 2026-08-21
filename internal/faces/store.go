package faces

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite" // driver "sqlite", pure Go (no CGO), same as media
)

// FaceRow is one stored face detection.
type FaceRow struct {
	ID        int64      `json:"id"`
	FileHash  string     `json:"fileHash"`
	Box       [4]float32 `json:"box"`
	Embedding []float32  `json:"embedding,omitempty"`
	PersonID  int64      `json:"personId"`
	ClusterID int64      `json:"clusterId"`
}

// Person is a cluster of faces with an optional user-assigned name.
type Person struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	CoverFaceID int64  `json:"coverFaceId"`
	CreatedAt   int64  `json:"createdAt"`
}

const storeSchema = `
CREATE TABLE IF NOT EXISTS faces (
    id INTEGER PRIMARY KEY,
    file_hash TEXT NOT NULL,
    box_json TEXT NOT NULL,
    embedding BLOB NOT NULL,
    person_id INTEGER NOT NULL DEFAULT 0,
    cluster_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_faces_person ON faces(person_id);
CREATE INDEX IF NOT EXISTS idx_faces_hash ON faces(file_hash);
CREATE TABLE IF NOT EXISTS persons (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    cover_face_id INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);
-- processed images: path+mtime → content hash (scan bookkeeping)
CREATE TABLE IF NOT EXISTS processed (
    path TEXT PRIMARY KEY,
    mtime INTEGER NOT NULL,
    file_hash TEXT NOT NULL
);
-- path → content hash cache, filled by scans and import linking
CREATE TABLE IF NOT EXISTS hashes (
    path TEXT PRIMARY KEY,
    mtime INTEGER NOT NULL,
    file_hash TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT);
`

// Store wraps faces.db.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if needed) <root>/.pocketnas/faces.db.
func OpenStore(root string) (*Store, error) {
	dir := filepath.Join(root, ".pocketnas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dsn := filepath.Join(dir, "faces.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(storeSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("faces schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// encodeEmbedding stores float32s little-endian.
func encodeEmbedding(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func decodeEmbedding(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// InsertFace stores one face row, returning its id.
func (s *Store) InsertFace(f FaceRow) (int64, error) {
	box, _ := json.Marshal(f.Box)
	res, err := s.db.Exec(
		`INSERT INTO faces (file_hash,box_json,embedding,person_id,cluster_id) VALUES (?,?,?,?,?)`,
		f.FileHash, string(box), encodeEmbedding(f.Embedding), f.PersonID, f.ClusterID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FaceByID returns one face row or nil.
func (s *Store) FaceByID(id int64) (*FaceRow, error) {
	row := s.db.QueryRow(`SELECT id,file_hash,box_json,embedding,person_id,cluster_id FROM faces WHERE id=?`, id)
	return scanFace(row)
}

func scanFace(row interface{ Scan(...any) error }) (*FaceRow, error) {
	var f FaceRow
	var box string
	var emb []byte
	if err := row.Scan(&f.ID, &f.FileHash, &box, &emb, &f.PersonID, &f.ClusterID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(box), &f.Box); err != nil {
		return nil, err
	}
	f.Embedding = decodeEmbedding(emb)
	return &f, nil
}

func (s *Store) queryFaces(where string, args ...any) ([]FaceRow, error) {
	rows, err := s.db.Query(`SELECT id,file_hash,box_json,embedding,person_id,cluster_id FROM faces `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FaceRow{}
	for rows.Next() {
		f, err := scanFace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// FacesForPerson returns all faces of a person.
func (s *Store) FacesForPerson(personID int64) ([]FaceRow, error) {
	return s.queryFaces(`WHERE person_id=? ORDER BY id`, personID)
}

// AllFaces returns every face (clustering rebuilds, export).
func (s *Store) AllFaces() ([]FaceRow, error) {
	return s.queryFaces(`ORDER BY id`)
}

// FaceCount returns the number of stored faces.
func (s *Store) FaceCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM faces`).Scan(&n)
	return n, err
}

// CreatePerson inserts an unnamed person, returning its id.
func (s *Store) CreatePerson() (int64, error) {
	res, err := s.db.Exec(`INSERT INTO persons (name,cover_face_id,created_at) VALUES ('',0,?)`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// PersonRow is a person plus its face count.
type PersonRow struct {
	Person
	FaceCount int `json:"faceCount"`
}

// Persons returns all persons with face counts, unnamed first (by size),
// then named alphabetically.
func (s *Store) Persons() ([]PersonRow, error) {
	rows, err := s.db.Query(`
SELECT p.id, p.name, p.cover_face_id, p.created_at, COUNT(f.id)
FROM persons p LEFT JOIN faces f ON f.person_id = p.id
GROUP BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PersonRow{}
	for rows.Next() {
		var p PersonRow
		if err := rows.Scan(&p.ID, &p.Name, &p.CoverFaceID, &p.CreatedAt, &p.FaceCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == "") != (out[j].Name == "") {
			return out[i].Name == "" // unnamed first
		}
		if out[i].Name != "" {
			return out[i].Name < out[j].Name
		}
		return out[i].FaceCount > out[j].FaceCount
	})
	return out, rows.Err()
}

// PersonCount returns the number of persons.
func (s *Store) PersonCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM persons`).Scan(&n)
	return n, err
}

// PersonByID returns one person or nil.
func (s *Store) PersonByID(id int64) (*Person, error) {
	row := s.db.QueryRow(`SELECT id,name,cover_face_id,created_at FROM persons WHERE id=?`, id)
	var p Person
	if err := row.Scan(&p.ID, &p.Name, &p.CoverFaceID, &p.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// SetPersonName renames a person.
func (s *Store) SetPersonName(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE persons SET name=? WHERE id=?`, name, id)
	return err
}

// SetPersonCover updates the cover face.
func (s *Store) SetPersonCover(id, faceID int64) error {
	_, err := s.db.Exec(`UPDATE persons SET cover_face_id=? WHERE id=?`, faceID, id)
	return err
}

// MergePersons moves all faces of fromID to toID and deletes fromID.
func (s *Store) MergePersons(fromID, toID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE faces SET person_id=? WHERE person_id=?`, toID, fromID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM persons WHERE id=?`, fromID); err != nil {
		return err
	}
	return tx.Commit()
}

// ClusterInfo aggregates one cluster for incremental assignment.
type ClusterInfo struct {
	ID       int64
	PersonID int64
	Centroid []float32 // running mean of member embeddings (normalized)
	Count    int
}

// Clusters returns all clusters with centroids computed from member faces.
func (s *Store) Clusters() ([]ClusterInfo, error) {
	faces, err := s.queryFaces(`WHERE cluster_id > 0 ORDER BY cluster_id, id`)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*ClusterInfo{}
	var order []int64
	for _, f := range faces {
		ci, ok := byID[f.ClusterID]
		if !ok {
			ci = &ClusterInfo{ID: f.ClusterID, PersonID: f.PersonID}
			byID[f.ClusterID] = ci
			order = append(order, f.ClusterID)
		}
		ci.Centroid = append(ci.Centroid, 0) // placeholder, fixed below
	}
	// Compute running means per cluster.
	sums := map[int64][]float32{}
	for _, f := range faces {
		sum := sums[f.ClusterID]
		if sum == nil {
			sum = make([]float32, len(f.Embedding))
			sums[f.ClusterID] = sum
		}
		for i, v := range f.Embedding {
			sum[i] += v
		}
		byID[f.ClusterID].Count++
	}
	out := make([]ClusterInfo, 0, len(order))
	for _, id := range order {
		ci := byID[id]
		sum := sums[id]
		for i := range sum {
			sum[i] /= float32(ci.Count)
		}
		ci.Centroid = l2normalize(sum)
		out = append(out, *ci)
	}
	return out, nil
}

// NextClusterID returns max(cluster_id)+1 (1 when empty).
func (s *Store) NextClusterID() (int64, error) {
	var n sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(cluster_id) FROM faces`).Scan(&n); err != nil {
		return 0, err
	}
	return n.Int64 + 1, nil
}

// UpdateFaceCluster sets person/cluster assignment of a face.
func (s *Store) UpdateFaceCluster(faceID, personID, clusterID int64) error {
	_, err := s.db.Exec(`UPDATE faces SET person_id=?, cluster_id=? WHERE id=?`, personID, clusterID, faceID)
	return err
}

// ProcessedMap returns path → (mtime, file_hash) for scan bookkeeping.
func (s *Store) ProcessedMap() (map[string][2]string, error) {
	rows, err := s.db.Query(`SELECT path, mtime, file_hash FROM processed`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]string{}
	for rows.Next() {
		var p, h string
		var mt int64
		if err := rows.Scan(&p, &mt, &h); err != nil {
			return nil, err
		}
		out[p] = [2]string{fmt.Sprint(mt), h}
	}
	return out, rows.Err()
}

// MarkProcessed records that path (mtime) was scanned with content hash.
func (s *Store) MarkProcessed(path string, mtime int64, fileHash string) error {
	_, err := s.db.Exec(
		`INSERT INTO processed (path,mtime,file_hash) VALUES (?,?,?)
		 ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime, file_hash=excluded.file_hash`,
		path, mtime, fileHash)
	return err
}

// PutHash updates the path → content-hash cache.
func (s *Store) PutHash(path string, mtime int64, fileHash string) error {
	_, err := s.db.Exec(
		`INSERT INTO hashes (path,mtime,file_hash) VALUES (?,?,?)
		 ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime, file_hash=excluded.file_hash`,
		path, mtime, fileHash)
	return err
}

// HashMap returns path → file_hash for all known files.
func (s *Store) HashMap() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT path, file_hash FROM hashes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			return nil, err
		}
		out[p] = h
	}
	return out, rows.Err()
}

// HashEntry returns (mtime, hash, ok) for path.
func (s *Store) HashEntry(path string) (int64, string, bool, error) {
	row := s.db.QueryRow(`SELECT mtime, file_hash FROM hashes WHERE path=?`, path)
	var mt int64
	var h string
	if err := row.Scan(&mt, &h); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}
	return mt, h, true, nil
}

// SetMeta/GetMeta store small key/value facts (model identity etc).
func (s *Store) SetMeta(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO meta (k,v) VALUES (?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// GetMeta returns "" when unset.
func (s *Store) GetMeta(k string) (string, error) {
	var v string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k=?`, k).Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// Reset deletes all faces/persons/processed rows (model switch rebuild).
func (s *Store) Reset() error {
	_, err := s.db.Exec(`DELETE FROM faces; DELETE FROM persons; DELETE FROM processed; DELETE FROM meta`)
	return err
}

// ImportResult summarizes an import.
type ImportResult struct {
	Persons int `json:"persons"`
	Faces   int `json:"faces"`
	Skipped int `json:"skipped"` // duplicate faces
}

// ExportData is the migration payload (SPEC-M11 §3).
type ExportData struct {
	Version int       `json:"version"`
	Model   string    `json:"modelRec"`
	Dims    int       `json:"dims"`
	Persons []Person  `json:"persons"`
	Faces   []FaceRow `json:"faces"`
}

// Export dumps persons+faces (with embeddings) for migration.
func (s *Store) Export(model string, dims int) (*ExportData, error) {
	persons := []Person{}
	rows, err := s.db.Query(`SELECT id,name,cover_face_id,created_at FROM persons ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.CoverFaceID, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		persons = append(persons, p)
	}
	rows.Close()
	faces, err := s.AllFaces()
	if err != nil {
		return nil, err
	}
	return &ExportData{Version: 1, Model: model, Dims: dims, Persons: persons, Faces: faces}, nil
}

// Import merges exported data into the store. Persons are matched by name
// (named) or re-created (unnamed); faces dedupe on (file_hash, box).
// No re-identification happens: rows carry their embeddings.
func (s *Store) Import(d *ExportData) (*ImportResult, error) {
	if d.Version != 1 {
		return nil, fmt.Errorf("unsupported export version %d", d.Version)
	}
	res := &ImportResult{}
	idMap := map[int64]int64{}
	for _, p := range d.Persons {
		var newID int64
		if p.Name != "" {
			if existing, err := s.personByName(p.Name); err != nil {
				return nil, err
			} else if existing != nil {
				newID = existing.ID
			}
		}
		if newID == 0 {
			row := s.db.QueryRow(
				`INSERT INTO persons (name,cover_face_id,created_at) VALUES (?,?,?) RETURNING id`,
				p.Name, 0, p.CreatedAt)
			if err := row.Scan(&newID); err != nil {
				return nil, err
			}
			res.Persons++
		}
		idMap[p.ID] = newID
	}
	for _, f := range d.Faces {
		pid := idMap[f.PersonID]
		dup, err := s.faceExists(f.FileHash, f.Box)
		if err != nil {
			return nil, err
		}
		if dup {
			res.Skipped++
			continue
		}
		f.PersonID = pid
		if _, err := s.InsertFace(f); err != nil {
			return nil, err
		}
		res.Faces++
	}
	return res, nil
}

func (s *Store) personByName(name string) (*Person, error) {
	row := s.db.QueryRow(`SELECT id,name,cover_face_id,created_at FROM persons WHERE name=?`, name)
	var p Person
	if err := row.Scan(&p.ID, &p.Name, &p.CoverFaceID, &p.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) faceExists(hash string, box [4]float32) (bool, error) {
	b, _ := json.Marshal(box)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM faces WHERE file_hash=? AND box_json=?`, hash, string(b)).Scan(&n)
	return n > 0, err
}
