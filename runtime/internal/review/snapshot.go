package review

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SnapshotSchema is the only snapshot version this package accepts.
const SnapshotSchema = "sessions.review-snapshot/1"

// Size limits keep a snapshot reviewable and bounded for every reviewer.
const (
	MaxSnapshotBytes = 256 << 10
	MaxTaskBytes     = 32 << 10
	MaxTailBytes     = 16 << 10
)

// FactSources is the closed provenance vocabulary. The observer names match the
// lane observation contract (provider-stream, transcript-file, rollout-file,
// terminal); self-report stays a claim; derived is a deterministic projection;
// operator is stated by a person.
var FactSources = map[string]bool{
	"runner": true, "ledger": true, "provider-stream": true, "transcript-file": true, "rollout-file": true,
	"terminal": true, "git": true, "integration": true, "self-report": true, "derived": true, "operator": true,
}

// SupportedSourceContracts lists the fact contracts a snapshot may declare it
// was exported from, by name and version. Anything else is rejected.
var SupportedSourceContracts = map[string]int{"sessions.lane-observations": 1}

// SourceContract names the projection the facts were exported from.
type SourceContract struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// Fact is one named observation with its provenance. Unknown is explicit:
// an unknown fact has no value, and a known fact cannot be JSON null.
type Fact struct {
	Source  string          `json:"source"`
	Value   json.RawMessage `json:"value,omitempty"`
	Unknown bool            `json:"unknown,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

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

// Snapshot is one frozen evidence snapshot. Fact names are defined by the
// producer (the core observation projection), not by this package.
type Snapshot struct {
	Schema         string            `json:"schema"`
	Subject        string            `json:"subject"`
	CapturedAt     string            `json:"captured_at"`
	Producer       string            `json:"producer"`
	SourceContract *SourceContract   `json:"source_contract,omitempty"`
	Capabilities   map[string]string `json:"capabilities"`
	Facts          map[string]Fact   `json:"facts"`
	Task           *Task             `json:"task,omitempty"`
	Tail           *Tail             `json:"tail,omitempty"`
}

// FrozenSnapshot is a validated snapshot with the exact canonical bytes and
// hash every reviewer receives.
type FrozenSnapshot struct {
	Snapshot  Snapshot
	Canonical []byte
	Hash      string
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
	return FrozenSnapshot{Snapshot: snapshot, Canonical: canonical, Hash: Hash(canonical)}, nil
}

func validateSnapshot(s Snapshot) error {
	if strings.TrimSpace(s.Subject) == "" || strings.TrimSpace(s.Producer) == "" {
		return fmt.Errorf("subject and producer are required")
	}
	if _, err := time.Parse(time.RFC3339, s.CapturedAt); err != nil {
		return fmt.Errorf("captured_at must be RFC3339: %w", err)
	}
	if c := s.SourceContract; c != nil {
		if supported, ok := SupportedSourceContracts[c.Name]; !ok || supported != c.Version {
			return fmt.Errorf("facts were exported from %s version %d, which this version cannot read", c.Name, c.Version)
		}
	}
	if s.Capabilities == nil || s.Facts == nil {
		return fmt.Errorf("capabilities and facts are required (use empty objects when there are none)")
	}
	for _, name := range sortedKeys(s.Facts) {
		if err := validateFact(name, s.Facts[name]); err != nil {
			return err
		}
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

func validateFact(name string, fact Fact) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("fact names must be non-empty")
	}
	if !FactSources[fact.Source] {
		return fmt.Errorf("fact %q has source %q; allowed: %s", name, fact.Source, strings.Join(sortedKeys(FactSources), ", "))
	}
	hasValue := len(fact.Value) > 0
	if fact.Unknown && hasValue {
		return fmt.Errorf("fact %q is unknown and also carries a value", name)
	}
	if !fact.Unknown && (!hasValue || string(fact.Value) == "null") {
		return fmt.Errorf("fact %q has no value; mark it \"unknown\": true instead of null or omission", name)
	}
	return nil
}

// UnknownFacts lists the facts the snapshot marks unknown, sorted.
func (f FrozenSnapshot) UnknownFacts() []string {
	var out []string
	for _, name := range sortedKeys(f.Snapshot.Facts) {
		if f.Snapshot.Facts[name].Unknown {
			out = append(out, name)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
