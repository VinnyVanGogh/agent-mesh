//go:build windows

package chat

import (
	"context"
	"os/exec"
)

func execCommandWithProcessGroup(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
