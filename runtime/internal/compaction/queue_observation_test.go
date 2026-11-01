package compaction

import (
	"encoding/json"
	"testing"
)

func TestQueuedInputAcrossRetainedSummary(t *testing.T) {
	events := []json.RawMessage{
		json.RawMessage(`{"type":"queue-operation","operation":"enqueue"}`),
		json.RawMessage(`{"type":"system","subtype":"compact_boundary"}`),
	}
	if !QueuedInput(events) {
		t.Fatal("retained summary concealed queued input")
	}
	events = append(events, json.RawMessage(`{"type":"queue-operation","operation":"dequeue"}`))
	if QueuedInput(events) {
		t.Fatal("latest queue operation not respected")
	}
}

// Editing or reordering a saved message leaves input waiting, and cancelling
// one saved message does not by itself say the queue is empty.
func TestQueuedInputAfterSessionsQueueChanges(t *testing.T) {
	base := []json.RawMessage{json.RawMessage(`{"type":"queue-operation","operation":"enqueue","source":"sessions-next-turn"}`)}
	for _, change := range []string{
		`{"type":"queue-operation","operation":"edit","source":"sessions-next-turn"}`,
		`{"type":"queue-operation","operation":"reorder","source":"sessions-next-turn"}`,
		`{"type":"queue-operation","operation":"remove","source":"sessions-next-turn"}`,
	} {
		if !QueuedInput(append(append([]json.RawMessage(nil), base...), json.RawMessage(change))) {
			t.Fatalf("%s hid queued input", change)
		}
	}
	if QueuedInput([]json.RawMessage{json.RawMessage(`{"type":"queue-operation","operation":"remove"}`)}) {
		t.Fatal("a provider's own remove must still end the queue")
	}
}
