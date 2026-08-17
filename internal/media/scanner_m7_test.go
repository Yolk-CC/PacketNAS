package media

import (
	"context"
	"path/filepath"
	"testing"

	"pocket-nas/internal/files"
)

// TestSharedScanRoots verifies multi-root scanning with virtual path
// prefixes (SPEC-M7 §4), and that rows of removed shares are pruned.
func TestSharedScanRoots(t *testing.T) {
	root := t.TempDir() // DB home only
	shareA := t.TempDir()
	shareB := t.TempDir()
	makeJPEG(t, filepath.Join(shareA, "a.jpg"), 10, 10)
	makeJPEG(t, filepath.Join(shareB, "sub", "b.jpg"), 10, 10)
	// A media file outside any share must never be indexed.
	makeJPEG(t, filepath.Join(root, "outside.jpg"), 10, 10)

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sc := NewScanner(st, files.ResolveRoot(root))

	roots := []ScanRoot{
		{Prefix: "/photos", Dir: files.ResolveRoot(shareA)},
		{Prefix: "/pics", Dir: files.ResolveRoot(shareB)},
	}
	sc.SetRootsFn(func() []ScanRoot { return roots }, nil)

	if err := sc.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	items, total, err := st.Page(0, 100, "all")
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total=%d items=%v", total, items)
	}
	got := map[string]bool{}
	for _, m := range items {
		got[m.Path] = true
	}
	if !got["/photos/a.jpg"] || !got["/pics/sub/b.jpg"] {
		t.Fatalf("paths: %v", got)
	}

	// Drop share B and rescan: its rows disappear.
	roots = roots[:1]
	if err := sc.Full(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	_, total, err = st.Page(0, 100, "all")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("after removing share: total=%d", total)
	}
}
