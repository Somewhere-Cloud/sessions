//go:build !windows

package main

import (
	"os"
	"syscall"
)

// reviewCancelSignals cancel `sessions review compare`. The adapter runs in
// its own process group, so a terminal hangup or a supervisor's SIGTERM
// reaches only this command and must stop the adapter through cancellation.
var reviewCancelSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
