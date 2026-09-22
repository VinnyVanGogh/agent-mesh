package doctor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/VinnyVanGogh/agent-mesh/internal/bridge"
	"github.com/VinnyVanGogh/agent-mesh/internal/config"
	_ "modernc.org/sqlite"
)

// CurrentVersion is the default local version of agent-mesh.
var CurrentVersion = "0.1.0"

// Status represents the health status of a check.
type Status string

const (
	StatusOK      Status = "OK"
	StatusWarn    Status = "WARN"
	StatusError   Status = "ERROR"
	StatusSkipped Status = "SKIPPED"
)

// Tokyo Night color palette constants.
const (
	ColorCyan    = "\033[38;2;125;207;255m"
	ColorBlue    = "\033[38;2;122;162;247m"
	ColorPurple  = "\033[38;2;187;154;247m"
	ColorGreen   = "\033[38;2;158;206;106m"
	ColorYellow  = "\033[38;2;224;175;104m"
	ColorRed     = "\033[38;2;247;118;142m"
	ColorOrange  = "\033[38;2;255;158;100m"
	ColorGray    = "\033[38;2;86;95;137m"
	ColorTeal    = "\033[38;2;115;218;202m"
	ColorDim     = "\033[2m"
	ColorBold    = "\033[1m"
	ColorReset   = "\033[0m"

	CheckmarkOK   = "\033[38;2;158;206;106m✔\033[0m"
	CheckmarkWarn = "\033[38;2;224;175;104m⚠\033[0m"
	CheckmarkErr  = "\033[38;2;247;118;142m✖\033[0m"
	CheckmarkSkip = "\033[38;2;86;95;137m○\033[0m"
)

// CheckResult records the outcome of a single diagnostic probe.
type CheckResult struct {
	Name        string        `json:"name"`
	Status      Status        `json:"status"`
	Message     string        `json:"message"`
	Latency     time.Duration `json:"latency,omitempty"`
	Remediation string        `json:"remediation,omitempty"`
}

// SectionResult groups diagnostic checks for a specific subsystem.
type SectionResult struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Status Status        `json:"status"`
	Checks []CheckResult `json:"checks"`
}

// ReportSummary aggregates diagnostic statistics.
type ReportSummary struct {
	Total       int           `json:"total"`
	Passed      int           `json:"passed"`
	Warnings    int           `json:"warnings"`
	Errors      int           `json:"errors"`
	Skipped     int           `json:"skipped"`
	Duration    time.Duration `json:"duration"`
	DurationStr string        `json:"duration_str"`
}

// Report represents the complete fleet diagnostic evaluation.
type Report struct {
	RemoteHost string          `json:"remote_host"`
	Fast       bool            `json:"fast"`
	Sections   []SectionResult `json:"sections"`
	Summary    ReportSummary   `json:"summary"`
}

// DoctorOptions configures which diagnostics the FleetDoctor runs.
type DoctorOptions struct {
	Fast        bool           `json:"fast"`
	ClaudeOnly  bool           `json:"claude_only"`
	GeminiOnly  bool           `json:"gemini_only"`
	RemoteOnly  bool           `json:"remote_only"`
	RemoteHost  string         `json:"remote_host"`
	Config      *config.Config `json:"-"`
}

// FleetDoctor executes modular fleet health diagnostics.
type FleetDoctor struct {
	opts         DoctorOptions
	localVersion string
	remoteHost   string
	cfg          *config.Config

	// Pluggable functions for testing & isolation
	LocalVersionFunc     func() (string, error)
	RemoteVersionFunc    func(ctx context.Context, host string) (string, time.Duration, error)
	ProbeSSHFunc         func(ctx context.Context, host string, timeout time.Duration) bridge.ProbeResult
	CheckPortFunc        func(port int) (bool, string, time.Duration, error)
	LocalFileHashFunc    func(path string) (string, error)
	RemoteFileHashFunc   func(ctx context.Context, host string, path string) (string, time.Duration, error)
	CheckTelemetryDBFunc func(path string) error
	CheckStateAgeFunc    func(path string) (time.Duration, error)
	ClaudeDoctorFunc     func(ctx context.Context) (string, time.Duration, error)
	LookPathFunc         func(file string) (string, error)
	ReadFileFunc         func(filename string) ([]byte, error)
	StatFileFunc         func(name string) (os.FileInfo, error)
	RemoteCommandFunc    func(ctx context.Context, host string, cmd string) (string, time.Duration, error)
}

// NewFleetDoctor initializes a FleetDoctor with production defaults.
func NewFleetDoctor(opts DoctorOptions) *FleetDoctor {
	cfg := opts.Config
	if cfg == nil {
		cfg, _ = config.LoadConfig()
	}

	remoteHost := opts.RemoteHost
	if remoteHost == "" && cfg != nil && cfg.RemoteHost != "" {
		remoteHost = cfg.RemoteHost
	}
	if remoteHost == "" {
		remoteHost = "mansol-mbp"
	}

	d := &FleetDoctor{
		opts:         opts,
		localVersion: CurrentVersion,
		remoteHost:   remoteHost,
		cfg:          cfg,
		LookPathFunc: exec.LookPath,
		ReadFileFunc: os.ReadFile,
		StatFileFunc: os.Stat,
	}

	d.LocalVersionFunc = func() (string, error) {
		return d.localVersion, nil
	}

	d.RemoteVersionFunc = func(ctx context.Context, host string) (string, time.Duration, error) {
		start := time.Now()
		out, err := d.execSSH(ctx, host, "~/.local/bin/mesh version")
		dur := time.Since(start)
		if err != nil {
			return "", dur, err
		}
		// Expect e.g. "mesh version 0.1.0"
		fields := strings.Fields(strings.TrimSpace(out))
		if len(fields) > 0 {
			return fields[len(fields)-1], dur, nil
		}
		return strings.TrimSpace(out), dur, nil
	}

	d.ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) bridge.ProbeResult {
		return bridge.ProbeSSH(ctx, host, timeout)
	}

	d.CheckPortFunc = func(port int) (bool, string, time.Duration, error) {
		start := time.Now()
		// 1. Check if an active reverse bridge server is already responding
		client := &http.Client{Timeout: 300 * time.Millisecond}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/ping", port))
		if err == nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			dur := time.Since(start)
			if strings.TrimSpace(string(body)) == "pong" {
				return true, "Loopback port 4119 active (mesh reverse bridge server running)", dur, nil
			}
		}

		// 2. Test binding to verify availability
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		dur := time.Since(start)
		if err == nil {
			_ = ln.Close()
			return true, "Loopback port 4119 available for reverse bridge", dur, nil
		}

		return false, fmt.Sprintf("Loopback port %d unavailable: %v", port, err), dur, err
	}

	d.LocalFileHashFunc = func(path string) (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]), nil
	}

	d.RemoteFileHashFunc = func(ctx context.Context, host string, path string) (string, time.Duration, error) {
		start := time.Now()
		cmd := fmt.Sprintf("shasum -a 256 %s 2>/dev/null || sha256sum %s 2>/dev/null", path, path)
		out, err := d.execSSH(ctx, host, cmd)
		dur := time.Since(start)
		if err != nil {
			return "", dur, err
		}
		fields := strings.Fields(strings.TrimSpace(out))
		if len(fields) == 0 {
			return "", dur, fmt.Errorf("empty sha256 output from remote host")
		}
		return fields[0], dur, nil
	}

	d.CheckTelemetryDBFunc = func(path string) error {
		if _, err := os.Stat(path); err != nil {
			return err
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return err
		}
		defer db.Close()
		var count int
		return db.QueryRow("SELECT COUNT(*) FROM sqlite_master").Scan(&count)
	}

	d.CheckStateAgeFunc = func(path string) (time.Duration, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}

		data, err := os.ReadFile(path)
		if err == nil {
			var raw struct {
				Quotas map[string]struct {
					LastUpdated string `json:"last_updated"`
				} `json:"quotas"`
			}
			if json.Unmarshal(data, &raw) == nil && raw.Quotas != nil {
				for _, q := range raw.Quotas {
					if q.LastUpdated != "" {
						if t, parseErr := time.Parse(time.RFC3339, q.LastUpdated); parseErr == nil {
							return time.Since(t), nil
						}
						if t, parseErr := time.Parse("2006-01-02T15:04:05.999999999-07:00", q.LastUpdated); parseErr == nil {
							return time.Since(t), nil
						}
					}
				}
			}
		}

		return time.Since(info.ModTime()), nil
	}

	d.ClaudeDoctorFunc = func(ctx context.Context) (string, time.Duration, error) {
		start := time.Now()
		cmdCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, "claude", "doctor")
		out, err := cmd.CombinedOutput()
		dur := time.Since(start)
		return string(out), dur, err
	}

	d.RemoteCommandFunc = func(ctx context.Context, host string, cmd string) (string, time.Duration, error) {
		start := time.Now()
		out, err := d.execSSH(ctx, host, cmd)
		return out, time.Since(start), err
	}

	return d
}

// SetLocalVersion overrides the local version reported by the doctor.
func (d *FleetDoctor) SetLocalVersion(v string) {
	d.localVersion = v
}

// execSSH runs an SSH command with BatchMode and ConnectTimeout.
func (d *FleetDoctor) execSSH(ctx context.Context, host string, remoteCmd string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=2",
		"-o", "StrictHostKeyChecking=accept-new",
		host,
		remoteCmd,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func getHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// Run executes the diagnostic suite based on configured options.
func (d *FleetDoctor) Run(ctx context.Context) (*Report, error) {
	startTime := time.Now()
	report := &Report{
		RemoteHost: d.remoteHost,
		Fast:       d.opts.Fast,
		Sections:   make([]SectionResult, 0),
	}

	runAll := !d.opts.ClaudeOnly && !d.opts.GeminiOnly && !d.opts.RemoteOnly

	// Section 1: Agent-Mesh Infrastructure
	if runAll {
		report.Sections = append(report.Sections, d.checkInfrastructure(ctx))
	}

	// Section 2: Claude Code Health
	if runAll || d.opts.ClaudeOnly {
		report.Sections = append(report.Sections, d.checkClaudeCode(ctx))
	}

	// Section 3: AGY / Gemini Health
	if runAll || d.opts.GeminiOnly {
		report.Sections = append(report.Sections, d.checkGemini(ctx))
	}

	// Section 4: Remote Node Health
	if runAll || d.opts.RemoteOnly {
		report.Sections = append(report.Sections, d.checkRemoteNode(ctx))
	}

	// Compute summary stats
	for _, sec := range report.Sections {
		for _, chk := range sec.Checks {
			report.Summary.Total++
			switch chk.Status {
			case StatusOK:
				report.Summary.Passed++
			case StatusWarn:
				report.Summary.Warnings++
			case StatusError:
				report.Summary.Errors++
			case StatusSkipped:
				report.Summary.Skipped++
			}
		}
	}
	report.Summary.Duration = time.Since(startTime)
	report.Summary.DurationStr = formatDuration(report.Summary.Duration)

	return report, nil
}

// checkInfrastructure runs Section a) Agent-Mesh Infrastructure diagnostics.
func (d *FleetDoctor) checkInfrastructure(ctx context.Context) SectionResult {
	sec := SectionResult{
		ID:     "infrastructure",
		Name:   "Agent-Mesh Infrastructure",
		Status: StatusOK,
		Checks: make([]CheckResult, 0),
	}

	// 1. Version parity
	chkVersion := CheckResult{Name: "Version Parity"}
	if d.opts.Fast {
		chkVersion.Status = StatusSkipped
		chkVersion.Message = "Remote version check skipped (--fast mode)"
	} else {
		localVer, err := d.LocalVersionFunc()
		if err != nil {
			chkVersion.Status = StatusError
			chkVersion.Message = fmt.Sprintf("Failed to get local version: %v", err)
		} else {
			remoteVer, lat, err := d.RemoteVersionFunc(ctx, d.remoteHost)
			chkVersion.Latency = lat
			if err != nil {
				chkVersion.Status = StatusWarn
				chkVersion.Message = fmt.Sprintf("Remote mesh version unavailable on %s: %v", d.remoteHost, err)
				chkVersion.Remediation = fmt.Sprintf("Ensure mesh is installed at %s:~/.local/bin/mesh", d.remoteHost)
			} else if localVer != remoteVer {
				chkVersion.Status = StatusWarn
				chkVersion.Message = fmt.Sprintf("Version mismatch: local v%s != remote v%s", localVer, remoteVer)
				chkVersion.Remediation = fmt.Sprintf("Update remote binary: scp bin/mesh %s:~/.local/bin/mesh", d.remoteHost)
			} else {
				chkVersion.Status = StatusOK
				chkVersion.Message = fmt.Sprintf("Parity verified: v%s (local == remote)", localVer)
			}
		}
	}
	sec.Checks = append(sec.Checks, chkVersion)

	// 2. SSH Connectivity
	chkSSH := CheckResult{Name: "SSH Connectivity"}
	if d.opts.Fast {
		chkSSH.Status = StatusSkipped
		chkSSH.Message = "SSH probe skipped (--fast mode)"
	} else {
		probe := d.ProbeSSHFunc(ctx, d.remoteHost, 2*time.Second)
		chkSSH.Latency = probe.Latency
		if probe.Reachable {
			chkSSH.Status = StatusOK
			chkSSH.Message = fmt.Sprintf("%s reachable", d.remoteHost)
		} else {
			chkSSH.Status = StatusError
			chkSSH.Message = fmt.Sprintf("%s unreachable: %s", d.remoteHost, probe.Error)
			chkSSH.Remediation = fmt.Sprintf("Verify Tailscale, SSH daemon, or ~/.ssh/config entry for %s", d.remoteHost)
		}
	}
	sec.Checks = append(sec.Checks, chkSSH)

	// 3. Reverse Tunnel
	chkPort := CheckResult{Name: "Reverse Tunnel"}
	ok, msg, lat, _ := d.CheckPortFunc(4119)
	chkPort.Latency = lat
	chkPort.Message = msg
	if ok {
		chkPort.Status = StatusOK
	} else {
		chkPort.Status = StatusWarn
		chkPort.Remediation = "Inspect and free port 4119 using `lsof -i :4119`"
	}
	sec.Checks = append(sec.Checks, chkPort)

	// 4. Instruction Parity
	chkInst := CheckResult{Name: "Instruction Parity"}
	if d.opts.Fast {
		chkInst.Status = StatusSkipped
		chkInst.Message = "Instruction parity check skipped (--fast mode)"
	} else {
		home := getHome()
		localPath := filepath.Join(home, ".agents", "BASE_INSTRUCTIONS.md")
		localHash, err := d.LocalFileHashFunc(localPath)
		if err != nil {
			chkInst.Status = StatusWarn
			chkInst.Message = fmt.Sprintf("Local instructions missing: %v", err)
			chkInst.Remediation = "Ensure ~/.agents/BASE_INSTRUCTIONS.md exists locally"
		} else {
			remoteHash, lat, err := d.RemoteFileHashFunc(ctx, d.remoteHost, "~/.agents/BASE_INSTRUCTIONS.md")
			chkInst.Latency = lat
			if err != nil {
				chkInst.Status = StatusWarn
				chkInst.Message = fmt.Sprintf("Remote instructions unreadable on %s: %v", d.remoteHost, err)
				chkInst.Remediation = fmt.Sprintf("Sync instructions: scp ~/.agents/BASE_INSTRUCTIONS.md %s:~/.agents/", d.remoteHost)
			} else if localHash != remoteHash {
				chkInst.Status = StatusWarn
				chkInst.Message = fmt.Sprintf("Instruction drift detected (local: %s... vs remote: %s...)", localHash[:8], remoteHash[:8])
				chkInst.Remediation = fmt.Sprintf("Sync instructions to remote: scp ~/.agents/BASE_INSTRUCTIONS.md %s:~/.agents/", d.remoteHost)
			} else {
				chkInst.Status = StatusOK
				chkInst.Message = fmt.Sprintf("Instruction parity: SHA256 %s... matches", localHash[:8])
			}
		}
	}
	sec.Checks = append(sec.Checks, chkInst)

	// 5. Database & Quota
	chkDB := CheckResult{Name: "Database & Quota"}
	telemetryPath := filepath.Join(getHome(), ".config", "token-telemetry", "telemetry.db")
	if d.cfg != nil && d.cfg.TelemetryDBPath != "" {
		telemetryPath = d.cfg.TelemetryDBPath
	}

	dbErr := d.CheckTelemetryDBFunc(telemetryPath)
	statePath := filepath.Join(getHome(), ".config", "rate-limits", "state.json")
	stateAge, stateErr := d.CheckStateAgeFunc(statePath)

	if dbErr != nil {
		chkDB.Status = StatusError
		chkDB.Message = fmt.Sprintf("Telemetry DB unreadable (%s): %v", telemetryPath, dbErr)
		chkDB.Remediation = "Initialize token telemetry database or run `mesh init`"
	} else if stateErr != nil {
		chkDB.Status = StatusWarn
		chkDB.Message = fmt.Sprintf("Telemetry DB readable, but rate limits state missing: %v", stateErr)
		chkDB.Remediation = "Run statusline poll or prompt hook to generate state.json"
	} else if stateAge > 24*time.Hour {
		chkDB.Status = StatusWarn
		chkDB.Message = fmt.Sprintf("Telemetry DB readable; rate limits state is stale (%s old)", formatDuration(stateAge))
		chkDB.Remediation = "Run `mesh statusline` or trigger prompt hook to refresh telemetry"
	} else {
		chkDB.Status = StatusOK
		chkDB.Message = fmt.Sprintf("Telemetry DB readable, state.json fresh (%s old)", formatDuration(stateAge))
	}
	sec.Checks = append(sec.Checks, chkDB)

	sec.Status = aggregateStatus(sec.Checks)
	return sec
}

// checkClaudeCode runs Section b) Claude Code Health diagnostics.
func (d *FleetDoctor) checkClaudeCode(ctx context.Context) SectionResult {
	sec := SectionResult{
		ID:     "claude",
		Name:   "Claude Code Health",
		Status: StatusOK,
		Checks: make([]CheckResult, 0),
	}

	// 1. Claude CLI binary in PATH
	chkCLI := CheckResult{Name: "Claude CLI"}
	claudePath, err := d.LookPathFunc("claude")
	if err != nil {
		chkCLI.Status = StatusError
		chkCLI.Message = "Claude CLI ('claude') not found in PATH"
		chkCLI.Remediation = "Install Claude Code CLI globally or add ~/.local/bin to PATH"
	} else {
		chkCLI.Status = StatusOK
		chkCLI.Message = fmt.Sprintf("CLI binary found at %s", claudePath)
	}
	sec.Checks = append(sec.Checks, chkCLI)

	// 2. Claude Doctor execution
	chkDoctor := CheckResult{Name: "Claude Doctor"}
	if chkCLI.Status == StatusError {
		chkDoctor.Status = StatusSkipped
		chkDoctor.Message = "Skipped (Claude CLI not found)"
	} else {
		docOut, lat, docErr := d.ClaudeDoctorFunc(ctx)
		chkDoctor.Latency = lat
		if docErr == nil || strings.Contains(docOut, "No installation issues found") {
			chkDoctor.Status = StatusOK
			chkDoctor.Message = "Installation health checks passed"
		} else {
			chkDoctor.Status = StatusWarn
			chkDoctor.Message = fmt.Sprintf("Claude doctor reported warnings: %v", docErr)
			chkDoctor.Remediation = "Run `claude doctor` in terminal or `/doctor` in Claude session"
		}
	}
	sec.Checks = append(sec.Checks, chkDoctor)

	// 3. Claude Authentication Token
	chkAuth := CheckResult{Name: "Claude Authentication"}
	claudeJSONPath := filepath.Join(getHome(), ".claude.json")
	claudeJSONData, err := d.ReadFileFunc(claudeJSONPath)
	if err != nil {
		chkAuth.Status = StatusWarn
		chkAuth.Message = fmt.Sprintf("~/.claude.json missing or unreadable: %v", err)
		chkAuth.Remediation = "Run `claude login` to authenticate"
	} else {
		var raw map[string]interface{}
		if err := json.Unmarshal(claudeJSONData, &raw); err != nil {
			chkAuth.Status = StatusWarn
			chkAuth.Message = "Invalid JSON in ~/.claude.json"
			chkAuth.Remediation = "Restore valid JSON structure in ~/.claude.json"
		} else {
			hasAuth := raw["oauthAccount"] != nil ||
				raw["sessionKey"] != nil ||
				raw["tokens"] != nil ||
				raw["claudeCodeFirstTokenDate"] != nil
			if hasAuth {
				chkAuth.Status = StatusOK
				chkAuth.Message = "Active authentication session present in ~/.claude.json"
			} else {
				chkAuth.Status = StatusWarn
				chkAuth.Message = "No active authentication session found in ~/.claude.json"
				chkAuth.Remediation = "Run `claude login` to authenticate"
			}
		}
	}
	sec.Checks = append(sec.Checks, chkAuth)

	// 4. Claude Settings & Mesh Hook
	chkSettings := CheckResult{Name: "Claude Settings & Hooks"}
	settingsPath := filepath.Join(getHome(), ".claude", "settings.json")
	settingsData, err := d.ReadFileFunc(settingsPath)
	if err != nil {
		chkSettings.Status = StatusError
		chkSettings.Message = fmt.Sprintf("~/.claude/settings.json not found: %v", err)
		chkSettings.Remediation = "Create ~/.claude/settings.json or run `mesh init`"
	} else {
		var settings struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(settingsData, &settings); err != nil {
			chkSettings.Status = StatusError
			chkSettings.Message = fmt.Sprintf("Invalid JSON in ~/.claude/settings.json: %v", err)
			chkSettings.Remediation = "Fix syntax error in ~/.claude/settings.json"
		} else {
			hasMeshHook := false
			if promptHooks, ok := settings.Hooks["UserPromptSubmit"]; ok {
				for _, wrapper := range promptHooks {
					for _, h := range wrapper.Hooks {
						if strings.Contains(h.Command, "mesh hook prompt") {
							hasMeshHook = true
							break
						}
					}
					if hasMeshHook {
						break
					}
				}
			}

			if hasMeshHook {
				chkSettings.Status = StatusOK
				chkSettings.Message = "settings.json valid, 'mesh hook prompt' hook registered"
			} else {
				chkSettings.Status = StatusWarn
				chkSettings.Message = "settings.json valid, but 'mesh hook prompt' hook is not registered"
				chkSettings.Remediation = "Add 'mesh hook prompt' to UserPromptSubmit hooks in ~/.claude/settings.json"
			}
		}
	}
	sec.Checks = append(sec.Checks, chkSettings)

	sec.Status = aggregateStatus(sec.Checks)
	return sec
}

// checkGemini runs Section c) AGY / Gemini Health diagnostics.
func (d *FleetDoctor) checkGemini(ctx context.Context) SectionResult {
	sec := SectionResult{
		ID:     "gemini",
		Name:   "AGY / Gemini Health",
		Status: StatusOK,
		Checks: make([]CheckResult, 0),
	}

	home := getHome()

	// 1. ~/.gemini/config directory exists
	chkDir := CheckResult{Name: "Gemini Config Directory"}
	geminiConfigDir := filepath.Join(home, ".gemini", "config")
	dirStat, err := d.StatFileFunc(geminiConfigDir)
	if err != nil || !dirStat.IsDir() {
		chkDir.Status = StatusWarn
		chkDir.Message = "Directory ~/.gemini/config missing"
		chkDir.Remediation = "Create directory: mkdir -p ~/.gemini/config"
	} else {
		chkDir.Status = StatusOK
		chkDir.Message = "Config directory present (~/.gemini/config)"
	}
	sec.Checks = append(sec.Checks, chkDir)

	// 2. MCP Server configurations in mcp_config.json
	chkMCP := CheckResult{Name: "MCP Servers"}
	mcpPath := filepath.Join(geminiConfigDir, "mcp_config.json")
	mcpData, err := d.ReadFileFunc(mcpPath)
	if err != nil {
		chkMCP.Status = StatusWarn
		chkMCP.Message = "mcp_config.json missing or unreadable"
		chkMCP.Remediation = "Configure MCP servers in ~/.gemini/config/mcp_config.json"
	} else {
		var mcpCfg struct {
			MCPServers map[string]interface{} `json:"mcpServers"`
		}
		if err := json.Unmarshal(mcpData, &mcpCfg); err != nil {
			chkMCP.Status = StatusWarn
			chkMCP.Message = "Invalid JSON in ~/.gemini/config/mcp_config.json"
			chkMCP.Remediation = "Fix JSON syntax in ~/.gemini/config/mcp_config.json"
		} else {
			count := len(mcpCfg.MCPServers)
			if count > 0 {
				chkMCP.Status = StatusOK
				chkMCP.Message = fmt.Sprintf("%d MCP server(s) configured in mcp_config.json", count)
			} else {
				chkMCP.Status = StatusWarn
				chkMCP.Message = "No MCP servers configured in mcp_config.json"
				chkMCP.Remediation = "Add MCP server definitions to ~/.gemini/config/mcp_config.json"
			}
		}
	}
	sec.Checks = append(sec.Checks, chkMCP)

	// 3. Google AI Ultra Quota State
	chkQuota := CheckResult{Name: "Google AI Ultra Quota"}
	statePath := filepath.Join(home, ".config", "rate-limits", "state.json")
	stateData, err := d.ReadFileFunc(statePath)
	if err != nil {
		chkQuota.Status = StatusOK
		chkQuota.Message = "Google AI Ultra tier configured (no active lockouts)"
	} else {
		var raw struct {
			Lockouts map[string]struct {
				Locked bool `json:"locked"`
			} `json:"lockouts"`
			Quotas map[string]struct {
				FiveHourUsed      float64 `json:"five_hour_used"`
				FiveHourRemaining float64 `json:"five_hour_remaining"`
			} `json:"quotas"`
		}
		if err := json.Unmarshal(stateData, &raw); err == nil {
			isLocked := false
			for k, l := range raw.Lockouts {
				if strings.Contains(strings.ToLower(k), "gemini") || strings.Contains(strings.ToLower(k), "google") {
					if l.Locked {
						isLocked = true
						break
					}
				}
			}

			if isLocked {
				chkQuota.Status = StatusError
				chkQuota.Message = "Google AI Ultra quota locked!"
				chkQuota.Remediation = "Quota exhausted: switch to 3P Claude via /model claude-sonnet-4-6 or wait for reset"
			} else {
				var remaining float64 = 100
				found := false
				for k, q := range raw.Quotas {
					if strings.Contains(strings.ToLower(k), "gemini") {
						remaining = q.FiveHourRemaining
						found = true
						break
					}
				}

				if found && remaining <= 10.0 {
					chkQuota.Status = StatusWarn
					chkQuota.Message = fmt.Sprintf("Google AI Ultra quota low (%.1f%% remaining rolling 5h)", remaining)
					chkQuota.Remediation = "Pace turns or failover to 3P Claude via /model claude-sonnet-4-6"
				} else if found {
					chkQuota.Status = StatusOK
					chkQuota.Message = fmt.Sprintf("Quota healthy (%.1f%% remaining rolling 5h)", remaining)
				} else {
					chkQuota.Status = StatusOK
					chkQuota.Message = "Google AI Ultra tier configured (no active lockouts)"
				}
			}
		} else {
			chkQuota.Status = StatusOK
			chkQuota.Message = "Google AI Ultra tier configured"
		}
	}
	sec.Checks = append(sec.Checks, chkQuota)

	// 4. .caveman-mode flag
	chkCaveman := CheckResult{Name: "Caveman Mode"}
	cavemanPaths := []string{
		filepath.Join(home, ".claude", ".caveman-mode"),
		filepath.Join(home, ".gemini", ".caveman-mode"),
		filepath.Join(home, ".caveman-mode"),
	}
	hasCaveman := false
	for _, p := range cavemanPaths {
		if _, err := d.StatFileFunc(p); err == nil {
			hasCaveman = true
			break
		}
	}
	if hasCaveman {
		chkCaveman.Status = StatusOK
		chkCaveman.Message = "Active (.caveman-mode flag detected)"
	} else {
		chkCaveman.Status = StatusWarn
		chkCaveman.Message = "Inactive (.caveman-mode flag not detected)"
		chkCaveman.Remediation = "Enable caveman mode: touch ~/.claude/.caveman-mode"
	}
	sec.Checks = append(sec.Checks, chkCaveman)

	sec.Status = aggregateStatus(sec.Checks)
	return sec
}

// checkRemoteNode runs Section d) Remote Node Health diagnostics.
func (d *FleetDoctor) checkRemoteNode(ctx context.Context) SectionResult {
	sec := SectionResult{
		ID:     "remote",
		Name:   fmt.Sprintf("Remote Node Health (%s)", d.remoteHost),
		Status: StatusOK,
		Checks: make([]CheckResult, 0),
	}

	if d.opts.Fast {
		chkFast := CheckResult{
			Name:    "Remote Checks",
			Status:  StatusSkipped,
			Message: "Remote node checks skipped (--fast mode)",
		}
		sec.Checks = append(sec.Checks, chkFast)
		sec.Status = StatusSkipped
		return sec
	}

	// 1. Remote Claude Settings & Hook
	chkSettings := CheckResult{Name: "Remote Claude Settings & Hook"}
	remoteSettingsOut, lat, err := d.RemoteCommandFunc(ctx, d.remoteHost, "cat ~/.claude/settings.json 2>/dev/null")
	chkSettings.Latency = lat
	if err != nil || strings.TrimSpace(remoteSettingsOut) == "" {
		chkSettings.Status = StatusError
		chkSettings.Message = fmt.Sprintf("Unable to read ~/.claude/settings.json on %s: %v", d.remoteHost, err)
		chkSettings.Remediation = fmt.Sprintf("Verify ~/.claude/settings.json exists and is readable on %s", d.remoteHost)
	} else {
		var settings struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(remoteSettingsOut), &settings); err != nil {
			chkSettings.Status = StatusError
			chkSettings.Message = fmt.Sprintf("Invalid JSON in remote ~/.claude/settings.json: %v", err)
			chkSettings.Remediation = fmt.Sprintf("Fix syntax error in %s:~/.claude/settings.json", d.remoteHost)
		} else {
			hasHook := false
			if promptHooks, ok := settings.Hooks["UserPromptSubmit"]; ok {
				for _, wrapper := range promptHooks {
					for _, h := range wrapper.Hooks {
						if strings.Contains(h.Command, "mesh hook prompt") {
							hasHook = true
							break
						}
					}
					if hasHook {
						break
					}
				}
			}

			if hasHook {
				chkSettings.Status = StatusOK
				chkSettings.Message = "settings.json valid, 'mesh hook prompt' hook active"
			} else {
				chkSettings.Status = StatusWarn
				chkSettings.Message = "settings.json valid, but 'mesh hook prompt' hook missing"
				chkSettings.Remediation = fmt.Sprintf("Add 'mesh hook prompt' to UserPromptSubmit on %s:~/.claude/settings.json", d.remoteHost)
			}
		}
	}
	sec.Checks = append(sec.Checks, chkSettings)

	// 2. Mapped Repositories from scan-repos.json
	chkRepos := CheckResult{Name: "Mapped Repositories"}
	scanCfg, err := bridge.LoadScanRepos("")
	if err != nil || scanCfg == nil || len(scanCfg.Repos) == 0 {
		chkRepos.Status = StatusWarn
		chkRepos.Message = "No mapped repositories found in scan-repos.json"
		chkRepos.Remediation = "Configure repos in ~/.agents/skills/ticket-notes/scan-repos.json"
	} else {
		// Test presence of each mapped repo on remote host in a single SSH invocation
		type repoCheck struct {
			Name       string
			RemotePath string
		}
		repoChecks := make([]repoCheck, 0, len(scanCfg.Repos))
		bashChecks := make([]string, 0, len(scanCfg.Repos))

		for _, repo := range scanCfg.Repos {
			remotePath := bridge.ToRemotePathWithConfig(repo.Path, d.cfg, scanCfg)
			repoChecks = append(repoChecks, repoCheck{Name: repo.Name, RemotePath: remotePath})

			// Convert ~/ to $HOME/ so shell expansion works correctly within tests
			testPath := remotePath
			if strings.HasPrefix(testPath, "~/") {
				testPath = "$HOME/" + strings.TrimPrefix(testPath, "~/")
			} else if testPath == "~" {
				testPath = "$HOME"
			}
			// Escape double quotes for shell
			safePath := strings.ReplaceAll(testPath, "\"", "\\\"")
			bashChecks = append(bashChecks, fmt.Sprintf("if [ -d \"%s\" ]; then echo 'FOUND'; else echo 'MISSING'; fi", safePath))
		}

		remoteCmd := strings.Join(bashChecks, " ; ")
		checkOut, checkLat, checkErr := d.RemoteCommandFunc(ctx, d.remoteHost, remoteCmd)
		chkRepos.Latency = checkLat

		if checkErr != nil {
			chkRepos.Status = StatusWarn
			chkRepos.Message = fmt.Sprintf("Failed to query remote repo paths on %s: %v", d.remoteHost, checkErr)
			chkRepos.Remediation = fmt.Sprintf("Verify SSH access and filesystem permissions on %s", d.remoteHost)
		} else {
			lines := strings.Split(strings.TrimSpace(checkOut), "\n")
			foundCount := 0
			var missingNames []string

			for i, rc := range repoChecks {
				res := "MISSING"
				if i < len(lines) {
					res = strings.TrimSpace(lines[i])
				}
				if res == "FOUND" {
					foundCount++
				} else {
					missingNames = append(missingNames, rc.Name)
				}
			}

			total := len(repoChecks)
			if foundCount == total {
				chkRepos.Status = StatusOK
				chkRepos.Message = fmt.Sprintf("All %d mapped repositories verified on %s", total, d.remoteHost)
			} else if foundCount > 0 {
				chkRepos.Status = StatusWarn
				chkRepos.Message = fmt.Sprintf("%d/%d mapped repositories verified on %s (missing: %s)",
					foundCount, total, d.remoteHost, strings.Join(missingNames, ", "))
				chkRepos.Remediation = fmt.Sprintf("Clone or sync missing repos on %s: `mesh bridge` or git clone", d.remoteHost)
			} else {
				chkRepos.Status = StatusWarn
				chkRepos.Message = fmt.Sprintf("0/%d mapped repositories found on %s", total, d.remoteHost)
				chkRepos.Remediation = fmt.Sprintf("Sync repositories to %s remote root (%s)", d.remoteHost, d.cfg.RemoteRepoRoot)
			}
		}
	}
	sec.Checks = append(sec.Checks, chkRepos)

	sec.Status = aggregateStatus(sec.Checks)
	return sec
}

// aggregateStatus computes the overall section status from its check results.
func aggregateStatus(checks []CheckResult) Status {
	hasWarn := false
	hasError := false
	hasOK := false

	for _, c := range checks {
		switch c.Status {
		case StatusError:
			hasError = true
		case StatusWarn:
			hasWarn = true
		case StatusOK:
			hasOK = true
		}
	}

	if hasError {
		return StatusError
	}
	if hasWarn {
		return StatusWarn
	}
	if hasOK {
		return StatusOK
	}
	return StatusSkipped
}

// FormatReport creates a formatted Tokyo Night terminal display of the diagnostic report.
func (d *FleetDoctor) FormatReport(report *Report) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("%s%s[Agent-Mesh :: Fleet Doctor Engine]%s\n", ColorBold, ColorCyan, ColorReset))
	b.WriteString(fmt.Sprintf("  • Target Remote Node:  %s%s%s\n", ColorPurple, report.RemoteHost, ColorReset))
	if report.Fast {
		b.WriteString(fmt.Sprintf("  • Execution Mode:      %s⚡ Fast (Remote network checks skipped)%s\n", ColorYellow, ColorReset))
	}
	b.WriteString("\n")

	for i, sec := range report.Sections {
		b.WriteString(fmt.Sprintf("%s%s• Section %d: %s%s\n", ColorBold, ColorCyan, i+1, sec.Name, ColorReset))

		for _, chk := range sec.Checks {
			icon := CheckmarkOK
			switch chk.Status {
			case StatusWarn:
				icon = CheckmarkWarn
			case StatusError:
				icon = CheckmarkErr
			case StatusSkipped:
				icon = CheckmarkSkip
			}

			latStr := ""
			if chk.Latency > 0 {
				latStr = fmt.Sprintf(" %s(%s)%s", ColorGray, formatDuration(chk.Latency), ColorReset)
			}

			b.WriteString(fmt.Sprintf("  %s %s%s:%s %s%s\n",
				icon,
				ColorBold, chk.Name, ColorReset,
				chk.Message,
				latStr,
			))

			if chk.Remediation != "" && (chk.Status == StatusWarn || chk.Status == StatusError) {
				b.WriteString(fmt.Sprintf("    %s→ Advice:%s %s%s%s\n",
					ColorYellow, ColorReset,
					ColorPurple, chk.Remediation, ColorReset,
				))
			}
		}
		b.WriteString("\n")
	}

	// Footer summary
	warnStr := fmt.Sprintf("%d warnings", report.Summary.Warnings)
	if report.Summary.Warnings == 1 {
		warnStr = "1 warning"
	}
	errStr := fmt.Sprintf("%d errors", report.Summary.Errors)
	if report.Summary.Errors == 1 {
		errStr = "1 error"
	}

	b.WriteString(fmt.Sprintf("%s──────────────────────────────────────────────────────────────────────────%s\n", ColorGray, ColorReset))
	b.WriteString(fmt.Sprintf("%sFleet Health Summary:%s %s%d passed%s, %s%s%s, %s%s%s",
		ColorBold, ColorReset,
		ColorGreen, report.Summary.Passed, ColorReset,
		ColorYellow, warnStr, ColorReset,
		ColorRed, errStr, ColorReset,
	))
	if report.Summary.Skipped > 0 {
		b.WriteString(fmt.Sprintf(", %s%d skipped%s", ColorGray, report.Summary.Skipped, ColorReset))
	}
	b.WriteString(fmt.Sprintf(" %s(completed in %s)%s\n", ColorGray, report.Summary.DurationStr, ColorReset))

	return b.String()
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
