package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCRUDAndPaging(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// DB file and thumb dir are created under .pocketnas.
	if _, err := os.Stat(filepath.Join(root, MetaDir, "index.db")); err != nil {
		t.Fatalf("index.db not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, MetaDir, ThumbDir)); err != nil {
		t.Fatalf("thumb dir not created: %v", err)
	}

	seed := []Media{
		{Path: "/b.jpg", Name: "b.jpg", MimeType: "image/jpeg", Size: 10, ModifiedTime: 2, TakenTime: 200, Width: 20, Height: 10, CreatedAt: 2},
		{Path: "/a.jpg", Name: "a.jpg", MimeType: "image/jpeg", Size: 10, ModifiedTime: 1, TakenTime: 300, Width: 20, Height: 10, CreatedAt: 1},
		{Path: "/v.mp4", Name: "v.mp4", MimeType: "video/mp4", Size: 99, ModifiedTime: 3, TakenTime: 100, Width: 640, Height: 360, Duration: 2000, CreatedAt: 3},
	}
	for _, m := range seed {
		if err := st.Upsert(m); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := st.Page(0, 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("total=%d len=%d", total, len(items))
	}
	// taken_time DESC ordering.
	if items[0].Path != "/a.jpg" || items[1].Path != "/b.jpg" || items[2].Path != "/v.mp4" {
		t.Fatalf("bad order: %v %v %v", items[0].Path, items[1].Path, items[2].Path)
	}
	if items[2].Duration != 2000 {
		t.Fatalf("duration not persisted: %+v", items[2])
	}

	// type filters.
	imgs, imgTotal, err := st.Page(0, 10, "image")
	if err != nil || imgTotal != 2 || len(imgs) != 2 {
		t.Fatalf("image page: total=%d len=%d err=%v", imgTotal, len(imgs), err)
	}
	vids, vidTotal, err := st.Page(0, 10, "video")
	if err != nil || vidTotal != 1 || len(vids) != 1 || vids[0].Path != "/v.mp4" {
		t.Fatalf("video page: %+v total=%d err=%v", vids, vidTotal, err)
	}

	// pagination window.
	page, total, err := st.Page(1, 1, "all")
	if err != nil || total != 3 || len(page) != 1 || page[0].Path != "/b.jpg" {
		t.Fatalf("page(1,1): %+v total=%d err=%v", page, total, err)
	}

	// Upsert replaces by path.
	if err := st.Upsert(Media{Path: "/a.jpg", Name: "a.jpg", MimeType: "image/jpeg", TakenTime: 999, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("/a.jpg")
	if err != nil || got == nil || got.TakenTime != 999 {
		t.Fatalf("after upsert: %+v err=%v", got, err)
	}
	if _, total, _ := st.Page(0, 10, "all"); total != 3 {
		t.Fatalf("upsert duplicated row, total=%d", total)
	}

	// ModifiedTimes for incremental scans.
	mt, err := st.ModifiedTimes()
	if err != nil || len(mt) != 3 || mt["/b.jpg"] != 2 {
		t.Fatalf("ModifiedTimes: %v err=%v", mt, err)
	}

	// SetThumbnail persists.
	if err := st.SetThumbnail("/a.jpg", "abc.jpg"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get("/a.jpg")
	if got.ThumbnailPath != "abc.jpg" {
		t.Fatalf("thumbnail_path=%q", got.ThumbnailPath)
	}

	// DeleteMissing removes stale rows only.
	if err := st.DeleteMissing(map[string]bool{"/a.jpg": true}); err != nil {
		t.Fatal(err)
	}
	_, total, _ = st.Page(0, 10, "all")
	if total != 1 {
		t.Fatalf("after DeleteMissing total=%d", total)
	}
	gone, err := st.Get("/v.mp4")
	if err != nil || gone != nil {
		t.Fatalf("deleted row still present: %+v", gone)
	}
}
