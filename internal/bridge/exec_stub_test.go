package bridge

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubbedTools are replaced by fake shell scripts so tests never touch the
// network, the clipboard, or a browser.
var stubbedTools = []string{"ssh", "scp", "rsync", "pbcopy", "open"}

// sharedStubDir holds one script per tool, written and executed once in
// TestMain. Fresh executables can take seconds to launch on macOS the first
// time (they get scanned), which would trip ProbeSSH's 2s default timeout, so
// each test reuses these warmed scripts and picks behaviour via STUB_<TOOL>.
var sharedStubDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bridge-stubs-")
	if err != nil {
		panic(err)
	}
	for _, name := range stubbedTools {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> \"$STUB_LOG\"\neval \"${" + stubVar(name) + ":-exit 0}\"\n"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0755); err != nil {
			panic(err)
		}
		warm := exec.Command(path)
		warm.Env = []string{"STUB_LOG=/dev/null"}
		_ = warm.Run()
	}
	sharedStubDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func stubVar(name string) string {
	return "STUB_" + strings.ToUpper(name)
}

// stubEnv isolates HOME and PATH so that the stubbed tools resolve to the
// shared fake scripts, which record their argv into a per-test log file.
type stubEnv struct {
	t       *testing.T
	home    string
	logPath string
}

func newStubEnv(t *testing.T) *stubEnv {
	t.Helper()
	root := t.TempDir()
	env := &stubEnv{
		t:       t,
		home:    filepath.Join(root, "home"),
		logPath: filepath.Join(root, "calls.log"),
	}
	if err := os.MkdirAll(env.home, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", env.home)
	t.Setenv("PATH", sharedStubDir)
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("STUB_LOG", env.logPath)
	for _, name := range stubbedTools {
		t.Setenv(stubVar(name), "exit 0")
	}
	return env
}

// stub sets the shell snippet a fake tool evals after logging its argv.
func (e *stubEnv) stub(name, body string) {
	e.t.Helper()
	e.t.Setenv(stubVar(name), body)
}

func (e *stubEnv) calls() []string {
	e.t.Helper()
	data, err := os.ReadFile(e.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func (e *stubEnv) callsTo(name string) []string {
	var out []string
	for _, c := range e.calls() {
		if strings.HasPrefix(c, name+" ") || c == name {
			out = append(out, c)
		}
	}
	return out
}

func TestProbeSSH_ReachableAppliesDefaults(t *testing.T) {
	env := newStubEnv(t)
	env.stub("ssh", "exit 0")

	res := ProbeSSH(context.Background(), "", 0)
	if !res.Reachable || res.Error != "" {
		t.Fatalf("expected reachable probe, got %+v", res)
	}
	if res.Host != DefaultRemoteHost {
		t.Errorf("host = %q, want default %q", res.Host, DefaultRemoteHost)
	}
	calls := env.callsTo("ssh")
	if len(calls) != 1 {
		t.Fatalf("expected 1 ssh call, got %v", calls)
	}
	for _, want := range []string{"BatchMode=yes", "ConnectTimeout=2", "StrictHostKeyChecking=accept-new", DefaultRemoteHost + " true"} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("ssh argv %q missing %q", calls[0], want)
		}
	}
}

func TestProbeSSH_Unreachable(t *testing.T) {
	env := newStubEnv(t)
	env.stub("ssh", "exit 255")

	res := ProbeSSH(context.Background(), "box", 3*time.Second)
	if res.Reachable {
		t.Fatalf("expected unreachable, got %+v", res)
	}
	if res.Host != "box" || res.Error == "" {
		t.Errorf("unexpected result %+v", res)
	}
	if c := env.callsTo("ssh"); len(c) != 1 || !strings.Contains(c[0], "ConnectTimeout=3") {
		t.Errorf("ssh argv should carry ConnectTimeout=3, got %v", c)
	}
}

func TestProbeSSH_TimeoutReportsDeadline(t *testing.T) {
	env := newStubEnv(t)
	env.stub("ssh", "exec /bin/sleep 5")

	res := ProbeSSH(context.Background(), "slow", 200*time.Millisecond)
	if res.Reachable {
		t.Fatalf("expected timeout, got %+v", res)
	}
	if !strings.Contains(res.Error, "connection timed out after 200ms") {
		t.Errorf("error = %q, want timeout message", res.Error)
	}
	// The stub's argv is not asserted here: the context can kill the process
	// before the script gets to log it.
}

func TestCheck_RouteDecisions(t *testing.T) {
	cases := []struct {
		name      string
		reachable bool
		mapped    bool
		want      string
	}{
		{"standalone unreachable", false, false, "local (standalone)"},
		{"non-work reachable", true, false, "local (non-work repo)"},
		{"mapped reachable", true, true, "remote (company-mbp)"},
		{"mapped unreachable", false, true, "local fallback (company-mbp unreachable)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newStubEnv(t)
			if tc.reachable {
				env.stub("ssh", "exit 0")
			} else {
				env.stub("ssh", "exit 1")
			}
			dir := t.TempDir()
			if tc.mapped {
				cfgDir := filepath.Join(env.home, ".agents", "skills", "ticket-notes")
				if err := os.MkdirAll(cfgDir, 0755); err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(ScanReposConfig{Repos: []RepoMapping{{Name: "proj", Path: ToLocalPath(dir)}}})
				if err := os.WriteFile(filepath.Join(cfgDir, "scan-repos.json"), data, 0644); err != nil {
					t.Fatal(err)
				}
			}

			res, err := Check(context.Background(), dir, "")
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if res.RouteDecision != tc.want {
				t.Errorf("RouteDecision = %q, want %q", res.RouteDecision, tc.want)
			}
			if res.RemoteHost != DefaultRemoteHost {
				t.Errorf("RemoteHost = %q, want default", res.RemoteHost)
			}
			if (res.MappedRepo != nil) != tc.mapped {
				t.Errorf("MappedRepo = %+v, mapped want %v", res.MappedRepo, tc.mapped)
			}
			if res.Probe.Reachable != tc.reachable {
				t.Errorf("Probe.Reachable = %v, want %v", res.Probe.Reachable, tc.reachable)
			}
		})
	}
}

func TestCheck_EmptyDirUsesWorkingDirectory(t *testing.T) {
	env := newStubEnv(t)
	env.stub("ssh", "exit 1")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Check(context.Background(), "", "h")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Directory != wd {
		t.Errorf("Directory = %q, want cwd %q", res.Directory, wd)
	}
	if res.RemoteHost != "h" {
		t.Errorf("RemoteHost = %q, want h", res.RemoteHost)
	}
}

func TestProbeRemotePort(t *testing.T) {
	env := newStubEnv(t)
	env.stub("ssh", "exit 0")
	if !ProbeRemotePort(context.Background(), "box", 8080) {
		t.Error("expected listening port when ssh succeeds")
	}
	c := env.callsTo("ssh")
	if len(c) != 1 || !strings.Contains(c[0], "nc -z 127.0.0.1 8080") || !strings.Contains(c[0], "lsof -i :8080") {
		t.Errorf("unexpected ssh argv %v", c)
	}

	env.stub("ssh", "exit 1")
	if ProbeRemotePort(context.Background(), "box", 8080) {
		t.Error("expected not listening when ssh fails")
	}
}

func TestRunSSHPortForwardAndOpenBrowser(t *testing.T) {
	env := newStubEnv(t)
	env.stub("ssh", "exit 0")
	env.stub("open", "exit 0")

	if err := RunSSHPortForward(context.Background(), "box", 9000, 3000); err != nil {
		t.Fatalf("RunSSHPortForward: %v", err)
	}
	c := env.callsTo("ssh")
	if len(c) != 1 || !strings.Contains(c[0], "-N -L 9000:127.0.0.1:3000") || !strings.HasSuffix(c[0], " box") {
		t.Errorf("unexpected ssh argv %v", c)
	}

	if err := OpenBrowserURL("http://localhost:9000"); err != nil {
		t.Fatalf("OpenBrowserURL: %v", err)
	}
	if c := env.callsTo("open"); len(c) != 1 || c[0] != "open http://localhost:9000" {
		t.Errorf("unexpected open argv %v", c)
	}

	env.stub("ssh", "exit 3")
	if err := RunSSHPortForward(context.Background(), "box", 9000, 3000); err == nil {
		t.Error("expected error when ssh exits non-zero")
	}
}

func TestCloneRepoOnRemote(t *testing.T) {
	t.Run("with branch", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		if err := CloneRepoOnRemote(context.Background(), "box", "git@x:r.git", "feat", "~/dev/r"); err != nil {
			t.Fatalf("CloneRepoOnRemote: %v", err)
		}
		c := env.callsTo("ssh")
		if len(c) != 2 {
			t.Fatalf("expected mkdir + clone ssh calls, got %v", c)
		}
		if !strings.Contains(c[0], `mkdir -p $(dirname "$HOME"/'dev/r')`) {
			t.Errorf("mkdir call = %q", c[0])
		}
		if !strings.Contains(c[1], `git clone -b 'feat' 'git@x:r.git' "$HOME"/'dev/r' || git clone 'git@x:r.git'`) {
			t.Errorf("clone call = %q", c[1])
		}
	})

	t.Run("HEAD branch clones default", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		if err := CloneRepoOnRemote(context.Background(), "box", "u", "HEAD", "/abs/r"); err != nil {
			t.Fatalf("CloneRepoOnRemote: %v", err)
		}
		c := env.callsTo("ssh")
		if len(c) != 2 || !strings.HasSuffix(c[1], "git clone 'u' '/abs/r'") {
			t.Errorf("unexpected clone call %v", c)
		}
	})

	t.Run("mkdir failure aborts", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 1")
		err := CloneRepoOnRemote(context.Background(), "box", "u", "", "/abs/r")
		if err == nil || !strings.Contains(err.Error(), "failed to create remote parent directory") {
			t.Fatalf("expected mkdir error, got %v", err)
		}
		if c := env.callsTo("ssh"); len(c) != 1 {
			t.Errorf("clone must not run after mkdir failure, got %v", c)
		}
	})
}

func TestSyncDirectoryToRemote(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("rsync", "exit 0")
		local := t.TempDir()
		if err := SyncDirectoryToRemote(context.Background(), "box", local, "~/dev/r"); err != nil {
			t.Fatalf("SyncDirectoryToRemote: %v", err)
		}
		r := env.callsTo("rsync")
		want := "rsync -az --progress --exclude=.DS_Store " + filepath.Clean(local) + "/ box:dev/r/"
		if len(r) != 1 || r[0] != want {
			t.Errorf("rsync argv = %v, want %q", r, want)
		}
	})

	t.Run("mkdir failure skips rsync", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 1")
		env.stub("rsync", "exit 0")
		err := SyncDirectoryToRemote(context.Background(), "box", t.TempDir(), "/r")
		if err == nil || !strings.Contains(err.Error(), "failed to create remote destination directory") {
			t.Fatalf("expected mkdir error, got %v", err)
		}
		if r := env.callsTo("rsync"); len(r) != 0 {
			t.Errorf("rsync should not run, got %v", r)
		}
	})
}

func TestDefaultRemoteHelpers(t *testing.T) {
	env := newStubEnv(t)
	env.stub("ssh", "exit 0")
	env.stub("rsync", "exit 0")
	ctx := context.Background()

	if !defaultRemoteDirExists(ctx, "box", "~/x") {
		t.Error("expected remote dir to exist when ssh succeeds")
	}
	if err := defaultRunRemoteGitPush(ctx, "box", "~/x"); err != nil {
		t.Errorf("defaultRunRemoteGitPush: %v", err)
	}
	c := env.callsTo("ssh")
	if len(c) != 2 || !strings.Contains(c[0], `[ -d "$HOME"/'x' ]`) || !strings.Contains(c[1], `cd "$HOME"/'x' && git push`) {
		t.Errorf("unexpected ssh argv %v", c)
	}

	if err := defaultRunGitGuardSync(ctx, "box", "~/x", ""); err == nil {
		t.Error("expected error for empty local dir")
	}
	local := filepath.Join(t.TempDir(), "new", "dir")
	if err := defaultRunGitGuardSync(ctx, "box", "~/x", local); err != nil {
		t.Fatalf("defaultRunGitGuardSync: %v", err)
	}
	if _, err := os.Stat(local); err != nil {
		t.Errorf("local dir should be created: %v", err)
	}
	if r := env.callsTo("rsync"); len(r) != 1 || !strings.HasSuffix(r[0], "box:x/ "+local+"/") {
		t.Errorf("unexpected rsync argv %v", r)
	}

	env.stub("ssh", "exit 1")
	if defaultRemoteDirExists(ctx, "box", "~/x") {
		t.Error("expected false when ssh fails")
	}
	if st, err := defaultInspectRemoteGitStatus(ctx, "", ""); err != nil || st.IsRepo {
		t.Errorf("empty host/dir should short-circuit, got %+v %v", st, err)
	}
}

func TestTransfer(t *testing.T) {
	ctx := context.Background()

	t.Run("unreachable host", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 255")
		env.stub("scp", "exit 0")
		_, err := Transfer(ctx, TransferOptions{Host: "box", Source: "x"})
		if err == nil || !strings.Contains(err.Error(), "remote host box unreachable") {
			t.Fatalf("expected unreachable error, got %v", err)
		}
		if s := env.callsTo("scp"); len(s) != 0 {
			t.Errorf("scp must not run, got %v", s)
		}
	})

	t.Run("pull requires source", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		if _, err := Transfer(ctx, TransferOptions{Host: "box", Pull: true}); err == nil {
			t.Fatal("expected error for missing remote source")
		}
	})

	t.Run("pull with explicit dest", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("scp", "exit 0")
		res, err := Transfer(ctx, TransferOptions{Host: "box", Pull: true, Recursive: true, Source: "/srv/logs", Dest: "./logs"})
		if err != nil {
			t.Fatalf("Transfer: %v", err)
		}
		if res.Action != "pulled" || res.Dest != "./logs" || !res.IsDirectory {
			t.Errorf("unexpected result %+v", res)
		}
		if s := env.callsTo("scp"); len(s) != 1 || s[0] != "scp -r -p box:/srv/logs ./logs" {
			t.Errorf("unexpected scp argv %v", s)
		}
	})

	t.Run("pull default dest uses basename", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("scp", "exit 0")
		res, err := Transfer(ctx, TransferOptions{Host: "box", Pull: true, Source: "/srv/report.txt"})
		if err != nil {
			t.Fatalf("Transfer: %v", err)
		}
		if res.Dest != "./report.txt" {
			t.Errorf("Dest = %q, want ./report.txt", res.Dest)
		}
	})

	t.Run("pull scp failure", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("scp", "echo boom >&2; exit 1")
		_, err := Transfer(ctx, TransferOptions{Host: "box", Pull: true, Source: "/srv/a"})
		if err == nil || !strings.Contains(err.Error(), "scp pull failed") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected scp pull error with stderr, got %v", err)
		}
	})

	t.Run("push requires existing source", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		if _, err := Transfer(ctx, TransferOptions{Host: "box"}); err == nil {
			t.Error("expected error for empty local source")
		}
		_, err := Transfer(ctx, TransferOptions{Host: "box", Source: filepath.Join(t.TempDir(), "missing.txt")})
		if err == nil || !strings.Contains(err.Error(), "local source not found") {
			t.Errorf("expected not-found error, got %v", err)
		}
	})

	t.Run("push file into directory-like dest", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("scp", "exit 0")
		env.stub("pbcopy", `read -r line; echo "pbcopy-stdin $line" >> "$STUB_LOG"`)
		src := filepath.Join(t.TempDir(), "notes.txt")
		if err := os.WriteFile(src, []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}

		res, err := Transfer(ctx, TransferOptions{Host: "box", Source: src, Dest: "~/Downloads", RawName: true})
		if err != nil {
			t.Fatalf("Transfer: %v", err)
		}
		if res.Action != "pushed" || res.Dest != "~/Downloads/notes.txt" || res.IsDirectory {
			t.Errorf("unexpected result %+v", res)
		}
		if s := env.callsTo("scp"); len(s) != 1 || s[0] != "scp -p "+src+" box:~/Downloads/notes.txt" {
			t.Errorf("unexpected scp argv %v", s)
		}
		if p := env.callsTo("pbcopy-stdin"); len(p) != 1 || p[0] != "pbcopy-stdin ~/Downloads/notes.txt" {
			t.Errorf("remote dest should be copied to clipboard, got %v", p)
		}
	})

	t.Run("push translates shell-expanded local home", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("scp", "exit 0")
		src := filepath.Join(t.TempDir(), "a.txt")
		if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		res, err := Transfer(ctx, TransferOptions{Host: "box", Source: src, Dest: filepath.Join(env.home, "inbox", "b.txt"), RawName: true})
		if err != nil {
			t.Fatalf("Transfer: %v", err)
		}
		if res.Dest != "~/inbox/b.txt" {
			t.Errorf("Dest = %q, want ~/inbox/b.txt", res.Dest)
		}

		res, err = Transfer(ctx, TransferOptions{Host: "box", Source: src, Dest: env.home, RawName: true})
		if err != nil {
			t.Fatalf("Transfer: %v", err)
		}
		if res.Dest != "~/a.txt" {
			t.Errorf("home dest = %q, want ~/a.txt", res.Dest)
		}
	})

	t.Run("push directory forces recursive", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("scp", "exit 0")
		src := t.TempDir()
		res, err := Transfer(ctx, TransferOptions{Host: "box", Source: src, Dest: "/remote/dir"})
		if err != nil {
			t.Fatalf("Transfer: %v", err)
		}
		if !res.IsDirectory || res.Dest != "/remote/dir" {
			t.Errorf("unexpected result %+v", res)
		}
		if s := env.callsTo("scp"); len(s) != 1 || !strings.HasPrefix(s[0], "scp -r -p ") {
			t.Errorf("directory push must pass -r, got %v", s)
		}
	})

	t.Run("push scp failure", func(t *testing.T) {
		env := newStubEnv(t)
		env.stub("ssh", "exit 0")
		env.stub("scp", "exit 1")
		src := filepath.Join(t.TempDir(), "a.txt")
		if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := Transfer(ctx, TransferOptions{Host: "box", Source: src, Dest: "/r/a.txt"})
		if err == nil || !strings.Contains(err.Error(), "scp push failed") {
			t.Fatalf("expected scp push error, got %v", err)
		}
	})
}

func TestIsDirectoryDest(t *testing.T) {
	cases := []struct {
		dest      string
		srcHasExt bool
		want      bool
	}{
		{"~/stuff/", false, true},
		{"~", false, true},
		{".", false, true},
		{"..", false, true},
		{"~/Downloads", false, true},
		{"/Users/x/Documents", false, true},
		{"~/Desktop", false, true},
		{"/tmp", false, true},
		{"downloads", false, true},
		{"tmp", false, true},
		{"~/inbox", true, true},
		{"~/inbox", false, false},
		{"~/inbox/file.txt", true, false},
	}
	for _, tc := range cases {
		if got := isDirectoryDest(tc.dest, tc.srcHasExt); got != tc.want {
			t.Errorf("isDirectoryDest(%q, %v) = %v, want %v", tc.dest, tc.srcHasExt, got, tc.want)
		}
	}
}

func TestGenerateSessionToken(t *testing.T) {
	a, b := GenerateSessionToken(), GenerateSessionToken()
	if len(a) != 32 {
		t.Fatalf("token length = %d, want 32", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("token %q is not hex: %v", a, err)
	}
	if a == b {
		t.Error("consecutive tokens must differ")
	}
}

func TestDefaultSessionPath(t *testing.T) {
	env := newStubEnv(t)
	newPath := filepath.Join(env.home, ".staypoint", "bridge-session.json")
	legacyPath := filepath.Join(env.home, ".agent-mesh", "bridge-session.json")

	if got := DefaultSessionPath(); got != newPath {
		t.Errorf("no files: got %q, want %q", got, newPath)
	}

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := DefaultSessionPath(); got != legacyPath {
		t.Errorf("legacy only: got %q, want %q", got, legacyPath)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := DefaultSessionPath(); got != newPath {
		t.Errorf("both present: got %q, want new path %q", got, newPath)
	}
}

func TestBridgeSessionRoundTripAndClear(t *testing.T) {
	newStubEnv(t)
	p := DefaultSessionPath()

	in := BridgeSession{ClientUser: "u", ClientHome: "/h", ClientHost: "c", BridgePort: 4119, Token: "tok", Active: true}
	if err := SaveBridgeSession(in); err != nil {
		t.Fatalf("SaveBridgeSession: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("session file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("session file perm = %o, want 600", perm)
	}

	out, err := LoadBridgeSession()
	if err != nil {
		t.Fatalf("LoadBridgeSession: %v", err)
	}
	if out.Token != "tok" || out.BridgePort != 4119 || !out.Active || out.UpdatedAt == "" {
		t.Errorf("unexpected loaded session %+v", out)
	}

	if err := ClearBridgeSession(); err != nil {
		t.Fatalf("ClearBridgeSession: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("session file should be removed, stat err = %v", err)
	}
	if _, err := LoadBridgeSession(); err == nil {
		t.Error("LoadBridgeSession should fail after clear")
	}
	if err := ClearBridgeSession(); err != nil {
		t.Errorf("clearing a missing session should be a no-op, got %v", err)
	}
}

func TestLoadBridgeSession_CorruptJSON(t *testing.T) {
	newStubEnv(t)
	p := DefaultSessionPath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBridgeSession(); err == nil {
		t.Error("expected JSON decode error")
	}
}

func TestFindRecentDesktopScreenshot_PicksNewestMatching(t *testing.T) {
	env := newStubEnv(t)
	if got := findRecentDesktopScreenshot(); got != "" {
		t.Errorf("missing Desktop should yield empty, got %q", got)
	}

	desktop := filepath.Join(env.home, "Desktop")
	if err := os.MkdirAll(filepath.Join(desktop, "Screenshot dir.png"), 0755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	files := map[string]time.Time{
		"Screenshot old.png":       now.Add(-10 * time.Minute),
		"Screen Shot 1.jpg":        now.Add(-2 * time.Minute),
		"CleanShot newest.jpeg":    now.Add(-30 * time.Second),
		"holiday.png":              now,
		"Screenshot notes.txt":     now,
		"screenshot-uppercase.PNG": now.Add(-1 * time.Minute),
	}
	for name, mt := range files {
		p := filepath.Join(desktop, name)
		if err := os.WriteFile(p, []byte("img"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	want := filepath.Join(desktop, "CleanShot newest.jpeg")
	if got := findRecentDesktopScreenshot(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	if got, w := findRecentDesktopScreenshot(), filepath.Join(desktop, "screenshot-uppercase.PNG"); got != w {
		t.Errorf("after removal got %q, want %q", got, w)
	}
}

func TestCopyStringToClipboard(t *testing.T) {
	env := newStubEnv(t)
	t.Setenv("PATH", t.TempDir())
	if err := copyStringToClipboard("x"); err != nil {
		t.Errorf("missing pbcopy should be a no-op, got %v", err)
	}
	t.Setenv("PATH", sharedStubDir)
	env.stub("pbcopy", `read -r line; echo "pbcopy-stdin $line" >> "$STUB_LOG"`)
	if err := copyStringToClipboard("hello world"); err != nil {
		t.Fatalf("copyStringToClipboard: %v", err)
	}
	if p := env.callsTo("pbcopy-stdin"); len(p) != 1 || p[0] != "pbcopy-stdin hello world" {
		t.Errorf("unexpected pbcopy stdin %v", p)
	}
}

func TestSuggestAIName_ShortCircuits(t *testing.T) {
	ctx := context.Background()
	img := filepath.Join(t.TempDir(), "IMG_0001.png")
	if err := os.WriteFile(img, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := SuggestAIName(ctx, img, ""); got != "" {
		t.Errorf("no API key should return empty, got %q", got)
	}
	if got := SuggestAIName(ctx, filepath.Join(t.TempDir(), "missing.png"), "k"); got != "" {
		t.Errorf("missing file should return empty, got %q", got)
	}
	empty := filepath.Join(t.TempDir(), "empty.png")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := SuggestAIName(ctx, empty, "k"); got != "" {
		t.Errorf("empty file should return empty, got %q", got)
	}
}
