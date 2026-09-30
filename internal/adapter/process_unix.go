//go:build !windows

package adapter

import (
	"os/exec"
	"syscall"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// killProcessGroup kills the subprocess group: first SIGTERM, wait gracePeriod (1.5s),
// then SIGKILL if any processes in the group are still alive.
func killProcessGroup(cmd *exec.Cmd, gracePeriod time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}

	// 1. Send SIGTERM to the entire process group (-pgid)
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// 2. Wait up to gracePeriod for the process group to exit
	deadline := time.Now().Add(gracePeriod)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err != nil {
			// Process group no longer exists
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 3. SIGKILL to entire process group if still alive
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
