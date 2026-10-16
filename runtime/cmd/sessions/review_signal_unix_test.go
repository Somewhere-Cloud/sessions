//go:build !windows

package main

import (
	"os"
	"slices"
	"syscall"
	"testing"
	"time"
)

// The adapter runs in its own process group, so a hangup or SIGTERM reaches
// only the CLI. Those must cancel the comparison (which stops the adapter's
// group) instead of ending the CLI and orphaning the adapter.
func TestReviewCompareCancelsOnTerminationSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		if !slices.Contains(reviewCancelSignals, os.Signal(sig)) {
			t.Fatalf("%v does not cancel a comparison", sig)
		}
		ctx, stop := reviewContext()
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			stop()
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Errorf("%v did not cancel the comparison context", sig)
		}
		stop()
	}
}
