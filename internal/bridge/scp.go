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

// TransferOptions configures arbitrary file and directory SCP transfers.
type TransferOptions struct {
	Host      string
	Source    string
	Dest      string
	Pull      bool
	Recursive bool
}

// TransferResult captures transfer outcome and paths.
type TransferResult struct {
	Host       string `json:"host"`
	Source     string `json:"source"`
	Dest       string `json:"dest"`
	Action     string `json:"action"` // "pushed" or "pulled"
	IsDirectory bool  `json:"is_directory"`
}

// Transfer handles pushing or pulling files across the bridge with smart path translation.
func Transfer(ctx context.Context, opts TransferOptions) (*TransferResult, error) {
	cfg, _ := config.LoadConfig()
	host := opts.Host
	if host == "" && cfg != nil && cfg.RemoteHost != "" {
		host = cfg.RemoteHost
	}
	if host == "" {
		host = DefaultRemoteHost
	}

	probe := ProbeSSH(ctx, host, DefaultSSHTimeout)
	if !probe.Reachable {
		return nil, fmt.Errorf("remote host %s unreachable: %s", host, probe.Error)
	}

	scpCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var args []string
	if opts.Recursive {
		args = append(args, "-r")
	}

	if opts.Pull {
		// Pull: remote Source -> local Dest
		remoteSrc := opts.Source
		if remoteSrc == "" {
			return nil, fmt.Errorf("remote source path is required for pull")
		}

		localDest := opts.Dest
		if localDest == "" {
			localDest = ToLocalPath(remoteSrc)
			if localDest == remoteSrc {
				localDest = "./" + filepath.Base(remoteSrc)
			}
		}

		args = append(args, "-p", fmt.Sprintf("%s:%s", host, remoteSrc), localDest)
		cmd := exec.CommandContext(scpCtx, "scp", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("scp pull failed: %w (%s)", err, strings.TrimSpace(string(out)))
		}

		return &TransferResult{
			Host:        host,
			Source:      remoteSrc,
			Dest:        localDest,
			Action:      "pulled",
			IsDirectory: opts.Recursive,
		}, nil
	}

	// Push: local Source -> remote Dest
	localSrc := opts.Source
	if localSrc == "" {
		return nil, fmt.Errorf("local source path is required for push")
	}

	info, err := os.Stat(localSrc)
	if err != nil {
		return nil, fmt.Errorf("local source not found at %s: %w", localSrc, err)
	}
	isDir := info.IsDir()
	if isDir && !opts.Recursive {
		args = append([]string{"-r"}, args...)
	}

	remoteDest := opts.Dest
	if remoteDest == "" {
		absLocal, _ := filepath.Abs(localSrc)
		remoteDest = ToRemotePath(absLocal)
		if strings.HasPrefix(remoteDest, "~/") {
			// SCP handles ~ on remote side: host:~/Documents/...
		} else if !strings.HasPrefix(remoteDest, "/") && !strings.HasPrefix(remoteDest, "~") {
			remoteDest = "/tmp/" + filepath.Base(localSrc)
		}
	}

	args = append(args, "-p", localSrc, fmt.Sprintf("%s:%s", host, remoteDest))
	cmd := exec.CommandContext(scpCtx, "scp", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("scp push failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// Copy remote destination path to local clipboard for convenience
	_ = copyStringToClipboard(remoteDest)

	return &TransferResult{
		Host:        host,
		Source:      localSrc,
		Dest:        remoteDest,
		Action:      "pushed",
		IsDirectory: isDir,
	}, nil
}
