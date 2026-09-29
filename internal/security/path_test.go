package security

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTree(t *testing.T) (*Boundary, string) {
	t.Helper()
	base := t.TempDir()
	wt := filepath.Join(base, "wt")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(wt, "sub"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b, err := NewBoundary(wt)
	if err != nil {
		t.Fatal(err)
	}
	return b, outside
}

func TestBoundaryAllowsInside(t *testing.T) {
	b, _ := newTree(t)
	for _, p := range []string{".", "sub", "sub/new.txt", "a/b/c/not-yet", filepath.Join(b.Root(), "sub"), "sub/../sub/x"} {
		if err := b.Check(p); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
}

func TestBoundaryBlocksTraversal(t *testing.T) {
	b, outside := newTree(t)
	for _, p := range []string{"..", "../outside", "sub/../../outside/x", outside, filepath.Join(outside, "f"), "/etc/passwd", "sub/\x00x", "../wt-evil"} {
		if err := b.Check(p); !errors.Is(err, ErrOutsideBoundary) {
			t.Errorf("%q not blocked: %v", p, err)
		}
	}
}

func TestBoundaryBlocksSymlinkEscape(t *testing.T) {
	b, outside := newTree(t)
	link := filepath.Join(b.Root(), "sub", "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	for _, p := range []string{"sub/escape", "sub/escape/secret.txt", "sub/escape/new/deep"} {
		if err := b.Check(p); !errors.Is(err, ErrOutsideBoundary) {
			t.Errorf("%q not blocked: %v", p, err)
		}
	}
}

func TestBoundaryFollowsInternalSymlink(t *testing.T) {
	b, _ := newTree(t)
	if err := os.Symlink(filepath.Join(b.Root(), "sub"), filepath.Join(b.Root(), "alias")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := b.Check("alias/file"); err != nil {
		t.Fatal(err)
	}
}
