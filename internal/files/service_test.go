package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	return New(root), root
}

func TestResolveTraversal(t *testing.T) {
	svc, _ := newTestService(t)
	cases := []string{
		"../etc/passwd",
		"/../../etc/passwd",
		"/sub/../../../etc/passwd",
		"..",
		"/..",
	}
	for _, c := range cases {
		if _, err := svc.resolve(c); !errors.Is(err, ErrForbidden) {
			t.Fatalf("resolve(%q) = %v, want ErrForbidden", c, err)
		}
	}
	// Legitimate paths must pass.
	for _, ok := range []string{"/", "/a.txt", "/sub/dir/file.jpg"} {
		if _, err := svc.resolve(ok); err != nil {
			t.Fatalf("resolve(%q) = %v, want nil", ok, err)
		}
	}
}

func TestResolveSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix")
	}
	svc, root := newTestService(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "evil")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.resolve("/evil/secret.txt"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("resolve symlink escape = %v, want ErrForbidden", err)
	}
}

func TestResolveNonExistentPath(t *testing.T) {
	svc, root := newTestService(t)
	// Upload/mkdir target that does not exist yet must resolve via parent.
	got, err := svc.resolve("/newdir/newfile.txt")
	if err != nil {
		t.Fatalf("resolve non-existent = %v", err)
	}
	want := filepath.Join(root, "newdir", "newfile.txt")
	if got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

func TestMkdirRenameDelete(t *testing.T) {
	svc, root := newTestService(t)

	if err := svc.Mkdir("/", "photos"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Mkdir("/", "photos"); !errors.Is(err, ErrConflict) {
		t.Fatalf("mkdir existing = %v, want ErrConflict", err)
	}

	f := filepath.Join(root, "photos", "a.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rename("/photos/a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "photos", "b.txt")); err != nil {
		t.Fatal(err)
	}
	// Rename onto existing -> conflict.
	if err := os.WriteFile(filepath.Join(root, "photos", "c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rename("/photos/c.txt", "b.txt"); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename onto existing = %v, want ErrConflict", err)
	}

	// Delete root itself is forbidden.
	if err := svc.Delete([]string{"/"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("delete root = %v, want ErrBadRequest", err)
	}
	if err := svc.Delete([]string{"/photos"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "photos")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("photos should be gone")
	}
}

func TestMove(t *testing.T) {
	svc, root := newTestService(t)
	if err := svc.Mkdir("/", "src"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Mkdir("/", "dst"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Move([]string{"/src/x.txt"}, "/dst"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "dst", "x.txt")); err != nil {
		t.Fatal(err)
	}
	// destDir not a directory -> bad request.
	if err := svc.Move([]string{"/dst/x.txt"}, "/dst/x.txt"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("move into file = %v, want ErrBadRequest", err)
	}
}

func TestMoveIntoOwnSubdir(t *testing.T) {
	svc, root := newTestService(t)
	if err := svc.Mkdir("/", "dir"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Mkdir("/dir", "sub"); err != nil {
		t.Fatal(err)
	}
	// Moving a directory into its own subdirectory must be rejected.
	if err := svc.Move([]string{"/dir"}, "/dir/sub"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("move into own subdir = %v, want ErrBadRequest", err)
	}
	// Moving a directory onto itself must be rejected.
	if err := svc.Move([]string{"/dir"}, "/dir"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("move into itself = %v, want ErrBadRequest", err)
	}
	// The tree must be untouched.
	if _, err := os.Stat(filepath.Join(root, "dir", "sub")); err != nil {
		t.Fatal("dir/sub should still exist")
	}
	// Moving a file whose destination dir merely shares a name prefix is fine.
	if err := os.WriteFile(filepath.Join(root, "dir", "f.txt"), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Move([]string{"/dir/f.txt"}, "/dir/sub"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "dir", "sub", "f.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestListSortingAndFilter(t *testing.T) {
	svc, root := newTestService(t)
	must := func(p string, data string) {
		if err := os.WriteFile(filepath.Join(root, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("b.txt", "b")
	must("a.jpg", "a")
	must("c.mp4", "c")
	if err := svc.Mkdir("/", "zdir"); err != nil {
		t.Fatal(err)
	}

	infos, err := svc.List("/", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 4 {
		t.Fatalf("want 4 entries, got %d", len(infos))
	}
	if !infos[0].IsDir || infos[0].Name != "zdir" {
		t.Fatalf("directory should sort first, got %+v", infos[0])
	}
	if infos[1].Name != "a.jpg" || infos[2].Name != "b.txt" || infos[3].Name != "c.mp4" {
		t.Fatalf("wrong name order: %v", infos)
	}
	if infos[1].MimeType != "image/jpeg" {
		t.Fatalf("mime = %q", infos[1].MimeType)
	}

	imgs, err := svc.List("/", "image")
	if err != nil {
		t.Fatal(err)
	}
	// Directory kept for navigation; only image files included.
	names := []string{}
	for _, i := range imgs {
		names = append(names, i.Name)
	}
	if len(names) != 2 || names[0] != "zdir" || names[1] != "a.jpg" {
		t.Fatalf("image filter = %v", names)
	}

	vids, err := svc.List("/", "video")
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 2 || vids[1].Name != "c.mp4" || vids[1].MimeType != "video/mp4" {
		t.Fatalf("video filter = %+v", vids)
	}

	if _, err := svc.List("/nope", "all"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list missing = %v, want ErrNotFound", err)
	}
}

func TestMimeType(t *testing.T) {
	if got := MimeType("x", true); got != "inode/directory" {
		t.Fatal(got)
	}
	if got := MimeType("a.PNG", false); got != "image/png" {
		t.Fatal(got)
	}
	if got := MimeType("a.bin", false); got != "application/octet-stream" {
		t.Fatal(got)
	}
}

func TestListHidesMetaDir(t *testing.T) {
	svc, root := newTestService(t)
	// Internal metadata dir at root and nested: must be hidden everywhere.
	if err := os.MkdirAll(filepath.Join(root, MetaDirName, "thumb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub", MetaDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "inner.txt"), []byte("i"), 0o644); err != nil {
		t.Fatal(err)
	}

	infos, err := svc.List("/", "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range infos {
		if i.Name == MetaDirName {
			t.Fatalf("%s leaked into root listing: %+v", MetaDirName, infos)
		}
	}
	if len(infos) != 2 { // sub/ + visible.txt
		t.Fatalf("root listing = %+v", infos)
	}

	infos, err = svc.List("/sub", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "inner.txt" {
		t.Fatalf("nested %s not hidden: %+v", MetaDirName, infos)
	}
}
