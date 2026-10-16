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

// Synthetic observations; field names are arbitrary because review embeds the
// core object verbatim and interprets none of it.
const reviewObservationsFixture = `{"version":1,"alpha":{"value":true,"source":"synthetic"},"beta":null}`

const reviewSnapshotFixture = `{"schema":"sessions.review-snapshot/1","subject":"synthetic","captured_at":"2026-01-02T03:04:05Z",
 "producer":"test","observations":` + reviewObservationsFixture + `}`

const reviewQuestionsFixture = `{"schema":"sessions.review-questions/1","id":"q","version":"1",
 "options":[{"id":"a","description":"a"},{"id":"b","description":"b"}],
 "abstain":{"id":"insufficient_evidence","description":"not a state"}}`

const reviewSessionID = "00000000-0000-4000-8000-00000000000a"

func writeReviewFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// review compare/validate must never reach the daemon: any request fails the test.
func countingDaemon(t *testing.T) (*httptest.Server, *atomic.Int64) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "review must not call the daemon", http.StatusTeapot)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// sessionsDaemon serves one session listing record, as `sessions status` reads it.
func sessionsDaemon(t *testing.T, record string) (*httptest.Server, *atomic.Int64) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/sessions" {
			t.Errorf("unexpected daemon request %s %s", r.Method, r.URL.Path)
			http.Error(w, "no", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessions":[` + record + `]}`))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func runReview(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestReviewCompareReadsOnlyNamedFilesAndNeverCallsTheDaemon(t *testing.T) {
	server, calls := countingDaemon(t)
	snapshot := writeReviewFile(t, "snapshot.json", reviewSnapshotFixture)
	questions := writeReviewFile(t, "questions.json", reviewQuestionsFixture)
	code, stdout, stderr := runReview(t, "--host", server.URL, "--json", "review", "compare", "--snapshot", snapshot, "--questions", questions)
	if code != 0 || calls.Load() != 0 {
		t.Fatalf("exit %d, %d daemon requests, stderr %q", code, calls.Load(), stderr)
	}
	var comparison struct {
		Schema    string   `json:"schema"`
		Authority string   `json:"authority"`
		NullPaths []string `json:"null_paths"`
		Results   []struct {
			Status string `json:"status"`
			Answer struct {
				Choice string `json:"choice"`
			} `json:"answer"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &comparison); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if comparison.Schema != "sessions.review-comparison/1" || comparison.Authority != "none" || len(comparison.Results) != 1 ||
		comparison.Results[0].Status != "abstained" || comparison.Results[0].Answer.Choice != "insufficient_evidence" ||
		strings.Join(comparison.NullPaths, ",") != "beta" {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestReviewValidateAndRejections(t *testing.T) {
	server, calls := countingDaemon(t)
	snapshot := writeReviewFile(t, "snapshot.json", reviewSnapshotFixture)
	questions := writeReviewFile(t, "questions.json", reviewQuestionsFixture)
	if code, stdout, stderr := runReview(t, "--host", server.URL, "review", "validate", snapshot); code != 0 || !strings.Contains(stdout, "1 null (unknown) values") {
		t.Fatalf("validate exit %d out %q err %q", code, stdout, stderr)
	}
	future := writeReviewFile(t, "future.json", strings.Replace(reviewSnapshotFixture, `"version":1`, `"version":2`, 1))
	status := writeReviewFile(t, "status.json", `{"id":"x","observations":`+reviewObservationsFixture+`}`)
	for name, args := range map[string][]string{
		"future observations": {"review", "validate", future},
		"relative exec":       {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--reviewer", "exec:my-tool"},
		"stray flag":          {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--network", "on"},
		"orphan arg":          {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--reviewer-arg", "x"},
		"removed env flag":    {"review", "compare", "--snapshot", snapshot, "--questions", questions, "--reviewer", "exec:/bin/true", "--reviewer-env", "HOME"},
		"missing questions":   {"review", "compare", "--snapshot", snapshot},
		"session and file":    {"review", "snapshot", "abc", "--observations", status},
		"neither":             {"review", "snapshot", "--task-file", snapshot},
	} {
		if code, _, _ := runReview(t, append([]string{"--host", server.URL}, args...)...); code == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("review made %d daemon requests", calls.Load())
	}
}

func TestReviewSnapshotFromSavedRecordEmbedsObservationsWithoutTheDaemon(t *testing.T) {
	server, calls := countingDaemon(t)
	status := writeReviewFile(t, "status.json", `{"id":"`+reviewSessionID+`","name":"private name","observations":`+reviewObservationsFixture+`}`)
	tail := writeReviewFile(t, "tail.txt", "last lines")
	code, stdout, stderr := runReview(t, "--host", server.URL, "review", "snapshot", "--observations", status)
	if code != 0 || calls.Load() != 0 {
		t.Fatalf("exit %d, %d daemon requests, stderr %q", code, calls.Load(), stderr)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &snapshot); err != nil {
		t.Fatalf("snapshot output is not JSON: %v", err)
	}
	if string(snapshot["observations"]) != reviewObservationsFixture || snapshot["tail"] != nil || snapshot["task"] != nil ||
		strings.Contains(stdout, "private name") || string(snapshot["subject"]) != `"`+reviewSessionID+`"` {
		t.Fatalf("snapshot = %s", stdout)
	}
	if code, stdout, _ = runReview(t, "--host", server.URL, "review", "snapshot", "--observations", status, "--tail-file", tail); code != 0 ||
		!strings.Contains(stdout, `"opt_in":true`) {
		t.Fatalf("--tail-file did not add the opted-in tail: %s", stdout)
	}
}

func TestReviewSnapshotOfASessionReusesItsStatusRecord(t *testing.T) {
	server, calls := sessionsDaemon(t, `{"id":"`+reviewSessionID+`","observations":`+reviewObservationsFixture+`}`)
	code, stdout, stderr := runReview(t, "--host", server.URL, "review", "snapshot", reviewSessionID[:8])
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, `"observations":`+reviewObservationsFixture) || calls.Load() == 0 {
		t.Fatalf("snapshot did not embed the daemon's observations verbatim: %s", stdout)
	}
	without, _ := sessionsDaemon(t, `{"id":"`+reviewSessionID+`"}`)
	if code, _, stderr := runReview(t, "--host", without.URL, "review", "snapshot", reviewSessionID[:8]); code == 0 ||
		!strings.Contains(stderr, "does not report lane observations") {
		t.Fatalf("a daemon without observations: exit %d stderr %q", code, stderr)
	}
}

// A long regular tail file is bounded to its last 16 KiB, not refused, and
// the omitted byte count covers everything before the kept text.
func TestReviewSnapshotBoundsALongTailFileAndRejectsBadInput(t *testing.T) {
	server, calls := countingDaemon(t)
	status := writeReviewFile(t, "status.json", `{"id":"`+reviewSessionID+`","observations":`+reviewObservationsFixture+`}`)
	long := strings.Repeat("line é\n", 300000)
	tail := writeReviewFile(t, "tail.log", long)
	code, stdout, stderr := runReview(t, "--host", server.URL, "review", "snapshot", "--observations", status, "--tail-file", tail)
	if code != 0 {
		t.Fatalf("long tail refused: exit %d stderr %q", code, stderr)
	}
	var snapshot struct {
		Tail struct {
			Text         string `json:"text"`
			OmittedBytes int    `json:"omitted_bytes"`
		} `json:"tail"`
	}
	if err := json.Unmarshal([]byte(stdout), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tail.Text) > 16<<10 || snapshot.Tail.OmittedBytes+len(snapshot.Tail.Text) != len(long) ||
		!strings.HasSuffix(long, snapshot.Tail.Text) {
		t.Fatalf("tail kept %d bytes, omitted %d, of %d", len(snapshot.Tail.Text), snapshot.Tail.OmittedBytes, len(long))
	}
	invalid := writeReviewFile(t, "bad.txt", "ok \xff\xfe end")
	for name, args := range map[string][]string{
		"invalid UTF-8 task":  {"--task-file", invalid},
		"invalid UTF-8 tail":  {"--tail-file", invalid},
		"directory as a tail": {"--tail-file", t.TempDir()},
	} {
		code, _, stderr := runReview(t, append([]string{"--host", server.URL, "review", "snapshot", "--observations", status}, args...)...)
		if code == 0 || (strings.Contains(name, "UTF-8") && !strings.Contains(stderr, "UTF-8")) {
			t.Errorf("%s: exit %d stderr %q", name, code, stderr)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("snapshot from a file made %d daemon requests", calls.Load())
	}
}
