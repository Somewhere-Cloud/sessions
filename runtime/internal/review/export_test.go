package review

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Synthetic observation records in the shape of the lane observation
// contract (version 1). No real lane data.
const structuredObservations = `{"version":1,
 "capabilities":{"runtime":"claude-structured","turn_boundaries":"provider-stream","tool_records":"provider-stream","terminal_output":false},
 "runner":{"state":"reachable","since":1760000000000,"exit_code":null},
 "working":{"value":true,"source":"provider-stream"},
 "turn":{"open":true,"started_at":1760000000000,"started_by":"sessions-dispatch","provider_confirmed_at":null,"ended_at":null,"ended_by":null},
 "last":{"provider_record_at":1760000001000,"terminal_output_at":null,"transcript_record_at":null,"input_dispatched_at":1760000000000,"input_confirmed_by_provider_at":null},
 "tools":{"open":[{"id":"t1","name":"Bash","started_at":1760000000500}],"open_count":1,"last_started_at":1760000000500,"last_ended_at":null,"history_complete":true},
 "observers":[]}`

const ptyObservations = `{"version":1,
 "capabilities":{"runtime":"claude-pty","turn_boundaries":"transcript-file","tool_records":"transcript-file","terminal_output":true},
 "runner":{"state":"reachable","since":1760000000000,"exit_code":null},
 "working":{"value":true,"source":"terminal-heuristic"},
 "turn":{"open":false,"started_at":1760000000000,"started_by":"provider","provider_confirmed_at":1760000000100,"ended_at":1760000002000,"ended_by":"transcript-end-turn"},
 "last":{"provider_record_at":1760000002000,"terminal_output_at":1760000003000,"transcript_record_at":1760000002000,"input_dispatched_at":null,"input_confirmed_by_provider_at":null},
 "tools":{"open":[],"open_count":0,"last_started_at":null,"last_ended_at":null,"history_complete":false},
 "observers":[{"name":"claude-transcript","state":"following","detail":"","since":null}]}`

const shellObservations = `{"version":1,
 "capabilities":{"runtime":"terminal","turn_boundaries":"none","tool_records":"none","terminal_output":true},
 "runner":{"state":"exited","since":1760000000000,"exit_code":0},
 "working":null,"turn":null,"tools":null,
 "last":{"provider_record_at":null,"terminal_output_at":1760000000000,"transcript_record_at":null,"input_dispatched_at":null,"input_confirmed_by_provider_at":null},
 "observers":[]}`

const restoredStatusRecord = `{"id":"00000000-0000-4000-8000-000000000009","kind":"claude","observations":
 {"version":1,"capabilities":null,"runner":null,"working":null,"turn":null,"last":null,"tools":null,"observers":null}}`

var exportAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func export(t *testing.T, record string, options ExportOptions) FrozenSnapshot {
	t.Helper()
	if options.Subject == "" && !strings.Contains(record, `"observations"`) {
		options.Subject = "synthetic-lane"
	}
	options.CapturedAt = exportAt
	encoded, err := ExportObservations([]byte(record), options)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	frozen, err := DecodeSnapshot(encoded)
	if err != nil {
		t.Fatalf("exported snapshot invalid: %v", err)
	}
	return frozen
}

func factOf(t *testing.T, s FrozenSnapshot, name string) Fact {
	t.Helper()
	fact, ok := s.Snapshot.Facts[name]
	if !ok {
		t.Fatalf("fact %s missing; have %v", name, sortedKeys(s.Snapshot.Facts))
	}
	return fact
}

func TestExportKeepsProvenanceAndNullAsUnknown(t *testing.T) {
	s := export(t, structuredObservations, ExportOptions{})
	if c := s.Snapshot.SourceContract; c == nil || c.Name != ObservationsContract || c.Version != 1 {
		t.Fatalf("source contract = %+v", c)
	}
	checks := map[string]string{"turn.open": "provider-stream", "runner.state": "runner", "tools.open_count": "provider-stream",
		"last.input_dispatched_at": "ledger", "working.value": "provider-stream", "working.source": "derived"}
	for name, source := range checks {
		if f := factOf(t, s, name); f.Source != source || f.Unknown {
			t.Errorf("%s = %+v, want known with source %s", name, f, source)
		}
	}
	for _, name := range []string{"turn.provider_confirmed_at", "turn.ended_at", "runner.exit_code", "last.terminal_output_at"} {
		if f := factOf(t, s, name); !f.Unknown || len(f.Value) != 0 {
			t.Errorf("%s = %+v, want explicit unknown (null in observations)", name, f)
		}
	}
	if s.Snapshot.Capabilities["turn_boundaries"] != "provider-stream" || s.Snapshot.Task != nil || s.Snapshot.Tail != nil {
		t.Fatalf("capabilities or implicit task/tail: %+v", s.Snapshot)
	}
}

func TestExportPTYAndShellUseTheirOwnSources(t *testing.T) {
	pty := export(t, ptyObservations, ExportOptions{})
	for name, source := range map[string]string{"turn.ended_by": "transcript-file", "last.terminal_output_at": "terminal",
		"working.value": "terminal", "observers.claude-transcript.state": "derived"} {
		if f := factOf(t, pty, name); f.Source != source {
			t.Errorf("pty %s source = %s, want %s", name, f.Source, source)
		}
	}
	shell := export(t, shellObservations, ExportOptions{})
	for _, block := range []string{"turn", "tools", "working"} {
		if f := factOf(t, shell, block); !f.Unknown {
			t.Errorf("shell %s = %+v, want unknown (capability none)", block, f)
		}
	}
	if _, ok := shell.Snapshot.Facts["turn.open"]; ok {
		t.Fatal("shell exported a turn field it never observed")
	}
}

func TestExportRestoredStatusRecordIsAllUnknown(t *testing.T) {
	s := export(t, restoredStatusRecord, ExportOptions{})
	if s.Snapshot.Subject != "00000000-0000-4000-8000-000000000009" {
		t.Fatalf("subject = %q", s.Snapshot.Subject)
	}
	if len(s.UnknownFacts()) != len(s.Snapshot.Facts) || len(s.Snapshot.Facts) == 0 {
		t.Fatalf("restored record exported known facts: %v of %v unknown", s.UnknownFacts(), sortedKeys(s.Snapshot.Facts))
	}
}

func TestExportRejectsOtherVersionsAndMissingObservations(t *testing.T) {
	for name, record := range map[string]string{
		"version 2":     strings.Replace(structuredObservations, `"version":1`, `"version":2`, 1),
		"null block":    `{"id":"x","observations":null}`,
		"no object":     `{"id":"x","name":"lane"}`,
		"not an object": `[1,2]`,
	} {
		if _, err := ExportObservations([]byte(record), ExportOptions{Subject: "s", CapturedAt: exportAt}); err == nil {
			t.Errorf("%s: exported, want rejection", name)
		}
	}
}

func TestExportMarksUnmappedFieldsUnknownInsteadOfGuessing(t *testing.T) {
	record := strings.Replace(structuredObservations, `"history_complete":true`, `"history_complete":true,"future_field":42`, 1)
	record = strings.Replace(record, `"observers":[]`, `"observers":[],"approval":{"pending":true}`, 1)
	s := export(t, record, ExportOptions{})
	for _, name := range []string{"tools.future_field", "approval"} {
		if f := factOf(t, s, name); !f.Unknown || !strings.Contains(f.Reason, "outside the v1 mapping") {
			t.Errorf("unmapped %s = %+v, want unknown with stated reason", name, f)
		}
	}
}

func TestExportIsDeterministicAndTaskTailAreExplicit(t *testing.T) {
	a := export(t, structuredObservations, ExportOptions{})
	b := export(t, structuredObservations, ExportOptions{})
	if a.Hash != b.Hash {
		t.Fatalf("same record and capture time gave different hashes: %s %s", a.Hash, b.Hash)
	}
	long := strings.Repeat("é", MaxTailBytes) // two bytes per rune
	with := export(t, structuredObservations, ExportOptions{Task: &Task{Text: "do x", Provenance: "test"},
		Tail: BoundedTail(long, "test")})
	if with.Snapshot.Tail == nil || !with.Snapshot.Tail.OptIn || with.Snapshot.Tail.OmittedBytes != len(long)-MaxTailBytes ||
		len(with.Snapshot.Tail.Text) != MaxTailBytes || !json.Valid([]byte(`"`+with.Snapshot.Tail.Text+`"`)) {
		t.Fatalf("tail = opt_in %v omitted %d len %d", with.Snapshot.Tail.OptIn, with.Snapshot.Tail.OmittedBytes, len(with.Snapshot.Tail.Text))
	}
	if with.Hash == a.Hash {
		t.Fatal("task and tail did not change the snapshot hash")
	}
}
