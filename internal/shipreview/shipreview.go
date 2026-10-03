// Package shipreview manages Ship Review cards: the Board-facing review gate
// that replaces silent agent self-merges. When a task's branch is ready an
// agent creates a card; the Board approves (merging exactly the pinned SHA),
// sends it back (agent iterates), or rejects (branch deleted).
package shipreview

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status values for a Card.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusSentBack = "sent_back"
	StatusRejected = "rejected"
)

// CheckRun records the outcome of a single verification command.
type CheckRun struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	OutputTail string `json:"output_tail,omitempty"`
}

// Card represents a Ship Review card for a task.
type Card struct {
	ID              string     `json:"id"`
	TaskID          string     `json:"task_id"`
	Branch          string     `json:"branch"`
	HeadSHA         string     `json:"head_sha"`
	TestSteps       []string   `json:"test_steps"`
	DevURL          string     `json:"dev_url"`
	DevPID          int        `json:"dev_pid"`
	Status          string     `json:"status"`
	ApprovedSHA     string     `json:"approved_sha,omitempty"`
	MainSHA         string     `json:"main_sha,omitempty"`
	SendBackComment string     `json:"send_back_comment,omitempty"`
	RejectComment   string     `json:"reject_comment,omitempty"`
	FilesChanged    []string   `json:"files_changed"`
	CheckRuns       []CheckRun `json:"check_runs"`
	// AgentSummary is the most recent agent-summary comment for this task (from
	// task_comments WHERE author='agent-summary'). Not stored on the card; populated
	// at read time by GetCard so the Board sees the agent's final summary.
	AgentSummary   string `json:"agent_summary,omitempty"`
	// HasDBMigration is true when any file in FilesChanged matches a DB migration
	// path pattern (supabase/migrations/, db/migrations/, prisma/migrations/, *.sql
	// inside a migrations/ dir). Computed at read time; not stored.
	HasDBMigration bool      `json:"has_db_migration"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ErrHeadMoved is returned when the branch HEAD changed after the card was rendered.
var ErrHeadMoved = errors.New("branch HEAD moved since card was rendered; re-render required")

// ErrNoCard is returned when no card exists for a task.
var ErrNoCard = errors.New("no ship review card found")

// ErrTestStepsRequired is returned when test_steps is empty.
var ErrTestStepsRequired = errors.New("test_steps are required to create a ship review card")

// ErrInvalidBranch is returned when a branch name is unsafe.
var ErrInvalidBranch = errors.New("invalid branch name")

// ErrProtectedBranch is returned when trying to delete a protected branch.
var ErrProtectedBranch = errors.New("refusing to delete protected branch")

// ErrInvalidDevURL is returned when dev_url is not a safe loopback http/https URL.
var ErrInvalidDevURL = errors.New("dev_url must be an http/https URL pointing to a loopback address")

// validateBranch rejects branch names that could be injected as git flags or
// path-traversal vectors.
func validateBranch(branch string) error {
	if branch == "" {
		return ErrInvalidBranch
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("%w: branch name may not start with '-'", ErrInvalidBranch)
	}
	// Disallow shell metacharacters; branch names are passed directly to exec.
	for _, c := range branch {
		if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '&' || c == '|' || c == '`' || c == '$' || c == '>' || c == '<' {
			return fmt.Errorf("%w: branch name contains disallowed character %q", ErrInvalidBranch, c)
		}
	}
	return nil
}

// ValidateDevURL rejects non-loopback or non-http(s) URLs.
func ValidateDevURL(rawURL string) error {
	return validateDevURL(rawURL)
}

// ValidateSQLEditorURL rejects non-http(s) URLs. Unlike ValidateDevURL it
// permits external hosts (e.g. supabase.com dashboard links).
func ValidateSQLEditorURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	lower := strings.ToLower(rawURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("sql_editor_url must start with http:// or https://")
	}
	// Require a non-empty host after the scheme.
	rest := lower[strings.Index(lower, "//")+2:]
	host := rest
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return fmt.Errorf("sql_editor_url must include a valid host")
	}
	return nil
}

// validateDevURL rejects non-loopback or non-http(s) URLs.
func validateDevURL(rawURL string) error {
	if rawURL == "" {
		return nil // empty is ok (no dev server)
	}
	// Only http/https, only loopback hosts allowed.
	lower := strings.ToLower(rawURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ErrInvalidDevURL
	}
	// Allow 127.x.x.x, [::1], and "localhost" only.
	host := lower[strings.Index(lower, "//")+2:]
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	if host != "localhost" && host != "::1" && !strings.HasPrefix(host, "127.") {
		return fmt.Errorf("%w: host %q is not a loopback address", ErrInvalidDevURL, host)
	}
	return nil
}

// devServerManager tracks running dev-server subprocesses by task ID.
var devServerManager = &procManager{
	procs:      make(map[string]*os.Process),
	repoPaths:  make(map[string]string),
	worktrees:  make(map[string]string),
}

type procManager struct {
	mu         sync.Mutex
	procs      map[string]*os.Process
	repoPaths  map[string]string // taskID -> repoPath (for worktree cleanup)
	worktrees  map[string]string // taskID -> temp worktree path
}

func (m *procManager) store(taskID string, p *os.Process, repoPath, wtPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.procs[taskID] = p
	if repoPath != "" {
		m.repoPaths[taskID] = repoPath
	}
	if wtPath != "" {
		m.worktrees[taskID] = wtPath
	}
}

func (m *procManager) kill(taskID string) {
	m.mu.Lock()
	p, ok := m.procs[taskID]
	delete(m.procs, taskID)
	repoPath := m.repoPaths[taskID]
	delete(m.repoPaths, taskID)
	wtPath := m.worktrees[taskID]
	delete(m.worktrees, taskID)
	m.mu.Unlock()
	if ok && p != nil {
		_ = p.Kill()
	}
	if wtPath != "" {
		removeDevWorktree(repoPath, wtPath)
	}
}

func (m *procManager) has(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.procs[taskID]
	return ok
}

// removeDevWorktree removes a temporary dev-server worktree.
// Uses `git worktree remove --force` when a repoPath is available, else os.RemoveAll.
func removeDevWorktree(repoPath, wtPath string) {
	if repoPath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = gitOutput(ctx, repoPath, "worktree", "remove", "--force", wtPath)
		return
	}
	_ = os.RemoveAll(wtPath)
}

// CreateCard inserts a new ship review card or replaces an existing pending one.
// testSteps must be non-empty. headSHA is the current branch HEAD.
// repoDir is used to derive files changed via git diff; pass "" to skip.
// checkRuns is optional evidence from CI / verification commands.
func CreateCard(db *sql.DB, taskID, branch, headSHA string, testSteps []string, devURL string, repoDir string, checkRuns []CheckRun) (*Card, error) {
	if len(testSteps) == 0 {
		return nil, ErrTestStepsRequired
	}
	if err := validateBranch(branch); err != nil {
		return nil, err
	}
	if err := validateDevURL(devURL); err != nil {
		return nil, err
	}

	stepsJSON, err := json.Marshal(testSteps)
	if err != nil {
		return nil, fmt.Errorf("marshal test_steps: %w", err)
	}

	// Derive files changed from git diff against the merge-base.
	filesChanged := diffFilesChanged(repoDir, headSHA)
	filesJSON, err := json.Marshal(filesChanged)
	if err != nil {
		return nil, fmt.Errorf("marshal files_changed: %w", err)
	}

	if checkRuns == nil {
		checkRuns = []CheckRun{}
	}
	checksJSON, err := json.Marshal(checkRuns)
	if err != nil {
		return nil, fmt.Errorf("marshal check_runs: %w", err)
	}

	// Replace any existing pending card for this task (agent iterating).
	_, _ = db.Exec(`DELETE FROM ship_review_cards WHERE task_id = ? AND status IN ('pending', 'sent_back')`, taskID)

	id := uuid.New().String()
	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO ship_review_cards
			(id, task_id, branch, head_sha, test_steps_json, dev_url, dev_pid, status,
			 files_changed_json, check_runs_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, 'pending', ?, ?, ?, ?)`,
		id, taskID, branch, headSHA, string(stepsJSON), devURL,
		string(filesJSON), string(checksJSON),
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert card: %w", err)
	}
	return GetCard(db, taskID)
}

// diffFilesChanged returns the list of files changed between the merge-base of
// "main" (or "master") and headSHA. Returns an empty slice on any error.
func diffFilesChanged(repoDir, headSHA string) []string {
	if repoDir == "" || headSHA == "" {
		return []string{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Find merge-base against main (or master as fallback).
	var base string
	for _, target := range []string{"main", "master"} {
		out, err := gitOutput(ctx, repoDir, "merge-base", target, headSHA)
		if err == nil && out != "" {
			base = out
			break
		}
	}
	if base == "" {
		return []string{}
	}

	out, err := gitOutput(ctx, repoDir, "diff", "--name-only", base, headSHA)
	if err != nil || out == "" {
		return []string{}
	}
	files := strings.Split(out, "\n")
	result := make([]string, 0, len(files))
	for _, f := range files {
		if f != "" {
			result = append(result, f)
		}
	}
	return result
}

// GetCard returns the most recent ship review card for a task.
func GetCard(db *sql.DB, taskID string) (*Card, error) {
	row := db.QueryRow(`
		SELECT id, task_id, branch, head_sha, test_steps_json, dev_url, dev_pid,
		       status, approved_sha, main_sha, send_back_comment, reject_comment,
		       COALESCE(files_changed_json, '[]'), COALESCE(check_runs_json, '[]'),
		       created_at, updated_at
		FROM ship_review_cards
		WHERE task_id = ?
		ORDER BY created_at DESC LIMIT 1`, taskID)

	var c Card
	var stepsJSON, filesJSON, checksJSON string
	var approvedSHA, mainSHA, sendBack, reject sql.NullString
	var createdAt, updatedAt string

	if err := row.Scan(
		&c.ID, &c.TaskID, &c.Branch, &c.HeadSHA, &stepsJSON, &c.DevURL, &c.DevPID,
		&c.Status, &approvedSHA, &mainSHA, &sendBack, &reject,
		&filesJSON, &checksJSON,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoCard
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(stepsJSON), &c.TestSteps); err != nil {
		c.TestSteps = []string{}
	}
	if err := json.Unmarshal([]byte(filesJSON), &c.FilesChanged); err != nil {
		c.FilesChanged = []string{}
	}
	if err := json.Unmarshal([]byte(checksJSON), &c.CheckRuns); err != nil {
		c.CheckRuns = []CheckRun{}
	}
	c.ApprovedSHA = approvedSHA.String
	c.MainSHA = mainSHA.String
	c.SendBackComment = sendBack.String
	c.RejectComment = reject.String
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)

	// Populate AgentSummary from the most recent agent-summary comment (posted by
	// the harness postflight; see internal/orchestrator/run_summary.go).
	var summary sql.NullString
	_ = db.QueryRow(
		`SELECT message FROM task_comments WHERE task_id = ? AND author = 'agent-summary' ORDER BY created_at DESC LIMIT 1`,
		taskID,
	).Scan(&summary)
	c.AgentSummary = summary.String

	// Compute HasDBMigration from FilesChanged.
	c.HasDBMigration = isMigrationInFiles(c.FilesChanged)

	return &c, nil
}

// isMigrationInFiles returns true when any path looks like a DB migration file.
func isMigrationInFiles(files []string) bool {
	for _, f := range files {
		lower := strings.ToLower(filepath.ToSlash(f))
		// supabase/migrations/, db/migrations/, prisma/migrations/, etc.
		if strings.Contains(lower, "/migrations/") {
			return true
		}
		// Bare top-level migrations/ dir
		if strings.HasPrefix(lower, "migrations/") {
			return true
		}
	}
	return false
}

// SetDevPID persists the dev server PID to the card.
func SetDevPID(db *sql.DB, cardID string, pid int) error {
	_, err := db.Exec(
		`UPDATE ship_review_cards SET dev_pid = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		pid, cardID,
	)
	return err
}

// SetDevURL persists an auto-detected dev URL to the card.
func SetDevURL(db *sql.DB, cardID, devURL string) error {
	_, err := db.Exec(
		`UPDATE ship_review_cards SET dev_url = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		devURL, cardID,
	)
	return err
}

// StartDevServer launches the dev server for a card and stores the PID.
// repoPath is the main repository root; a temporary detached worktree is created
// at card.HeadSHA so the dev server always runs on the exact pinned commit, not
// whatever the repo root or any stale task worktree happens to be checked out at.
// Returns the URL to show the Board.
func StartDevServer(db *sql.DB, card *Card, cfg *ProjectDevConfig, repoPath string) (string, error) {
	if cfg.DevCommand == "" {
		return "", fmt.Errorf("no dev_command configured for this project")
	}

	// Kill any stale server (and clean up its worktree) for this task first.
	devServerManager.kill(card.TaskID)

	// Create a temporary detached worktree at the pinned commit SHA so the dev
	// server always serves the exact reviewed code, not the repo root (main).
	wtPath := filepath.Join(repoPath, ".worktrees", "devserver-"+card.TaskID)
	_ = os.RemoveAll(wtPath) // clean any stale leftover
	bgCtx := context.Background()
	if _, err := gitOutput(bgCtx, repoPath, "worktree", "add", "--detach", wtPath, card.HeadSHA); err != nil {
		return "", fmt.Errorf("create dev worktree at %s: %w", card.HeadSHA, err)
	}

	// Run setup steps inside the new worktree.
	for _, step := range cfg.SetupSteps {
		if err := runShellStep(step, wtPath); err != nil {
			removeDevWorktree(repoPath, wtPath)
			return "", fmt.Errorf("setup step %q failed: %w", step, err)
		}
	}

	// Start the dev server in background.
	parts := strings.Fields(cfg.DevCommand)
	cmd := exec.Command(parts[0], parts[1:]...) //nolint:gosec
	cmd.Dir = wtPath
	cmd.Env = append(os.Environ(), "FORCE_COLOR=1")

	if err := cmd.Start(); err != nil {
		removeDevWorktree(repoPath, wtPath)
		return "", fmt.Errorf("start dev server: %w", err)
	}

	devServerManager.store(card.TaskID, cmd.Process, repoPath, wtPath)
	_ = SetDevPID(db, card.ID, cmd.Process.Pid)

	// Reap zombie when process exits; also cleans up the temp worktree.
	go func() {
		_ = cmd.Wait()
		devServerManager.kill(card.TaskID)
	}()

	url := cfg.DevURL
	if url == "" {
		url = "http://localhost:3000"
	}
	return url, nil
}

// StopDevServer kills the dev server for a task and clears the PID.
func StopDevServer(db *sql.DB, card *Card) {
	devServerManager.kill(card.TaskID)
	_, _ = db.Exec(
		`UPDATE ship_review_cards SET dev_pid = 0, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		card.ID,
	)
}

// ApproveAndMerge merges branch into the target branch (usually main) only if
// the current HEAD matches card.HeadSHA. After merge, verifies the reviewed SHA
// is an ancestor of the new main HEAD.
func ApproveAndMerge(ctx context.Context, db *sql.DB, card *Card, repoDir, targetBranch string) (mainSHA string, err error) {
	// 1. Check that HEAD hasn't moved.
	currentHEAD, err := gitOutput(ctx, repoDir, "rev-parse", card.Branch)
	if err != nil {
		return "", fmt.Errorf("resolve branch HEAD: %w", err)
	}
	if currentHEAD != card.HeadSHA {
		return "", ErrHeadMoved
	}

	// 2. Fetch latest and check out target branch.
	// Tolerate repos with no remote (e.g. tests / offline).
	_, _ = gitOutput(ctx, repoDir, "fetch", "--all", "--prune")

	// 3. Merge the branch into target.
	if _, err := gitOutput(ctx, repoDir, "checkout", targetBranch); err != nil {
		return "", fmt.Errorf("checkout %s: %w", targetBranch, err)
	}
	// Pull only when a tracking branch exists; skip silently otherwise.
	if out, err := gitOutput(ctx, repoDir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil && out != "" {
		if _, err := gitOutput(ctx, repoDir, "pull", "--ff-only"); err != nil {
			return "", fmt.Errorf("pull %s: %w", targetBranch, err)
		}
	}
	// Merge the exact pinned commit (not the branch ref) to prevent TOCTOU.
	if _, err := gitOutput(ctx, repoDir, "merge", "--no-ff", "-m",
		fmt.Sprintf("Merge branch '%s' (reviewed SHA %s)", card.Branch, card.HeadSHA),
		card.HeadSHA); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}

	// 4. Push.
	if _, err := gitOutput(ctx, repoDir, "push", "origin", targetBranch); err != nil {
		return "", fmt.Errorf("push: %w", err)
	}

	// 5. Capture new main HEAD.
	mainSHA, err = gitOutput(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve main HEAD after merge: %w", err)
	}

	// 6. Verify reviewed SHA is ancestor of main.
	if err := verifyAncestor(ctx, repoDir, card.HeadSHA, mainSHA); err != nil {
		return mainSHA, fmt.Errorf("ancestor check failed: %w", err)
	}

	// 7. Persist outcome.
	_, err = db.Exec(`
		UPDATE ship_review_cards
		SET status = 'approved', approved_sha = ?, main_sha = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?`,
		card.HeadSHA, mainSHA, card.ID,
	)
	return mainSHA, err
}

// SendBack marks the card sent_back with a comment, waking the agent.
func SendBack(db *sql.DB, card *Card, comment string) error {
	_, err := db.Exec(`
		UPDATE ship_review_cards
		SET status = 'sent_back', send_back_comment = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?`, comment, card.ID,
	)
	return err
}

// Reject marks the card rejected. The caller is responsible for deleting the branch.
func Reject(db *sql.DB, card *Card, comment string) error {
	_, err := db.Exec(`
		UPDATE ship_review_cards
		SET status = 'rejected', reject_comment = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?`, comment, card.ID,
	)
	return err
}

// DeleteBranch deletes the remote branch for a rejected review.
// It refuses to delete main, master, or the remote default branch.
func DeleteBranch(ctx context.Context, repoDir, branch string) error {
	if err := validateBranch(branch); err != nil {
		return err
	}
	if branch == "main" || branch == "master" {
		return fmt.Errorf("%w: %q", ErrProtectedBranch, branch)
	}
	// Detect remote default branch (e.g. "origin/main" → "main").
	if remoteRef, err := gitOutput(ctx, repoDir, "rev-parse", "--abbrev-ref", "origin/HEAD"); err == nil {
		defaultBranch := strings.TrimPrefix(remoteRef, "origin/")
		if defaultBranch != "" && branch == defaultBranch {
			return fmt.Errorf("%w: %q is the remote default branch", ErrProtectedBranch, branch)
		}
	}
	// Use refs/heads/ form so the arg can never be misinterpreted as a flag.
	_, err := gitOutput(ctx, repoDir, "push", "origin", "--delete", "refs/heads/"+branch)
	return err
}

// CurrentBranchHEAD resolves the HEAD SHA for a branch in repoDir.
func CurrentBranchHEAD(ctx context.Context, repoDir, branch string) (string, error) {
	return gitOutput(ctx, repoDir, "rev-parse", branch)
}

// verifyAncestor returns nil if sha is an ancestor of ref in repoDir.
func verifyAncestor(ctx context.Context, repoDir, sha, ref string) error {
	_, err := gitOutput(ctx, repoDir, "merge-base", "--is-ancestor", sha, ref)
	return err
}

// gitOutput runs git in dir with a 60s timeout and returns trimmed stdout.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// runShellStep runs a single shell step (no shell expansion) in workDir.
func runShellStep(step, workDir string) error {
	parts := strings.Fields(step)
	if len(parts) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...) //nolint:gosec
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ProjectDevConfig holds per-project dev-server and migration settings.
type ProjectDevConfig struct {
	RepoPath        string   `json:"repo_path"`
	DevCommand      string   `json:"dev_command"`
	DevURL          string   `json:"dev_url"`
	SetupSteps      []string `json:"setup_steps"`
	// MigrationGlobs is the list of glob patterns used to detect migration files
	// in the task's diff. When empty the package-level defaults are used.
	MigrationGlobs  []string `json:"migration_globs"`
	// SQLEditorURL is the project's SQL editor deep-link (e.g. Supabase dashboard).
	SQLEditorURL    string   `json:"sql_editor_url"`
}

// GetProjectDevConfig loads the dev config for a repo path, or returns defaults.
func GetProjectDevConfig(db *sql.DB, repoPath string) (*ProjectDevConfig, error) {
	var stepsJSON, devCommand, devURL, migGlobsJSON, sqlEditorURL string
	err := db.QueryRow(
		`SELECT dev_command, dev_url, setup_steps_json,
		        COALESCE(migration_globs_json,'[]'), COALESCE(sql_editor_url,'')
		 FROM project_dev_configs WHERE repo_path = ?`,
		repoPath,
	).Scan(&devCommand, &devURL, &stepsJSON, &migGlobsJSON, &sqlEditorURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &ProjectDevConfig{RepoPath: repoPath}, nil
		}
		return nil, err
	}
	cfg := &ProjectDevConfig{
		RepoPath:     repoPath,
		DevCommand:   devCommand,
		DevURL:       devURL,
		SQLEditorURL: sqlEditorURL,
	}
	if err := json.Unmarshal([]byte(stepsJSON), &cfg.SetupSteps); err != nil {
		cfg.SetupSteps = []string{}
	}
	if err := json.Unmarshal([]byte(migGlobsJSON), &cfg.MigrationGlobs); err != nil {
		cfg.MigrationGlobs = []string{}
	}
	return cfg, nil
}

// UpsertProjectDevConfig saves a project dev config.
func UpsertProjectDevConfig(db *sql.DB, cfg *ProjectDevConfig) error {
	stepsJSON, err := json.Marshal(cfg.SetupSteps)
	if err != nil {
		return err
	}
	migGlobsJSON, err := json.Marshal(cfg.MigrationGlobs)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO project_dev_configs
			(repo_path, dev_command, dev_url, setup_steps_json, migration_globs_json, sql_editor_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		ON CONFLICT(repo_path) DO UPDATE SET
			dev_command          = excluded.dev_command,
			dev_url              = excluded.dev_url,
			setup_steps_json     = excluded.setup_steps_json,
			migration_globs_json = excluded.migration_globs_json,
			sql_editor_url       = excluded.sql_editor_url,
			updated_at           = excluded.updated_at`,
		cfg.RepoPath, cfg.DevCommand, cfg.DevURL,
		string(stepsJSON), string(migGlobsJSON), cfg.SQLEditorURL,
	)
	return err
}

// ListProjectDevConfigs returns all project dev configs.
func ListProjectDevConfigs(db *sql.DB) ([]*ProjectDevConfig, error) {
	rows, err := db.Query(`
		SELECT repo_path, dev_command, dev_url, setup_steps_json,
		       COALESCE(migration_globs_json,'[]'), COALESCE(sql_editor_url,'')
		FROM project_dev_configs ORDER BY repo_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProjectDevConfig
	for rows.Next() {
		var c ProjectDevConfig
		var stepsJSON, migGlobsJSON string
		if err := rows.Scan(&c.RepoPath, &c.DevCommand, &c.DevURL, &stepsJSON, &migGlobsJSON, &c.SQLEditorURL); err != nil {
			continue
		}
		if err := json.Unmarshal([]byte(stepsJSON), &c.SetupSteps); err != nil {
			c.SetupSteps = []string{}
		}
		if err := json.Unmarshal([]byte(migGlobsJSON), &c.MigrationGlobs); err != nil {
			c.MigrationGlobs = []string{}
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

