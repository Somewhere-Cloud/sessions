package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SnapshotSchema is the only snapshot version this package accepts.
const SnapshotSchema = "sessions.review-snapshot/1"

// ObservationsVersion is the only lane observation contract version a
// snapshot may embed. The observations object itself is owned by the core
// observation contract and is embedded verbatim, with its own nested source
// labels and null-as-unknown semantics; this package does not interpret its
// fields.
const ObservationsVersion = 1

// Size limits keep a snapshot reviewable and bounded for every reviewer.
const (
	MaxSnapshotBytes = 256 << 10
	MaxTaskBytes     = 32 << 10
	MaxTailBytes     = 16 << 10
)

// Task is optional task text supplied by the caller, with its provenance.
type Task struct {
	Text       string `json:"text"`
	Provenance string `json:"provenance"`
}

// Tail is an optional, explicitly opted-in short transcript excerpt.
type Tail struct {
	OptIn        bool   `json:"opt_in"`
	Text         string `json:"text"`
	OmittedBytes int    `json:"omitted_bytes,omitempty"`
	Provenance   string `json:"provenance"`
}

// Snapshot is one frozen evidence snapshot: one lane's core observations
// object, verbatim, plus optional caller-supplied task text and tail.
type Snapshot struct {
	Schema       string          `json:"schema"`
	Subject      string          `json:"subject"`
	CapturedAt   string          `json:"captured_at"`
	Producer     string          `json:"producer"`
	Observations json.RawMessage `json:"observations"`
	Task         *Task           `json:"task,omitempty"`
	Tail         *Tail           `json:"tail,omitempty"`
}

// FrozenSnapshot is a validated snapshot with the exact canonical bytes and
// hash every reviewer receives.
type FrozenSnapshot struct {
	Snapshot  Snapshot
	Canonical []byte
	Hash      string
	nullPaths []string
}

// DecodeSnapshot validates encoded strictly and freezes it.
func DecodeSnapshot(encoded []byte) (FrozenSnapshot, error) {
	if len(encoded) > MaxSnapshotBytes {
		return FrozenSnapshot{}, fmt.Errorf("snapshot is %d bytes; the limit is %d", len(encoded), MaxSnapshotBytes)
	}
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(encoded, &probe); err != nil {
		return FrozenSnapshot{}, fmt.Errorf("snapshot is not a JSON object: %w", err)
	}
	if probe.Schema != SnapshotSchema {
		return FrozenSnapshot{}, fmt.Errorf("snapshot schema %q is not supported; this version reads only %q", probe.Schema, SnapshotSchema)
	}
	var snapshot Snapshot
	if err := decodeStrict(encoded, &snapshot); err != nil {
		return FrozenSnapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return FrozenSnapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	canonical, err := Canonical(encoded)
	if err != nil {
		return FrozenSnapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	paths, err := nullPaths(snapshot.Observations)
	if err != nil {
		return FrozenSnapshot{}, fmt.Errorf("snapshot observations: %w", err)
	}
	return FrozenSnapshot{Snapshot: snapshot, Canonical: canonical, Hash: Hash(canonical), nullPaths: paths}, nil
}

func validateSnapshot(s Snapshot) error {
	if strings.TrimSpace(s.Subject) == "" || strings.TrimSpace(s.Producer) == "" {
		return fmt.Errorf("subject and producer are required")
	}
	if _, err := time.Parse(time.RFC3339, s.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339: %w", err)
	}
	if err := checkObservationsVersion(s.Observations); err != nil {
		return err
	}
	if s.Task != nil && (len(s.Task.Text) > MaxTaskBytes || strings.TrimSpace(s.Task.Provenance) == "") {
		return fmt.Errorf("task needs provenance and at most %d bytes of text", MaxTaskBytes)
	}
	if s.Tail != nil {
		if !s.Tail.OptIn {
			return fmt.Errorf("tail is present without opt_in: true; a transcript excerpt is never included implicitly")
		}
		if len(s.Tail.Text) > MaxTailBytes || strings.TrimSpace(s.Tail.Provenance) == "" || s.Tail.OmittedBytes < 0 {
			return fmt.Errorf("tail needs provenance, omitted_bytes >= 0 and at most %d bytes of text", MaxTailBytes)
		}
	}
	return nil
}

// checkObservationsVersion accepts only a JSON object whose version is the
// supported observation contract version. It reads no other field.
func checkObservationsVersion(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("observations must be the lane's observations object; got %s", describeJSON(trimmed))
	}
	var probe struct {
		Version json.Number `json:"version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&probe); err != nil {
		return fmt.Errorf("observations: %w", err)
	}
	if probe.Version.String() != fmt.Sprint(ObservationsVersion) {
		return fmt.Errorf("observations version %q is not supported; this version reads only version %d", probe.Version.String(), ObservationsVersion)
	}
	return nil
}

func describeJSON(raw []byte) string {
	if len(raw) == 0 {
		return "nothing"
	}
	if len(raw) > 20 {
		return string(raw[:20]) + "…"
	}
	return string(raw)
}

// NullPaths lists every JSON path inside the observations object whose value
// is null (unknown under the observation contract), sorted. It is a generic
// walk: it does not know or assume any observation field names.
func (f FrozenSnapshot) NullPaths() []string {
	return append([]string{}, f.nullPaths...)
}

func nullPaths(raw json.RawMessage) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var out []string
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch typed := v.(type) {
		case nil:
			out = append(out, path)
		case map[string]any:
			for key, child := range typed {
				walk(joinPath(path, key), child)
			}
		case []any:
			for index, child := range typed {
				walk(fmt.Sprintf("%s[%d]", path, index), child)
			}
		}
	}
	walk("", value)
	sort.Strings(out)
	return out, nil
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
