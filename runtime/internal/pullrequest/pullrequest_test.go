package pullrequest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

func TestParseCanonicalizesAcceptedForms(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/acme/app/pull/42",
		"  https://github.com/acme/app/pull/42  ",
		"https://github.com/acme/app/pull/42/",
		"https://github.com/acme/app/pull/42/files",
		"https://github.com/acme/app/pull/42/commits",
		"https://github.com/acme/app/pull/42/checks",
		"https://github.com/acme/app/pull/42#issuecomment-1",
		"https://github.com/acme/app/pull/42?w=1",
		"https://www.github.com/acme/app/pull/42",
		"HTTPS://GitHub.com/Acme/App/pull/42",
	} {
		ref, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) = %v", raw, err)
		}
		want := ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/42", Host: "github.com", Owner: "acme", Repo: "app", Number: 42}
		if ref != want {
			t.Fatalf("Parse(%q) = %#v, want %#v", raw, ref, want)
		}
	}
}

func TestParseRefusesMalformedURLs(t *testing.T) {
	for _, raw := range []string{
		"",
		"   ",
		"github.com/acme/app/pull/42",
		"http://github.com/acme/app/pull/42",
		"ftp://github.com/acme/app/pull/42",
		"https://user:secret@github.com/acme/app/pull/42",
		"https://github.com:443/acme/app/pull/42",
		"https://gitlab.com/acme/app/pull/42",
		"https://github.com.evil.example/acme/app/pull/42",
		"https://api.github.com/repos/acme/app/pulls/42",
		"https://github.com/acme/app/issues/42",
		"https://github.com/acme/app/pull/",
		"https://github.com/acme/app/pull/0",
		"https://github.com/acme/app/pull/042",
		"https://github.com/acme/app/pull/-1",
		"https://github.com/acme/app/pull/4x2",
		"https://github.com/acme/app/pull/1234567890",
		"https://github.com/acme/app/pull/42/merge",
		"https://github.com/acme/app/pull/42/files/extra",
		"https://github.com/acme/app.git/pull/42",
		"https://github.com/-acme/app/pull/42",
		"https://github.com/acme/../pull/42",
		"https://github.com/acme/a%2Fb/pull/42",
		"https://github.com//app/pull/42",
		"https:github.com/acme/app/pull/42",
		"https://github.com/acme/app/pull/42\n/evil",
	} {
		if _, err := Parse(raw); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidURL", raw, err)
		}
	}
}

func TestParseErrorIsInstructional(t *testing.T) {
	_, err := Parse("https://gitlab.com/acme/app/pull/42")
	if err == nil || !strings.Contains(err.Error(), urlShape) || !strings.Contains(err.Error(), "github.com") {
		t.Fatalf("error = %v, want the expected shape named", err)
	}
}

func TestDecodeSummarizesChecksWithoutKeepingContent(t *testing.T) {
	ref := ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/42", Number: 42}
	observation, err := decodeGH(ref, []byte(`{"number":42,"url":"https://github.com/acme/app/pull/42","state":"OPEN",
		"isDraft":true,"mergedAt":"","mergeCommit":null,"headRefName":"sessions/fix","headRefOid":"abc","baseRefName":"main",
		"mergeable":"CONFLICTING","title":"secret title","statusCheckRollup":[
		{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS"},
		{"__typename":"CheckRun","name":"lint","status":"IN_PROGRESS","conclusion":""},
		{"__typename":"StatusContext","context":"ci","state":"FAILURE"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := ledger.PullRequestObservation{State: "open", Draft: true, HeadRef: "sessions/fix", HeadSHA: "abc", BaseRef: "main",
		Mergeable: "conflicting", Checks: "failing", ChecksTotal: 3, ChecksFailing: 1, ChecksPending: 1}
	if observation != want {
		t.Fatalf("observation = %#v, want %#v", observation, want)
	}

	for name, rollup := range map[string]string{
		ChecksNone:    `[]`,
		ChecksPassing: `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SKIPPED"},{"__typename":"StatusContext","state":"SUCCESS"}]`,
		ChecksPending: `[{"__typename":"CheckRun","status":"QUEUED"},{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]`,
		ChecksUnknown: `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SOMETHING_NEW"}]`,
	} {
		observation, err := decodeGH(ref, []byte(`{"number":42,"url":"https://github.com/Acme/App/pull/42","state":"MERGED","mergeable":"UNKNOWN","mergeCommit":{"oid":"def"},"statusCheckRollup":`+rollup+`}`))
		if err != nil {
			t.Fatal(err)
		}
		if observation.Checks != name || observation.State != StateMerged || observation.MergeCommit != "def" {
			t.Fatalf("rollup %s summarized as %#v", rollup, observation)
		}
	}
}

func TestDecodeRefusesMalformedAndMismatchedAnswers(t *testing.T) {
	ref := ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/42", Number: 42}
	url := `"https://github.com/acme/app/pull/42"`
	for body, kind := range map[string]string{
		`not json`:         ErrorMalformedResponse,
		`{"state":"OPEN"}`: ErrorMalformedResponse,
		`{"number":42,"url":` + url + `,"state":"HALF_MERGED"}`:  ErrorMalformedResponse,
		`{"number":7,"url":` + url + `,"state":"OPEN"}`:          ErrorMismatch,
		`{"number":42,"url":` + url + `,"state":"OPEN"} garbage`: ErrorMalformedResponse,
		// The number alone is not identity: the same number in another
		// repository, a renamed or transferred repository, a different host,
		// or no URL at all is never accepted as the linked pull request.
		`{"number":42,"state":"OPEN"}`:                                                     ErrorMalformedResponse,
		`{"number":42,"url":"","state":"OPEN"}`:                                            ErrorMalformedResponse,
		`{"number":42,"url":"https://github.com/evil/app/pull/42","state":"OPEN"}`:         ErrorMismatch,
		`{"number":42,"url":"https://github.com/acme/app-renamed/pull/42","state":"OPEN"}`: ErrorMismatch,
		`{"number":42,"url":"https://github.example.com/acme/app/pull/42","state":"OPEN"}`: ErrorMismatch,
		`{"number":42,"url":"https://github.com/acme/app/pull/43","state":"OPEN"}`:         ErrorMismatch,
		`{"number":42,"url":"not a url","state":"OPEN"}`:                                   ErrorMismatch,
	} {
		_, err := decodeGH(ref, []byte(body))
		var fetchErr *FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Kind != kind {
			t.Fatalf("decodeGH(%q) error = %v, want kind %s", body, err, kind)
		}
	}
}

// fakeGH writes a shell script that stands in for gh. It never contacts the
// network and records its argv so the test can prove only a read was run.
func fakeGH(t *testing.T, script string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a POSIX shell script")
	}
	directory := t.TempDir()
	argvPath := filepath.Join(directory, "argv")
	path := filepath.Join(directory, "gh")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argvPath + "'\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, argvPath
}

func TestGHFetchClassifiesFailures(t *testing.T) {
	ref := ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/42", Number: 42}
	cases := map[string]string{
		ErrorAuth:        `echo 'To get started with GitHub CLI, please run:  gh auth login' >&2; exit 4`,
		ErrorNotFound:    `echo 'GraphQL: Could not resolve to a PullRequest with the number of 42.' >&2; exit 1`,
		ErrorNetwork:     `echo 'error connecting to api.github.com' >&2; exit 1`,
		ErrorRateLimited: `echo 'HTTP 403: API rate limit exceeded' >&2; exit 1`,
		ErrorFailed:      `echo 'something unexpected' >&2; exit 1`,
	}
	for kind, script := range cases {
		path, _ := fakeGH(t, script)
		_, err := GH{Path: path}.Fetch(context.Background(), ref)
		var fetchErr *FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Kind != kind {
			t.Fatalf("script %q error = %v, want kind %s", script, err, kind)
		}
		if !strings.Contains(fetchErr.Message, ref.URL) {
			t.Fatalf("message %q does not name the pull request", fetchErr.Message)
		}
	}

	path, _ := fakeGH(t, `sleep 5`)
	_, err := GH{Path: path, Timeout: 50 * time.Millisecond}.Fetch(context.Background(), ref)
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Kind != ErrorTimeout {
		t.Fatalf("slow gh error = %v, want timeout", err)
	}

	_, err = GH{Path: filepath.Join(t.TempDir(), "missing-gh")}.Fetch(context.Background(), ref)
	if !errors.As(err, &fetchErr) || fetchErr.Kind != ErrorGHMissing {
		t.Fatalf("missing gh error = %v, want gh_missing", err)
	}
}

func TestGHFetchRunsOnlyAReadAndDecodes(t *testing.T) {
	ref := ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/42", Number: 42}
	path, argvPath := fakeGH(t, `echo '{"number":42,"url":"https://github.com/acme/app/pull/42","state":"OPEN","mergeable":"MERGEABLE","statusCheckRollup":[]}'`)
	observation, err := GH{Path: path}.Fetch(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != StateOpen || observation.Mergeable != MergeableYes || observation.Checks != ChecksNone {
		t.Fatalf("observation = %#v", observation)
	}
	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(argv)), []string{"pr", "view", ref.URL, "--json", ghFields}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("gh argv = %q, want %q", got, want)
	}
}

func TestComposeClosurePolicyNeverOffersToEndLiveWork(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	lane := ledger.LaneState{LaneID: "lane", Created: true, Branch: "sessions/fix"}
	lane.PullRequest = ledger.LanePullRequest{
		PullRequestRef: ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/42", Host: "github.com", Owner: "acme", Repo: "app", Number: 42},
		Observed:       true, ObservedAtMS: now.Add(-time.Hour).UnixMilli(),
		Observation: ledger.PullRequestObservation{State: "open", HeadRef: "sessions/fix", Checks: "passing", Mergeable: "mergeable"},
	}
	open := Compose(lane, true, time.Time{}, now)
	if open.Freshness != FreshnessStale || open.Work != WorkOpen || open.Closure.Action != ClosureNone || open.BranchMatch != "match" {
		t.Fatalf("stale open = %#v", open)
	}
	if open.FreshForMS != 0 {
		t.Fatalf("a stale answer claims %dms of freshness", open.FreshForMS)
	}
	confirmed := Compose(lane, true, now.Add(-time.Minute), now)
	if confirmed.Freshness != FreshnessFresh {
		t.Fatalf("a recent identical read must count as fresh, got %s", confirmed.Freshness)
	}
	// A client ages the answer by this duration, so it must be exactly what
	// is left of the window, never the whole window.
	if want := (FreshFor - time.Minute).Milliseconds(); confirmed.FreshForMS != want {
		t.Fatalf("fresh_for_ms = %d, want %d", confirmed.FreshForMS, want)
	}

	lane.PullRequest.Merged = true
	lane.PullRequest.Observation.State = "merged"
	lane.PullRequest.Observation.HeadRef = "other"
	for _, test := range []struct {
		live   bool
		lane   func(*ledger.LaneState)
		runner string
		action string
	}{
		{live: true, lane: func(*ledger.LaneState) {}, runner: RunnerLive, action: ClosureKeep},
		{live: false, lane: func(*ledger.LaneState) {}, runner: RunnerNotConnected, action: ClosureKeep},
		{live: true, lane: func(l *ledger.LaneState) { l.UserKillRequested = true }, runner: RunnerLive, action: ClosureKeep},
		{live: false, lane: func(l *ledger.LaneState) { l.RunnerExited = true }, runner: RunnerEnded, action: ClosureArchiveAvailable},
	} {
		copied := lane
		test.lane(&copied)
		status := Compose(copied, test.live, time.Time{}, now)
		if status.Runner != test.runner || status.Closure.Action != test.action || status.Work != WorkMerged ||
			status.Freshness != FreshnessFinal || status.BranchMatch != "mismatch" {
			t.Fatalf("merged live=%v = %#v closure=%#v", test.live, status, status.Closure)
		}
		if test.runner != RunnerEnded && strings.Contains(status.Closure.Reason, "archive") {
			t.Fatalf("a lane that has not ended was offered archive: %q", status.Closure.Reason)
		}
	}
}
