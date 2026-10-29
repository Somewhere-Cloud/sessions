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
