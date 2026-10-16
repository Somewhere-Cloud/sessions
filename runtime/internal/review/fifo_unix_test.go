//go:build !windows

package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO configured as the reviewer is neither hashed (opening it would block)
// nor waited on: the comparison returns promptly with an unavailable error.
func TestFIFOReviewerDoesNotBlock(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "reviewer")
	if err := syscall.Mkfifo(fifo, 0o700); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	ext, err := NewExternal(fifo, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, q := frozen(t, fixtureSnapshot)
	started := time.Now()
	r := Compare(context.Background(), s, q, []Reviewer{ext}).Results[0]
	if r.Error == nil || r.Error.Kind != ErrUnavailable || r.Reviewer.FileSHA256BeforeRun != "" || time.Since(started) > 3*time.Second {
		t.Fatalf("FIFO reviewer: %+v after %s", r, time.Since(started))
	}
}

// An adapter that answers but leaves a background process holding its output
// open is not a complete answer: the reply is not used, and the error says a
// process may still be running. Sessions signals nothing after the adapter
// exited (that process may have left the group, and the group ID may be
// reused), so the fixture's own background process is still alive here and
// the test stops it itself.
func TestLeftoverAdapterOutputIsReportedAndNothingIsSignalledAfterExit(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\nsleep 37 &\necho $! > " + pidFile + "\n" +
		`printf '{"protocol":"sessions.review-adapter/1","reviewer":{"id":"bg","version":"1"},"choice":"working"}'` + "\n"
	adapter := filepath.Join(dir, "adapter.sh")
	if err := os.WriteFile(adapter, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ext, err := NewExternal(adapter, nil, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s, q := frozen(t, fixtureSnapshot)
	r := Compare(context.Background(), s, q, []Reviewer{ext}).Results[0]
	raw, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	t.Cleanup(func() { stopFixtureSleep(pid) })
	if r.Error == nil || r.Error.Kind != ErrUnavailable || r.Answer != nil ||
		!strings.Contains(r.Error.Detail, "may still be running and was not stopped") || strings.Contains(r.Error.Detail, "group was stopped") {
		t.Fatalf("leftover output: %+v", r)
	}
	if !fixtureSleepRunning(pid) {
		t.Fatalf("the adapter's background process %d was stopped after the adapter exited", pid)
	}
}

// fixtureSleepRunning reports whether pid is still the fixture's own running
// "sleep 37" (not a zombie, which a signal 0 probe would still find).
func fixtureSleepRunning(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=,command=", "-p", strconv.Itoa(pid)).Output()
	fields := strings.Fields(string(out))
	return err == nil && len(fields) == 3 && !strings.HasPrefix(fields[0], "Z") && fields[1]+" "+fields[2] == "sleep 37"
}

// stopFixtureSleep stops the fixture's own background sleep if a failed test
// left it running, after checking the pid still names that command.
func stopFixtureSleep(pid int) {
	if pid <= 1 {
		return
	}
	if fixtureSleepRunning(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
