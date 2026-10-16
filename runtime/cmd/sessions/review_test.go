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
		comparison.Results[0].Status != "abstained" || comparison.Results[0].Answer.Choice != "insufficient_evidence" ||
		strings.Join(comparison.UnknownFacts, ",") != "runner.state" {
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
		"removed env flag": {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--reviewer", "exec:/bin/true", "--reviewer-env", "HOME"},
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

const reviewStatusFixture = `{"id":"00000000-0000-4000-8000-00000000000a","observations":{"version":1,
 "capabilities":{"runtime":"codex-app-server","turn_boundaries":"provider-stream","tool_records":"provider-stream","terminal_output":false},
 "runner":{"state":"reachable","since":1760000000000,"exit_code":null},"working":{"value":true,"source":"provider-stream"},
 "turn":{"open":true,"started_at":1760000000000,"started_by":"provider","provider_confirmed_at":1760000000100,"ended_at":null,"ended_by":null},
 "last":{"provider_record_at":1760000001000,"terminal_output_at":null,"transcript_record_at":null,"input_dispatched_at":null,"input_confirmed_by_provider_at":null},
 "tools":{"open":[],"open_count":0,"last_started_at":null,"last_ended_at":null,"history_complete":true},"observers":[]}}`

func TestReviewSnapshotExportsASavedStatusRecordWithoutTheDaemon(t *testing.T) {
	server, calls := countingDaemon(t)
	dir := t.TempDir()
	status, tail := filepath.Join(dir, "status.json"), filepath.Join(dir, "tail.txt")
	_ = os.WriteFile(status, []byte(reviewStatusFixture), 0o600)
	_ = os.WriteFile(tail, []byte("last lines"), 0o600)
	snapshotFor := func(extra ...string) map[string]any {
		var stdout, stderr bytes.Buffer
		args := append([]string{"--host", server.URL, "review", "snapshot", "--observations", status}, extra...)
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("snapshot exit %d stderr %q", code, stderr.String())
		}
		var snapshot map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
			t.Fatalf("snapshot output is not JSON: %v", err)
		}
		return snapshot
	}
	plain := snapshotFor()
	if plain["subject"] != "00000000-0000-4000-8000-00000000000a" || plain["tail"] != nil || plain["task"] != nil {
		t.Fatalf("snapshot = %v", plain)
	}
	if withTail := snapshotFor("--tail-file", tail); withTail["tail"] == nil {
		t.Fatal("--tail-file did not add the opted-in tail")
	}
	encoded, _ := json.Marshal(plain)
	saved := filepath.Join(dir, "snapshot.json")
	_ = os.WriteFile(saved, encoded, 0o600)
	_, questions := reviewFiles(t, reviewSnapshotFixture)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "--json", "review", "compare", "--snapshot", saved, "--questions", questions},
		strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"status": "abstained"`) {
		t.Fatalf("compare on exported snapshot: exit %d out %s err %s", code, stdout.String(), stderr.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("review made %d daemon requests", calls.Load())
	}
}
