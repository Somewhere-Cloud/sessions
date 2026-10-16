package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a synthetic external reviewer when this
// variable is set; the adapter inherits it from the test's environment.
const fixtureMode = "REVIEW_FIXTURE_MODE"
const fixtureOut = "REVIEW_FIXTURE_OUT"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fixtureMode); mode != "" {
		os.Exit(fixtureAdapter(mode))
	}
	os.Exit(m.Run())
}

func fixtureAdapter(mode string) int {
	raw, _ := io.ReadAll(os.Stdin)
	var req adapterRequest
	_ = json.Unmarshal(raw, &req)
	sum := sha256.Sum256(req.Snapshot)
	if out := os.Getenv(fixtureOut); out != "" {
		_ = os.WriteFile(out, []byte(hex.EncodeToString(sum[:])+"\n"+strings.Join(os.Environ(), "\n")), 0o600)
	}
	reply := `{"protocol":"sessions.review-adapter/1","reviewer":{"id":"fixture","version":"0"},"choice":"claimed_done_unverified","scores":{"claimed_done_unverified":0.7}}`
	switch mode {
	case "slow":
		time.Sleep(20 * time.Second)
	case "garbage":
		reply = "not json"
	case "extra":
		reply = strings.Replace(reply, `"choice"`, `"surprise":1,"choice"`, 1)
	case "badchoice":
		reply = strings.Replace(reply, `"choice":"claimed_done_unverified"`, `"choice":"done_for_sure"`, 1)
	case "badscore":
		reply = strings.Replace(reply, "0.7", "1.5", 1)
	case "huge":
		reply = `{"notes":"` + strings.Repeat("x", MaxReplyBytes+10) + `"}`
	case "empty":
		return 0
	case "exit":
		fmt.Fprint(os.Stderr, "adapter refused")
		return 3
	}
	fmt.Print(reply)
	return 0
}

// A synthetic observations object: version 1 with arbitrary nested content
// and nulls. The seam must embed it verbatim without interpreting fields.
const fixtureSnapshot = `{
  "schema": "sessions.review-snapshot/1",
  "subject": "synthetic-lane-1",
  "captured_at": "2026-01-02T03:04:05Z",
  "producer": "test fixture",
  "observations": {
    "version": 1,
    "alpha": {"value": true, "source": "synthetic-source"},
    "beta": {"inner": null, "list": [1, null]},
    "gamma": null
  }
}`

const fixtureQuestions = `{"schema":"sessions.review-questions/1","id":"synthetic","version":"1",
 "options":[{"id":"working","description":"w"},{"id":"waiting_on_review","description":"r"},
  {"id":"blocked_on_access","description":"b"},{"id":"claimed_done_unverified","description":"c"},
  {"id":"done_verified","description":"v"}],
 "abstain":{"id":"insufficient_evidence","description":"not a state"}}`

func frozen(t *testing.T, snapshot string) (FrozenSnapshot, FrozenQuestions) {
	t.Helper()
	s, err := DecodeSnapshot([]byte(snapshot))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	q, err := DecodeQuestions([]byte(fixtureQuestions))
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	return s, q
}

func fixtureReviewer(t *testing.T, mode string, timeout time.Duration) (*External, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "seen")
	t.Setenv(fixtureMode, mode)
	t.Setenv(fixtureOut, out)
	ext, err := NewExternal(self, nil, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return ext, out
}

func TestSnapshotRejectsIncompatibleAndAmbiguousInput(t *testing.T) {
	cases := map[string]string{
		"schema version":       strings.Replace(fixtureSnapshot, "review-snapshot/1", "review-snapshot/2", 1),
		"observations version": strings.Replace(fixtureSnapshot, `"version": 1`, `"version": 2`, 1),
		"observations missing": strings.Replace(fixtureSnapshot, `"observations": {`, `"other": {`, 1),
		"observations null":    strings.Replace(fixtureSnapshot, `"producer": "test fixture",`, `"producer": "test fixture", "observations": null, "x":`, 1),
		"duplicate key":        strings.Replace(fixtureSnapshot, `"subject": "synthetic-lane-1",`, `"subject": "a", "subject": "b",`, 1),
		"nested duplicate key": strings.Replace(fixtureSnapshot, `"gamma": null`, `"gamma": null, "gamma": 1`, 1),
		"unknown top field":    strings.Replace(fixtureSnapshot, `"producer"`, `"extra": 1, "producer"`, 1),
		"tail no optin":        strings.Replace(fixtureSnapshot, `"observations"`, `"tail": {"opt_in": false, "text": "x", "provenance": "t"}, "observations"`, 1),
		"trailing data":        fixtureSnapshot + " {}",
	}
	for name, input := range cases {
		if _, err := DecodeSnapshot([]byte(input)); err == nil {
			t.Errorf("%s: accepted, want rejection", name)
		}
	}
}

func TestHashesAreStableAcrossEncodings(t *testing.T) {
	a, _ := frozen(t, fixtureSnapshot)
	var generic map[string]any
	_ = json.Unmarshal([]byte(fixtureSnapshot), &generic)
	reordered, _ := json.MarshalIndent(generic, "", "\t")
	b, err := DecodeSnapshot(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != b.Hash || string(a.Canonical) != string(b.Canonical) {
		t.Fatalf("hash differs across encodings: %s vs %s", a.Hash, b.Hash)
	}
}

const (
	goldenSnapshotHash = "22a9201fb323492ebe8eab13d734af59277993dc0783e05c46cec812f8f34a15"
	goldenQuestionHash = "912ea574bc385e79e8069152a0b5ed470d29b60cde94a8c68df7d51b219fd018"
)

func TestGoldenHash(t *testing.T) {
	s, q := frozen(t, fixtureSnapshot)
	if got := s.Hash; got != goldenSnapshotHash {
		t.Fatalf("snapshot hash = %s, want %s (canonical form changed)", got, goldenSnapshotHash)
	}
	if got := q.Hash; got != goldenQuestionHash {
		t.Fatalf("question hash = %s, want %s (canonical form changed)", got, goldenQuestionHash)
	}
}

// Nulls inside the embedded observations stay exactly where they were and are
// reported generically by path; nothing is defaulted or reinterpreted.
func TestUnknownStaysUnknown(t *testing.T) {
	s, q := frozen(t, fixtureSnapshot)
	c := Compare(context.Background(), s, q, []Reviewer{Baseline{}})
	if got := strings.Join(c.NullPaths, ","); got != "beta.inner,beta.list[1],gamma" {
		t.Fatalf("null paths = %s", got)
	}
	if !strings.Contains(string(s.Canonical), `"beta":{"inner":null,"list":[1,null]}`) || !strings.Contains(string(s.Canonical), `"gamma":null`) {
		t.Fatalf("canonical bytes lost a null: %s", s.Canonical)
	}
}

// The baseline predicts no lane state for any snapshot or option set; its note
// reports the unknown values and whether task text and a tail were supplied.
func TestBaselineAlwaysAbstainsAndShowsMissingEvidence(t *testing.T) {
	allKnown := strings.Replace(strings.Replace(fixtureSnapshot, `"inner": null, "list": [1, null]`, `"inner": 1, "list": [1]`, 1), `"gamma": null`, `"gamma": 0`, 1)
	for name, snap := range map[string]string{"with unknowns": fixtureSnapshot, "nothing unknown": allKnown} {
		s, q := frozen(t, snap)
		r := Compare(context.Background(), s, q, []Reviewer{Baseline{}}).Results[0]
		if r.Status != "abstained" || r.Answer.Choice != "insufficient_evidence" {
			t.Errorf("%s: baseline answered %+v, want abstention", name, r)
		}
	}
	s, q := frozen(t, fixtureSnapshot)
	note := Compare(context.Background(), s, q, []Reviewer{Baseline{}}).Results[0].Answer.Notes
	for _, want := range []string{"predicts no lane state", "null (unknown): 3 (beta.inner, beta.list[1], gamma)", "transcript tail not supplied"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}
	arbitrary, err := DecodeQuestions([]byte(`{"schema":"sessions.review-questions/1","id":"other","version":"1",
		"options":[{"id":"working","description":"a"},{"id":"done","description":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := Compare(context.Background(), s, arbitrary, []Reviewer{Baseline{}}).Results[0]; r.Status != "abstained" || r.Answer.Choice != "" {
		t.Fatalf("baseline mapped caller options without an abstention: %+v", r)
	}
}

func TestBaselineAndExternalReceiveByteIdenticalInput(t *testing.T) {
	s, q := frozen(t, fixtureSnapshot)
	ext, seen := fixtureReviewer(t, "ok", 10*time.Second)
	c := Compare(context.Background(), s, q, []Reviewer{Baseline{}, ext})
	if len(c.Results) != 2 || c.Results[1].Status != "ok" {
		t.Fatalf("comparison = %+v", c.Results)
	}
	for _, r := range c.Results {
		if r.InputSHA256 != s.Hash || r.Authority != "none" {
			t.Fatalf("%s input %s authority %q, want %s none", r.Reviewer.ID, r.InputSHA256, r.Authority, s.Hash)
		}
	}
	recorded, _ := os.ReadFile(seen)
	if got := strings.SplitN(string(recorded), "\n", 2)[0]; got != s.Hash {
		t.Fatalf("adapter received snapshot hash %s, want %s", got, s.Hash)
	}
	if c.Results[1].Reviewer.FileSHA256BeforeRun == "" || c.Results[1].Answer.Declared.ID != "fixture" {
		t.Fatalf("identity not recorded: %+v", c.Results[1])
	}
}

func TestExternalFailuresAreTypedErrors(t *testing.T) {
	s, q := frozen(t, fixtureSnapshot)
	want := map[string]ErrorKind{"garbage": ErrMalformed, "extra": ErrMalformed, "huge": ErrMalformed, "empty": ErrMalformed,
		"badchoice": ErrInvalidAnswer, "badscore": ErrInvalidAnswer, "exit": ErrUnavailable}
	for mode, kind := range want {
		ext, _ := fixtureReviewer(t, mode, 10*time.Second)
		r := Compare(context.Background(), s, q, []Reviewer{ext}).Results[0]
		if r.Status != "error" || r.Error == nil || r.Error.Kind != kind || r.Answer != nil {
			t.Errorf("%s: got %+v, want error %s", mode, r, kind)
		}
	}
	missing, err := NewExternal(filepath.Join(t.TempDir(), "absent"), nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r := Compare(context.Background(), s, q, []Reviewer{missing}).Results[0]; r.Error == nil || r.Error.Kind != ErrUnavailable {
		t.Fatalf("missing executable: %+v", r)
	}
	if _, err := NewExternal("relative/tool", nil, time.Second); err == nil {
		t.Fatal("relative path accepted; PATH lookup must not happen")
	}
}

func TestExternalTimeoutAndCancellationStopTheReviewer(t *testing.T) {
	s, q := frozen(t, fixtureSnapshot)
	ext, _ := fixtureReviewer(t, "slow", 300*time.Millisecond)
	started := time.Now()
	r := Compare(context.Background(), s, q, []Reviewer{ext}).Results[0]
	if r.Error == nil || r.Error.Kind != ErrTimeout || time.Since(started) > 5*time.Second {
		t.Fatalf("timeout: %+v after %s", r, time.Since(started))
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	slow, _ := fixtureReviewer(t, "slow", 10*time.Second)
	if r := Compare(ctx, s, q, []Reviewer{slow}).Results[0]; r.Error == nil || r.Error.Kind != ErrCancelled {
		t.Fatalf("cancellation: %+v", r)
	}
}

// An adapter is the user's own code and runs with the user's environment (for
// example to reach an existing CLI login). Sessions claims no isolation; this
// pins that it does not silently strip the environment either.
func TestExternalRunsWithTheCallersEnvironment(t *testing.T) {
	t.Setenv("REVIEW_FIXTURE_CALLER_MARK", "visible-to-adapter")
	s, q := frozen(t, fixtureSnapshot)
	ext, seen := fixtureReviewer(t, "ok", 10*time.Second)
	Compare(context.Background(), s, q, []Reviewer{ext})
	recorded, _ := os.ReadFile(seen)
	if !strings.Contains(string(recorded), "REVIEW_FIXTURE_CALLER_MARK=visible-to-adapter") {
		t.Fatal("adapter did not receive the caller's environment")
	}
}

// The seam has no mutation or network path: production files import no
// network package and no other Sessions package, and never write files.
func TestPackageHasNoMutationOrNetworkPath(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range parsed.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			if strings.HasPrefix(path, "net") || strings.Contains(path, "somewhere-tech/sessions") {
				t.Errorf("%s imports %s", name, path)
			}
		}
		source, _ := os.ReadFile(name)
		for _, call := range []string{"os.WriteFile", "os.Create", "os.OpenFile", "os.Remove", "os.Rename", "os.Mkdir"} {
			if strings.Contains(string(source), call) {
				t.Errorf("%s calls %s", name, call)
			}
		}
	}
}

// The executable digest is a record of the file observed before invocation:
// the real sha256 of a regular file, and omitted (never blocking) otherwise.
func TestFileDigestIsBoundedAndRegularOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte("synthetic executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	sum := sha256.Sum256([]byte("synthetic executable"))
	if got := fileDigest(context.Background(), path, info); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %s", got)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := fileDigest(cancelled, path, info); got != "" {
		t.Fatalf("digest after cancellation = %s, want omitted", got)
	}
	dirInfo, _ := os.Stat(t.TempDir())
	if got := fileDigest(context.Background(), path, dirInfo); got != "" {
		t.Fatalf("digest for a non-regular file = %s, want omitted", got)
	}
	s, q := frozen(t, fixtureSnapshot)
	ext, _ := fixtureReviewer(t, "ok", 10*time.Second)
	self, _ := os.Executable()
	body, _ := os.ReadFile(self)
	selfSum := sha256.Sum256(body)
	if got := Compare(context.Background(), s, q, []Reviewer{ext}).Results[0].Reviewer.FileSHA256BeforeRun; got != hex.EncodeToString(selfSum[:]) {
		t.Fatalf("recorded file sha256 = %s, want the file's sha256", got)
	}
	// The timeout covers hashing: an exhausted budget omits the hash and times out.
	expired, _ := fixtureReviewer(t, "ok", time.Nanosecond)
	if r := Compare(context.Background(), s, q, []Reviewer{expired}).Results[0]; r.Error == nil || r.Error.Kind != ErrTimeout ||
		r.Reviewer.FileSHA256BeforeRun != "" {
		t.Fatalf("expired timeout: %+v", r)
	}
}
