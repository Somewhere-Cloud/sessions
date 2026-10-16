//go:build !windows

package review

import (
	"context"
	"errors"
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
// open is not a complete answer: the reply is not used, the error says what
// happened, and the adapter's own process group is stopped.
func TestLeftoverAdapterProcessIsStoppedAndReported(t *testing.T) {
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
	if r.Error == nil || r.Error.Kind != ErrUnavailable || !strings.Contains(r.Error.Detail, "kept its output open") || r.Answer != nil {
		t.Fatalf("leftover process: %+v", r)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the adapter's background process %d is still running (%v)", pid, err)
	}
}

// stopFixtureSleep stops the fixture's own background sleep if a failed test
// left it running, after checking the pid still names that command.
func stopFixtureSleep(pid int) {
	if pid <= 1 {
		return
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err == nil && strings.TrimSpace(string(out)) == "sleep 37" {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
