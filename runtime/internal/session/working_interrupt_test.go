package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// claudeInterrupted is the Sessions-authored record a structured Claude runner
// writes when the person interrupts a turn before Claude gave a result.
func claudeInterrupted() json.RawMessage {
	return json.RawMessage(`{"type":"claude","subtype":"turn_interrupted","source":"` + claudep.HistorySource +
		`","origin":"sessions","timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","session_id":"` + testClaudeID + `"}`)
}

// requireInterruptLifecycle skips where the runner's turn_interrupted record
// is not yet a lifecycle boundary, so the turn never ends in the daemon.
func requireInterruptLifecycle(t *testing.T) {
	t.Helper()
	if working, ok := claudep.HistoryLifecycle(claudeInterrupted()); !ok || working {
		t.Skip("turn_interrupted is not a lifecycle record in this tree")
	}
}

// An interrupted turn has no result of its own. It must not borrow the
// previous turn's successful result: no completed reason, no green done, no
// hook or completion sentinel. A turn Claude answered before it was stopped
// keeps its answer.
func TestInterruptedClaudeTurnClaimsNoCompletion(t *testing.T) {
	requireInterruptLifecycle(t)
	h := newEffectHarness(t, harnessOptions{waitingDelay: 50 * time.Millisecond})
	request := structuredRequest(state.KindClaudeStructured)
	request.OnIdle = h.hookScript("lane")
	_, session := h.create(t, request)
	id := session.Info().ID
	runner := h.launcher.Runner(id)
	kind := state.KindClaudeStructured

	structuredStart(t, kind, runner)
	structuredAssistant(t, kind, runner, "First turn done.")
	runner.AddCodexEvent(structuredResult(t, kind, false))
	awaitCondition(t, func() bool { return h.log.pushCount() == 1 && h.log.idleCount() == 1 })
	h.awaitHookLines(t, "lane done")

	structuredStart(t, kind, runner)
	structuredAssistant(t, kind, runner, "Halfway through.")
	runner.AddCodexEvent(claudeInterrupted())
	awaitCondition(t, func() bool { return !session.Info().Working && h.log.idleCount() == 2 })
	runner.AddCodexEvent(claudeInterrupted())
	time.Sleep(100 * time.Millisecond)
	h.assertQuiet(t, 1, 2)
	h.awaitHookLines(t, "lane done")
	if got := session.Info(); got.IdleReason != "" || got.IdleSince != nil || got.IdleDetail != "" {
		t.Fatalf("interrupted turn reads %q since %v (%q)", got.IdleReason, got.IdleSince, got.IdleDetail)
	}
	if h.sentinelExists(id) {
		t.Fatal("an interrupted turn wrote a completion sentinel")
	}

	structuredStart(t, kind, runner)
	structuredAssistant(t, kind, runner, "Third answered.")
	runner.AddCodexEvent(structuredResult(t, kind, false))
	runner.AddCodexEvent(claudeInterrupted())
	awaitCondition(t, func() bool { return h.log.pushCount() == 2 && h.log.idleCount() == 3 })
	h.assertQuiet(t, 2, 3)
	if push := h.log.pushAt(1); push.payload.Title != "🟢 rich — done" || push.payload.Body != "Third answered." {
		t.Fatalf("answered turn push = %#v", push)
	}
	if got := session.Info().IdleReason; got != state.IdleReasonCompleted {
		t.Fatalf("answered-then-stopped turn idle = %q, want completed", got)
	}
	h.awaitHookLines(t, "lane done", "lane done")
}

// Codex reports an interruption in its own result, as turn status
// "interrupted". That is neither a completed nor a failed turn.
func TestInterruptedCodexTurnClaimsNoCompletion(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{waitingDelay: 50 * time.Millisecond})
	request := structuredRequest(state.KindCodexAppServer)
	request.OnIdle = h.hookScript("lane")
	_, session := h.create(t, request)
	runner := h.launcher.Runner(session.Info().ID)
	codexTurn(t, runner, "turn-1", "First turn done.", "completed")
	awaitCondition(t, func() bool { return h.log.pushCount() == 1 && h.log.idleCount() == 1 })
	h.awaitHookLines(t, "lane done")

	runner.AddCodexEvent(codexEvent(t, codexapp.TurnStarted{ConversationID: "conversation-1", TurnID: "turn-2"}))
	structuredAssistant(t, state.KindCodexAppServer, runner, "Halfway through.")
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnComplete{ConversationID: "conversation-1", TurnID: "turn-2", Status: "interrupted"}))
	awaitCondition(t, func() bool { return !session.Info().Working && h.log.idleCount() == 2 })
	time.Sleep(100 * time.Millisecond)
	h.assertQuiet(t, 1, 2)
	h.awaitHookLines(t, "lane done")
	if got := session.Info(); got.IdleReason != "" || got.IdleSince != nil {
		t.Fatalf("interrupted Codex turn reads %q since %v", got.IdleReason, got.IdleSince)
	}
}

// A replacement daemon adopting an idle runner whose last turn has no result
// in its own records does not borrow the earlier turn's result. A completed
// last turn still reads completed (TestReconnectBootstrapPublishesNoEffects).
func TestBootstrapOfAResultlessLastTurnClaimsNoCompletion(t *testing.T) {
	eachStructuredKind(t, func(t *testing.T, kind string) {
		h := newEffectHarness(t, harnessOptions{})
		id := "00000000-0000-4000-8000-000000000093"
		cmd := "codex"
		if kind == state.KindClaudeStructured {
			cmd = "claude"
		}
		info := proto.RunnerInfo{
			ID: id, Cmd: cmd, Cwd: h.root, Cols: 120, Rows: 40, PID: 4242,
			ProtocolVersion: proto.ProtocolVersion, Turn: &proto.TurnState{Working: false},
		}
		runner := prototest.NewRunner(info)
		structuredStart(t, kind, runner)
		structuredAssistant(t, kind, runner, "First turn done.")
		runner.AddCodexEvent(structuredResult(t, kind, false))
		structuredStart(t, kind, runner)
		structuredAssistant(t, kind, runner, "Halfway through.")
		if kind == state.KindClaudeStructured {
			runner.AddCodexEvent(claudeInterrupted())
		}
		session, err := h.manager.registry.RegisterMetadata(context.Background(), runner, state.RunnerMetadata{
			Info: info, Kind: kind, Name: "adopted",
		}, h.hookScript("lane"))
		if err != nil {
			t.Fatal(err)
		}
		h.manager.manage(session)
		if got := session.Info(); got.Working || got.IdleReason != "" || got.IdleSince != nil {
			t.Fatalf("adopted resultless turn = working %v idle %q since %v", got.Working, got.IdleReason, got.IdleSince)
		}
		h.assertQuiet(t, 0, 0)
	})
}

func TestStructuredClassificationReadsOnlyTheCurrentTurn(t *testing.T) {
	raw := func(value string) json.RawMessage { return json.RawMessage(value) }
	claudeStart := raw(`{"type":"claude","subtype":"turn_started","source":"claude-p-stream-json"}`)
	claudeSuccess := raw(`{"type":"result","subtype":"success","source":"claude-p-stream-json"}`)
	claudeAsk := raw(`{"type":"system","subtype":"approval_requested","source":"claude-p-stream-json","approval":{"summary":"Bash: rm -rf build"}}`)
	claudeAnswered := raw(`{"type":"system","subtype":"approval_resolved","source":"claude-p-stream-json"}`)
	steer := raw(`{"type":"user","source":"codex-app-server","message":{"role":"user","content":"also run the tests"}}`)
	codexStart := raw(`{"type":"codex","subtype":"turn_started","source":"codex-app-server"}`)
	codexAsk := raw(`{"type":"codex","subtype":"approval_requested","source":"codex-app-server"}`)
	codexStatus := func(status string) json.RawMessage {
		return raw(`{"type":"codex","subtype":"turn_completed","status":"` + status + `","source":"codex-app-server"}`)
	}
	tests := []struct {
		name   string
		kind   string
		events []json.RawMessage
		want   IdleOutcome
	}{
		{"result in the turn", state.KindClaudeStructured, []json.RawMessage{claudeStart, claudeSuccess}, IdleDone},
		{"earlier turn's result is not borrowed", state.KindClaudeStructured, []json.RawMessage{claudeStart, claudeSuccess, claudeStart}, idleNoOutcome},
		{"interrupted turn has no outcome", state.KindClaudeStructured, []json.RawMessage{claudeStart, claudeSuccess, claudeStart, claudeInterrupted()}, idleNoOutcome},
		{"answer before the interruption stands", state.KindClaudeStructured, []json.RawMessage{claudeStart, claudeSuccess, claudeInterrupted()}, IdleDone},
		{"open approval blocks", state.KindClaudeStructured, []json.RawMessage{claudeStart, claudeAsk}, IdleBlocked},
		{"answered approval does not block", state.KindClaudeStructured, []json.RawMessage{claudeStart, claudeAsk, claudeAnswered, claudeInterrupted()}, idleNoOutcome},
		{"approval of an interrupted turn does not block", state.KindClaudeStructured, []json.RawMessage{claudeStart, claudeAsk, claudeInterrupted()}, idleNoOutcome},
		{"window without an opener is the current turn", state.KindClaudeStructured, []json.RawMessage{claudeSuccess}, IdleDone},
		{"empty log has no outcome", state.KindClaudeStructured, nil, idleNoOutcome},
		{"codex completed", state.KindCodexAppServer, []json.RawMessage{codexStart, codexStatus("completed")}, IdleDone},
		{"codex failed", state.KindCodexAppServer, []json.RawMessage{codexStart, codexStatus("failed")}, IdleError},
		{"codex interrupted", state.KindCodexAppServer, []json.RawMessage{codexStart, codexStatus("interrupted")}, idleNoOutcome},
		{"codex steer stays inside the turn", state.KindCodexAppServer, []json.RawMessage{codexStart, codexAsk, steer}, IdleBlocked},
		{"codex earlier result is not borrowed", state.KindCodexAppServer, []json.RawMessage{codexStart, codexStatus("completed"), codexStart}, idleNoOutcome},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification, authoritative := structuredIdleClassification(test.kind, test.events)
			if !authoritative || classification.Outcome != test.want {
				t.Fatalf("classification = %+v authoritative %v, want %q", classification, authoritative, test.want)
			}
		})
	}
	if _, authoritative := structuredIdleClassification(state.KindLane, []json.RawMessage{claudeSuccess}); authoritative {
		t.Fatal("a terminal runtime's records were treated as structured evidence")
	}
}
