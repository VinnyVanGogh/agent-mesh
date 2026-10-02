package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/security"
)

// Diagnostic is returned when the interceptor rejects a completion transition.
type Diagnostic struct {
	// Message is injected as a task comment so the agent can self-correct.
	Message string
	// FailedChecks names the checks that did not pass.
	FailedChecks []string
}

// GuardFunc is a pluggable verification hook. It returns ("", nil) on pass,
// or a human-readable failure reason on rejection.
type GuardFunc func(ctx context.Context, taskID, wtPath, repoRoot string) (failReason string, err error)

// Interceptor is the Mechanical Completion Interceptor.
//
// It intercepts every [[TASK_COMPLETE]] signal and transition to in_review or done,
// runs a bounded suite of verifications (<30 s total), and either approves the
// transition or rejects it with structured diagnostics that are injected back to
// the agent context.
type Interceptor struct {
	DB     *sql.DB
	Guards []GuardFunc
}

// NewInterceptor returns an Interceptor wired with the default verification suite.
func NewInterceptor(db *sql.DB) *Interceptor {
	ic := &Interceptor{DB: db}
	ic.Guards = []GuardFunc{
		ic.checkWorkProducts,
		ic.checkGitSync,
		ic.checkMutexLease,
	}
	return ic
}

// RegisterGuard appends a custom verification hook.
func (ic *Interceptor) RegisterGuard(g GuardFunc) {
	ic.Guards = append(ic.Guards, g)
}

// InterceptCompletion runs all verifications with a 28-second deadline
// (leaving 2 s head-room under the 30-second SLA). It returns (approved, diag, err).
//
// On approval diag is nil. On rejection diag carries a human-readable message
// and a list of failed checks; the caller must inject it back as a task comment
// and keep the task in_progress.
func (ic *Interceptor) InterceptCompletion(ctx context.Context, taskID, wtPath, repoRoot string) (bool, *Diagnostic, error) {
	deadline := 28 * time.Second
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	start := time.Now()
	var failed []string

	for _, g := range ic.Guards {
		if ctx.Err() != nil {
			failed = append(failed, "interceptor timeout")
			break
		}
		reason, err := g(ctx, taskID, wtPath, repoRoot)
		if err != nil {
			slog.Warn("interceptor guard error", slog.Any("error", err))
			failed = append(failed, fmt.Sprintf("guard error: %v", err))
			continue
		}
		if reason != "" {
			failed = append(failed, reason)
		}
	}

	elapsed := time.Since(start)
	slog.Info("interceptor run", slog.Duration("elapsed", elapsed), slog.Int("failed_checks", len(failed)))

	if len(failed) == 0 {
		return true, nil, nil
	}

	msg := buildDiagnostic(failed)
	return false, &Diagnostic{Message: msg, FailedChecks: failed}, nil
}

// checkWorkProducts verifies at least one work product is registered for the task.
func (ic *Interceptor) checkWorkProducts(_ context.Context, taskID, _, _ string) (string, error) {
	if ic.DB == nil {
		return "", nil
	}
	var n int
	err := ic.DB.QueryRow(`SELECT COUNT(1) FROM task_work_products WHERE task_id=?`, taskID).Scan(&n)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "no work products registered for task " + taskID, nil
	}
	return "", nil
}

// checkGitSync verifies:
//  1. No uncommitted changes in the worktree.
//  2. No unpushed commits (branch tracks a remote and is not ahead).
func (ic *Interceptor) checkGitSync(ctx context.Context, _, wtPath, _ string) (string, error) {
	if wtPath == "" {
		return "", nil
	}
	if _, err := os.Stat(wtPath); err != nil {
		return "", nil // worktree may have been pruned already
	}

	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = wtPath
		cmd.Env = security.ChildEnv()
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	// 1. Uncommitted changes.
	status, err := run("status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}
	if status != "" {
		lines := strings.Count(strings.TrimSpace(status), "\n") + 1
		return fmt.Sprintf("worktree has %d uncommitted change(s); commit or stash before marking done", lines), nil
	}

	// 2. Unpushed commits (only if branch has an upstream).
	ahead, err := run("rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		// No upstream configured is not a hard failure.
		return "", nil
	}
	if ahead != "" && ahead != "0" {
		return fmt.Sprintf("branch is %s commit(s) ahead of remote; push before marking done", ahead), nil
	}

	return "", nil
}

// checkMutexLease prevents concurrent deployment collisions by checking
// whether another task already holds an active in_progress claim on a
// work product that overlaps with the current task's repo root.
//
// Worktree sub-paths (repo_path LIKE repoRoot+'/.worktrees/%') are excluded so
// that rig/dogfood tasks running in a .worktrees/ branch do not block the parent
// checkout from completing.
func (ic *Interceptor) checkMutexLease(_ context.Context, taskID, _, repoRoot string) (string, error) {
	if ic.DB == nil {
		return "", nil
	}
	var identifier string
	err := ic.DB.QueryRow(
		`SELECT COALESCE(NULLIF(name,''), id) FROM tasks
		  WHERE execution_stage = 'in_progress'
		    AND status = 'active'
		    AND id != ?
		    AND (repo_path = ?
		         OR repo_path GLOB ? || '/*'
		         OR ? GLOB repo_path || '/*')
		    AND repo_path NOT LIKE ? || '/.worktrees/%'
		  LIMIT 1`,
		taskID, repoRoot, repoRoot, repoRoot, repoRoot,
	).Scan(&identifier)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("task %s is still in_progress on the same repo; stop or cancel it first", identifier), nil
}

// buildDiagnostic formats failed checks into a plain-English self-correcting message.
func buildDiagnostic(failed []string) string {
	var sb strings.Builder
	sb.WriteString("**[Mechanical Completion Interceptor] Transition to done/in_review BLOCKED**\n\n")
	sb.WriteString("Fix each item below, then emit `[[TASK_COMPLETE]]` again.\n\n")
	for i, f := range failed {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, plainEnglishCheck(f)))
	}
	sb.WriteString("\nTask remains `in_progress`.")
	return sb.String()
}

// plainEnglishCheck converts a raw failure reason into an actionable plain-English sentence.
func plainEnglishCheck(reason string) string {
	switch {
	case strings.HasPrefix(reason, "task ") && strings.Contains(reason, "still in_progress"):
		return reason + "."
	case strings.HasPrefix(reason, "no work product") || strings.HasPrefix(reason, "No work product"):
		return "No work product registered. Register at least one (PR link, commit, or file) before marking done."
	case strings.Contains(reason, "uncommitted change"):
		return "The worktree has uncommitted changes. Commit or stash them, then retry."
	case strings.Contains(reason, "ahead of remote"):
		return "The branch has unpushed commits. Run `git push`, then retry."
	default:
		return reason
	}
}
