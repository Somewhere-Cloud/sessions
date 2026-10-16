package state

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
)

func observationRegistry(t *testing.T) (*Registry, *prototest.Launcher, string) {
	t.Helper()
	root := t.TempDir()
	launcher := prototest.NewLauncher()
	return NewRegistry(Config{
		DefaultShell: "/bin/sh", DefaultCwd: root, DefaultCols: 80, DefaultRows: 24,
		RunnerStateDir: filepath.Join(root, "runners"), LaunchAgentsDir: filepath.Join(root, "agents"),
	}, launcher), launcher, root
}

func observedSession(t *testing.T, command, kind string) (*Session, *prototest.Runner) {
	t.Helper()
	registry, launcher, root := observationRegistry(t)
	created, err := registry.Create(context.Background(), CreateSessionRequest{Cmd: command, Cwd: root, Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := registry.Get(created.ID)
	return session, launcher.Runner(created.ID)
}

func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }

func awaitObservations(t *testing.T, session *Session, satisfied func(*Observations) bool) *Observations {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if observations := session.Info().Observations; satisfied(observations) {
			return observations
		}
		time.Sleep(time.Millisecond)
	}
	encoded, _ := json.Marshal(session.Info().Observations)
	t.Fatalf("observations never reached the expected state: %s", encoded)
	return nil
}

// A structured tool that started hours ago and never completed is reported as
// exactly that: open, with its start time, and nothing inferred about why.
// Records Sessions wrote and metadata-only records move neither the observed
// nor the event clock, and an unreadable record makes history incomplete.
func TestObservedStructuredToolOpenForHours(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/codex", KindCodexAppServer)
	started := time.Now().Add(-5 * time.Hour)
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "turn_started",
		"timestamp": stamp(started), "timestamp_source": "observed", "observed_at": stamp(started), "turnId": "t1"})
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_started",
		"timestamp": stamp(started), "timestamp_source": "provider", "observed_at": stamp(started),
		"item": map[string]any{"id": "call-1", "type": "commandExecution"}})
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "token_count", "timestamp": stamp(time.Now())})
	runner.AddCodexEvent(map[string]any{"type": "user", "subtype": "user_message", "source": "codex-app-server", "origin": "sessions", "timestamp": stamp(time.Now())})
	observations := awaitObservations(t, session, func(o *Observations) bool { return o.Tools != nil && o.Tools.OpenCount == 1 })
	if observations.Capabilities.Runtime != "codex-app-server" || observations.Capabilities.ToolRecords != ObservedProviderStream || observations.Capabilities.TerminalOutput {
		t.Fatalf("capabilities = %+v", observations.Capabilities)
	}
	if observations.Turn == nil || observations.Turn.Open == nil || !*observations.Turn.Open || observations.Turn.StartedBy != "provider" || observations.Turn.EndedAt != nil {
		t.Fatalf("turn = %+v", observations.Turn)
	}
	if tool := observations.Tools.Open[0]; tool.ID != "call-1" || tool.StartedAt == nil || *tool.StartedAt != started.UnixMilli() {
		t.Fatalf("open tool = %+v", tool)
	}
	last := observations.Last
	if last.ProviderRecordObservedAt == nil || *last.ProviderRecordObservedAt != started.UnixMilli() ||
		last.ProviderRecordEventAt == nil || *last.ProviderRecordEventAt != started.UnixMilli() {
		t.Fatalf("provider clocks = %v / %v, want the 5-hour-old record; Sessions and metadata records must not move them", last.ProviderRecordObservedAt, last.ProviderRecordEventAt)
	}
	if last.TerminalOutputAt != nil || observations.Working.Source != WorkingFromProviderStream || !observations.Tools.HistoryComplete || !observations.Tools.OpenCountExact {
		t.Fatalf("observations = %+v / %+v / %+v", last, observations.Working, observations.Tools)
	}
	runner.Emit(proto.Event{Kind: proto.EventCodex, CodexEvent: json.RawMessage(`{not json`)})
	awaitObservations(t, session, func(o *Observations) bool { return !o.Tools.HistoryComplete && !o.Tools.OpenCountExact })
}

// An event time is the record's claim, reported as given -- even one ahead of
// the clock -- and kept apart from when Sessions observed the record.
func TestObservedEventAndObservedTimesStayDistinct(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/codex", KindCodexAppServer)
	observedAt := time.Now().Add(-time.Minute)
	claimed := time.Now().Add(2 * time.Hour)
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_started",
		"timestamp": stamp(claimed), "timestamp_source": "provider", "observed_at": stamp(observedAt),
		"item": map[string]any{"id": "call-f", "type": "commandExecution"}})
	last := awaitObservations(t, session, func(o *Observations) bool { return o.Last.ProviderRecordEventAt != nil }).Last
	if *last.ProviderRecordEventAt != claimed.UnixMilli() || *last.ProviderRecordObservedAt != observedAt.UnixMilli() {
		t.Fatalf("event %v / observed %v", *last.ProviderRecordEventAt, *last.ProviderRecordObservedAt)
	}
}

// The structured Claude runner's own turn_started is Sessions' dispatch, not
// provider evidence; the provider's init confirms it, and its result ends it.
// A tool with no result stays open: the turn ending does not invent one.
func TestObservedClaudeDispatchIsNotProviderEvidence(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/claude", KindClaudeStructured)
	base := time.Now().Add(-time.Minute)
	runner.AddCodexEvent(map[string]any{"type": "claude", "subtype": "turn_started", "source": "claude-p-stream-json", "origin": "sessions", "timestamp": stamp(base)})
	observations := awaitObservations(t, session, func(o *Observations) bool { return o.Turn != nil && o.Turn.Open != nil })
	if observations.Turn.StartedBy != "sessions-dispatch" || observations.Turn.ProviderConfirmedAt != nil ||
		observations.Last.ProviderRecordObservedAt != nil || observations.Last.ProviderRecordEventAt != nil {
		t.Fatalf("dispatch read as provider evidence: turn=%+v last=%+v", observations.Turn, observations.Last)
	}
	runner.AddCodexEvent(map[string]any{"type": "system", "subtype": "init", "source": "claude-p-stream-json", "session_id": "s", "timestamp": stamp(base.Add(time.Second))})
	runner.AddCodexEvent(map[string]any{"type": "assistant", "source": "claude-p-stream-json", "session_id": "s", "timestamp": stamp(base.Add(2 * time.Second)),
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash"}}}})
	runner.AddCodexEvent(map[string]any{"type": "result", "subtype": "success", "source": "claude-p-stream-json", "session_id": "s", "timestamp": stamp(base.Add(3 * time.Second))})
	observations = awaitObservations(t, session, func(o *Observations) bool { return o.Turn.EndedAt != nil })
	if *observations.Turn.ProviderConfirmedAt != base.Add(time.Second).UnixMilli() || observations.Turn.EndedBy != "provider-result" || *observations.Turn.Open {
		t.Fatalf("turn = %+v", observations.Turn)
	}
	if observations.Tools.OpenCount != 1 || observations.Tools.Open[0].Name != "Bash" {
		t.Fatalf("tools = %+v", observations.Tools)
	}
}

// Spinner repaints are terminal output, not provider records: a PTY Claude
// session shows recent output with no provider or transcript record and no
// observed turn.
func TestObservedSpinnerRepaintsWithoutProviderRecords(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/claude", "")
	for _, frame := range []string{"\r✻ Working… (esc to interrupt)", "\r✶ Working… (esc to interrupt)"} {
		runner.AddOutput(frame)
	}
	observations := awaitObservations(t, session, func(o *Observations) bool { return o.Last.TerminalOutputAt != nil })
	if observations.Capabilities.Runtime != "claude-pty" || observations.Capabilities.TurnBoundaries != ObservedTranscriptFile {
		t.Fatalf("capabilities = %+v", observations.Capabilities)
	}
	if observations.Last.ProviderRecordObservedAt != nil || observations.Last.ProviderRecordEventAt != nil || observations.Last.TranscriptRecordEventAt != nil {
		t.Fatalf("repaints became provider evidence: %+v", observations.Last)
	}
	if observations.Turn == nil || observations.Turn.Open != nil || observations.Turn.EndedAt != nil {
		t.Fatalf("a turn was inferred from terminal output: %+v", observations.Turn)
	}
}

// A runner re-adopted after a restart replays its history. Replayed records
// keep their own times, replayed output is not new output, and the replay is
// not called complete because it may be bounded.
func TestObservedReplayDoesNotRefreshClocks(t *testing.T) {
	registry, _, root := observationRegistry(t)
	runner := prototest.NewRunner(proto.RunnerInfo{ID: "00000000-0000-4000-8000-000000000701", Cmd: "/usr/local/bin/codex", Cwd: root,
		Cols: 80, Rows: 24, CreatedAt: time.Now().Add(-2 * time.Hour).UnixMilli(), PID: 4242, ProtocolVersion: proto.ProtocolVersion})
	old := time.Now().Add(-time.Hour)
	runner.AddOutput("old output\n")
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_started", "timestamp": stamp(old),
		"item": map[string]any{"id": "call-9", "type": "commandExecution"}})
	session, err := registry.RegisterMetadata(context.Background(), runner, RunnerMetadata{Kind: KindCodexAppServer}, "")
	if err != nil {
		t.Fatal(err)
	}
	observations := session.Info().Observations
	if last := observations.Last; last.ProviderRecordObservedAt != nil || last.ProviderRecordEventAt == nil || *last.ProviderRecordEventAt != old.UnixMilli() {
		t.Fatalf("replay restamped a clock: observed=%v event=%v", last.ProviderRecordObservedAt, last.ProviderRecordEventAt)
	}
	if observations.Tools.OpenCount != 1 || observations.Tools.HistoryComplete || observations.Tools.OpenCountExact {
		t.Fatalf("replayed tools = %+v", observations.Tools)
	}
	if info := session.Info(); !info.LastDataUnknown {
		t.Fatalf("re-adoption reported activity: %+v", info.LastDataAt)
	}
}

// What a record restored from the ledger, or one still launching, never
// observed stays null on the wire -- never false or zero.
func TestRestoredAndLaunchingObservationsStayUnknown(t *testing.T) {
	code := 0
	for _, info := range []SessionInfo{
		{ID: "a", Cmd: "claude", Tool: ToolClaude, Launching: true},
		{ID: "b", Cmd: "codex", Tool: ToolCodex, Exited: true, ExitCode: &code},
		{ID: "c", Cmd: "claude", Tool: ToolClaude, Kind: KindClaudeStructured, Exited: true},
	} {
		encoded, err := json.Marshal(RestoredObservations(info))
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, want := range []string{`"turn":null`, `"tools":null`, `"last":null`, `"working":null`} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s: restored observations %s lack %s", info.ID, text, want)
			}
		}
	}
	if runner := RestoredObservations(SessionInfo{Launching: true}).Runner; runner.State != "launching" || runner.ExitCode != nil {
		t.Fatalf("launching runner = %+v", runner)
	}
	if runner := RestoredObservations(SessionInfo{Exited: true}).Runner; runner.State != "exited" || runner.ExitCode != nil {
		t.Fatalf("exit with no reaped status reported a code: %+v", runner)
	}
}

func TestObserverHealthIsReported(t *testing.T) {
	session, _ := observedSession(t, "/usr/local/bin/claude", "")
	session.SetObserverHealth("claude-transcript", ObserverUnresolved, "no provider transcript file is bound yet")
	session.SetObserverHealth("claude-transcript", ObserverError, "read failed")
	observers := session.Info().Observations.Observers
	if len(observers) != 1 || observers[0].State != ObserverError || observers[0].Detail != "read failed" {
		t.Fatalf("observers = %+v", observers)
	}
}
