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

// Card represents a Ship Review card for a task.
type Card struct {
	ID               string    `json:"id"`
	TaskID           string    `json:"task_id"`
	Branch           string    `json:"branch"`
	HeadSHA          string    `json:"head_sha"`
	TestSteps        []string  `json:"test_steps"`
	DevURL           string    `json:"dev_url"`
	DevPID           int       `json:"dev_pid"`
	Status           string    `json:"status"`
	ApprovedSHA      string    `json:"approved_sha,omitempty"`
	MainSHA          string    `json:"main_sha,omitempty"`
	SendBackComment  string    `json:"send_back_comment,omitempty"`
	RejectComment    string    `json:"reject_comment,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ErrHeadMoved is returned when the branch HEAD changed after the card was rendered.
var ErrHeadMoved = errors.New("branch HEAD moved since card was rendered; re-render required")

// ErrNoCard is returned when no card exists for a task.
var ErrNoCard = errors.New("no ship review card found")

// ErrTestStepsRequired is returned when test_steps is empty.
var ErrTestStepsRequired = errors.New("test_steps are required to create a ship review card")

// ErrInvalidBranch is returned when a branch name is unsafe.
var ErrInvalidBranch = errors.New("invalid branch name")

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
var devServerManager = &procManager{procs: make(map[string]*os.Process)}

type procManager struct {
	mu    sync.Mutex
	procs map[string]*os.Process
}

func (m *procManager) store(taskID string, p *os.Process) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.procs[taskID] = p
}

func (m *procManager) kill(taskID string) {
	m.mu.Lock()
	p, ok := m.procs[taskID]
	delete(m.procs, taskID)
	m.mu.Unlock()
	if ok && p != nil {
		_ = p.Kill()
	}
}

func (m *procManager) has(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.procs[taskID]
	return ok
}

// CreateCard inserts a new ship review card or replaces an existing pending one.
// testSteps must be non-empty. headSHA is the current branch HEAD.
func CreateCard(db *sql.DB, taskID, branch, headSHA string, testSteps []string, devURL string) (*Card, error) {
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

	// Replace any existing pending card for this task (agent iterating).
	_, _ = db.Exec(`DELETE FROM ship_review_cards WHERE task_id = ? AND status IN ('pending', 'sent_back')`, taskID)

	id := uuid.New().String()
	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO ship_review_cards
			(id, task_id, branch, head_sha, test_steps_json, dev_url, dev_pid, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, 'pending', ?, ?)`,
		id, taskID, branch, headSHA, string(stepsJSON), devURL,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert card: %w", err)
	}
	return GetCard(db, taskID)
}

// GetCard returns the most recent ship review card for a task.
func GetCard(db *sql.DB, taskID string) (*Card, error) {
	row := db.QueryRow(`
		SELECT id, task_id, branch, head_sha, test_steps_json, dev_url, dev_pid,
		       status, approved_sha, main_sha, send_back_comment, reject_comment,
		       created_at, updated_at
		FROM ship_review_cards
		WHERE task_id = ?
		ORDER BY created_at DESC LIMIT 1`, taskID)

	var c Card
	var stepsJSON string
	var approvedSHA, mainSHA, sendBack, reject sql.NullString
	var createdAt, updatedAt string

	if err := row.Scan(
		&c.ID, &c.TaskID, &c.Branch, &c.HeadSHA, &stepsJSON, &c.DevURL, &c.DevPID,
		&c.Status, &approvedSHA, &mainSHA, &sendBack, &reject,
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
	c.ApprovedSHA = approvedSHA.String
	c.MainSHA = mainSHA.String
	c.SendBackComment = sendBack.String
	c.RejectComment = reject.String
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &c, nil
}

// SetDevPID persists the dev server PID to the card.
func SetDevPID(db *sql.DB, cardID string, pid int) error {
	_, err := db.Exec(
		`UPDATE ship_review_cards SET dev_pid = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		pid, cardID,
	)
	return err
}

// StartDevServer launches the dev server for a card and stores the PID.
// cfg is the per-project dev config. workDir is the task's worktree directory.
// Returns the URL to show the Board.
func StartDevServer(db *sql.DB, card *Card, cfg *ProjectDevConfig, workDir string) (string, error) {
	if cfg.DevCommand == "" {
		return "", fmt.Errorf("no dev_command configured for this project")
	}

	// Kill any stale server for this task first.
	devServerManager.kill(card.TaskID)

	// Run setup steps before starting dev server.
	for _, step := range cfg.SetupSteps {
		if err := runShellStep(step, workDir); err != nil {
			return "", fmt.Errorf("setup step %q failed: %w", step, err)
		}
	}

	// Start the dev server in background.
	parts := strings.Fields(cfg.DevCommand)
	cmd := exec.Command(parts[0], parts[1:]...) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("FORCE_COLOR=1"))

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start dev server: %w", err)
	}

	devServerManager.store(card.TaskID, cmd.Process)
	_ = SetDevPID(db, card.ID, cmd.Process.Pid)

	// Reap zombie when process exits to avoid log spam.
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
func DeleteBranch(ctx context.Context, repoDir, branch string) error {
	if err := validateBranch(branch); err != nil {
		return err
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

// ProjectDevConfig holds per-project dev-server settings.
type ProjectDevConfig struct {
	RepoPath   string   `json:"repo_path"`
	DevCommand string   `json:"dev_command"`
	DevURL     string   `json:"dev_url"`
	SetupSteps []string `json:"setup_steps"`
}

// GetProjectDevConfig loads the dev config for a repo path, or returns defaults.
func GetProjectDevConfig(db *sql.DB, repoPath string) (*ProjectDevConfig, error) {
	var stepsJSON, devCommand, devURL string
	err := db.QueryRow(
		`SELECT dev_command, dev_url, setup_steps_json FROM project_dev_configs WHERE repo_path = ?`,
		repoPath,
	).Scan(&devCommand, &devURL, &stepsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &ProjectDevConfig{RepoPath: repoPath}, nil
		}
		return nil, err
	}
	cfg := &ProjectDevConfig{RepoPath: repoPath, DevCommand: devCommand, DevURL: devURL}
	if err := json.Unmarshal([]byte(stepsJSON), &cfg.SetupSteps); err != nil {
		cfg.SetupSteps = []string{}
	}
	return cfg, nil
}

// UpsertProjectDevConfig saves a project dev config.
func UpsertProjectDevConfig(db *sql.DB, cfg *ProjectDevConfig) error {
	stepsJSON, err := json.Marshal(cfg.SetupSteps)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO project_dev_configs (repo_path, dev_command, dev_url, setup_steps_json, updated_at)
		VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		ON CONFLICT(repo_path) DO UPDATE SET
			dev_command = excluded.dev_command,
			dev_url = excluded.dev_url,
			setup_steps_json = excluded.setup_steps_json,
			updated_at = excluded.updated_at`,
		cfg.RepoPath, cfg.DevCommand, cfg.DevURL, string(stepsJSON),
	)
	return err
}

// ListProjectDevConfigs returns all project dev configs.
func ListProjectDevConfigs(db *sql.DB) ([]*ProjectDevConfig, error) {
	rows, err := db.Query(`SELECT repo_path, dev_command, dev_url, setup_steps_json FROM project_dev_configs ORDER BY repo_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProjectDevConfig
	for rows.Next() {
		var c ProjectDevConfig
		var stepsJSON string
		if err := rows.Scan(&c.RepoPath, &c.DevCommand, &c.DevURL, &stepsJSON); err != nil {
			continue
		}
		if err := json.Unmarshal([]byte(stepsJSON), &c.SetupSteps); err != nil {
			c.SetupSteps = []string{}
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

