package state

import (
	"context"
	"encoding/json"
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
	if observations.Tools.OpenCount != 0 || *observations.Tools.LastEndStatus != ToolEndResult || *observations.Last.ProviderRecordEventAt != base.Add(time.Minute).UnixMilli() {
		t.Fatalf("after a result: %+v / %+v", observations.Tools, observations.Last)
	}
	use("b", base.Add(2*time.Minute))
	result("b", base.Add(3*time.Minute), true)
	observations = awaitObservations(t, session, func(o *Observations) bool { return *o.Tools.LastEndStatus == ToolEndError })
	if *observations.Last.ProviderRecordEventAt != base.Add(3*time.Minute).UnixMilli() {
		t.Fatalf("continuing results did not move the provider clock: %+v", observations.Last)
	}
	if codexToolEndStatus("declined") != ToolEndCancelled || codexToolEndStatus("completed") != ToolEndResult || codexToolEndStatus("weird") != ToolEndUnknown {
		t.Fatal("codex item status mapping changed")
	}
}

// Records may carry no timestamp of their own. A live one was observed now,
// by this daemon, so structured activity is not left unknown; a replayed one
// was not observed at a known time and is never restamped. Either way its
// facts -- a tool open, a turn started -- still count.
func TestObservedTimestampLessRecords(t *testing.T) {
	session, runner := observedSession(t, "/usr/local/bin/codex", KindCodexAppServer)
	before := time.Now().UnixMilli()
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "turn_started"})
	runner.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_started",
		"item": map[string]any{"id": "call-x", "type": "commandExecution"}})
	observations := awaitObservations(t, session, func(o *Observations) bool { return o.Tools.OpenCount == 1 })
	if last := observations.Last; last.ProviderRecordEventAt != nil || last.ProviderRecordObservedAt == nil || *last.ProviderRecordObservedAt < before {
		t.Fatalf("live untimed records: event=%v observed=%v", last.ProviderRecordEventAt, last.ProviderRecordObservedAt)
	}
	if turn := observations.Turn; turn.Open == nil || !*turn.Open || turn.StartedAt == nil {
		t.Fatalf("turn = %+v", turn)
	}

	registry, _, root := observationRegistry(t)
	replaying := prototest.NewRunner(proto.RunnerInfo{ID: "00000000-0000-4000-8000-000000000703", Cmd: "/usr/local/bin/codex", Cwd: root,
		Cols: 80, Rows: 24, CreatedAt: time.Now().Add(-time.Hour).UnixMilli(), PID: 4244, ProtocolVersion: proto.ProtocolVersion})
	replaying.AddCodexEvent(map[string]any{"type": "codex", "source": "codex-app-server", "subtype": "item_started",
		"item": map[string]any{"id": "call-old", "type": "commandExecution"}})
	adopted, err := registry.RegisterMetadata(context.Background(), replaying, RunnerMetadata{Kind: KindCodexAppServer}, "")
	if err != nil {
		t.Fatal(err)
	}
	replayed := adopted.Info().Observations
	if replayed.Tools.OpenCount != 1 || replayed.Tools.Open[0].StartedAt != nil || replayed.Last.ProviderRecordObservedAt != nil {
		t.Fatalf("a replayed untimed record was restamped: tools=%+v last=%+v", replayed.Tools, replayed.Last)
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

// A provider init confirms only a dispatch this daemon saw live, and only when
// it arrives live itself. A replayed dispatch and init cannot be tied to the
// turn now open, so the confirmation stays unknown; a runner-authored user
// record never confirms anything.
func TestStartConfirmationExcludesReplayAndRunnerRecords(t *testing.T) {
	registry, _, root := observationRegistry(t)
	runner := prototest.NewRunner(proto.RunnerInfo{ID: "00000000-0000-4000-8000-000000000704", Cmd: "/usr/local/bin/claude", Cwd: root,
		Cols: 80, Rows: 24, CreatedAt: time.Now().Add(-time.Hour).UnixMilli(), PID: 4245, ProtocolVersion: proto.ProtocolVersion})
	old := time.Now().Add(-30 * time.Minute)
	runner.AddCodexEvent(map[string]any{"type": "claude", "subtype": "turn_started", "source": "claude-p-stream-json", "origin": "sessions", "timestamp": stamp(old)})
	runner.AddCodexEvent(map[string]any{"type": "system", "subtype": "init", "source": "claude-p-stream-json", "timestamp": stamp(old.Add(time.Second))})
	session, err := registry.RegisterMetadata(context.Background(), runner, RunnerMetadata{Kind: KindClaudeStructured}, "")
	if err != nil {
		t.Fatal(err)
	}
	if turn := session.Info().Observations.Turn; turn.Open == nil || !*turn.Open || turn.ProviderConfirmedAt != nil {
		t.Fatalf("a replayed init confirmed the open turn: %+v", turn)
	}
	runner.AddCodexEvent(map[string]any{"type": "user", "subtype": "user_message", "source": "claude-p-stream-json", "origin": "sessions", "timestamp": stamp(time.Now())})
	runner.AddCodexEvent(map[string]any{"type": "system", "subtype": "init", "source": "claude-p-stream-json", "timestamp": stamp(time.Now())})
	time.Sleep(50 * time.Millisecond)
	if turn := session.Info().Observations.Turn; turn.ProviderConfirmedAt != nil {
		t.Fatalf("a live init confirmed a dispatch only seen in replay: %+v", turn)
	}
	runner.AddCodexEvent(map[string]any{"type": "claude", "subtype": "turn_started", "source": "claude-p-stream-json", "origin": "sessions", "timestamp": stamp(time.Now())})
	runner.AddCodexEvent(map[string]any{"type": "system", "subtype": "init", "source": "claude-p-stream-json", "timestamp": stamp(time.Now())})
	awaitObservations(t, session, func(o *Observations) bool { return o.Turn.ProviderConfirmedAt != nil })
}

// An observer error is health, not amnesia: what the transcript already
// showed stays, beside the error.
func TestObserverErrorKeepsLastKnownTranscriptFacts(t *testing.T) {
	session, _ := observedSession(t, "/usr/local/bin/claude", "")
	at := time.Now().Add(-time.Minute)
	record, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": stamp(at),
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "toolu_t", "name": "Read"}}}})
	session.RecordClaudeEvent(record)
	awaitObservations(t, session, func(o *Observations) bool { return o.Last.TranscriptRecordEventAt != nil })
	session.SetObserverHealth("claude-transcript", ObserverError, "read failed")
	observations := session.Info().Observations
	if *observations.Last.TranscriptRecordEventAt != at.UnixMilli() || observations.Tools.OpenCount != 1 || observations.Observers[0].State != ObserverError {
		t.Fatalf("observer error erased facts: last=%+v tools=%+v observers=%+v", observations.Last, observations.Tools, observations.Observers)
	}
}

// A rollout lifecycle edge carries the rollout record's own time, or none;
// observing it never stamps the observer's clock, so a reattach backfill
// cannot manufacture turn timing.
func TestRolloutTurnUsesRecordTimeOrUnknown(t *testing.T) {
	session, _ := observedSession(t, "/usr/local/bin/codex", "")
	session.ObserveRolloutTurn(true, 0)
	turn := session.Info().Observations.Turn
	if turn.Open == nil || !*turn.Open || turn.StartedAt != nil {
		t.Fatalf("an untimed rollout start manufactured a time: %+v", turn)
	}
	started, ended := time.Now().Add(-2*time.Hour).UnixMilli(), time.Now().Add(-time.Hour).UnixMilli()
	session.ObserveRolloutTurn(false, ended)
	session.ObserveRolloutTurn(true, started)
	session.ObserveRolloutTurn(false, ended)
	turn = session.Info().Observations.Turn
	if *turn.Open || *turn.StartedAt != started || *turn.EndedAt != ended || turn.EndedBy != "rollout-file" {
		t.Fatalf("rollout turn = %+v", turn)
	}
	if session.Info().Observations.Capabilities.TurnBoundaries != ObservedRolloutFile {
		t.Fatal("codex PTY lost its rollout capability")
	}
}
