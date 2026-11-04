package main

import (
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestRestartContinuationReadsInListAndStatus(t *testing.T) {
	now := time.Now()
	pending := session{RestartContinuation: &state.RestartContinuation{Reason: "reboot", State: "pending"}}
	if got := sessionStateAt(pending, now); got != "continuing" {
		t.Fatalf("pending continuation state = %q", got)
	}
	working := session{Working: true, RestartContinuation: &state.RestartContinuation{Reason: "reboot", State: "pending"}}
	if got := sessionStateAt(working, now); got != "working" {
		t.Fatalf("working session state = %q", got)
	}
	sent := &state.RestartContinuation{Reason: "crash", State: "sent", SentAt: now.UnixMilli()}
	if line := restartContinuationLine(sent, now); !strings.Contains(line, "continued after a crash") {
		t.Fatalf("sent line = %q", line)
	}
	unknown := &state.RestartContinuation{Reason: "unknown", State: "failed", Detail: "did not come back"}
	if line := restartContinuationLine(unknown, now); !strings.Contains(line, "reason unknown") || !strings.Contains(line, "did not come back") {
		t.Fatalf("failed line = %q", line)
	}
	if line := restartContinuationLine(&state.RestartContinuation{Reason: "reboot", State: "skipped"}, now); line != "" {
		t.Fatalf("skipped line = %q", line)
	}
}
