//go:build windows

package main

import (
	"os"
	"syscall"
)

// reviewCancelSignals cancel `sessions review compare`. Go reports Ctrl-C as
// os.Interrupt and a console close, logoff or shutdown as syscall.SIGTERM.
var reviewCancelSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
