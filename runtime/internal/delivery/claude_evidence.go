package delivery

import (
	"bytes"
	"encoding/json"
	"iter"
	"time"
)

// ClaudeEvidence is what a structured Claude runner's own history records
// show about one accepted message, read from the window the daemon retains.
// Acceptance says the runner took the message; this says what happened after,
// as far as records show it. Every time is the runner's clock when it wrote or
// received a record, and is null when no record shows it. not-observed means
// only that the retained window holds no such record, not that it never
// happened. Nothing here says what the model read or understood.
type ClaudeEvidence struct {
	Source       string `json:"source"`
	EnqueuedAt   *int64 `json:"enqueued_at"`
	Dispatch     string `json:"dispatch"`
	DispatchedAt *int64 `json:"dispatched_at"`
	// ProviderRecordObservedAt is when the runner received the first record
	// Claude's process wrote for the turn this message started.
	ProviderRecordObservedAt *int64 `json:"provider_record_observed_at"`
	// ProviderResult is what Claude's own result record for that turn said
	// (success or error), and ProviderResultObservedAt when the runner got it.
	ProviderResult           *string `json:"provider_result"`
	ProviderResultObservedAt *int64  `json:"provider_result_observed_at"`
}

const (
	DispatchObserved    = "dispatched"
	DispatchUnknown     = "dispatch-unknown"
	DispatchNotObserved = "not-observed"
)

// ObserveClaudeOperation reads the records a structured Claude runner writes
// under "sessions-message:<operation id>": the enqueue record, the user record
// it writes when it starts a turn with the message, or the dispatch-unknown
// record a restarted runner writes for a claim it cannot settle. The provider
// records that follow the user record, up to the next message Sessions
// dispatched, belong to that message's turn, including a retry of it.
func ObserveClaudeOperation(events iter.Seq[json.RawMessage], operationID string) ClaudeEvidence {
	evidence := ClaudeEvidence{Source: "retained-history", Dispatch: DispatchNotObserved}
	id := "sessions-message:" + operationID
	marker := []byte(`"` + id)
	dispatched := false
	for raw := range events {
		if !dispatched && !bytes.Contains(raw, marker) {
			continue
		}
		var record struct {
			Type, Subtype, Operation, UUID, Origin, Source string
			Timestamp                                      string `json:"timestamp"`
			ObservedAt                                     string `json:"observed_at"`
			IsError                                        bool   `json:"is_error"`
		}
		if json.Unmarshal(raw, &record) != nil {
			continue
		}
		if !dispatched {
			switch {
			case record.Type == "queue-operation" && record.Operation == "enqueue" && record.UUID == id:
				evidence.EnqueuedAt = recordMillis(record.Timestamp)
			case record.Type == "queue-operation" && record.Operation == "dispatch-unknown" && record.UUID == id+":unknown":
				evidence.Dispatch = DispatchUnknown
			case record.Type == "user" && record.UUID == id:
				dispatched = true
				evidence.Dispatch, evidence.DispatchedAt = DispatchObserved, recordMillis(record.Timestamp)
			}
			continue
		}
		if record.Origin == "sessions" && record.Type == "user" {
			break
		}
		if record.Origin == "sessions" || record.Type == "queue-operation" || record.Source != "claude-p-stream-json" {
			continue
		}
		observed := recordMillis(record.ObservedAt)
		if evidence.ProviderRecordObservedAt == nil {
			evidence.ProviderRecordObservedAt = observed
		}
		if record.Type == "result" {
			result := "success"
			if record.IsError {
				result = "error"
			}
			evidence.ProviderResult, evidence.ProviderResultObservedAt = &result, observed
		}
	}
	return evidence
}

func recordMillis(value string) *int64 {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	millis := parsed.UnixMilli()
	return &millis
}
