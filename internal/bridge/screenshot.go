package bridge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/VinnyVanGogh/agent-mesh/internal/config"
)

// ScreenshotOptions configures screenshot capture and transfer between local and remote hosts.
type ScreenshotOptions struct {
	Host          string
	SourcePath    string // Optional local image path to push
	RemotePath    string // Destination path on remote host (default /tmp/screenshot.png)
	Interactive   bool   // Trigger interactive crop selection (screencapture -i)
	FromClipboard bool   // Extract directly from clipboard image
	Pull          bool   // Pull remote screenshot to local Mac
	LocalDest     string // Local destination for pulled screenshot
	OpenAfterPull bool   // Automatically launch 'open' after pulling
	RemoteCwd     string // Optional remote working directory to copy into
}

// ScreenshotResult reports metrics and paths of the transferred screenshot.
type ScreenshotResult struct {
	LocalPath  string        `json:"local_path"`
	RemotePath string        `json:"remote_path"`
	FileSize   int64         `json:"file_size"`
	Duration   time.Duration `json:"duration"`
	Action     string        `json:"action"` // "pushed" or "pulled"
	Host       string        `json:"host"`
}

// PushScreenshot captures or reads a local screenshot and SCPs it to the remote Claude node.
// It also copies the remote path to the local system clipboard so the user can Cmd+V directly into Claude Code.
func PushScreenshot(ctx context.Context, opts ScreenshotOptions) (*ScreenshotResult, error) {
	start := time.Now()

	cfg, _ := config.LoadConfig()
	host := opts.Host
	if host == "" && cfg != nil && cfg.RemoteHost != "" {
		host = cfg.RemoteHost
	}
	if host == "" {
		host = DefaultRemoteHost
	}

	// 1. Determine local file to upload
	localFile := opts.SourcePath
	if localFile == "" {
		captured, err := CaptureLocalScreenshot("/tmp/screenshot.png", opts.Interactive, opts.FromClipboard)
		if err != nil {
			return nil, fmt.Errorf("failed to capture screenshot: %w", err)
		}
		localFile = captured
	}

	info, err := os.Stat(localFile)
	if err != nil {
		return nil, fmt.Errorf("local screenshot not found at %s: %w", localFile, err)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("local screenshot file is empty (0 bytes)")
	}

	// 2. Determine remote destination path
	remotePath := opts.RemotePath
	if remotePath == "" {
		remotePath = "/tmp/screenshot.png"
	}

	// Probe host connectivity
	probe := ProbeSSH(ctx, host, DefaultSSHTimeout)
	if !probe.Reachable {
		return nil, fmt.Errorf("remote host %s unreachable: %s", host, probe.Error)
	}

	// 3. SCP local file to remote host
	scpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	remoteTarget := fmt.Sprintf("%s:%s", host, remotePath)
	cmd := exec.CommandContext(scpCtx, "scp", "-p", localFile, remoteTarget)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("scp transfer failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// Also upload timestamped copy for permanence (e.g. /tmp/screenshot-20260922-091528.png)
	timestampedRemote := fmt.Sprintf("/tmp/screenshot-%s.png", time.Now().Format("20060102-150405"))
	_ = exec.CommandContext(scpCtx, "ssh", host, fmt.Sprintf("cp %s %s", quoteForShell(remotePath), quoteForShell(timestampedRemote))).Run()

	// If remote working directory provided, also copy image into it
	if opts.RemoteCwd != "" {
		remoteCwdImage := filepath.Join(opts.RemoteCwd, filepath.Base(localFile))
		remoteCdScript := fmt.Sprintf("cp %s %s", quoteForShell(remotePath), ShellPathForDir(remoteCwdImage))
		_ = exec.CommandContext(scpCtx, "ssh", host, remoteCdScript).Run()
	}

	// 4. Stage the remote path directly into local clipboard
	_ = copyStringToClipboard(remotePath)

	return &ScreenshotResult{
		LocalPath:  localFile,
		RemotePath: remotePath,
		FileSize:   info.Size(),
		Duration:   time.Since(start),
		Action:     "pushed",
		Host:       host,
	}, nil
}

// PullScreenshot downloads a screenshot from the remote machine to the local Mac and opens it.
func PullScreenshot(ctx context.Context, opts ScreenshotOptions) (*ScreenshotResult, error) {
	start := time.Now()

	cfg, _ := config.LoadConfig()
	host := opts.Host
	if host == "" && cfg != nil && cfg.RemoteHost != "" {
		host = cfg.RemoteHost
	}
	if host == "" {
		host = DefaultRemoteHost
	}

	remotePath := opts.RemotePath
	if remotePath == "" {
		remotePath = "/tmp/screenshot.png"
	}

	localDest := opts.LocalDest
	if localDest == "" {
		home, _ := os.UserHomeDir()
		localDest = filepath.Join(home, "Desktop", fmt.Sprintf("remote-screenshot-%s.png", time.Now().Format("150405")))
	}

	probe := ProbeSSH(ctx, host, DefaultSSHTimeout)
	if !probe.Reachable {
		return nil, fmt.Errorf("remote host %s unreachable: %s", host, probe.Error)
	}

	scpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	remoteSrc := fmt.Sprintf("%s:%s", host, remotePath)
	cmd := exec.CommandContext(scpCtx, "scp", "-p", remoteSrc, localDest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("scp pull failed from %s: %w (%s)", remoteSrc, err, strings.TrimSpace(string(out)))
	}

	info, err := os.Stat(localDest)
	if err != nil {
		return nil, fmt.Errorf("failed to verify downloaded screenshot at %s: %w", localDest, err)
	}

	if opts.OpenAfterPull {
		_ = exec.Command("open", localDest).Start()
	}

	return &ScreenshotResult{
		LocalPath:  localDest,
		RemotePath: remotePath,
		FileSize:   info.Size(),
		Duration:   time.Since(start),
		Action:     "pulled",
		Host:       host,
	}, nil
}

// CaptureLocalScreenshot captures a local screenshot to targetPath.
// Automatic discovery order:
// 1. Explicit interactive request -> screencapture -i
// 2. Clipboard image (via pngpaste or osascript)
// 3. Recent desktop screenshot (< 5 mins old)
// 4. Fallback to interactive screencapture -i
func CaptureLocalScreenshot(targetPath string, interactive bool, forceClipboard bool) (string, error) {
	if targetPath == "" {
		targetPath = "/tmp/screenshot.png"
	}
	_ = os.MkdirAll(filepath.Dir(targetPath), 0755)

	// 1. Force interactive
	if interactive {
		if err := captureInteractive(targetPath); err != nil {
			return "", err
		}
		return targetPath, nil
	}

	// 2. Force or try clipboard
	if forceClipboard {
		if extractClipboardImage(targetPath) {
			return targetPath, nil
		}
		return "", fmt.Errorf("no image found on system clipboard")
	}

	// 3. Automatic detection: Check clipboard first
	if extractClipboardImage(targetPath) {
		return targetPath, nil
	}

	// 4. Check for recent Desktop screenshot taken in last 5 minutes
	if recent := findRecentDesktopScreenshot(); recent != "" {
		// Copy recent screenshot to targetPath
		if data, err := os.ReadFile(recent); err == nil {
			_ = os.WriteFile(targetPath, data, 0644)
			return targetPath, nil
		}
	}

	// 5. Fallback: prompt interactive crop selection
	if err := captureInteractive(targetPath); err != nil {
		return "", err
	}

	return targetPath, nil
}

func extractClipboardImage(destPath string) bool {
	// Method A: pngpaste (fastest C binary if available)
	if _, err := exec.LookPath("pngpaste"); err == nil {
		cmd := exec.Command("pngpaste", destPath)
		if err := cmd.Run(); err == nil {
			if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
				return true
			}
		}
	}

	// Method B: Built-in macOS AppleScript
	appleScript := fmt.Sprintf(`
set destFile to POSIX file %s
try
    set imgData to the clipboard as «class PNGf»
    set f to open for access destFile with write permission
    set eof of f to 0
    write imgData to f
    close access f
    return "ok"
on error
    return "err"
end try`, quoteForAppleScript(destPath))

	cmd := exec.Command("osascript", "-e", appleScript)
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) == "ok" {
		if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
			return true
		}
	}

	return false
}

func captureInteractive(destPath string) error {
	if _, err := exec.LookPath("screencapture"); err != nil {
		return fmt.Errorf("screencapture utility not found")
	}

	// -i: interactive mode (cursor crosshair for area selection)
	cmd := exec.Command("screencapture", "-i", destPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("screencapture cancelled or failed: %w", err)
	}

	info, err := os.Stat(destPath)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("no screenshot was captured")
	}

	return nil
}

func findRecentDesktopScreenshot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	desktopDir := filepath.Join(home, "Desktop")
	entries, err := os.ReadDir(desktopDir)
	if err != nil {
		return ""
	}

	var newestPath string
	var newestTime time.Time
	fiveMinsAgo := time.Now().Add(-5 * time.Minute)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".png") && !strings.HasSuffix(lower, ".jpg") && !strings.HasSuffix(lower, ".jpeg") {
			continue
		}
		if !strings.HasPrefix(lower, "screenshot") && !strings.HasPrefix(lower, "screen shot") && !strings.HasPrefix(lower, "cleanshot") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		modTime := info.ModTime()
		if modTime.After(fiveMinsAgo) && modTime.After(newestTime) {
			newestTime = modTime
			newestPath = filepath.Join(desktopDir, name)
		}
	}

	return newestPath
}

func copyStringToClipboard(text string) error {
	if _, err := exec.LookPath("pbcopy"); err == nil {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	return nil
}

func quoteForAppleScript(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
