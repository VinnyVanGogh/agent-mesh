package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/VinnyVanGogh/agent-mesh/internal/config"
)

const (
	DefaultRemoteHost = "company-mbp"
	DefaultSSHTimeout = 2 * time.Second
)

var (
	LocalWorkPrefix  = filepath.Join(getHomeDir(), "Documents", "dev", "work")
	RemoteWorkPrefix = "/Users/remote/Documents/dev/work"
)

func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// RepoMapping represents a repository configuration from scan-repos.json.
type RepoMapping struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Projects []string `json:"projects,omitempty"`
}

// ScanReposConfig represents the root schema of scan-repos.json.
type ScanReposConfig struct {
	AuthorIdentities []string      `json:"author_identities,omitempty"`
	Repos            []RepoMapping `json:"repos"`
}

// DefaultScanReposPath returns the path to ~/.agents/skills/ticket-notes/scan-repos.json.
func DefaultScanReposPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agents", "skills", "ticket-notes", "scan-repos.json")
}

// LoadScanRepos reads repo mappings from the given path (or default path if empty).
func LoadScanRepos(configPath string) (*ScanReposConfig, error) {
	if configPath == "" {
		configPath = DefaultScanReposPath()
	}
	if configPath == "" {
		return nil, fmt.Errorf("unable to determine user home directory")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read scan-repos.json from %s: %w", configPath, err)
	}

	var cfg ScanReposConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse scan-repos.json: %w", err)
	}

	return &cfg, nil
}

// FindMappedRepo checks if targetPath matches or is inside one of the scanned repos.
func FindMappedRepo(cfg *ScanReposConfig, targetPath string) *RepoMapping {
	if cfg == nil || len(cfg.Repos) == 0 {
		return nil
	}

	cleanTarget := filepath.Clean(targetPath)
	for _, repo := range cfg.Repos {
		cleanRepo := filepath.Clean(repo.Path)
		if cleanTarget == cleanRepo || strings.HasPrefix(cleanTarget, cleanRepo+string(filepath.Separator)) {
			return &repo
		}
	}
	return nil
}

// ToRemotePath translates a local path to the remote enterprise path.
// It checks configured repositories, work prefixes, and replaces local home with ~
// so that local usernames are never leaked or used on remote hosts.
func ToRemotePath(localPath string) string {
	cfg, _ := config.LoadConfig()
	scanCfg, _ := LoadScanRepos("")
	return ToRemotePathWithConfig(localPath, cfg, scanCfg)
}

// ToRemotePathWithConfig translates localPath using the provided config and repo scans.
func ToRemotePathWithConfig(localPath string, cfg *config.Config, scanCfg *ScanReposConfig) string {
	if localPath == "" {
		return ""
	}
	clean := filepath.Clean(localPath)
	home := getHomeDir()

	remoteRoot := "~/Documents/dev/work"
	workRoot := LocalWorkPrefix
	if cfg != nil {
		if cfg.RemoteRepoRoot != "" {
			remoteRoot = cfg.RemoteRepoRoot
		}
		if cfg.WorkRepoRoot != "" {
			workRoot = cfg.WorkRepoRoot
		}
	}

	// 1. If LocalWorkPrefix is set (e.g. in tests), honor it directly
	if clean == LocalWorkPrefix {
		return RemoteWorkPrefix
	}
	if strings.HasPrefix(clean, LocalWorkPrefix+string(filepath.Separator)) {
		rel := strings.TrimPrefix(clean, LocalWorkPrefix)
		return filepath.Join(RemoteWorkPrefix, rel)
	}

	// 2. Check scan-repos.json mapping
	if scanCfg != nil {
		mapped := FindMappedRepo(scanCfg, clean)
		if mapped != nil {
			mappedClean := filepath.Clean(mapped.Path)
			repoBase := filepath.Base(mappedClean)
			rel, err := filepath.Rel(mappedClean, clean)
			if err == nil && rel != "." && rel != "" {
				return filepath.Join(remoteRoot, repoBase, rel)
			}
			return filepath.Join(remoteRoot, repoBase)
		}
	}

	// 3. Check work repo root
	if clean == workRoot {
		return remoteRoot
	}
	if strings.HasPrefix(clean, workRoot+string(filepath.Separator)) {
		rel := strings.TrimPrefix(clean, workRoot+string(filepath.Separator))
		return filepath.Join(remoteRoot, rel)
	}

	// 4. Translate local home directory (~/...) so remote shell uses ~ not local username
	if clean == home {
		return "~"
	}
	if strings.HasPrefix(clean, home+string(filepath.Separator)) {
		rel := strings.TrimPrefix(clean, home+string(filepath.Separator))
		return "~/" + rel
	}

	return clean
}

// ToLocalPath translates a remote enterprise path to the local path.
func ToLocalPath(remotePath string) string {
	cfg, _ := config.LoadConfig()
	scanCfg, _ := LoadScanRepos("")
	return ToLocalPathWithConfig(remotePath, cfg, scanCfg)
}

// ToLocalPathWithConfig translates remotePath using the provided config and repo scans.
func ToLocalPathWithConfig(remotePath string, cfg *config.Config, scanCfg *ScanReposConfig) string {
	if remotePath == "" {
		return ""
	}
	clean := filepath.Clean(remotePath)
	home := getHomeDir()

	remoteRoot := "~/Documents/dev/work"
	workRoot := LocalWorkPrefix
	if cfg != nil {
		if cfg.RemoteRepoRoot != "" {
			remoteRoot = cfg.RemoteRepoRoot
		}
		if cfg.WorkRepoRoot != "" {
			workRoot = cfg.WorkRepoRoot
		}
	}

	// 1. If RemoteWorkPrefix is set (e.g. in tests), honor it directly
	if clean == RemoteWorkPrefix {
		return LocalWorkPrefix
	}
	if strings.HasPrefix(clean, RemoteWorkPrefix+string(filepath.Separator)) {
		rel := strings.TrimPrefix(clean, RemoteWorkPrefix)
		return filepath.Join(LocalWorkPrefix, rel)
	}

	// 2. Check scan-repos.json for repo matching by basename
	if scanCfg != nil {
		for _, repo := range scanCfg.Repos {
			repoBase := filepath.Base(filepath.Clean(repo.Path))
			targetRemote := filepath.Join(remoteRoot, repoBase)
			if clean == targetRemote {
				return filepath.Clean(repo.Path)
			}
			if strings.HasPrefix(clean, targetRemote+string(filepath.Separator)) {
				rel := strings.TrimPrefix(clean, targetRemote+string(filepath.Separator))
				return filepath.Join(filepath.Clean(repo.Path), rel)
			}
		}
	}

	// 3. Check remoteRoot prefix
	if clean == remoteRoot {
		return workRoot
	}
	if strings.HasPrefix(clean, remoteRoot+string(filepath.Separator)) {
		rel := strings.TrimPrefix(clean, remoteRoot+string(filepath.Separator))
		return filepath.Join(workRoot, rel)
	}

	// 4. If remotePath begins with ~, translate to local home
	if clean == "~" {
		return home
	}
	if strings.HasPrefix(clean, "~/") {
		rel := strings.TrimPrefix(clean, "~/")
		return filepath.Join(home, rel)
	}

	return clean
}

// IsWorkRepo determines if the path resides under the local or remote work repo tree.
func IsWorkRepo(path string) bool {
	clean := filepath.Clean(path)
	if clean == LocalWorkPrefix ||
		strings.HasPrefix(clean, LocalWorkPrefix+string(filepath.Separator)) ||
		clean == RemoteWorkPrefix ||
		strings.HasPrefix(clean, RemoteWorkPrefix+string(filepath.Separator)) {
		return true
	}

	cfg, _ := config.LoadConfig()
	if cfg != nil {
		if cfg.WorkRepoRoot != "" {
			workClean := filepath.Clean(cfg.WorkRepoRoot)
			if clean == workClean || strings.HasPrefix(clean, workClean+string(filepath.Separator)) {
				return true
			}
		}
		if cfg.RemoteRepoRoot != "" {
			remoteClean := filepath.Clean(cfg.RemoteRepoRoot)
			if clean == remoteClean || strings.HasPrefix(clean, remoteClean+string(filepath.Separator)) {
				return true
			}
		}
	}

	scanCfg, _ := LoadScanRepos("")
	if scanCfg != nil && FindMappedRepo(scanCfg, clean) != nil {
		return true
	}

	return false
}

// ProbeResult holds connectivity test metrics for an SSH host.
type ProbeResult struct {
	Host      string        `json:"host"`
	Reachable bool          `json:"reachable"`
	Latency   time.Duration `json:"latency"`
	Error     string        `json:"error,omitempty"`
}

// ProbeSSH tests connectivity to the target host with the specified timeout (default 2s).
func ProbeSSH(ctx context.Context, host string, timeout time.Duration) ProbeResult {
	if host == "" {
		host = DefaultRemoteHost
	}
	if timeout <= 0 {
		timeout = DefaultSSHTimeout
	}

	timeoutSec := int(timeout.Seconds())
	if timeoutSec < 1 {
		timeoutSec = 2
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(probeCtx, "ssh",
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", timeoutSec),
		"-o", "StrictHostKeyChecking=accept-new",
		host,
		"true",
	)

	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		errStr := err.Error()
		if probeCtx.Err() == context.DeadlineExceeded {
			errStr = fmt.Sprintf("connection timed out after %s", timeout)
		}
		return ProbeResult{
			Host:      host,
			Reachable: false,
			Latency:   duration,
			Error:     errStr,
		}
	}

	return ProbeResult{
		Host:      host,
		Reachable: true,
		Latency:   duration,
	}
}

// CheckResult contains bridge diagnostics for a directory.
type CheckResult struct {
	Directory     string       `json:"directory"`
	LocalPath     string       `json:"local_path"`
	RemotePath    string       `json:"remote_path"`
	IsWorkRepo    bool         `json:"is_work_repo"`
	MappedRepo    *RepoMapping `json:"mapped_repo,omitempty"`
	RemoteHost    string       `json:"remote_host"`
	Probe         ProbeResult  `json:"probe"`
	RouteDecision string       `json:"route_decision"`
}

// Check inspects directory mapping, path translation, and remote SSH connectivity.
func Check(ctx context.Context, dir string, host string) (*CheckResult, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	if host == "" {
		host = DefaultRemoteHost
	}

	localPath := ToLocalPath(absDir)
	remotePath := ToRemotePath(absDir)
	isWork := IsWorkRepo(absDir)

	scanCfg, _ := LoadScanRepos("")
	mappedRepo := FindMappedRepo(scanCfg, localPath)

	probe := ProbeSSH(ctx, host, DefaultSSHTimeout)

	decision := "local (standalone)"
	if isWork || mappedRepo != nil {
		if probe.Reachable {
			decision = fmt.Sprintf("remote (%s)", host)
		} else {
			decision = fmt.Sprintf("local fallback (%s unreachable)", host)
		}
	} else if probe.Reachable {
		decision = "local (non-work repo)"
	}

	return &CheckResult{
		Directory:     absDir,
		LocalPath:     localPath,
		RemotePath:    remotePath,
		IsWorkRepo:    isWork,
		MappedRepo:    mappedRepo,
		RemoteHost:    host,
		Probe:         probe,
		RouteDecision: decision,
	}, nil
}

// LaunchOptions configures command or session launching across the bridge.
type LaunchOptions struct {
	Host       string
	TargetDir  string
	Args       []string
	Timeout    time.Duration
	ForceLocal bool
}

// QuoteForShell returns a single-quoted shell argument safe for remote bash/zsh execution.
func QuoteForShell(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func quoteForShell(arg string) string {
	return QuoteForShell(arg)
}

// ShellPathForDir formats a directory path for safe remote shell execution.
// If the path begins with ~, it wraps $HOME so the remote shell expands ~ without quoting issues.
func ShellPathForDir(path string) string {
	clean := filepath.Clean(path)
	if clean == "~" {
		return `"$HOME"`
	}
	if strings.HasPrefix(clean, "~/") {
		sub := strings.TrimPrefix(clean, "~/")
		return fmt.Sprintf(`"$HOME"/%s`, quoteForShell(sub))
	}
	return quoteForShell(clean)
}

// Launch executes remote commands or interactive Claude sessions over SSH,
// falling back to local execution without closing the shell.
func Launch(ctx context.Context, opts LaunchOptions) error {
	if opts.Host == "" {
		opts.Host = DefaultRemoteHost
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultSSHTimeout
	}

	if opts.TargetDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		opts.TargetDir = cwd
	}

	absDir, err := filepath.Abs(opts.TargetDir)
	if err != nil {
		absDir = opts.TargetDir
	}

	localDir := ToLocalPath(absDir)
	remoteDir := ToRemotePath(absDir)

	// Default to interactive Claude session if no command specified
	execArgs := opts.Args
	if len(execArgs) == 0 {
		execArgs = []string{"claude"}
	}

	// If force local requested, bypass probe
	if opts.ForceLocal {
		fmt.Printf("\033[1;33m[bridge]\033[0m Force local mode requested. Executing locally at: %s\n", localDir)
		return executeLocally(ctx, localDir, execArgs)
	}

	// Probe remote host
	fmt.Printf("\033[1;36m[bridge]\033[0m Probing SSH connectivity to %s (timeout: %s)...\n", opts.Host, opts.Timeout)
	probe := ProbeSSH(ctx, opts.Host, opts.Timeout)

	if probe.Reachable {
		fmt.Printf("\033[1;32m✔ [bridge]\033[0m Connected to %s (%s).\n",
			opts.Host, probe.Latency.Round(time.Millisecond))

		// Pre-flight background transcript sync: pull any remote Claude transcripts into local telemetry
		syncRemoteTranscripts(ctx, opts.Host)

		// Initialize Reverse Bridge Server for transparent file fetching
		clientHome := getHomeDir()
		sessionToken := GenerateSessionToken()
		bridgePort, shutdownServer, err := StartReverseBridgeServer(clientHome, sessionToken, 4119)
		if err == nil {
			defer shutdownServer()

			hostname, _ := os.Hostname()
			remoteSession := BridgeSession{
				ClientUser: os.Getenv("USER"),
				ClientHome: clientHome,
				ClientHost: hostname,
				BridgePort: bridgePort,
				Token:      sessionToken,
				Active:     true,
			}
			deployRemoteSession(ctx, opts.Host, remoteSession)
			defer cleanupRemoteSession(ctx, opts.Host)
		}

		fmt.Printf("\033[1;36m[bridge]\033[0m Executing remotely at %s (reverse bridge active on port %d)...\n\n", remoteDir, bridgePort)
		return executeRemotely(ctx, opts.Host, remoteDir, execArgs, bridgePort)
	}

	// Remote unreachable: Fall back to local execution without closing shell
	reason := probe.Error
	if reason == "" {
		reason = "host unreachable"
	}
	fmt.Fprintf(os.Stderr, "\033[1;31m✖ [bridge]\033[0m Remote host %s unreachable (%s).\n", opts.Host, reason)
	fmt.Fprintf(os.Stderr, "\033[1;33m⚡ [bridge]\033[0m Falling back to local execution at %s (shell maintained)...\n\n", localDir)

	return executeLocally(ctx, localDir, execArgs)
}

// syncRemoteTranscripts pulls remote Claude Code project transcripts into local storage
func syncRemoteTranscripts(ctx context.Context, host string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	localProjectsDir := filepath.Join(home, ".claude", "projects")
	remoteProjectsDir := fmt.Sprintf("%s:~/.claude/projects/", host)

	syncCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(syncCtx, "rsync", "-az", "--update", "--exclude=*.lock", remoteProjectsDir, localProjectsDir)
	_ = cmd.Run()
}

func executeRemotely(ctx context.Context, host, remoteDir string, args []string, reversePort int) error {
	var quoted []string
	for _, a := range args {
		quoted = append(quoted, quoteForShell(a))
	}
	cmdString := strings.Join(quoted, " ")

	// Sanitize session name from repo directory (e.g. "mesh-api-service")
	sessionName := "mesh-" + filepath.Base(remoteDir)
	sessionName = strings.ReplaceAll(sessionName, ".", "-")
	sessionName = strings.ReplaceAll(sessionName, ":", "-")

	remoteCdPath := ShellPathForDir(remoteDir)

	// Smart remote script:
	// If tmux is installed on remote node:
	//   1. Check if session exists -> attach to it (`tmux attach-session -t <name>`)
	//   2. Else create new detached session with the command and attach (`tmux new-session -s <name> ...`)
	// If tmux is not installed on remote node:
	//   Directly executes `cd <dir> && <cmd>`
	remoteScript := fmt.Sprintf(`
export PATH="$HOME/.local/bin:$HOME/.local/share/claude:$HOME/.cargo/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:$PATH"
[ -f "$HOME/.zprofile" ] && source "$HOME/.zprofile" >/dev/null 2>&1
[ "$TERM" = "dumb" ] || [ -z "$TERM" ] && export TERM=xterm-256color
if command -v tmux >/dev/null 2>&1; then
    if tmux has-session -t %s 2>/dev/null; then
        echo -e "\033[1;36m[bridge]\033[0m Re-attaching to existing remote tmux session: \033[1;32m%s\033[0m"
        exec tmux attach-session -t %s
    else
        echo -e "\033[1;36m[bridge]\033[0m Spawning persistent remote tmux session: \033[1;32m%s\033[0m"
        cd %s && exec tmux new-session -s %s %s
    fi
else
    cd %s && %s
fi`,
		quoteForShell(sessionName),
		sessionName,
		quoteForShell(sessionName),
		sessionName,
		remoteCdPath,
		quoteForShell(sessionName),
		cmdString,
		remoteCdPath,
		cmdString,
	)

	// Build SSH arguments with reverse tunnel forwarding if active
	sshArgs := []string{"-t"}
	if reversePort > 0 {
		sshArgs = append(sshArgs, "-R", fmt.Sprintf("%d:127.0.0.1:%d", reversePort, reversePort))
	}
	sshArgs = append(sshArgs, host, remoteScript)

	sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr

	return sshCmd.Run()
}

func deployRemoteSession(ctx context.Context, host string, session BridgeSession) {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return
	}
	deployScript := fmt.Sprintf(`
mkdir -p ~/.agent-mesh /tmp/mesh-cache
cat << 'EOF' > ~/.agent-mesh/bridge-session.json
%s
EOF
`, string(data))
	cmd := exec.CommandContext(ctx, "ssh", host, "bash -s")
	cmd.Stdin = strings.NewReader(deployScript)
	_ = cmd.Run()
}

func cleanupRemoteSession(ctx context.Context, host string) {
	cleanupScript := `
if [ -f ~/.agent-mesh/bridge-session.json ]; then
    rm -f ~/.agent-mesh/bridge-session.json
fi
`
	cmd := exec.CommandContext(ctx, "ssh", host, "bash -s")
	cmd.Stdin = strings.NewReader(cleanupScript)
	_ = cmd.Run()
}

func executeLocally(ctx context.Context, localDir string, args []string) error {
	bin := args[0]
	cmdArgs := args[1:]

	cmd := exec.CommandContext(ctx, bin, cmdArgs...)
	cmd.Dir = localDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
