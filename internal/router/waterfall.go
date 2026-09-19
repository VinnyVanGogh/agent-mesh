package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type RouteTarget string

const (
	TargetRemoteClaude    RouteTarget = "remote-claude"
	TargetLocalClaudeWork RouteTarget = "local-claude-work"
	TargetGeminiNative    RouteTarget = "gemini-native"
	TargetClaude3P        RouteTarget = "claude-3p"
	TargetClaudePersonal  RouteTarget = "claude-personal"
)

type RouteOptions struct {
	CheckSSH   bool
	RemoteHost string
}

type RouteDecision struct {
	Target         RouteTarget `json:"target"`
	Tool           string      `json:"tool"`            // "claude", "agy", "ssh"
	Model          string      `json:"model"`           // "gemini-3.8-flash-high", "claude-opus-5", "claude-sonnet-4-6"
	Command        string      `json:"command"`         // shell invocation command
	Workspace      string      `json:"workspace"`
	IsWorkRepo     bool        `json:"is_work_repo"`
	WorkRepoSource string      `json:"work_repo_source,omitempty"`
	SSHReachable   bool        `json:"ssh_reachable"`
	RemoteHost     string      `json:"remote_host,omitempty"`
	AccountRole    string      `json:"account_role"` // "work", "personal"
	AccountEmail   string      `json:"account_email"`
	Reason         string      `json:"reason"`
	Warnings       []string    `json:"warnings,omitempty"`
	PacerState     *PacerState `json:"pacer_state,omitempty"`
}

type scanReposFile struct {
	Repos []struct {
		Name     string   `json:"name"`
		Path     string   `json:"path"`
		Projects []string `json:"projects"`
	} `json:"repos"`
}

// IsWorkRepo determines whether the given directory is part of Managed Solution
// by matching ~/.agents/skills/ticket-notes/scan-repos.json or ~/Documents/dev/mansol*.
func IsWorkRepo(cwd string) (bool, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	if cwd == "" || cwd == "." {
		if cur, err := os.Getwd(); err == nil {
			cwd = cur
		}
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		absCwd = cwd
	}
	cleanCwd := filepath.Clean(absCwd)
	evalCwd, _ := filepath.EvalSymlinks(cleanCwd)
	if evalCwd == "" {
		evalCwd = cleanCwd
	}

	// 1. Check pattern: ~/Documents/dev/mansol*
	mansolPrefix := filepath.Join(home, "Documents", "dev", "mansol")
	if strings.HasPrefix(cleanCwd, mansolPrefix) || strings.HasPrefix(evalCwd, mansolPrefix) {
		return true, "prefix: ~/Documents/dev/mansol*", nil
	}

	// 2. Check scan-repos.json: ~/.agents/skills/ticket-notes/scan-repos.json
	scanPath := filepath.Join(home, ".agents", "skills", "ticket-notes", "scan-repos.json")
	if data, err := os.ReadFile(scanPath); err == nil {
		var srf scanReposFile
		if json.Unmarshal(data, &srf) == nil {
			for _, r := range srf.Repos {
				if r.Path == "" {
					continue
				}
				cleanRepoPath := filepath.Clean(r.Path)
				evalRepoPath, _ := filepath.EvalSymlinks(cleanRepoPath)
				if evalRepoPath == "" {
					evalRepoPath = cleanRepoPath
				}

				if cleanCwd == cleanRepoPath || evalCwd == evalRepoPath ||
					strings.HasPrefix(cleanCwd, cleanRepoPath+string(filepath.Separator)) ||
					strings.HasPrefix(evalCwd, evalRepoPath+string(filepath.Separator)) {
					return true, fmt.Sprintf("scan-repos: %s", r.Name), nil
				}
			}
		}
	}

	// 3. Fallback: check git remote for managed solution
	gitConfigPath := filepath.Join(cleanCwd, ".git", "config")
	if cfgData, err := os.ReadFile(gitConfigPath); err == nil {
		if strings.Contains(strings.ToLower(string(cfgData)), "managedsolution") {
			return true, "git remote: managedsolution", nil
		}
	}

	return false, "", nil
}

// CheckSSHConnectivity tests whether the remote host is reachable via SSH.
// Uses a fast cached result (/tmp/agent-mesh-ssh-<host>.cache) if within 20s.
func CheckSSHConnectivity(ctx context.Context, host string) bool {
	if host == "" {
		host = "mansol-mbp"
	}

	cachePath := filepath.Join(os.TempDir(), fmt.Sprintf("agent-mesh-ssh-%s.cache", host))
	if info, err := os.Stat(cachePath); err == nil {
		if time.Since(info.ModTime()) < 20*time.Second {
			content, _ := os.ReadFile(cachePath)
			return strings.TrimSpace(string(content)) == "ok"
		}
	}

	// Run quick SSH probe with 800ms timeout
	probeCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "ssh",
		"-o", "ConnectTimeout=1",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		host, "true",
	)
	err := cmd.Run()
	reachable := (err == nil)

	// Write cache
	status := "fail"
	if reachable {
		status = "ok"
	}
	_ = os.WriteFile(cachePath, []byte(status), 0644)

	return reachable
}

// Route executes the dynamic waterfall routing engine:
// 1. Check if cwd is a Managed Solution work repo.
// 2. If work repo: check SSH connectivity to mansol-mbp -> route to remote Claude.
// 3. If personal repo: compare quota headroom, preferring Gemini Native as primary daily driver,
//    falling back to 3P Claude / personal Claude when Gemini is locked out.
func Route(ctx context.Context, cwd string, pacerState *PacerState, opts RouteOptions) (*RouteDecision, error) {
	if cwd == "" || cwd == "." {
		if cur, err := os.Getwd(); err == nil {
			cwd = cur
		}
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		absCwd = cwd
	}

	if opts.RemoteHost == "" {
		opts.RemoteHost = "mansol-mbp"
	}

	if pacerState == nil {
		var err error
		pacerState, err = LoadPacerState()
		if err != nil {
			return nil, fmt.Errorf("failed to load pacer state: %w", err)
		}
	}

	decision := &RouteDecision{
		Workspace:  absCwd,
		RemoteHost: opts.RemoteHost,
		PacerState: pacerState,
		Warnings:   make([]string, 0),
	}

	// 1. Work Repo Check
	isWork, workSrc, _ := IsWorkRepo(absCwd)
	decision.IsWorkRepo = isWork
	decision.WorkRepoSource = workSrc

	if isWork {
		decision.AccountRole = "work"
		decision.AccountEmail = "vvasile@managedsolution.com"

		sshOk := false
		if opts.CheckSSH {
			sshOk = CheckSSHConnectivity(ctx, opts.RemoteHost)
		}
		decision.SSHReachable = sshOk

		if sshOk {
			decision.Target = TargetRemoteClaude
			decision.Tool = "ssh"
			decision.Model = "claude-opus-5"
			decision.Command = fmt.Sprintf("ssh -t %s \"cd %s && claude\"", opts.RemoteHost, absCwd)
			decision.Reason = fmt.Sprintf("Managed Solution work repo (%s); remote node %s reachable via SSH (Highest Priority)", workSrc, opts.RemoteHost)
			return decision, nil
		}

		// Work repo but remote not reachable
		decision.Target = TargetLocalClaudeWork
		decision.Tool = "claude"
		decision.Model = "claude-opus-5"
		decision.Command = "claude"
		decision.Reason = fmt.Sprintf("Managed Solution work repo (%s); remote node %s unreachable via SSH, routing to local Claude Code with work account", workSrc, opts.RemoteHost)
		if opts.CheckSSH {
			decision.Warnings = append(decision.Warnings, fmt.Sprintf("Remote node %s unreachable via SSH; falling back to local Claude", opts.RemoteHost))
		}
		return decision, nil
	}

	// 2. Personal Repo: Quota Headroom Waterfall
	decision.AccountRole = "personal"
	decision.AccountEmail = "stylesbyvinny@gmail.com"

	poolGemini := pacerState.Pools[PoolGeminiNative]
	pool3P := pacerState.Pools[Pool3PClaude]
	poolPersonal := pacerState.Pools[PoolPersonalClaude]

	// Prefer Gemini Native as primary daily driver
	if poolGemini != nil && !poolGemini.IsLocked && poolGemini.Weekly.RemainingPct > 0.0 && poolGemini.FiveHour.RemainingPct > 0.0 {
		decision.Target = TargetGeminiNative
		decision.Tool = "agy"
		decision.Model = "gemini-3.8-flash-high"
		decision.Command = "agy"
		decision.Reason = fmt.Sprintf("Personal repo; Gemini Native is healthy and preferred primary daily driver (%d turns runway | 5h: %.1f%% left | week: %.1f%% left)",
			poolGemini.TurnsRunway, poolGemini.FiveHour.RemainingPct, poolGemini.Weekly.RemainingPct)
		return decision, nil
	}

	// Gemini Native is locked out -> Fallback to 3P / Claude
	geminiLockReason := "Gemini quota exhausted"
	if poolGemini != nil && poolGemini.LockoutReason != "" {
		geminiLockReason = poolGemini.LockoutReason
	}

	// Option A: 3P Claude in Antigravity
	if pool3P != nil && !pool3P.IsLocked && pool3P.Weekly.RemainingPct > 0.0 && pool3P.FiveHour.RemainingPct > 0.0 {
		decision.Target = TargetClaude3P
		decision.Tool = "agy"
		decision.Model = "claude-sonnet-4-6"
		decision.Command = "agy"
		decision.Reason = fmt.Sprintf("Gemini Native locked (%s); falling back to 3P Claude in Antigravity (%d turns runway)",
			geminiLockReason, pool3P.TurnsRunway)
		decision.Warnings = append(decision.Warnings, fmt.Sprintf("Gemini Native is locked: %s. Switch model in agy using '/model claude-sonnet-4-6'.", geminiLockReason))
		return decision, nil
	}

	// Option B: Personal Claude CLI
	if poolPersonal != nil && !poolPersonal.IsLocked && poolPersonal.Weekly.RemainingPct > 0.0 && poolPersonal.FiveHour.RemainingPct > 0.0 {
		decision.Target = TargetClaudePersonal
		decision.Tool = "claude"
		decision.Model = "claude-opus-5"
		decision.Command = "claude"
		decision.Reason = fmt.Sprintf("Gemini Native & 3P Claude locked; falling back to standalone Claude CLI (%d turns runway | week: %.1f%% left)",
			poolPersonal.TurnsRunway, poolPersonal.Weekly.RemainingPct)
		decision.Warnings = append(decision.Warnings, "Gemini Native and 3P Claude are locked; routing to standalone Claude Code.")
		return decision, nil
	}

	// All personal quotas locked or exhausted
	decision.Target = TargetGeminiNative
	decision.Tool = "agy"
	decision.Model = "gemini-3.8-flash-high"
	decision.Command = "agy"
	decision.Reason = "All personal quota pools are currently locked or exhausted; awaiting window reset"

	if poolGemini != nil && poolGemini.IsLocked {
		decision.Warnings = append(decision.Warnings, fmt.Sprintf("Gemini Native locked until %s", poolGemini.LockoutUntil.Format("03:04pm")))
	}
	if pool3P != nil && pool3P.IsLocked {
		decision.Warnings = append(decision.Warnings, fmt.Sprintf("3P Claude locked until %s", pool3P.LockoutUntil.Format("03:04pm")))
	}
	if poolPersonal != nil && poolPersonal.IsLocked {
		decision.Warnings = append(decision.Warnings, fmt.Sprintf("Personal Claude locked until %s", poolPersonal.LockoutUntil.Format("03:04pm")))
	}

	return decision, nil
}
