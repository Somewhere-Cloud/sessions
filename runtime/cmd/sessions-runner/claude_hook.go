package main

import (
	"os"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudehooks"
)

// runClaudeHook captures one Claude hook event for the observation sink. It
// is started by Claude as a background hook, prints nothing on success, and
// always exits 0; the timer bounds a capture whose input never ends.
func runClaudeHook(args []string) int {
	deadline := time.AfterFunc(claudehooks.CaptureTimeout, func() { os.Exit(0) })
	defer deadline.Stop()
	return claudehooks.Run(args, os.Stdin, os.Stderr, time.Now)
}

// runSubcommand runs a helper subcommand the runner binary also serves, and
// reports whether args named one.
func runSubcommand(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "approval-shim":
		return runApprovalShim(args[1:]), true
	case claudehooks.Subcommand:
		return runClaudeHook(args[1:]), true
	}
	return 0, false
}
