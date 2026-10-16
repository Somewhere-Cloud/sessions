package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExportOptions carries what the caller explicitly supplies besides the
// observation record. Task and tail are included only when given.
type ExportOptions struct {
	Subject    string
	CapturedAt time.Time
	Producer   string
	Task       *Task
	Tail       *Tail
}

// ExportObservations freezes one lane's core observations into a
// sessions.review-snapshot/1 document. record is either a session record as
// `sessions --json status` prints it (with an "observations" member) or a
// bare observations object. The observations object is embedded verbatim: its
// nested source labels and nulls keep the meaning the observation contract
// gives them, and no field is renamed, flattened or interpreted here.
func ExportObservations(record []byte, options ExportOptions) ([]byte, error) {
	observations, subject, err := locateObservations(record)
	if err != nil {
		return nil, err
	}
	if err := checkObservationsVersion(observations); err != nil {
		return nil, err
	}
	if options.Subject == "" {
		options.Subject = subject
	}
	if options.Subject == "" {
		return nil, fmt.Errorf("the record has no id; pass a subject")
	}
	snapshot := Snapshot{Schema: SnapshotSchema, Subject: options.Subject, CapturedAt: options.CapturedAt.UTC().Format(time.RFC3339),
		Producer: firstNonEmpty(options.Producer, "sessions review snapshot"), Observations: observations,
		Task: options.Task, Tail: options.Tail}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if _, err := DecodeSnapshot(encoded); err != nil {
		return nil, fmt.Errorf("exported snapshot failed validation: %w", err)
	}
	return encoded, nil
}

func locateObservations(record []byte) (json.RawMessage, string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(record, &top); err != nil {
		return nil, "", fmt.Errorf("observation record is not a JSON object: %w", err)
	}
	var subject string
	_ = json.Unmarshal(top["id"], &subject)
	if nested, ok := top["observations"]; ok {
		if string(bytes.TrimSpace(nested)) == "null" {
			return nil, "", fmt.Errorf("this session's observations are null; there is no observation projection to export")
		}
		return nested, subject, nil
	}
	if _, ok := top["version"]; ok {
		return json.RawMessage(record), subject, nil
	}
	return nil, "", fmt.Errorf("no observations found; this record or daemon does not report lane observations")
}

// BoundedTail keeps the last MaxTailBytes of text on a UTF-8 boundary and
// records how much was omitted, so a long excerpt is never silently cut.
func BoundedTail(text, provenance string) *Tail {
	omitted := 0
	if len(text) > MaxTailBytes {
		cut := len(text) - MaxTailBytes
		for cut < len(text) && (text[cut]&0xC0) == 0x80 {
			cut++
		}
		omitted, text = cut, text[cut:]
	}
	return &Tail{OptIn: true, Text: text, OmittedBytes: omitted, Provenance: provenance}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
