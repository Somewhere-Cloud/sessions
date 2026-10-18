package delivery

import (
	"encoding/json"
	"slices"
	"testing"
)

func evidenceRecords(t *testing.T, values ...map[string]any) []json.RawMessage {
	t.Helper()
	records := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, raw)
	}
	return records
}

func TestClaudeEvidenceSeparatesEnqueueDispatchAndProviderRecords(t *testing.T) {
	const source = "claude-p-stream-json"
	records := evidenceRecords(t,
		map[string]any{"type": "queue-operation", "operation": "enqueue", "uuid": "sessions-message:op", "timestamp": "2026-10-18T08:11:00Z"},
		map[string]any{"type": "assistant", "source": source, "observed_at": "2026-10-18T08:11:01Z"},
		map[string]any{"type": "user", "uuid": "sessions-message:op", "origin": "sessions", "source": source, "timestamp": "2026-10-18T08:12:00Z"},
		map[string]any{"type": "claude", "subtype": "turn_started", "origin": "sessions", "source": source, "timestamp": "2026-10-18T08:12:00Z"},
		map[string]any{"type": "system", "subtype": "init", "source": source, "observed_at": "2026-10-18T08:12:02Z"},
		map[string]any{"type": "result", "is_error": true, "source": source, "observed_at": "2026-10-18T08:12:03Z"},
		map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "origin": "sessions", "source": source},
		map[string]any{"type": "claude", "subtype": "turn_started", "origin": "sessions", "source": source},
		map[string]any{"type": "result", "is_error": false, "source": source, "observed_at": "2026-10-18T08:13:00Z"},
		map[string]any{"type": "user", "uuid": "sessions-message:next", "origin": "sessions", "source": source},
		map[string]any{"type": "result", "is_error": true, "source": source, "observed_at": "2026-10-18T08:14:00Z"},
	)
	evidence := ObserveClaudeOperation(slices.Values(records), "op")
	millis := func(value string) int64 { return *recordMillis(value) }
	if evidence.Source != "retained-history" || evidence.EnqueuedAt == nil || *evidence.EnqueuedAt != millis("2026-10-18T08:11:00Z") ||
		evidence.Dispatch != DispatchObserved || *evidence.DispatchedAt != millis("2026-10-18T08:12:00Z") {
		t.Fatalf("evidence = %+v", evidence)
	}
	// Provider records before the dispatch belong to an earlier turn; the
	// retried turn's success is the latest result; the next message's turn
	// is not this one's.
	if *evidence.ProviderRecordObservedAt != millis("2026-10-18T08:12:02Z") || *evidence.ProviderResult != "success" ||
		*evidence.ProviderResultObservedAt != millis("2026-10-18T08:13:00Z") {
		t.Fatalf("provider evidence = %+v", evidence)
	}
}

func TestClaudeEvidenceSaysWhatIsNotObserved(t *testing.T) {
	saved := evidenceRecords(t, map[string]any{"type": "queue-operation", "operation": "enqueue", "uuid": "sessions-message:op", "timestamp": "2026-10-18T08:11:00Z"})
	evidence := ObserveClaudeOperation(slices.Values(saved), "op")
	if evidence.Dispatch != DispatchNotObserved || evidence.DispatchedAt != nil || evidence.ProviderRecordObservedAt != nil || evidence.ProviderResult != nil {
		t.Fatalf("saved-only evidence = %+v", evidence)
	}
	unknown := append(saved, evidenceRecords(t, map[string]any{"type": "queue-operation", "operation": "dispatch-unknown", "uuid": "sessions-message:op:unknown"})...)
	if evidence := ObserveClaudeOperation(slices.Values(unknown), "op"); evidence.Dispatch != DispatchUnknown || evidence.DispatchedAt != nil {
		t.Fatalf("restart evidence = %+v", evidence)
	}
	// A longer id that merely starts with this one is another operation.
	other := evidenceRecords(t, map[string]any{"type": "user", "uuid": "sessions-message:op-2", "origin": "sessions"})
	if evidence := ObserveClaudeOperation(slices.Values(other), "op"); evidence.Dispatch != DispatchNotObserved || evidence.EnqueuedAt != nil {
		t.Fatalf("prefix evidence = %+v", evidence)
	}
	if evidence := ObserveClaudeOperation(slices.Values([]json.RawMessage(nil)), "op"); evidence.EnqueuedAt != nil || evidence.Dispatch != DispatchNotObserved {
		t.Fatalf("empty evidence = %+v", evidence)
	}
}
