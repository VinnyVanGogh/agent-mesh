package security

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideBoundary is returned when a path resolves outside the worktree.
var ErrOutsideBoundary = errors.New("path escapes task worktree")

// Boundary confines file access to a single directory tree.
type Boundary struct {
	root string // absolute, symlink-resolved
}

// NewBoundary creates a Boundary rooted at the given worktree directory.
func NewBoundary(root string) (*Boundary, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree %q: %w", root, err)
	}
	return &Boundary{root: real}, nil
}

// Root returns the resolved worktree root.
func (b *Boundary) Root() string { return b.root }

// Resolve returns the absolute, symlink-resolved form of p (relative paths are
// taken from the root) or ErrOutsideBoundary. Symlinks are resolved on the
// longest existing prefix so a link inside the worktree cannot point out of it,
// and non-existent targets (files about to be created) are still checked.
func (b *Boundary) Resolve(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: NUL in path", ErrOutsideBoundary)
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.root, p)
	}
	p = filepath.Clean(p)

	existing, rest := p, ""
	for {
		real, err := filepath.EvalSymlinks(existing)
		if err == nil {
			existing = real
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("%w: %s", ErrOutsideBoundary, p)
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	full := filepath.Join(existing, rest)
	if !b.contains(full) {
		return "", fmt.Errorf("%w: %s", ErrOutsideBoundary, p)
	}
	return full, nil
}

// Check reports whether p is inside the boundary.
func (b *Boundary) Check(p string) error {
	_, err := b.Resolve(p)
	return err
}

func (b *Boundary) contains(abs string) bool {
	rel, err := filepath.Rel(b.root, abs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
