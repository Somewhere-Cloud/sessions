package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const reviewSnapshotFixture = `{"schema":"sessions.review-snapshot/1","subject":"synthetic","captured_at":"2026-01-02T03:04:05Z",
 "producer":"test","capabilities":{},
 "facts":{"turn.open":{"source":"provider-stream","value":false},
  "last_assistant_text":{"source":"self-report","value":"Committed; I'm idle for review."},
  "runner.state":{"source":"runner","unknown":true}}}`

const reviewQuestionsFixture = `{"schema":"sessions.review-questions/1","id":"q","version":"1",
 "options":[{"id":"waiting_on_review","description":"r"},{"id":"claimed_done_unverified","description":"c"}],
 "abstain":{"id":"insufficient_evidence","description":"not a state"}}`

func reviewFiles(t *testing.T, snapshot string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	s, q := filepath.Join(dir, "snapshot.json"), filepath.Join(dir, "questions.json")
	if err := os.WriteFile(s, []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(q, []byte(reviewQuestionsFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return s, q
}

// review must never reach the daemon: every request to this server is a failure.
func countingDaemon(t *testing.T) (*httptest.Server, *atomic.Int64) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "review must not call the daemon", http.StatusTeapot)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestReviewCompareReadsOnlyNamedFilesAndNeverCallsTheDaemon(t *testing.T) {
	server, calls := countingDaemon(t)
	snapshot, questions := reviewFiles(t, reviewSnapshotFixture)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--host", server.URL, "--json", "review", "compare", "--snapshot", snapshot, "--questions", questions},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("review made %d daemon requests", calls.Load())
	}
	var comparison struct {
		Schema       string   `json:"schema"`
		Authority    string   `json:"authority"`
		UnknownFacts []string `json:"unknown_facts"`
		Results      []struct {
			Status string `json:"status"`
			Answer struct {
				Choice string `json:"choice"`
			} `json:"answer"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &comparison); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if comparison.Schema != "sessions.review-comparison/1" || comparison.Authority != "none" || len(comparison.Results) != 1 ||
		comparison.Results[0].Answer.Choice != "waiting_on_review" || strings.Join(comparison.UnknownFacts, ",") != "runner.state" {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestReviewValidateAndRejections(t *testing.T) {
	server, calls := countingDaemon(t)
	snapshot, questions := reviewFiles(t, reviewSnapshotFixture)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "review", "validate", snapshot}, strings.NewReader(""), &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "1 unknown") {
		t.Fatalf("validate exit %d out %q err %q", code, stdout.String(), stderr.String())
	}
	future, _ := reviewFiles(t, strings.Replace(reviewSnapshotFixture, "snapshot/1", "snapshot/2", 1))
	for name, args := range map[string][]string{
		"future version":   {"review", "validate", future},
		"relative exec":    {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--reviewer", "exec:my-tool"},
		"stray flag":       {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--network", "on"},
		"orphan arg":       {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--reviewer-arg", "x"},
		"missing question": {"review", "compare", "--snapshot", snapshot},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(append([]string{"--host", server.URL}, args...), strings.NewReader(""), &stdout, &stderr); code == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("review made %d daemon requests", calls.Load())
	}
}
