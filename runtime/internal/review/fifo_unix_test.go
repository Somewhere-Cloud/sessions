//go:build !windows

package review

import (
	"context"
	"path/filepath"
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
