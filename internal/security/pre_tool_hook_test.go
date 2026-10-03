package security

import (
	"database/sql"
	"os/exec"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB creates an in-memory SQLite DB with the security gate schema applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS security_gate_requests (
			id           TEXT PRIMARY KEY,
			cmdline      TEXT NOT NULL,
			reasons_json TEXT NOT NULL DEFAULT '[]',
			run_id       TEXT,
			status       TEXT NOT NULL DEFAULT 'pending'
			             CHECK (status IN ('pending','approved','denied')),
			created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			decided_at   TEXT
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// TestPreToolHookRedCommands verifies that commands that should be held by the
// pre-tool hook are classified as Red by the same Classifier the hook uses.
// This is the "real harness Bash call" path: the hook reads the command from
// the Claude Code PreToolUse payload and calls c.Classify; these tests confirm
// those commands are Red so the hook will block and wait for Board approval.
func TestPreToolHookRedCommands(t *testing.T) {
	c := &Classifier{} // no worktree — hook operates without one
	blocked := []string{
		// direct main-push forms
		"git push origin main",
		"git push origin master",
		"git push origin HEAD:main",
		"git push origin HEAD:master",
		"git push origin HEAD:refs/heads/main",
		"git push origin mybranch:main",
		"git push --all",
		"git push origin --all",
		// gh pr merge variants
		"gh pr merge",
		"gh pr merge --squash",
		"gh pr merge 123 --merge",
		"gh -R owner/repo pr merge 123",
		"gh --repo owner/repo pr merge",
		// wrapped in bash -c
		`bash -c "git push origin main"`,
		`sh -c 'gh pr merge --squash'`,
	}
	for _, cmd := range blocked {
		v := c.Classify(cmd)
		if v.Tier != Red {
			t.Errorf("pre-tool hook should block %q: got tier %s", cmd, v.Tier)
		}
		if len(v.Reasons) == 0 {
			t.Errorf("pre-tool hook: Red verdict for %q has no reason", cmd)
		}
	}
}

// TestPreToolHookAllowedCommands verifies feature-branch pushes are NOT Red
// and will pass through the hook without a Board approval request.
func TestPreToolHookAllowedCommands(t *testing.T) {
	c := &Classifier{}
	allowed := []string{
		"git push origin feature-branch",
		"git push origin HEAD:refs/heads/feature-xyz",
		"gh pr view 123",
		"gh api repos/owner/repo/pulls",
		"git fetch",
		"git log --oneline -5",
	}
	for _, cmd := range allowed {
		v := c.Classify(cmd)
		if v.Tier == Red {
			t.Errorf("pre-tool hook should allow %q: got Red (%s)", cmd, strings.Join(v.Reasons, "; "))
		}
	}
}

// TestBarePushViaClassifier verifies that bare `git push` (no explicit refspec)
// on a main branch is detected as Red by the classifier when CWD is set.
// This is the real harness call path — the hook sets c.CWD from the payload.
func TestBarePushViaClassifier(t *testing.T) {
	// Create a temp git repo on a branch called "main".
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "-C", dir, "init", "-b", "main")
	run("git", "-C", dir, "config", "user.email", "test@test.com")
	run("git", "-C", dir, "config", "user.name", "Test")

	cwd := func() *Classifier { return &Classifier{CWD: dir} }

	red := func(t *testing.T, cmd string) {
		t.Helper()
		v := cwd().Classify(cmd)
		if v.Tier != Red {
			t.Errorf("want Red for %q, got %s", cmd, v.Tier)
		}
	}
	notRed := func(t *testing.T, cmd string) {
		t.Helper()
		v := cwd().Classify(cmd)
		if v.Tier == Red {
			t.Errorf("want !Red for %q, got Red (%s)", cmd, strings.Join(v.Reasons, "; "))
		}
	}

	// Bare push on main branch → Red.
	red(t, "git push")
	red(t, "git push origin")

	// Global -C flag tracked correctly inside classifier.
	red(t, "git -C "+dir+" push")

	// -c config override → fail-closed (Red) regardless of branch.
	red(t, "git -c core.hooksPath=/dev/null push origin")

	// Explicit refspec — pushTargetsMain already handles; not a bare-push.
	notRed(t, "git push origin feature-branch")

	// Empty CWD → fail-closed (Red).
	v := (&Classifier{CWD: ""}).Classify("git push")
	if v.Tier != Red {
		t.Error("bare 'git push' with empty CWD: want fail-closed (Red)")
	}

	// Non-git CWD → fail-closed (Red).
	v = (&Classifier{CWD: t.TempDir()}).Classify("git push")
	if v.Tier != Red {
		t.Error("bare 'git push' in non-git CWD: want fail-closed (Red)")
	}
}

// TestPreToolHookGateRequestPersistence verifies that gate requests round-trip
// through the DB-layer functions (create → get → decide).
func TestPreToolHookGateRequestPersistence(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	gr, err := CreateGateRequest(db, "git push origin main", []string{"git push targets main/master; Board approval required"}, "session-123")
	if err != nil {
		t.Fatalf("CreateGateRequest: %v", err)
	}
	if gr.Status != GateRequestPending {
		t.Fatalf("want pending, got %s", gr.Status)
	}

	// Fetch by id — must survive daemon restart (persisted in DB).
	got, err := GetGateRequest(db, gr.ID)
	if err != nil || got == nil {
		t.Fatalf("GetGateRequest: %v", err)
	}
	if got.Cmdline != "git push origin main" {
		t.Errorf("cmdline mismatch: %q", got.Cmdline)
	}

	// List pending — should show the request.
	pending, err := ListPendingGateRequests(db)
	if err != nil {
		t.Fatalf("ListPendingGateRequests: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != gr.ID {
		t.Errorf("want 1 pending request, got %d", len(pending))
	}

	// Approve.
	decided, err := DecideGateRequest(db, gr.ID, true)
	if err != nil {
		t.Fatalf("DecideGateRequest: %v", err)
	}
	if decided.Status != GateRequestApproved {
		t.Errorf("want approved, got %s", decided.Status)
	}
	if decided.DecidedAt == nil {
		t.Error("decided_at should be set after decision")
	}

	// Cannot decide again.
	if _, err := DecideGateRequest(db, gr.ID, false); err == nil {
		t.Error("second decision should fail")
	}

	// Pending list now empty.
	pending, _ = ListPendingGateRequests(db)
	if len(pending) != 0 {
		t.Errorf("want 0 pending, got %d", len(pending))
	}
}
