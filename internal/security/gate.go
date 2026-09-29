package security

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrConfirmationRequired is returned for Red commands that were not approved.
var ErrConfirmationRequired = errors.New("red command requires human confirmation")

// Confirmer asks a human whether a Red command may run. It must return true
// only on explicit approval; any error or false denies.
type Confirmer func(ctx context.Context, cmdline string, v Verdict) (bool, error)

// Gate is the single choke point autonomous runs use to launch commands:
// classify, require confirmation for Red, confine to the worktree, and scrub
// the child environment.
type Gate struct {
	Classifier *Classifier
	Worktree   *Boundary
	// Confirm is consulted for Red commands. Nil means Red is always denied.
	Confirm Confirmer
	// EnvExtra names variables a task needs passed through the sanitizer.
	EnvExtra []string
}

// NewGate builds a Gate confined to the worktree at dir.
func NewGate(dir string, confirm Confirmer, envExtra ...string) (*Gate, error) {
	b, err := NewBoundary(dir)
	if err != nil {
		return nil, err
	}
	return &Gate{Classifier: &Classifier{Worktree: b}, Worktree: b, Confirm: confirm, EnvExtra: envExtra}, nil
}

// Authorize classifies cmdline and returns its verdict. For Red it returns
// ErrConfirmationRequired unless Confirm approves.
func (g *Gate) Authorize(ctx context.Context, cmdline string) (Verdict, error) {
	return g.authorize(ctx, cmdline, g.Classifier.Classify(cmdline))
}

func (g *Gate) authorize(ctx context.Context, cmdline string, v Verdict) (Verdict, error) {
	if v.Tier < Red {
		return v, nil
	}
	if g.Confirm == nil {
		return v, fmt.Errorf("%w: %s", ErrConfirmationRequired, strings.Join(v.Reasons, "; "))
	}
	ok, err := g.Confirm(ctx, cmdline, v)
	if err != nil || !ok {
		return v, fmt.Errorf("%w: denied (%s)", ErrConfirmationRequired, strings.Join(v.Reasons, "; "))
	}
	return v, nil
}

// Command authorizes argv and, only if allowed, returns an *exec.Cmd running
// in the worktree with a sanitized environment. No shell is involved.
func (g *Gate) Command(ctx context.Context, argv ...string) (*exec.Cmd, Verdict, error) {
	if len(argv) == 0 {
		return nil, Verdict{}, errors.New("empty command")
	}
	v, err := g.authorize(ctx, strings.Join(argv, " "), g.Classifier.ClassifyArgv(argv))
	if err != nil {
		return nil, v, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = g.Worktree.Root()
	cmd.Env = ChildEnv(g.EnvExtra...)
	return cmd, v, nil
}
