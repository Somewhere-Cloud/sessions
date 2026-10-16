package watch

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// collectEvents gathers provider records, passing over watcher signals; use
// collectStream to see the signals in their place.
func collectEvents(t *testing.T, events <-chan SessionEvent, count int, timeout time.Duration) []SessionEvent {
	t.Helper()
	return collect(t, events, count, timeout, false)
}

func collectStream(t *testing.T, events <-chan SessionEvent, count int, timeout time.Duration) []SessionEvent {
	t.Helper()
	return collect(t, events, count, timeout, true)
}

func collect(t *testing.T, events <-chan SessionEvent, count int, timeout time.Duration, signals bool) []SessionEvent {
	t.Helper()
	collected := make([]SessionEvent, 0, count)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(collected) < count {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event channel closed after %d/%d events", len(collected), count)
			}
			if _, isSignal := SignalOf(event); isSignal && !signals {
				continue
			}
			collected = append(collected, event)
		case <-deadline.C:
			t.Fatalf("timed out after %d/%d events", len(collected), count)
		}
	}
	return collected
}

// assertNoEvent fails on any provider record within duration; watcher
// signals are not records and are passed over.
func assertNoEvent(t *testing.T, events <-chan SessionEvent, duration time.Duration) {
	t.Helper()
	deadline := time.After(duration)
	for {
		select {
		case event := <-events:
			if _, isSignal := SignalOf(event); isSignal {
				continue
			}
			t.Fatalf("unexpected event: %#v", event)
		case <-deadline:
			return
		}
	}
}

func assertEventsJSONEqual(t *testing.T, got, want []SessionEvent) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var gotDecoded any
	var wantDecoded any
	if err := json.Unmarshal(gotJSON, &gotDecoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantJSON, &wantDecoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDecoded, wantDecoded) {
		t.Fatalf("events = %s, want %s", gotJSON, wantJSON)
	}
}

func eventText(event SessionEvent) string {
	message, _ := event["message"].(map[string]any)
	content, _ := message["content"].([]any)
	text := ""
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if value, ok := block["text"].(string); ok {
			if text != "" {
				text += "\n"
			}
			text += value
		}
	}
	return text
}
