package review

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Synthetic records. Field names inside observations are arbitrary on purpose:
// the exporter must embed whatever the core projection reports, verbatim.
const bareObservations = `{"version":1,"alpha":{"value":true,"source":"synthetic"},"beta":null,"tools":{"open":[],"complete":false}}`

const statusRecord = `{"id":"00000000-0000-4000-8000-000000000009","kind":"claude","name":"x","observations":` + bareObservations + `}`

var exportAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func exportSnapshot(t *testing.T, record string, options ExportOptions) FrozenSnapshot {
	t.Helper()
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

func canonicalOf(t *testing.T, raw string) string {
	t.Helper()
	c, err := Canonical([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return string(c)
}

func TestExportEmbedsCoreObservationsVerbatim(t *testing.T) {
	s := exportSnapshot(t, statusRecord, ExportOptions{})
	if s.Snapshot.Subject != "00000000-0000-4000-8000-000000000009" || s.Snapshot.Task != nil || s.Snapshot.Tail != nil {
		t.Fatalf("subject/task/tail = %q %v %v", s.Snapshot.Subject, s.Snapshot.Task, s.Snapshot.Tail)
	}
	if got, want := canonicalOf(t, string(s.Snapshot.Observations)), canonicalOf(t, bareObservations); got != want {
		t.Fatalf("observations changed on export:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(string(s.Canonical), `"name":"x"`) {
		t.Fatal("non-observation fields of the session record leaked into the snapshot")
	}
	if got := strings.Join(s.NullPaths(), ","); got != "beta" {
		t.Fatalf("null paths = %s", got)
	}
	bare := exportSnapshot(t, bareObservations, ExportOptions{Subject: "given"})
	if string(bare.Snapshot.Observations) == "" || bare.Snapshot.Subject != "given" {
		t.Fatalf("bare observations not exported: %+v", bare.Snapshot)
	}
}

func TestExportRejectsOtherVersionsAndMissingObservations(t *testing.T) {
	for name, record := range map[string]string{
		"version 2":         strings.Replace(statusRecord, `"version":1`, `"version":2`, 1),
		"version missing":   strings.Replace(statusRecord, `"version":1,`, ``, 1),
		"null observations": `{"id":"x","observations":null}`,
		"no observations":   `{"id":"x","name":"lane"}`,
		"not an object":     `[1,2]`,
		"no subject":        bareObservations,
		"null record":       `null`,
		"duplicate id":      `{"id":"a","id":"b","observations":` + bareObservations + `}`,
		"two observations":  `{"id":"a","observations":` + bareObservations + `,"observations":{"version":1}}`,
		"nested duplicate":  `{"id":"a","observations":{"version":1,"beta":null,"beta":true}}`,
		"trailing document": statusRecord + `{"id":"other"}`,
		"trailing garbage":  statusRecord + ` x`,
	} {
		if _, err := ExportObservations([]byte(record), ExportOptions{CapturedAt: exportAt}); err == nil {
			t.Errorf("%s: exported, want rejection", name)
		}
	}
}

func TestExportIsDeterministicAndTaskTailAreExplicit(t *testing.T) {
	a := exportSnapshot(t, statusRecord, ExportOptions{})
	b := exportSnapshot(t, statusRecord, ExportOptions{})
	if a.Hash != b.Hash {
		t.Fatalf("same record and capture time gave different hashes: %s %s", a.Hash, b.Hash)
	}
	long := strings.Repeat("é", MaxTailBytes) // two bytes per rune
	with := exportSnapshot(t, statusRecord, ExportOptions{Task: &Task{Text: "do x", Provenance: "test"}, Tail: BoundedTail(long, "test")})
	tail := with.Snapshot.Tail
	if tail == nil || !tail.OptIn || tail.OmittedBytes != len(long)-MaxTailBytes || len(tail.Text) != MaxTailBytes ||
		!json.Valid([]byte(`"`+tail.Text+`"`)) {
		t.Fatalf("tail = %+v", tail)
	}
	if with.Hash == a.Hash {
		t.Fatal("task and tail did not change the snapshot hash")
	}
}
