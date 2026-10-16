//go:build windows

package review

import "os/exec"

// stoppableProcessGroup relies on the default cancellation, which kills the adapter
// process; WaitDelay bounds how long its output pipes may outlive it.
func stoppableProcessGroup(cmd *exec.Cmd) {}

// stopLeftoverProcessGroup has no group to stop on Windows: only the adapter
// process itself was started and it has already exited.
func stopLeftoverProcessGroup(cmd *exec.Cmd) {}
