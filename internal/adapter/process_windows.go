//go:build windows

package adapter

import (
	"os/exec"
	"strconv"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) {
	// Process trees on Windows are killed via taskkill /T /F
}

// killProcessGroup terminates the process and any child processes on Windows.
func killProcessGroup(cmd *exec.Cmd, gracePeriod time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
