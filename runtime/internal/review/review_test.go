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
// variable is set (passed explicitly through External.Env).
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

const fixtureSnapshot = `{
  "schema": "sessions.review-snapshot/1",
  "subject": "synthetic-lane-1",
  "captured_at": "2026-01-02T03:04:05Z",
  "producer": "test fixture",
  "capabilities": {"turn_boundaries": "provider-stream"},
  "facts": {
    "turn.open": {"source": "provider-stream", "value": false},
    "runner.state": {"source": "runner", "unknown": true, "reason": "not observed"},
    "last_assistant_text": {"source": "self-report", "value": "Implemented and committed the change."}
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
	ext, err := NewExternal(self, nil, []string{fixtureMode, fixtureOut}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return ext, out
}

func TestSnapshotRejectsIncompatibleAndAmbiguousInput(t *testing.T) {
	cases := map[string]string{
		"version":       strings.Replace(fixtureSnapshot, "review-snapshot/1", "review-snapshot/2", 1),
		"duplicate key": strings.Replace(fixtureSnapshot, `"subject": "synthetic-lane-1",`, `"subject": "a", "subject": "b",`, 1),
		"unknown field": strings.Replace(fixtureSnapshot, `"producer"`, `"extra": 1, "producer"`, 1),
		"null value":    strings.Replace(fixtureSnapshot, `"value": false`, `"value": null`, 1),
		"unknown+value": strings.Replace(fixtureSnapshot, `"unknown": true,`, `"unknown": true, "value": "reachable",`, 1),
		"bad source":    strings.Replace(fixtureSnapshot, `"source": "runner"`, `"source": "model"`, 1),
		"tail no optin": strings.Replace(fixtureSnapshot, `"facts"`, `"tail": {"opt_in": false, "text": "x", "provenance": "t"}, "facts"`, 1),
		"trailing data": fixtureSnapshot + " {}",
		"source contract v2": strings.Replace(fixtureSnapshot, `"producer": "test fixture",`,
			`"producer": "test fixture", "source_contract": {"name": "sessions.lane-observations", "version": 2},`, 1),
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
	goldenSnapshotHash = "b524bb519c8142528c2ac302a6e0ba2798b6a02ceabed94eccaea13066a0dfa9"
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

func TestUnknownStaysUnknown(t *testing.T) {
	s, q := frozen(t, strings.Replace(fixtureSnapshot, `"turn.open": {"source": "provider-stream", "value": false}`,
		`"turn.open": {"source": "provider-stream", "unknown": true}`, 1))
	c := Compare(context.Background(), s, q, []Reviewer{Baseline{}})
	if strings.Join(c.UnknownFacts, ",") != "runner.state,turn.open" {
		t.Fatalf("unknown facts = %v", c.UnknownFacts)
	}
	if c.Results[0].Status != "abstained" || c.Results[0].Answer.Choice != "insufficient_evidence" {
		t.Fatalf("baseline guessed from unknown facts: %+v", c.Results[0])
	}
	if !strings.Contains(string(s.Canonical), `"turn.open":{"source":"provider-stream","unknown":true}`) {
		t.Fatalf("canonical bytes lost the unknown marker: %s", s.Canonical)
	}
}

func TestBaselineNeverInfersWorkingOrVerifiedCompletion(t *testing.T) {
	missingEnd := strings.Replace(fixtureSnapshot, `"turn.open": {"source": "provider-stream", "value": false},`, ``, 1)
	terminalOnly := strings.Replace(fixtureSnapshot, `"source": "provider-stream", "value": false`, `"source": "terminal", "value": true`, 1)
	openNoRunner := strings.Replace(fixtureSnapshot, `"value": false`, `"value": true`, 1)
	for name, snap := range map[string]string{"missing end": missingEnd, "terminal-only turn": terminalOnly, "open, runner unknown": openNoRunner} {
		s, q := frozen(t, snap)
		r := Compare(context.Background(), s, q, []Reviewer{Baseline{}}).Results[0]
		if r.Answer == nil || r.Answer.Choice == "working" {
			t.Errorf("%s: baseline answered %+v, want abstention", name, r)
		}
	}
	s, q := frozen(t, fixtureSnapshot)
	r := Compare(context.Background(), s, q, []Reviewer{Baseline{}}).Results[0]
	if r.Answer.Choice != "claimed_done_unverified" {
		t.Fatalf("self-reported completion = %q, want claimed_done_unverified (never done_verified)", r.Answer.Choice)
	}
	review := strings.Replace(fixtureSnapshot, "Implemented and committed the change.", "Committed. I'm idle for review.", 1)
	s, q = frozen(t, review)
	if got := Compare(context.Background(), s, q, []Reviewer{Baseline{}}).Results[0].Answer.Choice; got != "waiting_on_review" {
		t.Fatalf("explicit review wording = %q, want waiting_on_review", got)
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
	if c.Results[1].Reviewer.ExecutableSHA256 == "" || c.Results[1].Answer.Declared.ID != "fixture" {
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
	missing, err := NewExternal(filepath.Join(t.TempDir(), "absent"), nil, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r := Compare(context.Background(), s, q, []Reviewer{missing}).Results[0]; r.Error == nil || r.Error.Kind != ErrUnavailable {
		t.Fatalf("missing executable: %+v", r)
	}
	if _, err := NewExternal("relative/tool", nil, nil, time.Second); err == nil {
		t.Fatal("relative path accepted; PATH lookup must not happen")
	}
	if _, err := NewExternal("/bin/true", nil, nil, MaxTimeout+time.Second); err == nil {
		t.Fatal("timeout above the limit accepted")
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

func TestExternalGetsOnlyMinimalEnvironment(t *testing.T) {
	t.Setenv("SESSIONS_TOKEN", "secret-should-not-pass")
	t.Setenv("ANTHROPIC_API_KEY", "secret-should-not-pass")
	s, q := frozen(t, fixtureSnapshot)
	ext, seen := fixtureReviewer(t, "ok", 10*time.Second)
	Compare(context.Background(), s, q, []Reviewer{ext})
	recorded, _ := os.ReadFile(seen)
	if strings.Contains(string(recorded), "secret-should-not-pass") {
		t.Fatal("adapter inherited a credential-like variable it was not given")
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

func TestSupportedSourceContractIsAccepted(t *testing.T) {
	ok := strings.Replace(fixtureSnapshot, `"producer": "test fixture",`,
		`"producer": "test fixture", "source_contract": {"name": "sessions.lane-observations", "version": 1},`, 1)
	if _, err := DecodeSnapshot([]byte(ok)); err != nil {
		t.Fatalf("observations v1 snapshot rejected: %v", err)
	}
}

// Working needs an observed open turn, a reachable runner and a provider
// record close to capture time; stale, future-dated or missing records abstain.
func TestBaselineWorkingRequiresFreshProviderRecord(t *testing.T) {
	captured := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	open := func(recordAt time.Time) string {
		facts := fmt.Sprintf(`"turn.open": {"source": "provider-stream", "value": true},
    "runner.state": {"source": "runner", "value": "reachable"},
    "last.provider_record_at": {"source": "provider-stream", "value": %d},`, recordAt.UnixMilli())
		s := strings.Replace(fixtureSnapshot, `"turn.open": {"source": "provider-stream", "value": false},`, facts, 1)
		return strings.Replace(s, `"runner.state": {"source": "runner", "unknown": true, "reason": "not observed"},`, ``, 1)
	}
	cases := map[string]string{"fresh": "working", "stale": "insufficient_evidence", "future": "insufficient_evidence"}
	at := map[string]time.Time{"fresh": captured.Add(-2 * time.Minute), "stale": captured.Add(-5 * time.Hour), "future": captured.Add(time.Hour)}
	for name, want := range cases {
		s, q := frozen(t, open(at[name]))
		if got := Compare(context.Background(), s, q, []Reviewer{Baseline{}}).Results[0].Answer.Choice; got != want {
			t.Errorf("%s provider record: baseline = %q, want %q", name, got, want)
		}
	}
}
