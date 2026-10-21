package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFollowsSymlinkedParentForMissingLeaf(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := Resolve(filepath.Join(aliasDir, "future", "output.dat"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realDir, "future", "output.dat")
	if got != want {
		t.Fatalf("Resolve()=%q, want %q", got, want)
	}
}

func TestResolveFollowsLeafSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.txt")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := Resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("Resolve()=%q, want %q", got, target)
	}
}

func TestResolveFollowsDanglingLeafSymlink(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.dat")
	want := filepath.Join(realDir, "future.dat")
	if err := os.Symlink(want, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := Resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve()=%q, want dangling symlink target %q", got, want)
	}
}
