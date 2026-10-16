package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
)

// Tool results keep arriving: each closes its call, moves the provider clock,
// and reports how it ended. A result is not success, an error stays an error,
// and a declined call is reported as cancelled.
func TestObservedContinuingToolResults(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/claude", KindClaudeStructured)
	base := time.Now().Add(-10 * time.Minute)
	use := func(id string, at time.Time) {
		runner.AddCodexEvent(map[string]any{"type": "assistant", "source": "claude-p-stream-json", "timestamp": stamp(at),
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash"}}}})
	}
	result := func(id string, at time.Time, isError bool) {
		runner.AddCodexEvent(map[string]any{"type": "user", "source": "claude-p-stream-json", "timestamp": stamp(at),
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isError}}}})
	}
	use("a", base)
	result("a", base.Add(time.Minute), false)
	observations := awaitObservations(t, session, func(o *Observations) bool { return o.Tools.LastEndStatus != nil })
	if observations.Tools.OpenCount != 0 || *observations.Tools.LastEndStatus != ToolEndResult || *observations.Last.ProviderRecordAt != base.Add(time.Minute).UnixMilli() {
		t.Fatalf("after a result: %+v / %+v", observations.Tools, observations.Last)
	}
	use("b", base.Add(2*time.Minute))
	result("b", base.Add(3*time.Minute), true)
	observations = awaitObservations(t, session, func(o *Observations) bool { return *o.Tools.LastEndStatus == ToolEndError })
	if *observations.Last.ProviderRecordAt != base.Add(3*time.Minute).UnixMilli() {
		t.Fatalf("continuing results did not move the provider clock: %+v", observations.Last)
	}
	if codexToolEndStatus("declined") != ToolEndCancelled || codexToolEndStatus("completed") != ToolEndResult || codexToolEndStatus("weird") != ToolEndUnknown {
		t.Fatal("codex item status mapping changed")
	}
}

// Old runners and old records may carry no timestamp. Their facts still
// count -- a tool is open, a turn started -- but their times are unknown and
// they move no clock.
func TestObservedTimestampLessRecordsStayUnknownInTime(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/codex", KindCodexAppServer)
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "turn_started"})
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_started",
		"item": map[string]any{"id": "call-x", "type": "commandExecution"}})
	observations := awaitObservations(t, session, func(o *Observations) bool { return o.Tools.OpenCount == 1 })
	if observations.Tools.Open[0].StartedAt != nil || observations.Last.ProviderRecordAt != nil {
		t.Fatalf("a timestamp-less record invented a time: tool=%+v last=%+v", observations.Tools.Open[0], observations.Last)
	}
	if turn := observations.Turn; turn.Open == nil || !*turn.Open || turn.StartedAt != nil {
		t.Fatalf("turn = %+v", turn)
	}
}

// Reattaching to a runner whose bounded replay starts after a tool began
// shows only the end it can see: no open call is invented, and the history is
// reported incomplete.
func TestObservedReattachWithToolStartOutsideReplay(t *testing.T) {
	registry, _, root := observationRegistry(t)
	runner := prototest.NewRunner(proto.RunnerInfo{ID: "00000000-0000-4000-8000-000000000702", Cmd: "/usr/local/bin/codex", Cwd: root,
		Cols: 80, Rows: 24, CreatedAt: time.Now().Add(-time.Hour).UnixMilli(), PID: 4243, ProtocolVersion: proto.ProtocolVersion})
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_completed", "timestamp": stamp(time.Now().Add(-time.Minute)),
		"item": map[string]any{"id": "call-early", "type": "commandExecution", "status": "completed"}})
	session, err := registry.RegisterMetadata(context.Background(), runner, RunnerMetadata{Kind: KindCodexAppServer}, "")
	if err != nil {
		t.Fatal(err)
	}
	tools := session.Info().Observations.Tools
	if tools.OpenCount != 0 || tools.LastEndedAt == nil || tools.HistoryComplete {
		t.Fatalf("tools = %+v", tools)
	}
}

// The flat facts carry a source each, unknown facts carry no value, and a
// block the runtime cannot produce is distinguished from one this record did
// not observe.
func TestObservationFactsCarrySourcesAndUnknowns(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/codex", KindCodexAppServer)
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_started", "timestamp": stamp(time.Now()),
		"item": map[string]any{"id": "call-1", "type": "commandExecution"}})
	observations := awaitObservations(t, session, func(o *Observations) bool { return o.Tools.OpenCount == 1 })
	byName := map[string]ObservedFact{}
	for _, fact := range observations.Facts() {
		byName[fact.Name] = fact
	}
	for name, source := range map[string]string{
		"runner.state": FactSourceRunner, "working.value": FactSourceProviderStream, "tools.open_count": FactSourceProviderStream,
		"tools.history_complete": FactSourceDerived, "capabilities.runtime": FactSourceDerived, "last.terminal_output_at": FactSourceTerminal,
	} {
		if byName[name].Source != source {
			t.Fatalf("%s source = %q, want %q", name, byName[name].Source, source)
		}
	}
	encoded, _ := json.Marshal(byName["runner.exit_code"])
	if strings.Contains(string(encoded), `"value"`) || !byName["runner.exit_code"].Unknown {
		t.Fatalf("unknown fact carried a value: %s", encoded)
	}
	encoded, _ = json.Marshal(byName["working.value"])
	if !strings.Contains(string(encoded), `"value":`) {
		t.Fatalf("a known false/true value was dropped: %s", encoded)
	}
	shell := RestoredObservations(SessionInfo{Cmd: "/bin/sh"}).Facts()
	restoredPTY := RestoredObservations(SessionInfo{Cmd: "claude", Tool: ToolClaude}).Facts()
	if reason := factReason(shell, "turn"); reason != "not observable for this runtime" {
		t.Fatalf("shell turn reason = %q", reason)
	}
	if reason := factReason(restoredPTY, "turn"); reason != "not observed for this record" {
		t.Fatalf("restored PTY turn reason = %q", reason)
	}
}

func factReason(facts []ObservedFact, name string) string {
	for _, fact := range facts {
		if fact.Name == name {
			return fact.Reason
		}
	}
	return ""
}

// Terminal output does not repair a failed observer.
func TestObserverErrorIsNotClearedByTerminalOutput(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/claude", "")
	session.SetObserverHealth("claude-transcript", ObserverError, "read failed")
	runner.AddOutput("unrelated output\n")
	awaitObservations(t, session, func(o *Observations) bool { return o.Last.TerminalOutputAt != nil })
	if observers := session.Info().Observations.Observers; observers[0].State != ObserverError {
		t.Fatalf("terminal output recovered the observer: %+v", observers)
	}
}
