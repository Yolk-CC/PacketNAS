package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"pocket-nas/internal/settings"
)

// setupShared creates a service whose root is decoy (should not be exposed)
// with two shares a/ and b/ outside the root.
func setupShared(t *testing.T) (svc *Service, dirA, dirB string) {
	t.Helper()
	root := t.TempDir()
	dirA = t.TempDir()
	dirB = t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dirA, "sub"), 0o755))
	must(os.WriteFile(filepath.Join(dirA, "sub", "a.jpg"), []byte("a"), 0o644))
	must(os.WriteFile(filepath.Join(dirB, "b.txt"), []byte("b"), 0o644))
	svc = New(root)
	svc.SetShares([]settings.Share{
		{Name: "photos", Path: dirA},
		{Name: "docs", Path: dirB},
	})
	return svc, dirA, dirB
}

func TestSharedListVirtualRoot(t *testing.T) {
	svc, dirA, _ := setupShared(t)
	infos, err := svc.List("/", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(infos), infos)
	}
	if infos[0].Name != "docs" || infos[1].Name != "photos" {
		t.Fatalf("names: %+v", infos)
	}
	for _, in := range infos {
		if !in.IsDir || in.MimeType != "inode/directory" || in.Path != "/"+in.Name || in.Size != 0 {
			t.Fatalf("bad pseudo entry: %+v", in)
		}
	}
	wantMod, _ := os.Stat(dirA)
	if infos[1].Modified != wantMod.ModTime().Unix() {
		t.Fatalf("ModTime = %d, want %d", infos[1].Modified, wantMod.ModTime().Unix())
	}
}

func TestSharedResolve(t *testing.T) {
	svc, dirA, dirB := setupShared(t)

	abs, err := svc.Resolve("/photos/sub/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if abs != filepath.Join(dirA, "sub", "a.jpg") {
		t.Fatalf("abs = %q", abs)
	}

	if _, err := svc.Resolve("/nosuch/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown share: %v", err)
	}
	if _, err := svc.Resolve("/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("virtual root: %v", err)
	}
	if _, err := svc.Resolve("/photos/../docs/b.txt"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("dotdot: %v", err)
	}
	if _, err := svc.Resolve("/photos/../../etc/passwd"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("escape: %v", err)
	}
	// Share root itself resolves.
	abs, err = svc.Resolve("/docs")
	if err != nil || abs != dirB {
		t.Fatalf("share root: %q %v", abs, err)
	}
}

func TestSharedListInsideShare(t *testing.T) {
	svc, _, _ := setupShared(t)
	infos, err := svc.List("/photos", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "sub" || infos[0].Path != "/photos/sub" {
		t.Fatalf("%+v", infos)
	}
	infos, err = svc.List("/photos/sub", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Path != "/photos/sub/a.jpg" {
		t.Fatalf("%+v", infos)
	}
}

func TestSharedOps(t *testing.T) {
	svc, dirA, dirB := setupShared(t)
	// Mkdir inside a share.
	if err := svc.Mkdir("/photos", "newdir"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirA, "newdir")); err != nil {
		t.Fatal(err)
	}
	// Rename inside a share.
	if err := svc.Rename("/photos/sub/a.jpg", "renamed.jpg"); err != nil {
		t.Fatal(err)
	}
	// Renaming a share root is rejected.
	if err := svc.Rename("/photos", "x"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("rename share root: %v", err)
	}
	// Deleting a share root is rejected.
	if err := svc.Delete([]string{"/docs"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("delete share root: %v", err)
	}
	// Cross-share move is allowed.
	if err := svc.Move([]string{"/docs/b.txt"}, "/photos/newdir"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirA, "newdir", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("source still exists")
	}
}

func TestLegacyModeUnchangedByNilShares(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(root)
	svc.SetShares(nil)
	infos, err := svc.List("/", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "f.txt" {
		t.Fatalf("%+v", infos)
	}
	abs, err := svc.Resolve("/f.txt")
	if err != nil || abs != filepath.Join(ResolveRoot(root), "f.txt") {
		t.Fatalf("%q %v", abs, err)
	}
}
