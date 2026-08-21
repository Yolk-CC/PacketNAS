package faces

import (
	"testing"
)

func openTempStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestStoreCRUD(t *testing.T) {
	st := openTempStore(t)
	pid, err := st.CreatePerson()
	if err != nil {
		t.Fatal(err)
	}
	fid, err := st.InsertFace(FaceRow{
		FileHash: "hash1", Box: [4]float32{1, 2, 3, 4},
		Embedding: []float32{0.5, -0.5, 1.0}, PersonID: pid, ClusterID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.FaceByID(fid)
	if err != nil || f == nil {
		t.Fatalf("FaceByID: %v %v", f, err)
	}
	if f.FileHash != "hash1" || f.Box[3] != 4 || len(f.Embedding) != 3 || f.Embedding[2] != 1.0 {
		t.Fatalf("roundtrip mismatch: %+v", f)
	}
	if err := st.SetPersonName(pid, "Alice"); err != nil {
		t.Fatal(err)
	}
	p, _ := st.PersonByID(pid)
	if p.Name != "Alice" {
		t.Fatalf("name not persisted: %+v", p)
	}
	if err := st.SetPersonCover(pid, fid); err != nil {
		t.Fatal(err)
	}
	list, err := st.Persons()
	if err != nil || len(list) != 1 || list[0].FaceCount != 1 || list[0].CoverFaceID != fid {
		t.Fatalf("persons: %+v %v", list, err)
	}

	// processed + hashes bookkeeping
	if err := st.MarkProcessed("/a.jpg", 111, "hash1"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutHash("/a.jpg", 111, "hash1"); err != nil {
		t.Fatal(err)
	}
	pm, _ := st.ProcessedMap()
	if pm["/a.jpg"] != [2]string{"111", "hash1"} {
		t.Fatalf("processed map: %v", pm)
	}
	hm, _ := st.HashMap()
	if hm["/a.jpg"] != "hash1" {
		t.Fatalf("hash map: %v", hm)
	}
	if mt, h, ok, _ := st.HashEntry("/a.jpg"); !ok || mt != 111 || h != "hash1" {
		t.Fatalf("hash entry: %v %v %v", mt, h, ok)
	}
}

func TestStoreMerge(t *testing.T) {
	st := openTempStore(t)
	p1, _ := st.CreatePerson()
	p2, _ := st.CreatePerson()
	st.InsertFace(FaceRow{FileHash: "a", Box: [4]float32{1, 1, 2, 2}, Embedding: []float32{1}, PersonID: p1, ClusterID: 1})
	st.InsertFace(FaceRow{FileHash: "b", Box: [4]float32{1, 1, 2, 2}, Embedding: []float32{1}, PersonID: p2, ClusterID: 2})
	if err := st.MergePersons(p2, p1); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.PersonByID(p2); p != nil {
		t.Fatal("from person not deleted")
	}
	fs, _ := st.FacesForPerson(p1)
	if len(fs) != 2 {
		t.Fatalf("merged faces: %d", len(fs))
	}
}

func TestStoreClusters(t *testing.T) {
	st := openTempStore(t)
	p1, _ := st.CreatePerson()
	st.InsertFace(FaceRow{FileHash: "a", Box: [4]float32{1, 1, 2, 2}, Embedding: []float32{1, 0}, PersonID: p1, ClusterID: 1})
	st.InsertFace(FaceRow{FileHash: "b", Box: [4]float32{1, 1, 2, 2}, Embedding: []float32{1, 0}, PersonID: p1, ClusterID: 1})
	st.InsertFace(FaceRow{FileHash: "c", Box: [4]float32{1, 1, 2, 2}, Embedding: []float32{0, 1}, PersonID: p1, ClusterID: 2})
	cl, err := st.Clusters()
	if err != nil || len(cl) != 2 {
		t.Fatalf("clusters: %+v %v", cl, err)
	}
	if cl[0].Count != 2 || cl[1].Count != 1 {
		t.Fatalf("counts: %+v", cl)
	}
	if s := CosineSimilarity(cl[0].Centroid, []float32{1, 0}); s < 0.999 {
		t.Fatalf("centroid: %v", cl[0].Centroid)
	}
	n, _ := st.NextClusterID()
	if n != 3 {
		t.Fatalf("next cluster: %d", n)
	}
}

func TestExportImportRoundtrip(t *testing.T) {
	src := openTempStore(t)
	p1, _ := src.CreatePerson()
	src.SetPersonName(p1, "Alice")
	src.InsertFace(FaceRow{FileHash: "h1", Box: [4]float32{1, 1, 2, 2}, Embedding: []float32{1, 0, 0}, PersonID: p1, ClusterID: 1})
	src.InsertFace(FaceRow{FileHash: "h2", Box: [4]float32{5, 5, 6, 6}, Embedding: []float32{0.9, 0.1, 0}, PersonID: p1, ClusterID: 1})

	data, err := src.Export("w600k_mbf.onnx", 3)
	if err != nil {
		t.Fatal(err)
	}
	if data.Version != 1 || data.Dims != 3 || len(data.Persons) != 1 || len(data.Faces) != 2 {
		t.Fatalf("export: %+v", data)
	}

	dst := openTempStore(t)
	res, err := dst.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Persons != 1 || res.Faces != 2 || res.Skipped != 0 {
		t.Fatalf("import result: %+v", res)
	}
	// Second import dedupes.
	res2, _ := dst.Import(data)
	if res2.Faces != 0 || res2.Skipped != 2 {
		t.Fatalf("re-import: %+v", res2)
	}
	list, _ := dst.Persons()
	if len(list) != 1 || list[0].Name != "Alice" || list[0].FaceCount != 2 {
		t.Fatalf("imported persons: %+v", list)
	}
	fs, _ := dst.FacesForPerson(list[0].ID)
	if len(fs) != 2 || fs[0].Embedding[0] != 1 {
		t.Fatalf("imported faces: %+v", fs)
	}
}

func TestImportUnsupportedVersion(t *testing.T) {
	st := openTempStore(t)
	if _, err := st.Import(&ExportData{Version: 2}); err == nil {
		t.Fatal("expected version error")
	}
}
