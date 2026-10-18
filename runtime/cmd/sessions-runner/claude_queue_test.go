package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/providerfault"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func newClaudeQueueFixture(t *testing.T) (*claudeStructuredRunner, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("inert shell fixture requires POSIX sh")
	}
	root := t.TempDir()
	paths := state.For(root, "fixture")
	history, err := os.Create(paths.ClaudeP)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { history.Close() })
	logPath := filepath.Join(root, "provider-calls")
	command := filepath.Join(root, "fake-claude")
	script := "#!/bin/sh\nfor prompt do :; done\nprintf '%s\\n' \"$prompt\" >> \"$QUEUE_TEST_LOG\"\n" +
		"if [ \"$prompt\" = fail ]; then\nprintf '%s\\n' '{\"type\":\"result\",\"session_id\":\"fixture-claude\",\"is_error\":true,\"result\":\"permission denied\",\"subtype\":\"error\"}'\nexit 1\nfi\n" +
		"printf '%s\\n' '{\"type\":\"result\",\"session_id\":\"fixture-claude\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"done\"}'\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	provider, err := claudep.NewClient(claudep.Options{ClaudePath: command, Env: []string{
		"HOME=" + root, "SESSIONS_STATE_DIR=" + root, "SESSIONS_LEDGER_PATH=" + filepath.Join(root, "ledger"),
		"SESSIONS_PORT=18999", "QUEUE_TEST_LOG=" + logPath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &claudeStructuredRunner{paths: paths, ctx: ctx, client: provider, sessionID: "fixture-claude",
		historyFile: history, logger: log.New(io.Discard, "", 0), clients: make(map[*client]struct{}),
		cfg: config{cwd: root}, active: true}
	t.Cleanup(func() { stopClaudeQueueFixture(t, r, cancel) })
	return r, logPath
}

// Canceling alone does not join runTurn: its deferred queue write can race
// history.Close and TempDir removal, especially after a failed assertion.
func stopClaudeQueueFixture(t *testing.T, r *claudeStructuredRunner, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.queueMu.Lock()
		r.mu.Lock()
		// The fixture's initial busy state has no real provider turn to join.
		waiting := r.active && r.queueCurrent != ""
		r.mu.Unlock()
		r.queueMu.Unlock()
		if !waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("canceled fixture turn did not finish before history cleanup")
}

func waitClaudeQueue(t *testing.T, r *claudeStructuredRunner, condition func(claudeMessageQueue) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.queueMu.Lock()
		ok := condition(r.queue)
		r.queueMu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	// Say which way it failed. Paused with the first item still dispatched
	// means its turn finished unsuccessfully, and the history says why; not
	// paused means the turn was still running when the deadline passed.
	calls, _ := os.ReadFile(filepath.Join(r.cfg.cwd, "provider-calls"))
	history, _ := os.ReadFile(r.paths.ClaudeP)
	if len(history) > 2048 {
		history = history[len(history)-2048:]
	}
	t.Fatalf("inert provider did not reach expected queue state within 3s: queue=%+v current=%q provider calls=%q history tail=%s",
		r.queue, r.queueCurrent, calls, history)
}

func TestClaudeQueueSavesBusyMessagesAndRunsFIFOOnce(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	for _, text := range []string{"first", "second"} {
		control := proto.MessageControl{OperationID: text, Text: text}
		result := r.submitMessage(control)
		if !result.Accepted || result.Boundary != "queue" {
			t.Fatalf("receipt = %+v", result)
		}
		if repeat := r.submitMessage(control); !repeat.Accepted {
			t.Fatalf("repeat = %+v", repeat)
		}
	}
	info, err := os.Stat(r.paths.MessageQueue)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("queue file must be private: %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("provider launched before current turn completed")
	}
	r.finishClaudeTurn("", true)
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool {
		return len(queue.Items) == 2 && queue.Items[0].Phase == "done" && queue.Items[1].Phase == "done"
	})
	data, err := os.ReadFile(logPath)
	if err != nil || string(data) != "first\nsecond\n" {
		t.Fatalf("provider calls = %q, %v", data, err)
	}
	if result := r.submitMessage(proto.MessageControl{OperationID: "first", Text: "different"}); result.Accepted {
		t.Fatal("reused operation id changed an accepted message")
	}
}

func TestClaudeQueueFixtureJoinsCanceledTurn(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	command := filepath.Join(r.cfg.cwd, "fake-claude")
	script := "#!/bin/sh\nprintf 'started\\n' >> \"$QUEUE_TEST_LOG\"\nexec /bin/sleep 30\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if result := r.submitMessage(proto.MessageControl{OperationID: "slow", Text: "slow"}); !result.Accepted {
		t.Fatal(result)
	}
	r.finishClaudeTurn("", true)
	deadline := time.Now().Add(3 * time.Second)
	var started bool
	for time.Now().Before(deadline) {
		if data, _ := os.ReadFile(logPath); string(data) == "started\n" {
			started = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !started {
		t.Fatal("inert provider never started")
	}
	r.mu.Lock()
	cancel := r.turnCancel
	r.mu.Unlock()
	if cancel == nil {
		t.Fatal("running fixture has no cancellation")
	}
	stopClaudeQueueFixture(t, r, cancel)
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active || !r.queue.Paused {
		t.Fatal("canceled turn was not joined and paused before cleanup")
	}
}

func TestClaudeQueueFailurePausesLaterMessages(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	for _, text := range []string{"fail", "later"} {
		if result := r.submitMessage(proto.MessageControl{OperationID: text, Text: text}); !result.Accepted {
			t.Fatal(result)
		}
	}
	r.finishClaudeTurn("", true)
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Paused })
	data, _ := os.ReadFile(logPath)
	if string(data) != "fail\n" {
		t.Fatalf("failure launched queued follow-up: %q", data)
	}
	if result := r.submitMessage(proto.MessageControl{OperationID: "third", Text: "third"}); !result.Accepted || result.Boundary != "queue" || !strings.Contains(result.Error, "paused") {
		t.Fatalf("paused queue receipt = %+v", result)
	}
}

func TestClaudeQueueRestartNeverReplaysClaimedMessage(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	queue := claudeMessageQueue{Version: 1, Items: []claudeQueuedMessage{
		{ID: "uncertain", Text: "never resend", Phase: "dispatched", Hash: claudeMessageHash("never resend"), Boundary: "queue"},
		{ID: "pending", Text: "pending", Phase: "queued", Hash: claudeMessageHash("pending"), Boundary: "queue"},
	}}
	if err := writeClaudeQueue(r.paths.MessageQueue, queue); err != nil {
		t.Fatal(err)
	}
	r.active = false
	if err := r.loadMessageQueue(); err != nil {
		t.Fatal(err)
	}
	if !r.queue.Paused {
		t.Fatal("restart did not pause queue")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("restart automatically launched work")
	}
	if started, err := r.resumeClaudeQueue(); !started || err != nil {
		t.Fatalf("resume = %v, %v", started, err)
	}
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Items[1].Phase == "done" })
	data, _ := os.ReadFile(logPath)
	if string(data) != "pending\n" {
		t.Fatalf("replayed claimed prompt: %q", data)
	}
}

func TestClaudeQueueCannotAcknowledgeUnsavedMessage(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	r.paths.MessageQueue = filepath.Join(t.TempDir(), "missing", "queue.json")
	result := r.submitMessage(proto.MessageControl{OperationID: "unsaved", Text: "keep draft"})
	if result.Accepted || result.Boundary != "" || !strings.Contains(result.Error, "not queued") || len(r.queue.Items) != 0 {
		t.Fatalf("save failure = %+v, queue = %+v", result, r.queue)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.history) != 0 {
		t.Fatal("unsaved message appeared as accepted in history")
	}
}

func TestClaudeQueueLimitsDoNotDropAcceptedMessages(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	for index := range claudeQueueLimit {
		id := string(rune('A' + index))
		if result := r.submitMessage(proto.MessageControl{OperationID: id, Text: "saved"}); !result.Accepted {
			t.Fatal(result)
		}
	}
	result := r.submitMessage(proto.MessageControl{OperationID: "overflow", Text: "overflow"})
	if result.Accepted || len(r.queue.Items) != claudeQueueLimit {
		t.Fatalf("overflow = %+v", result)
	}
	data, err := os.ReadFile(r.paths.MessageQueue)
	var queue claudeMessageQueue
	if err != nil || json.Unmarshal(data, &queue) != nil || len(queue.Items) != claudeQueueLimit {
		t.Fatal("overflow altered durable queue")
	}
}

func TestClaudeQueueInterruptionAndStorageFailureNeverDrain(t *testing.T) {
	for _, storageFailure := range []bool{false, true} {
		r, logPath := newClaudeQueueFixture(t)
		if result := r.submitMessage(proto.MessageControl{OperationID: "saved", Text: "saved"}); !result.Accepted {
			t.Fatal(result)
		}
		if storageFailure {
			r.paths.MessageQueue = t.TempDir()
		}
		r.finishClaudeTurn("", storageFailure)
		if !r.queue.Paused {
			t.Fatal("interruption or failed queue commit did not pause")
		}
		if _, err := os.Stat(logPath); !os.IsNotExist(err) {
			t.Fatal("paused queue launched provider")
		}
	}
}

func TestClaudeQueueRejectsOversizeAndCorruptRecovery(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	if result := r.submitMessage(proto.MessageControl{OperationID: "oversize", Text: strings.Repeat("x", claudeQueueBytes+1)}); result.Accepted {
		t.Fatal("oversized prompt accepted")
	}
	if err := os.WriteFile(r.paths.MessageQueue, []byte(`{"version":1,"items":[{"phase":"queued","operation_id":"corrupt","text":"changed","hash":"wrong","boundary":"queue"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.loadMessageQueue(); err == nil {
		t.Fatal("corrupted prompt was eligible for replay")
	}
}

func TestClaudeQueueRetainsBoundedCompletedReceipts(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	for range 90 {
		r.queue.Items = append(r.queue.Items, claudeQueuedMessage{Phase: "done"})
	}
	r.finishClaudeTurn("", true)
	if len(r.queue.Items) != 64 {
		t.Fatalf("completed queue grew: %d", len(r.queue.Items))
	}
}

func TestClaudeQueueUndispatchedReceiptNeverClaimsTurnStarted(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	r.queue.Items = []claudeQueuedMessage{{ID: "saved", Text: "saved", Hash: claudeMessageHash("saved"), Phase: "queued", Boundary: "runner"}}
	if result := r.submitMessage(proto.MessageControl{OperationID: "saved", Text: "saved"}); !result.Accepted || result.Boundary != "queue" {
		t.Fatalf("unclaimed duplicate was mislabeled as started: %+v", result)
	}
}

// A pause holds back saved messages behind a failed or interrupted turn. With
// none saved and no failed turn retained it holds nothing, so a new message
// starts rather than waiting behind a Retry that has nothing to retry.
func TestClaudeQueuePauseHoldingNothingDoesNotParkNextMessage(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	r.active = false
	r.queue = claudeMessageQueue{Version: 1, Paused: true, Items: []claudeQueuedMessage{
		{ID: "interrupted", Text: "never resend", Phase: "dispatched", Hash: claudeMessageHash("never resend"), Boundary: "runner"},
	}}
	if result := r.submitMessage(proto.MessageControl{OperationID: "next", Text: "next"}); !result.Accepted || result.Boundary != "runner" || result.Error != "" {
		t.Fatalf("receipt = %+v", result)
	}
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return !queue.Paused && queue.Items[1].Phase == "done" })
	if data, _ := os.ReadFile(logPath); string(data) != "next\n" {
		t.Fatalf("provider calls = %q", data)
	}
}

func TestClaudeQueuePauseStillHoldsSavedMessagesAndFailedTurns(t *testing.T) {
	for _, held := range []string{"saved message", "failed turn"} {
		t.Run(held, func(t *testing.T) {
			r, logPath := newClaudeQueueFixture(t)
			r.active = false
			r.queue = claudeMessageQueue{Version: 1, Paused: true}
			r.retry = newStructuredRetryController(r.startRetryTurn, r.appendStructured, r.publishRetryState)
			if held == "saved message" {
				r.queue.Items = []claudeQueuedMessage{{ID: "saved", Text: "saved", Phase: "queued", Hash: claudeMessageHash("saved"), Boundary: "queue"}}
			} else {
				r.retry.Failed("failed", 0, providerfault.Fault{Kind: providerfault.KindAuth}, "permission denied")
			}
			result := r.submitMessage(proto.MessageControl{OperationID: "later", Text: "later"})
			if !result.Accepted || result.Boundary != "queue" || !strings.Contains(result.Error, "paused") || !r.queue.Paused {
				t.Fatalf("receipt = %+v, paused = %v", result, r.queue.Paused)
			}
			if _, err := os.Stat(logPath); !os.IsNotExist(err) {
				t.Fatal("a paused queue launched the provider")
			}
		})
	}
}

// A restart that finds only dispatched entries has nothing to pause: those
// entries are never replayed, and are reported as unknown instead.
func TestClaudeQueueRestartWithOnlyDispatchedEntriesDoesNotPause(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	queue := claudeMessageQueue{Version: 1, Items: []claudeQueuedMessage{
		{ID: "uncertain", Text: "never resend", Phase: "dispatched", Hash: claudeMessageHash("never resend"), Boundary: "queue"},
		{ID: "finished", Phase: "done", Hash: claudeMessageHash("finished"), Boundary: "runner"},
	}}
	if err := writeClaudeQueue(r.paths.MessageQueue, queue); err != nil {
		t.Fatal(err)
	}
	r.active = false
	if err := r.loadMessageQueue(); err != nil {
		t.Fatal(err)
	}
	if r.queue.Paused {
		t.Fatal("restart paused a queue with nothing undispatched")
	}
	if !r.queueHistoryIDs()["sessions-message:uncertain:unknown"] {
		t.Fatal("restart did not report the dispatched entry as unknown")
	}
	if result := r.submitMessage(proto.MessageControl{OperationID: "next", Text: "next"}); result.Boundary != "runner" {
		t.Fatalf("receipt = %+v", result)
	}
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Items[2].Phase == "done" })
	if data, _ := os.ReadFile(logPath); string(data) != "next\n" {
		t.Fatalf("restart replayed a dispatched message: %q", data)
	}
}

// Whether Result reports the stopped process or the cancellation first is a
// race. Either way an interruption ends the turn in history, records no
// failure, and leaves nothing for Retry to run again.
func TestClaudeInterruptionEndsTurnWithoutFailure(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	r.retry = newStructuredRetryController(r.startRetryTurn, r.appendStructured, r.publishRetryState)
	command := filepath.Join(r.cfg.cwd, "fake-claude")
	script := "#!/bin/sh\nprintf 'started\\n' >> \"$QUEUE_TEST_LOG\"\nexec /bin/sleep 30\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r.active = false
	for attempt := range 12 {
		id := fmt.Sprintf("slow-%d", attempt)
		if result := r.submitMessage(proto.MessageControl{OperationID: id, Text: id}); !result.Accepted {
			t.Fatal(result)
		}
		deadline := time.Now().Add(3 * time.Second)
		for data, _ := os.ReadFile(logPath); strings.Count(string(data), "started") <= attempt; data, _ = os.ReadFile(logPath) {
			if time.Now().After(deadline) {
				t.Fatal("inert provider never started")
			}
			time.Sleep(time.Millisecond)
		}
		r.handleInput("\x1b")
		waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Paused })
		r.mu.Lock()
		active := r.active
		r.mu.Unlock()
		if active || r.retry.hasFailedInput() {
			t.Fatalf("attempt %d: interruption left active=%v failed input=%v", attempt, active, r.retry.hasFailedInput())
		}
	}
	r.mu.Lock()
	history := cloneStructured(r.history)
	r.mu.Unlock()
	interrupted := 0
	for _, raw := range history {
		if working, ok := claudep.HistoryLifecycle(raw); ok && !working {
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			if value["subtype"] != "turn_interrupted" {
				t.Fatalf("interruption ended a turn as %s", raw)
			}
			interrupted++
		}
		if strings.Contains(string(raw), "provider_fault") {
			t.Fatalf("interruption recorded a provider fault: %s", raw)
		}
	}
	if interrupted != 12 {
		t.Fatalf("history ended %d interrupted turns, want 12", interrupted)
	}
}

// A provider that answered but has not exited still holds the turn. The
// runner says so where the person asks, and stops nothing on its own.
func TestClaudeAnsweredTurnWhoseProcessLingersSaysSo(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	command := filepath.Join(r.cfg.cwd, "fake-claude")
	script := "#!/bin/sh\nprintf 'started\\n' >> \"$QUEUE_TEST_LOG\"\n" +
		"printf '%s\\n' '{\"type\":\"result\",\"session_id\":\"fixture-claude\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"done\"}'\n" +
		"exec /bin/sleep 30\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r.active = false
	if result := r.submitMessage(proto.MessageControl{OperationID: "answered", Text: "answered"}); !result.Accepted {
		t.Fatal(result)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !r.answeredTurnStillOpen() {
		if time.Now().After(deadline) {
			t.Fatal("runner never recorded the provider's answer")
		}
		time.Sleep(time.Millisecond)
	}
	result := r.submitMessage(proto.MessageControl{OperationID: "next", Text: "next"})
	if !result.Accepted || result.Boundary != "queue" || !strings.Contains(result.Error, "has not exited") || !strings.Contains(result.Error, "do not resend") {
		t.Fatalf("receipt = %+v", result)
	}
	if data, _ := os.ReadFile(logPath); string(data) != "started\n" {
		t.Fatalf("a second provider process started: %q", data)
	}
	r.handleInput("\x1b")
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Paused })
	if r.answeredTurnStillOpen() || r.activeTurnReason() != "Claude turn is active" {
		t.Fatal("a new turn inherited the previous answer")
	}
}

// Retrying a failed turn that Claude then answers successfully, but whose
// process has to be interrupted, leaves no failed input behind: Claude's own
// result said it succeeded, so Retry must not run it a second time.
func TestClaudeInterruptedTurnAfterSuccessfulAnswerRetainsNoFailure(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	r.retry = newStructuredRetryController(r.startRetryTurn, r.appendStructured, r.publishRetryState)
	command := filepath.Join(r.cfg.cwd, "fake-claude")
	script := "#!/bin/sh\nprintf 'started\\n' >> \"$QUEUE_TEST_LOG\"\n" +
		"printf '%s\\n' '{\"type\":\"result\",\"session_id\":\"fixture-claude\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"done\"}'\n" +
		"exec /bin/sleep 30\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r.active = false
	r.retry.Failed("earlier", 0, providerfault.Fault{Kind: providerfault.KindAuth}, "permission denied")
	if err := r.retry.RunNow(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !r.answeredTurnStillOpen() {
		if time.Now().After(deadline) {
			t.Fatal("runner never recorded the provider's answer")
		}
		time.Sleep(time.Millisecond)
	}
	r.handleInput("\x1b")
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Paused })
	if r.retry.hasFailedInput() {
		t.Fatal("a turn Claude answered successfully stayed retained as a failure")
	}
	if data, _ := os.ReadFile(logPath); string(data) != "started\n" {
		t.Fatalf("provider calls = %q", data)
	}
}

// Claude answers successfully, and only afterwards does its process report
// an error: a nonzero exit, or a stream line it could not read. The answered
// input is never retained for Retry or replayed; the error stays visible in
// history as what it is; later saved messages stay saved, paused for an
// explicit Retry rather than dropped or started behind an abnormal exit.
func TestClaudeAnsweredTurnThenProcessErrorNeverReplaysAnsweredInput(t *testing.T) {
	for name, tail := range map[string]string{
		"nonzero exit":    "exit 1\n",
		"unreadable line": "printf 'not json\\n'\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, logPath := newClaudeQueueFixture(t)
			r.retry = newStructuredRetryController(r.startRetryTurn, r.appendStructured, r.publishRetryState)
			command := filepath.Join(r.cfg.cwd, "fake-claude")
			script := "#!/bin/sh\nfor prompt do :; done\nprintf '%s\\n' \"$prompt\" >> \"$QUEUE_TEST_LOG\"\n" +
				"printf '%s\\n' '{\"type\":\"result\",\"session_id\":\"fixture-claude\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"done\"}'\n" +
				"if [ \"$prompt\" = answered ]; then\n" + tail + "fi\n"
			if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"answered", "later"} {
				if result := r.submitMessage(proto.MessageControl{OperationID: text, Text: text}); !result.Accepted || result.Boundary != "queue" {
					t.Fatalf("receipt = %+v", result)
				}
			}
			r.finishClaudeTurn("", true)
			waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Paused && queue.Items[0].Phase == "done" })
			if r.queue.Items[1].Phase != "queued" || r.retry.hasFailedInput() {
				t.Fatalf("queue = %+v, failed input retained = %v", r.queue, r.retry.hasFailedInput())
			}
			r.mu.Lock()
			history := cloneStructured(r.history)
			r.mu.Unlock()
			exitRecords := 0
			for _, raw := range history {
				var value map[string]any
				_ = json.Unmarshal(raw, &value)
				if value["subtype"] == "provider_fault" || (value["type"] == "result" && value["is_error"] == true) {
					t.Fatalf("an answered turn was recorded as failed: %s", raw)
				}
				if value["subtype"] == "provider_exit_after_result" {
					exitRecords++
					if detail, _ := value["detail"].(string); !strings.Contains(detail, "will not be run again") || value["error"] == "" {
						t.Fatalf("exit record = %s", raw)
					}
				}
			}
			if exitRecords != 1 {
				t.Fatalf("history kept %d records of the process error, want 1", exitRecords)
			}
			if started, err := r.resumeClaudeQueue(); !started || err != nil {
				t.Fatalf("resume = %v, %v", started, err)
			}
			waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool { return queue.Items[1].Phase == "done" && !queue.Paused })
			if data, _ := os.ReadFile(logPath); string(data) != "answered\nlater\n" {
				t.Fatalf("provider calls = %q; the answered message must run exactly once", data)
			}
		})
	}
}
