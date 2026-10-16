//go:build !windows

package review

import (
	"os/exec"
	"syscall"
)

// stoppableProcessGroup puts the adapter in its own process group so a timeout or
// cancellation stops the adapter and anything it started.
func stoppableProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
