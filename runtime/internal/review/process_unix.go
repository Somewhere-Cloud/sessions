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

// stopLeftoverProcessGroup stops what remains of the adapter's own process
// group after the adapter exited while something it started still held its
// output open. Setpgid made that group for this adapter alone, and a process
// group ID is not reused while any member is alive, which is the situation
// that leads here. If every member has gone by now the signal reaches no one
// (ESRCH); the only remaining overlap is the kernel handing that ID to a new
// group in the moment between the adapter's exit being reported and this call.
func stopLeftoverProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil && cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
