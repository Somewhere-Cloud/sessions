package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func codexMessageLine(t *testing.T, text string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"timestamp": "2026-07-16T12:00:00Z", "type": "response_item",
		"payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func watchCodexFixture(t *testing.T, lines []string) *FileWatcher {
	t.Helper()
	root := t.TempDir()
	rolloutPath := filepath.Join(root, "sessions", "2026", "07", "16", "rollout-fixture.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolloutPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher := WatchCodexRollout(CodexWatcherOptions{
		CreatedAt: time.Date(2026, time.July, 16, 12, 0, 0, 0, time.Local), SessionsDir: filepath.Join(root, "sessions"),
		RolloutPath: rolloutPath, InitialDelay: time.Millisecond, PollInterval: 10 * time.Millisecond,
	})
	t.Cleanup(watcher.Close)
	return watcher
}

func signalKind(event SessionEvent) SignalKind {
	signal, _ := SignalOf(event)
	return signal.Kind
}

// A backfill that starts after the beginning of the rollout signals the gap
// before its first record; one that reads the whole file signals nothing.
func TestCodexBoundedBackfillSignalsItsWindowFirst(t *testing.T) {
	lines := make([]string, 0, codexBackfillLineLimit+1)
	for index := 0; index <= codexBackfillLineLimit; index++ {
		lines = append(lines, codexMessageLine(t, fmt.Sprintf("line-%04d", index)))
	}
	stream := collectStream(t, watchCodexFixture(t, lines).Events, 2, 3*time.Second)
	if signalKind(stream[0]) != SignalWindowed || eventText(stream[1]) != "line-0001" {
		t.Fatalf("stream starts %#v, %#v", stream[0], stream[1])
	}

	whole := collectStream(t, watchCodexFixture(t, lines[:3]).Events, 3, 3*time.Second)
	for _, event := range whole {
		if _, isSignal := SignalOf(event); isSignal {
			t.Fatalf("a whole-file read signalled a gap: %#v", event)
		}
	}
}

// An undecodable rollout record is a gap signalled where it was.
func TestCodexUndecodableRecordIsSignalledInPlace(t *testing.T) {
	stream := collectStream(t, watchCodexFixture(t, []string{codexMessageLine(t, "before"), "{broken", codexMessageLine(t, "after")}).Events, 3, 3*time.Second)
	if eventText(stream[0]) != "before" || signalKind(stream[1]) != SignalSkipped || eventText(stream[2]) != "after" {
		t.Fatalf("stream = %#v", stream)
	}
}

func watchClaudeFixture(t *testing.T, records string) (*FileWatcher, string) {
	t.Helper()
	projectDir := t.TempDir()
	sessionID := "aaaaaaaa-1111-2222-3333-777777777777"
	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(records), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher, err := WatchClaudeSession(ClaudeWatcherOptions{
		ClaudeSessionID: sessionID, ProjectDir: projectDir, InitialDelay: time.Millisecond, PollInterval: 10 * time.Millisecond,
		MirrorPath: filepath.Join(t.TempDir(), "mirror.jsonl"), SessionID: "fixture-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watcher.Close)
	return watcher, path
}

// An undecodable transcript record is a gap signalled between its neighbours,
// and losing the bound file is signalled when it happens.
func TestClaudeWatcherSignalsUndecodableRecordsAndALostFile(t *testing.T) {
	watcher, path := watchClaudeFixture(t, `{"type":"user","uuid":"one"}`+"\n{broken\n"+`{"type":"user","uuid":"two"}`+"\n")
	stream := collectStream(t, watcher.Events, 3, 3*time.Second)
	if stream[0]["uuid"] != "one" || signalKind(stream[1]) != SignalSkipped || stream[2]["uuid"] != "two" {
		t.Fatalf("stream = %#v", stream)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if signal := awaitSignal(t, watcher.Events, SignalDetached, 3*time.Second); !strings.Contains(signal.Detail, path) {
		t.Fatalf("lost file signal = %#v", signal)
	}
	if watcher.Path() != "" {
		t.Fatalf("watcher still reports path %q", watcher.Path())
	}
}

// A provider record cannot pose as a signal: decoded JSON never holds the Go
// Signal type, whatever its keys.
func TestDecodedRecordsCannotSpoofASignal(t *testing.T) {
	var event SessionEvent
	if err := json.Unmarshal([]byte(`{"\u0000sessions-watch-signal":{"Kind":"detached"}}`), &event); err != nil {
		t.Fatal(err)
	}
	if _, isSignal := SignalOf(event); isSignal {
		t.Fatal("a decoded record was read as a signal")
	}
}

// failReads makes every readRange fail while the returned flag is set. It is
// installed before a watcher starts and removed after it closes.
func failReads(t *testing.T) *atomic.Bool {
	t.Helper()
	var failing atomic.Bool
	readRange = func(file *os.File, start, length int64) ([]byte, error) {
		if failing.Load() {
			return nil, errors.New("injected read failure")
		}
		return readFileRange(file, start, length)
	}
	t.Cleanup(func() { readRange = readFileRange })
	return &failing
}

// A bound transcript whose reads fail is reported as such, not left
// following; the read resumes at the same offset, so the record written
// meanwhile still arrives, followed by following again.
func TestClaudeReadFailureIsReportedAndResumesWithoutLoss(t *testing.T) {
	failing := failReads(t)
	watcher, path := watchClaudeFixture(t, `{"type":"user","uuid":"one"}`+"\n")
	if got := collectEvents(t, watcher.Events, 1, 3*time.Second); got[0]["uuid"] != "one" {
		t.Fatalf("first record = %#v", got[0])
	}
	awaitSignal(t, watcher.Events, SignalFollowing, 3*time.Second)
	failing.Store(true)
	appendLine(t, path, `{"type":"user","uuid":"two"}`)
	if signal := awaitSignal(t, watcher.Events, SignalReadFailed, 3*time.Second); !strings.Contains(signal.Detail, "injected read failure") || !strings.Contains(signal.Detail, path) {
		t.Fatalf("read failure signal = %#v", signal)
	}
	failing.Store(false)
	stream := collectStream(t, watcher.Events, 2, 3*time.Second)
	if stream[0]["uuid"] != "two" || signalKind(stream[1]) != SignalFollowing {
		t.Fatalf("after recovery = %#v", stream)
	}
	if watcher.Path() != path {
		t.Fatalf("a read failure unbound the file: %q", watcher.Path())
	}
}

// A Codex backfill whose read fails is reported, and is retried rather than
// replaced by a live read from an offset the backfill never set.
func TestCodexBackfillReadFailureIsReportedAndRetried(t *testing.T) {
	failing := failReads(t)
	failing.Store(true)
	watcher := watchCodexFixture(t, []string{codexMessageLine(t, "only")})
	if signal := awaitSignal(t, watcher.Events, SignalReadFailed, 3*time.Second); !strings.Contains(signal.Detail, "injected read failure") {
		t.Fatalf("read failure signal = %#v", signal)
	}
	failing.Store(false)
	stream := collectStream(t, watcher.Events, 2, 3*time.Second)
	if eventText(stream[0]) != "only" || signalKind(stream[1]) != SignalFollowing {
		t.Fatalf("after recovery = %#v", stream)
	}
	assertNoEvent(t, watcher.Events, 100*time.Millisecond)
}

// The same read failure is reported once, not on every poll.
func TestRepeatedReadFailureIsReportedOnce(t *testing.T) {
	failing := failReads(t)
	failing.Store(true)
	watcher := watchCodexFixture(t, []string{codexMessageLine(t, "only")})
	awaitSignal(t, watcher.Events, SignalReadFailed, 3*time.Second)
	select {
	case event := <-watcher.Events:
		t.Fatalf("a repeated failure was reported again: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}
