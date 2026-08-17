package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	st, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := st.Shares(); got != nil {
		t.Fatalf("expected nil shares, got %v", got)
	}
}

func TestSetSharesValidation(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		shares []Share
		want   string // substring of error
	}{
		{"empty name", []Share{{Name: "", Path: dir}}, "empty"},
		{"duplicate", []Share{{Name: "a", Path: dir}, {Name: "a", Path: dir}}, "duplicate"},
		{"slash", []Share{{Name: "a/b", Path: dir}}, "slash"},
		{"backslash", []Share{{Name: `a\b`, Path: dir}}, "slash"},
		{"dot", []Share{{Name: ".", Path: dir}}, "invalid"},
		{"dotdot", []Share{{Name: "..", Path: dir}}, "invalid"},
		{"metadir", []Share{{Name: ".pocketnas", Path: dir}}, "invalid"},
		{"missing path", []Share{{Name: "a", Path: filepath.Join(dir, "nope")}}, ""},
		{"not a dir", []Share{{Name: "a", Path: file}}, "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			err = st.SetShares(tc.shares)
			if err == nil {
				t.Fatalf("expected error for %v", tc.shares)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q missing %q", err, tc.want)
			}
		})
	}
}

func TestSetSharesPersistsAndReloads(t *testing.T) {
	root := t.TempDir()
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	st, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	in := []Share{{Name: "照片", Path: dir1}, {Name: "movies", Path: dir2}}
	if err := st.SetShares(in); err != nil {
		t.Fatalf("SetShares: %v", err)
	}
	// File exists with 0600 perms.
	fi, err := os.Stat(filepath.Join(root, ".pocketnas", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", fi.Mode().Perm())
	}
	// Reload from disk.
	st2, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	got := st2.Shares()
	if len(got) != 2 || got[0].Name != "照片" || got[1].Name != "movies" {
		t.Fatalf("reloaded shares = %+v", got)
	}
	if got[0].Path != ResolveRoot(dir1) {
		t.Fatalf("path not normalized: %q", got[0].Path)
	}
	// Returned slice is a copy.
	got[0].Name = "mutated"
	if st2.Shares()[0].Name != "照片" {
		t.Fatal("Shares() did not return a copy")
	}
	// Empty array → legacy mode, persisted.
	if err := st2.SetShares(nil); err != nil {
		t.Fatal(err)
	}
	st3, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if st3.Shares() != nil {
		t.Fatalf("expected nil shares after clearing, got %v", st3.Shares())
	}
}

func TestLoadCorrupt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pocketnas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".pocketnas", "settings.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("expected error for corrupt settings.json")
	}
}
