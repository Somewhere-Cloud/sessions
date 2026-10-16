package review

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// encoding/json matches struct fields case-insensitively. Field names in
// snapshots, questions and adapter replies must be exact, and the
// observations version must be the JSON integer 1 under the key "version",
// or two readers can see different evidence.
func TestFieldNamesAndObservationsVersionAreExact(t *testing.T) {
	withObservations := func(observations string) string {
		return `{"schema":"sessions.review-snapshot/1","subject":"s","captured_at":"2026-01-02T03:04:05Z","producer":"t","observations":` +
			observations + `}`
	}
	rejected := map[string]string{
		"case-folded duplicate version": withObservations(`{"version":2,"Version":1,"x":null}`),
		"only VERSION":                  withObservations(`{"VERSION":1}`),
		"string version":                withObservations(`{"version":"1"}`),
		"float version":                 withObservations(`{"version":1.0}`),
		"exponent version":              withObservations(`{"version":1e0}`),
		"boolean version":               withObservations(`{"version":true}`),
		"second Observations": `{"schema":"sessions.review-snapshot/1","subject":"s","captured_at":"2026-01-02T03:04:05Z","producer":"t",` +
			`"observations":{"version":2,"evil":true},"Observations":{"version":1}}`,
		"Subject and subject": strings.Replace(withObservations(`{"version":1}`), `"subject":"s"`, `"subject":"A","Subject":"B"`, 1),
		"capitalised task field": strings.Replace(withObservations(`{"version":1}`), `"producer":"t"`,
			`"producer":"t","task":{"Text":"x","provenance":"p"}`, 1),
	}
	for name, input := range rejected {
		if _, err := DecodeSnapshot([]byte(input)); err == nil {
			t.Errorf("%s: accepted, want rejection", name)
		}
	}
	// Observation keys are the core contract's data, not seam fields: keys
	// differing only in case are kept as they are, never folded.
	if _, err := DecodeSnapshot([]byte(withObservations(`{"version":1,"alpha":1,"Alpha":2}`))); err != nil {
		t.Errorf("observation keys differing only in case were refused: %v", err)
	}
	for name, input := range map[string]string{
		"Options":     strings.Replace(fixtureQuestions, `"options"`, `"Options":[],"options"`, 1),
		"option ID":   strings.Replace(fixtureQuestions, `{"id":"working"`, `{"ID":"working"`, 1),
		"abstain Id":  strings.Replace(fixtureQuestions, `{"id":"insufficient_evidence"`, `{"Id":"insufficient_evidence"`, 1),
		"SCHEMA only": strings.Replace(fixtureQuestions, `"schema"`, `"SCHEMA":"x","schema"`, 1),
	} {
		if _, err := DecodeQuestions([]byte(input)); err == nil {
			t.Errorf("questions %s: accepted, want rejection", name)
		}
	}
	s, q := frozen(t, fixtureSnapshot)
	ext, _ := fixtureReviewer(t, "casefold", 10*time.Second)
	if r := Compare(context.Background(), s, q, []Reviewer{ext}).Results[0]; r.Error == nil || r.Error.Kind != ErrMalformed {
		t.Fatalf("reply with \"Choice\": %+v", r)
	}
}

// The adapter must receive exactly the canonical bytes that were hashed,
// including characters json.Marshal would rewrite (<, >, &, U+2028).
func TestAdapterReceivesTheHashedBytesExactly(t *testing.T) {
	edge := "a<b & c>d \u2028 \u2029 é 😀 \\\" \u0001"
	quoted := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(edge, `\`, `\\`), `"`, `\"`), "\u0001", `\u0001`)
	snapshot := strings.Replace(fixtureSnapshot, `"synthetic-source"`, `"`+quoted+`"`, 1)
	s, err := DecodeSnapshot([]byte(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	q, err := DecodeQuestions([]byte(strings.Replace(fixtureQuestions, `"description":"w"`, `"description":"`+quoted+`"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(s.Canonical), "<") || !strings.Contains(string(q.Canonical), "&") {
		t.Fatalf("canonical bytes HTML-escaped: %s", s.Canonical)
	}
	ext, seen := fixtureReviewer(t, "ok", 10*time.Second)
	r := Compare(context.Background(), s, q, []Reviewer{ext}).Results[0]
	recorded, _ := os.ReadFile(seen)
	lines := strings.SplitN(string(recorded), "\n", 3)
	if r.Status != "ok" || len(lines) < 2 || lines[0] != s.Hash || lines[1] != q.Hash || r.InputSHA256 != s.Hash {
		t.Fatalf("adapter received snapshot %s questions %s, want %s %s (result %+v)", lines[0], lines[1], s.Hash, q.Hash, r)
	}
}
