//go:build !windows

package chat

import (
	"context"
	"os/exec"
	"syscall"
)

func execCommandWithProcessGroup(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd
}
