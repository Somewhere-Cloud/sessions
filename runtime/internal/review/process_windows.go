//go:build windows

package review

import "os/exec"

// stoppableProcessGroup relies on the default cancellation, which kills the adapter
// process; WaitDelay bounds how long its output pipes may outlive it.
func stoppableProcessGroup(cmd *exec.Cmd) {}
