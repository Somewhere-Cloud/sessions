package session

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/providerfault"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const testClaudeID = "019f7181-cb32-46e0-952d-2f5f7862e668"

func claudeEvent(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	event, err := claudep.NormalizeEvent(json.RawMessage(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return event.Raw
}

// structuredResult is a provider result event of the given outcome; failed
// results carry no provider fault.
func structuredResult(t *testing.T, kind string, failed bool) json.RawMessage {
	t.Helper()
	if kind == state.KindCodexAppServer {
		status := "completed"
		if failed {
			status = "failed"
		}
		return codexEvent(t, codexapp.TurnComplete{ConversationID: "conversation-1", TurnID: "turn", Status: status})
	}
	if failed {
		return claudeEvent(t, `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"Reached the turn limit","session_id":"`+testClaudeID+`"}`)
	}
	return claudeEvent(t, `{"type":"result","subtype":"success","result":"ok","session_id":"`+testClaudeID+`"}`)
}

func structuredStart(t *testing.T, kind string, runner *prototest.Runner) {
	t.Helper()
	if kind == state.KindCodexAppServer {
		runner.AddCodexEvent(codexEvent(t, codexapp.TurnStarted{ConversationID: "conversation-1", TurnID: "turn"}))
		return
	}
	started, err := claudep.TurnStartedEvent(testClaudeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runner.AddCodexEvent(started)
}

func structuredAssistant(t *testing.T, kind string, runner *prototest.Runner, text string) {
	t.Helper()
	if kind == state.KindCodexAppServer {
		phase := "final_answer"
		runner.AddCodexEvent(codexEvent(t, codexapp.ItemCompleted{
			ConversationID: "conversation-1", TurnID: "turn", CompletedAtMS: time.Now().UnixMilli(),
			Item: codexapp.ThreadItem{ID: "message-" + text, Type: "agentMessage", Text: text, Phase: &phase},
		}))
		return
	}
	runner.AddCodexEvent(claudeEvent(t, `{"type":"assistant","session_id":"`+testClaudeID+
		`","message":{"role":"assistant","content":[{"type":"text","text":"`+text+`"}]}}`))
}

func eachStructuredKind(t *testing.T, run func(t *testing.T, kind string)) {
	for _, kind := range []string{state.KindCodexAppServer, state.KindClaudeStructured} {
		t.Run(kind, func(t *testing.T) { run(t, kind) })
	}
}

// A provider result event can be delivered more than once for one turn. The
// result notification is sent once per observed turn; only the next turn's
// start opens another. Without event identity this is per turn, not global.
func TestRepeatedStructuredResultNotifiesOncePerTurn(t *testing.T) {
	eachStructuredKind(t, func(t *testing.T, kind string) {
		h := newEffectHarness(t, harnessOptions{})
		_, session := h.create(t, structuredRequest(kind))
		runner := h.launcher.Runner(session.Info().ID)

		// A result with no observed start still notifies, once.
		runner.AddCodexEvent(structuredResult(t, kind, false))
		runner.AddCodexEvent(structuredResult(t, kind, false))
		awaitCondition(t, func() bool { return h.log.pushCount() >= 1 })
		h.assertQuiet(t, 1, 0)

		structuredStart(t, kind, runner)
		structuredAssistant(t, kind, runner, "Turn two finished")
		runner.AddCodexEvent(structuredResult(t, kind, false))
		runner.AddCodexEvent(structuredResult(t, kind, false))
		runner.AddCodexEvent(structuredResult(t, kind, false))
		awaitCondition(t, func() bool { return h.log.pushCount() >= 2 && h.log.idleCount() >= 1 })
		h.assertQuiet(t, 2, 1)

		structuredStart(t, kind, runner)
		runner.AddCodexEvent(structuredResult(t, kind, false))
		awaitCondition(t, func() bool { return h.log.pushCount() >= 3 })
		h.assertQuiet(t, 3, 2)
	})
}

// A failed result is reported as a failure, never as a green "done".
func TestFailedStructuredResultIsNotReportedDone(t *testing.T) {
	eachStructuredKind(t, func(t *testing.T, kind string) {
		h := newEffectHarness(t, harnessOptions{})
		_, session := h.create(t, structuredRequest(kind))
		runner := h.launcher.Runner(session.Info().ID)
		structuredStart(t, kind, runner)
		runner.AddCodexEvent(structuredResult(t, kind, true))
		awaitCondition(t, func() bool { return h.log.pushCount() >= 1 })
		h.assertQuiet(t, 1, 1)
		push := h.log.pushAt(0)
		if push.payload.Title != "🔴 rich — failed" || push.idleReason != state.IdleReasonFailed {
			t.Fatalf("failed result push = %#v", push)
		}
		if session.Info().IdleReason != state.IdleReasonFailed {
			t.Fatalf("failed result idle = %q", session.Info().IdleReason)
		}
	})
}

// A provider fault already reported for a failed turn is the turn's whole
// notification: the result that ends that turn adds no green "done".
func TestProviderFaultTurnSendsNoDoneAfterTheFault(t *testing.T) {
	eachStructuredKind(t, func(t *testing.T, kind string) {
		h := newEffectHarness(t, harnessOptions{})
		_, session := h.create(t, structuredRequest(kind))
		runner := h.launcher.Runner(session.Info().ID)
		structuredStart(t, kind, runner)
		awaitCondition(t, func() bool { return session.Info().Working })
		runner.AddCodexEvent(map[string]any{
			"type": "system", "subtype": "provider_fault", "provider": providerForSession(session.Info()),
			"kind": providerfault.KindAuth, "detail": "Provider is not logged in", "timestamp": time.Now().UnixMilli(),
		})
		runner.AddCodexEvent(structuredResult(t, kind, true))
		runner.AddCodexEvent(structuredResult(t, kind, true))
		awaitCondition(t, func() bool { return h.log.pushCount() >= 1 })
		h.assertQuiet(t, 1, 1)
		if push := h.log.pushAt(0); push.payload.Body != "Provider is not logged in" || push.idleReason != state.IdleReasonFailed {
			t.Fatalf("fault push = %#v", push)
		}
	})
}

// A turn that produced no assistant text has no summary of its own. The
// previous turn's text is not evidence about it, for the push body, the
// hook's final message, or a question that would read as needing input.
func TestEmptyTurnDoesNotReuseThePreviousTurnSummary(t *testing.T) {
	eachStructuredKind(t, func(t *testing.T, kind string) {
		h := newEffectHarness(t, harnessOptions{})
		request := structuredRequest(kind)
		request.OnIdle = `printf '%s\n' "$SESSIONS_FINAL_MESSAGE" >> "` + h.hookLog + `"`
		_, session := h.create(t, request)
		runner := h.launcher.Runner(session.Info().ID)
		structuredStart(t, kind, runner)
		structuredAssistant(t, kind, runner, "Which option should I pick, A or B?")
		runner.AddCodexEvent(structuredResult(t, kind, false))
		awaitCondition(t, func() bool { return h.log.pushCount() >= 1 && h.log.idleCount() >= 1 })
		if got := session.Info().IdleReason; got != state.IdleReasonNeedsInput {
			t.Fatalf("question turn idle = %q", got)
		}
		h.awaitHookLines(t, "Which option")

		structuredStart(t, kind, runner)
		runner.AddCodexEvent(structuredResult(t, kind, false))
		awaitCondition(t, func() bool { return h.log.pushCount() >= 2 && h.log.idleCount() >= 2 })
		h.assertQuiet(t, 2, 2)
		if push := h.log.pushAt(1); push.payload.Body != "finished" {
			t.Fatalf("empty turn push body = %q", push.payload.Body)
		}
		if got := session.Info().IdleReason; got != state.IdleReasonCompleted {
			t.Fatalf("empty turn idle = %q, want completed", got)
		}
		lines := h.hookLines()
		if len(lines) != 1 {
			// The second hook prints an empty line, which hookLines trims.
			t.Fatalf("hook final messages = %q", lines)
		}
	})
}

// The pre-turn screen inspection is ordered with transitions and with input:
// a dialog read before a start edge or a message must not be published after
// it, leaving a working or answered session reading "needs input".
func TestPreTurnInspectionIsOrderedWithStartsAndInput(t *testing.T) {
	previous := preTurnInspectInterval
	preTurnInspectInterval = 0
	t.Cleanup(func() { preTurnInspectInterval = previous })
	h := newEffectHarness(t, harnessOptions{waitingDelay: time.Hour})
	runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "codex", Name: "pre-turn"})
	id := session.Info().ID
	h.launcher.Runner(id).AddOutput("Do you trust the files in this folder?\nContinue? [y/N]")
	<-runtime.outputObserved
	for round := range 300 {
		session.SetWorking(false)
		session.SetIdleResult(state.IdleReasonNeverStarted, "", "", time.Now().UnixMilli())
		runtime.mu.Lock()
		runtime.preTurnOutput, runtime.preTurnBlocked = true, false
		runtime.mu.Unlock()
		input := round%2 == 1
		var group sync.WaitGroup
		group.Add(2)
		go func() { defer group.Done(); runtime.inspectPreTurn(0) }()
		go func() {
			defer group.Done()
			if input {
				h.manager.clearIdleAfterInput(id)
			} else {
				runtime.observeRolloutEdge(true, 0)
			}
		}()
		group.Wait()
		info := session.Info()
		if input && info.IdleReason != "" {
			t.Fatalf("round %d: input was followed by a stale pre-turn %q", round, info.IdleReason)
		}
		if !input && info.Working && info.IdleReason != "" {
			t.Fatalf("round %d: a started turn reads %q", round, info.IdleReason)
		}
	}
}

// A turn adopted at attach from the runner's HELLO was not started under this
// runtime, so its end is the runtime's first sample, which only initializes.
// The result still notifies once, but no idle record, hook, sentinel or ledger
// idle observation is published for it. This is the documented bootstrap rule,
// pinned rather than changed; the next observed turn publishes normally.
func TestAdoptedTurnEndPublishesOnlyItsResultNotification(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{})
	id := "00000000-0000-4000-8000-000000000091"
	info := proto.RunnerInfo{
		ID: id, Cmd: "codex", Cwd: h.root, Cols: 120, Rows: 40, PID: 4242,
		ProtocolVersion: proto.ProtocolVersion, Turn: &proto.TurnState{Working: true},
	}
	runner := prototest.NewRunner(info)
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnStarted{ConversationID: "conversation-1", TurnID: "turn"}))
	session, err := h.manager.registry.RegisterMetadata(context.Background(), runner, state.RunnerMetadata{
		Info: info, Kind: state.KindCodexAppServer, Name: "adopted",
	}, h.hookScript("lane"))
	if err != nil {
		t.Fatal(err)
	}
	h.manager.manage(session)
	if !session.Info().Working {
		t.Fatal("HELLO working state was not adopted")
	}
	structuredAssistant(t, state.KindCodexAppServer, runner, "Adopted turn finished")
	runner.AddCodexEvent(structuredResult(t, state.KindCodexAppServer, false))
	runner.AddCodexEvent(structuredResult(t, state.KindCodexAppServer, false))
	awaitCondition(t, func() bool { return h.log.pushCount() >= 1 })
	h.assertQuiet(t, 1, 0)
	if push := h.log.pushAt(0); push.payload.Body != "Adopted turn finished" || push.working {
		t.Fatalf("adopted result push = %#v", push)
	}
	if got := session.Info(); got.Working || got.IdleReason != "" || h.sentinelExists(id) || len(h.hookLines()) != 0 {
		t.Fatalf("adopted end published an idle record: working %v idle %q", got.Working, got.IdleReason)
	}

	structuredStart(t, state.KindCodexAppServer, runner)
	runner.AddCodexEvent(structuredResult(t, state.KindCodexAppServer, false))
	awaitCondition(t, func() bool { return h.log.pushCount() >= 2 && h.log.idleCount() >= 1 })
	h.assertQuiet(t, 2, 1)
	h.awaitHookLines(t, "lane done")
}
