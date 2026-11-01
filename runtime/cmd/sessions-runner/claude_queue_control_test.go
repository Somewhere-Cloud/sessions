package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

func queueOne(t *testing.T, r *claudeStructuredRunner, id, text string) {
	t.Helper()
	if result := r.submitMessage(proto.MessageControl{OperationID: id, Text: text}); !result.Accepted || result.Boundary != "queue" {
		t.Fatalf("queue %s = %+v", id, result)
	}
}

func providerCalls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestClaudeQueueEditCancelMoveChangeWhatIsDispatched(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		queueOne(t, r, id, id)
	}
	if result := r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueEdit, OperationID: "b", Text: "b edited"}); result.Error != "" {
		t.Fatalf("edit = %+v", result)
	}
	if result := r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueCancel, OperationID: "c"}); result.Error != "" || result.Code != "" {
		t.Fatalf("cancel = %+v", result)
	}
	result := r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueMove, OperationID: "d", To: 1})
	if result.Error != "" || len(result.Items) != 3 {
		t.Fatalf("move = %+v", result)
	}
	order := []string{}
	for _, item := range result.Items {
		order = append(order, item.OperationID)
	}
	if strings.Join(order, ",") != "d,a,b" || result.Items[2].Text != "b edited" || result.Items[2].Edits != 1 || result.Items[0].Position != 1 {
		t.Fatalf("queue after changes = %+v", result.Items)
	}
	again := r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueCancel, OperationID: "c"})
	if again.Error != "" || again.Code != proto.QueueCodeAlreadyCancelled {
		t.Fatalf("second cancel = %+v", again)
	}
	if edit := r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueEdit, OperationID: "c", Text: "revive"}); edit.Code != proto.QueueCodeCancelled {
		t.Fatalf("edit of cancelled = %+v", edit)
	}
	if missing := r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueMove, OperationID: "zzz", To: 1}); missing.Code != proto.QueueCodeNotFound {
		t.Fatalf("missing = %+v", missing)
	}
	// The same operation resubmitted is still its original receipt; a
	// cancelled operation is never quietly queued again.
	if repeat := r.submitMessage(proto.MessageControl{OperationID: "b", Text: "b"}); !repeat.Accepted {
		t.Fatalf("resubmit of edited operation = %+v", repeat)
	}
	if revived := r.submitMessage(proto.MessageControl{OperationID: "c", Text: "c"}); revived.Accepted || !strings.Contains(revived.Error, "cancelled") {
		t.Fatalf("resubmit of cancelled operation = %+v", revived)
	}
	r.finishClaudeTurn("", true)
	waitClaudeQueue(t, r, func(queue claudeMessageQueue) bool {
		for _, item := range queue.Items {
			if item.Phase != "done" {
				return false
			}
		}
		return len(queue.Items) == 3
	})
	if calls := providerCalls(t, logPath); calls != "d\na\nb edited\n" {
		t.Fatalf("provider calls = %q", calls)
	}
}

func TestClaudeQueueRefusesChangesToDispatchedMessages(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	r.queue = claudeMessageQueue{Version: 1, Paused: true, Items: []claudeQueuedMessage{
		{ID: "current", Text: "retained", Hash: claudeMessageHash("retained"), Phase: "dispatched", Boundary: "queue"},
		{ID: "answered", Phase: "done", Hash: claudeMessageHash("x"), Boundary: "queue"},
	}}
	// A retained failed turn: current, but no provider process running.
	r.active, r.queueCurrent = false, "current"
	listed := r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueList})
	if len(listed.Items) != 1 || listed.Items[0].State != "dispatched" || listed.Items[0].Position != 0 || !listed.Paused {
		t.Fatalf("list = %+v", listed)
	}
	for _, control := range []proto.QueueControl{
		{Action: proto.QueueEdit, OperationID: "current", Text: "changed"},
		{Action: proto.QueueCancel, OperationID: "current"},
		{Action: proto.QueueCancel, OperationID: "answered"},
	} {
		result := r.controlClaudeQueue(control)
		if result.Code != proto.QueueCodeDispatched || result.Error == "" {
			t.Fatalf("%+v = %+v", control, result)
		}
	}
	if r.queue.Items[0].Text != "retained" || len(r.queue.Items) != 2 {
		t.Fatalf("refused change altered queue: %+v", r.queue.Items)
	}
}

// Each change races the claim that dispatches the message. Whichever wins,
// the answer must match what Claude was sent: an accepted cancel sends
// nothing, an accepted edit sends only the edit, and a refusal says the
// message was dispatched and Claude got the original.
func TestClaudeQueueChangesRaceDispatchSafely(t *testing.T) {
	for iteration := range 16 {
		action := proto.QueueCancel
		if iteration%2 == 1 {
			action = proto.QueueEdit
		}
		r, logPath := newClaudeQueueFixture(t)
		queueOne(t, r, "raced", "original")
		var result proto.QueueResult
		var wait sync.WaitGroup
		wait.Add(2)
		go func() { defer wait.Done(); r.finishClaudeTurn("", true) }()
		go func() {
			defer wait.Done()
			// Stagger the change so both orders occur across iterations.
			time.Sleep(time.Duration(iteration/2%4) * 300 * time.Microsecond)
			result = r.controlClaudeQueue(proto.QueueControl{Action: action, OperationID: "raced", Text: "edited"})
		}()
		wait.Wait()
		switch {
		case result.Error == "" && action == proto.QueueCancel:
			time.Sleep(50 * time.Millisecond)
			if calls := providerCalls(t, logPath); calls != "" {
				t.Fatalf("accepted cancel still dispatched: %q", calls)
			}
		case result.Error == "":
			waitProviderCalls(t, logPath, "edited\n")
		case result.Code == proto.QueueCodeDispatched:
			waitProviderCalls(t, logPath, "original\n")
		default:
			t.Fatalf("unexpected race answer: %+v", result)
		}
	}
}

func waitProviderCalls(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if providerCalls(t, path) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("provider calls = %q, want %q", providerCalls(t, path), want)
}

func TestClaudeQueueChangesAreRecordedAndSurviveRestart(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	queueOne(t, r, "kept", "first text")
	queueOne(t, r, "dropped", "drop me")
	r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueEdit, OperationID: "kept", Text: "second text"})
	r.controlClaudeQueue(proto.QueueControl{Action: proto.QueueCancel, OperationID: "dropped"})
	operations := map[string]map[string]any{}
	r.mu.Lock()
	for _, raw := range r.history {
		var event map[string]any
		_ = json.Unmarshal(raw, &event)
		if op, _ := event["operation"].(string); op != "" {
			operations[op] = event
		}
	}
	r.mu.Unlock()
	if operations["edit"]["content"] != "second text" || operations["edit"]["operation_id"] != "sessions-message:kept" ||
		operations["remove"]["uuid"] != "sessions-message:dropped:cancelled" {
		t.Fatalf("history records = %+v", operations)
	}
	r.queue = claudeMessageQueue{}
	if err := r.loadMessageQueue(); err != nil {
		t.Fatalf("edited queue did not reload: %v", err)
	}
	if len(r.queue.Items) != 1 || r.queue.Items[0].Text != "second text" || r.queue.Items[0].SubmittedHash != claudeMessageHash("first text") {
		t.Fatalf("reloaded queue = %+v", r.queue.Items)
	}
}
