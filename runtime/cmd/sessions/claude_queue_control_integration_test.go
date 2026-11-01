package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// Saved Claude messages can be edited, cancelled and reordered until the
// runner dispatches them, through the CLI, HTTP API, daemon, socket and a
// real runner process with an inert provider. A dispatched message refuses
// every change, and receipts say what happened.
func TestClaudeQueueEditCancelReorderEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inert provider fixture requires POSIX sh")
	}
	if testing.Short() {
		t.Skip("builds and runs a real runner process")
	}
	h := newQueueRecoveryHarness(t)
	h.waitFor("the runner to advertise queue control", func() bool { return h.info().MessageQueue })
	current := h.send("hang-a", "runner")
	h.waitCalls("hang-a")
	b := h.send("queued-b", "queue")
	c := h.send("queued-c", "queue")
	d := h.send("queued-d", "queue")

	listed := h.queue(0, h.id)
	if len(listed.Items) != 4 || listed.Items[0].State != "dispatched" || listed.Items[0].OperationID != current ||
		listed.Items[1].OperationID != b || listed.Items[3].Position != 3 {
		t.Fatalf("listed queue = %+v", listed)
	}
	if edited := h.queue(0, h.id, "edit", b[:8], "edited-b"); edited.Items[1].Text != "edited-b" || edited.Items[1].Edits != 1 {
		t.Fatalf("edit = %+v", edited)
	}
	h.queue(0, h.id, "cancel", c)
	if moved := h.queue(0, h.id, "next", d); moved.Items[1].OperationID != d || moved.Items[2].OperationID != b {
		t.Fatalf("next = %+v", moved)
	}
	if refused := h.queue(1, h.id, "edit", current, "changed"); refused.Code != "dispatched" || !strings.Contains(refused.Error, "already dispatched") {
		t.Fatalf("edit of the dispatched message = %+v", refused)
	}
	if again := h.queue(0, h.id, "cancel", c); again.Code != "already-cancelled" {
		t.Fatalf("second cancel = %+v", again)
	}

	if receipt := h.receipt(c); receipt.Status != "cancelled" || receipt.Delivered || receipt.Retry ||
		receipt.Evidence == nil || receipt.Evidence.Dispatch != "cancelled" {
		t.Fatalf("cancelled receipt = %+v evidence %+v", receipt, receipt.Evidence)
	}
	if receipt := h.receipt(b); receipt.Status != "accepted" || receipt.Edits != 1 || receipt.Evidence == nil || receipt.Evidence.Edits != 1 {
		t.Fatalf("edited receipt = %+v evidence %+v", receipt, receipt.Evidence)
	}
	// Resending a cancelled operation reads its receipt and sends nothing.
	if stdout, _, code := h.cli("--json", "send", h.id, "--operation-id", c, "--", "queued-c"); code != 1 || !strings.Contains(stdout, `"cancelled"`) {
		t.Fatalf("resend of cancelled operation: exit=%d %s", code, stdout)
	}

	h.interruptAndWaitPaused()
	h.retry(true, "")
	h.waitQueue(func(queue queueRecoveryQueue) bool { return !queue.Paused && len(h.providerCalls()) == 3 })
	h.waitFor("the edited message's turn to finish", func() bool { return len(h.queue(0, h.id).Items) == 0 })
	h.assertCalls("hang-a", "queued-d", "edited-b")
}

type queueControlOutput struct {
	OK    bool        `json:"ok"`
	Code  string      `json:"code"`
	Error string      `json:"error"`
	Items []queueItem `json:"items"`
}

func (h *queueRecoveryHarness) queue(wantExit int, args ...string) queueControlOutput {
	h.t.Helper()
	stdout, stderr, code := h.cli(append([]string{"--json", "queue"}, args...)...)
	var output queueControlOutput
	if code != wantExit || json.Unmarshal([]byte(stdout), &output) != nil || output.OK != (wantExit == 0) {
		h.t.Fatalf("queue %q: exit=%d stdout=%s stderr=%s, want exit %d", args, code, stdout, stderr, wantExit)
	}
	return output
}
