package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// bare observations object. Every observation value is preserved unchanged:
// nested source labels, nulls and unknown fields keep the meaning the
// observation contract gives them, and no field is renamed, flattened or
// interpreted. The bytes are re-encoded compactly without HTML escaping; the
// snapshot hash is over its canonical form.
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
		return nil, fmt.Errorf("the record has no session id; save the full session record with `sessions --json status <session>` and export that file, or run `sessions review snapshot <session>`")
	}
	snapshot := Snapshot{Schema: SnapshotSchema, Subject: options.Subject, CapturedAt: options.CapturedAt.UTC().Format(time.RFC3339),
		Producer: firstNonEmpty(options.Producer, "sessions review snapshot"), Observations: observations,
		Task: options.Task, Tail: options.Tail}
	encoded, err := encodeUnescaped(snapshot)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxSnapshotBytes {
		return nil, oversizedExport(len(encoded), options)
	}
	if _, err := DecodeSnapshot(encoded); err != nil {
		return nil, fmt.Errorf("exported snapshot failed validation: %w", err)
	}
	return encoded, nil
}

func locateObservations(record []byte) (json.RawMessage, string, error) {
	// The same structural rules as a snapshot: one JSON object, no duplicate
	// keys at any depth (a duplicate would let two readers see different
	// evidence), and nothing after it.
	if err := rejectDuplicateKeys(record); err != nil {
		return nil, "", fmt.Errorf("observation record: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(record))
	var top map[string]json.RawMessage
	if err := decoder.Decode(&top); err != nil || top == nil {
		return nil, "", fmt.Errorf("observation record is not a JSON object: %v", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, "", fmt.Errorf("observation record has data after the JSON object")
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

// oversizedExport says which caller-supplied part to shorten. Text within its
// own byte limit can still grow when encoded (a control character takes six
// bytes), so the encoded sizes are what the message reports.
func oversizedExport(size int, options ExportOptions) error {
	taskSize, tailSize := 0, 0
	if options.Task != nil {
		encoded, _ := encodeUnescaped(options.Task.Text)
		taskSize = len(encoded)
	}
	if options.Tail != nil {
		encoded, _ := encodeUnescaped(options.Tail.Text)
		tailSize = len(encoded)
	}
	return fmt.Errorf("the snapshot would be %d bytes; the limit is %d. The task text encodes to %d bytes and the tail to %d; "+
		"shorten the task text or the tail (or omit one) and export again", size, MaxSnapshotBytes, taskSize, tailSize)
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
