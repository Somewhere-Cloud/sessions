package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/providerfault"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// These tests pin what one working transition does outside the session
// record: the ledger idle observation, the idle sentinel, the lane and global
// hooks, and the pushes. They count every effect, and every push records the
// state it was delivered against, so a reordering or a duplicate is visible.

type capturedPush struct {
	payload    PushPayload
	working    bool
	idleReason string
	sentinel   bool
}

type effectLog struct {
	mu     sync.Mutex
	pushes []capturedPush
	idle   int
}

func (l *effectLog) pushCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pushes)
}

func (l *effectLog) pushAt(index int) capturedPush {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pushes[index]
}

func (l *effectLog) idleCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.idle
}

// countingObservations records nothing durable; it counts idle observations.
type countingObservations struct{ log *effectLog }

func (c countingObservations) RecordLaunchStarted(context.Context, ledger.Observation) error {
	return nil
}
func (c countingObservations) RecordRunnerReady(context.Context, ledger.Observation) error {
	return nil
}
func (c countingObservations) RecordProviderBound(context.Context, ledger.ProviderBound) error {
	return nil
}
func (c countingObservations) RecordAttached(context.Context, ledger.Observation) error { return nil }
func (c countingObservations) RecordActivity(context.Context, ledger.Activity) error    { return nil }
func (c countingObservations) RecordIdle(context.Context, ledger.Observation) error {
	c.log.mu.Lock()
	c.log.idle++
	c.log.mu.Unlock()
	return nil
}
func (c countingObservations) RecordRenamed(context.Context, ledger.Rename) error { return nil }
func (c countingObservations) RecordDescriptionDerived(context.Context, ledger.DescriptionDerived) error {
	return nil
}
func (c countingObservations) RecordRunnerExited(context.Context, ledger.RunnerExit) error {
	return nil
}
func (c countingObservations) RecordRunnerLost(context.Context, ledger.Observation) error {
	return nil
}
func (c countingObservations) RecordRunnerArtifactsRetired(context.Context, ledger.Observation) error {
	return nil
}
func (c countingObservations) RecordReaped(context.Context, ledger.Observation) error { return nil }
func (c countingObservations) RecordReopened(context.Context, ledger.Reopened) error  { return nil }
func (c countingObservations) RecordDaemonRestart(context.Context, ledger.Observation) error {
	return nil
}

type effectHarness struct {
	root     string
	config   state.Config
	launcher *prototest.Launcher
	manager  *Manager
	log      *effectLog
	hookLog  string
}

type harnessOptions struct {
	globalHook   bool
	waitingDelay time.Duration
}

func newEffectHarness(t *testing.T, options harnessOptions) *effectHarness {
	t.Helper()
	root := t.TempDir()
	h := &effectHarness{
		root: root, config: testConfig(root), launcher: prototest.NewLauncher(),
		log: &effectLog{}, hookLog: filepath.Join(root, "hooks.log"),
	}
	if options.globalHook {
		if err := os.MkdirAll(filepath.Dir(h.config.GlobalHooksPath), 0o700); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(map[string]string{"onIdle": h.hookScript("global")})
		if err := os.WriteFile(h.config.GlobalHooksPath, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.manager = NewManager(h.config, h.launcher, ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		NotifyCooldown: time.Nanosecond, NotifyWaitingDelay: options.waitingDelay,
		Observations: countingObservations{log: h.log},
		Notify:       h.capture,
	})
	t.Cleanup(h.manager.Close)
	return h
}

// hookScript appends one line naming the hook and the outcome it was given.
func (h *effectHarness) hookScript(name string) string {
	return `printf '%s %s %s\n' ` + name + ` "$SESSIONS_OUTCOME" "$SESSIONS_DURATION_MS" >> "` + h.hookLog + `"`
}

func (h *effectHarness) capture(payload PushPayload) {
	id, _ := payload.Data["sessionId"].(string)
	record := capturedPush{payload: payload}
	if session, ok := h.manager.Get(id); ok {
		info := session.Info()
		record.working, record.idleReason = info.Working, info.IdleReason
	}
	record.sentinel = h.sentinelExists(id)
	h.log.mu.Lock()
	h.log.pushes = append(h.log.pushes, record)
	h.log.mu.Unlock()
}

func (h *effectHarness) sentinelExists(id string) bool {
	_, err := os.Stat(filepath.Join(h.manager.idleDir(), id))
	return err == nil
}

func (h *effectHarness) create(t *testing.T, request state.CreateSessionRequest) (*runtimeSession, *state.Session) {
	t.Helper()
	if request.Cwd == "" {
		request.Cwd = h.root
	}
	created, err := h.manager.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	h.manager.mu.Lock()
	runtime := h.manager.runtimes[created.ID]
	h.manager.mu.Unlock()
	session, _ := h.manager.Get(created.ID)
	if runtime == nil || session == nil {
		t.Fatal("created session has no managed runtime")
	}
	return runtime, session
}

func (h *effectHarness) hookLines() []string {
	encoded, err := os.ReadFile(h.hookLog)
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(encoded))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// awaitHookLines waits for want hook lines, then confirms no more arrive.
// Hooks are detached processes, so their count settles after the edge.
func (h *effectHarness) awaitHookLines(t *testing.T, want ...string) {
	t.Helper()
	read := h.hookLines
	awaitCondition(t, func() bool { return len(read()) >= len(want) })
	time.Sleep(50 * time.Millisecond)
	lines := read()
	if len(lines) != len(want) {
		t.Fatalf("hook lines = %q, want %d lines %q", lines, len(want), want)
	}
	for _, expected := range want {
		found := false
		for _, line := range lines {
			if strings.HasPrefix(line, expected) {
				found = true
			}
		}
		if !found {
			t.Fatalf("hook lines = %q, missing %q", lines, expected)
		}
	}
}

func (h *effectHarness) assertQuiet(t *testing.T, pushes, idle int) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	if got := h.log.pushCount(); got != pushes {
		t.Fatalf("pushes = %d, want %d: %#v", got, pushes, h.log.pushes)
	}
	if got := h.log.idleCount(); got != idle {
		t.Fatalf("ledger idle observations = %d, want %d", got, idle)
	}
}

func TestIdleEdgeEffectsAreExactlyOncePerAcceptedTransition(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{globalHook: true})
	runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "/bin/sh", Name: "edge", OnIdle: h.hookScript("lane")})
	id := session.Info().ID

	// The first sample after attach initializes; it is not an edge.
	runtime.setWorking(false)
	h.assertQuiet(t, 0, 0)
	if h.sentinelExists(id) {
		t.Fatal("initial idle sample wrote a sentinel")
	}
	runtime.setWorking(true)
	runtime.setWorking(true)
	h.assertQuiet(t, 0, 0)

	runtime.setWorking(false)
	runtime.setWorking(false)
	h.assertQuiet(t, 1, 1)
	if !h.sentinelExists(id) {
		t.Fatal("accepted idle edge did not write the sentinel")
	}
	push := h.log.pushAt(0)
	if push.payload.Title != "🟢 edge — done" || !push.sentinel || push.working {
		t.Fatalf("idle push = %#v", push)
	}
	h.awaitHookLines(t, "lane done", "global done")

	runtime.setWorking(true)
	if h.sentinelExists(id) {
		t.Fatal("start edge did not remove the idle sentinel")
	}
	runtime.setWorking(false)
	h.assertQuiet(t, 2, 2)
	h.awaitHookLines(t, "lane done", "global done", "lane done", "global done")
	if got := session.Info().IdleReason; got != "" {
		t.Fatalf("shell idle reason = %q, want no lifecycle claim", got)
	}
}

func TestFirstSampleAfterAttachOnlyInitializes(t *testing.T) {
	t.Run("initial working then idle is one edge", func(t *testing.T) {
		h := newEffectHarness(t, harnessOptions{})
		runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "/bin/sh"})
		runtime.setWorking(true)
		h.assertQuiet(t, 0, 0)
		runtime.setWorking(false)
		h.assertQuiet(t, 1, 1)
		if !h.sentinelExists(session.Info().ID) {
			t.Fatal("first completed turn wrote no sentinel")
		}
	})
	// A working state adopted at attach (reconnect HELLO, replayed lifecycle)
	// was set without a runtime sample. The first runtime sample, even one
	// that ends that turn, only initializes. This pins the existing rule.
	t.Run("adopted working then idle only initializes", func(t *testing.T) {
		h := newEffectHarness(t, harnessOptions{})
		runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "/bin/sh"})
		session.SetWorking(true)
		runtime.setWorking(false)
		h.assertQuiet(t, 0, 0)
		if session.Info().Working {
			t.Fatal("the idle sample was not applied to the session")
		}
		runtime.setWorking(true)
		runtime.setWorking(false)
		h.assertQuiet(t, 1, 1)
	})
}

func codexEvent(t *testing.T, value codexapp.Event) json.RawMessage {
	t.Helper()
	encoded, err := codexapp.HistoryEvent(value, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func codexTurn(t *testing.T, runner *prototest.Runner, turn, text, status string) {
	t.Helper()
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnStarted{ConversationID: "conversation-1", TurnID: turn}))
	phase := "final_answer"
	runner.AddCodexEvent(codexEvent(t, codexapp.ItemCompleted{
		ConversationID: "conversation-1", TurnID: turn, CompletedAtMS: time.Now().UnixMilli(),
		Item: codexapp.ThreadItem{ID: "message-" + turn, Type: "agentMessage", Text: text, Phase: &phase},
	}))
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnComplete{ConversationID: "conversation-1", TurnID: turn, Status: status}))
}

func claudeTurn(t *testing.T, runner *prototest.Runner, sessionID, text string) {
	t.Helper()
	started, err := claudep.TurnStartedEvent(sessionID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runner.AddCodexEvent(started)
	for _, raw := range []string{
		`{"type":"assistant","session_id":"` + sessionID + `","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`,
		`{"type":"result","subtype":"success","session_id":"` + sessionID + `","result":"` + text + `"}`,
	} {
		event, err := claudep.NormalizeEvent(json.RawMessage(raw), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		runner.AddCodexEvent(event.Raw)
	}
}

func structuredTurn(t *testing.T, kind string, runner *prototest.Runner, sessionID, turn, text string) {
	t.Helper()
	if kind == state.KindCodexAppServer {
		codexTurn(t, runner, turn, text, "completed")
		return
	}
	claudeTurn(t, runner, sessionID, text)
}

func structuredRequest(kind string) state.CreateSessionRequest {
	if kind == state.KindCodexAppServer {
		return state.CreateSessionRequest{Cmd: "codex", Kind: kind, Name: "rich"}
	}
	return state.CreateSessionRequest{
		Cmd: "claude", Kind: kind, Name: "rich",
		Args: []string{"--session-id", "019f7181-cb32-46e0-952d-2f5f7862e668"},
	}
}

func TestStructuredResultNotifiesOncePerTurn(t *testing.T) {
	for _, kind := range []string{state.KindCodexAppServer, state.KindClaudeStructured} {
		t.Run(kind, func(t *testing.T) {
			h := newEffectHarness(t, harnessOptions{})
			request := structuredRequest(kind)
			request.OnIdle = h.hookScript("lane")
			runtime, session := h.create(t, request)
			runner := h.launcher.Runner(session.Info().ID)
			claudeID := "019f7181-cb32-46e0-952d-2f5f7862e668"

			structuredTurn(t, kind, runner, claudeID, "turn-1", "FirstResult")
			awaitCondition(t, func() bool { return h.log.pushCount() == 1 && !session.Info().Working })
			h.assertQuiet(t, 1, 1)
			if push := h.log.pushAt(0); push.payload.Title != "🟢 rich — done" || push.payload.Body != "FirstResult" {
				t.Fatalf("first result push = %#v", push)
			}
			h.awaitHookLines(t, "lane done")

			// Silence and a repeated idle sample add nothing.
			runtime.tick()
			runtime.setWorking(false)
			h.assertQuiet(t, 1, 1)
			h.awaitHookLines(t, "lane done")

			structuredTurn(t, kind, runner, claudeID, "turn-2", "SecondResult")
			awaitCondition(t, func() bool { return h.log.pushCount() == 2 && h.log.idleCount() == 2 })
			h.assertQuiet(t, 2, 2)
			h.awaitHookLines(t, "lane done", "lane done")
		})
	}
}

func TestStructuredResultWithoutAWorkingEdgeStillNotifiesOnce(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{})
	_, session := h.create(t, structuredRequest(state.KindCodexAppServer))
	runner := h.launcher.Runner(session.Info().ID)
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnComplete{ConversationID: "conversation-1", TurnID: "turn-1", Status: "completed"}))
	awaitCondition(t, func() bool { return h.log.pushCount() == 1 })
	h.assertQuiet(t, 1, 0)
	if h.sentinelExists(session.Info().ID) {
		t.Fatal("a result with no observed working edge wrote a sentinel")
	}
}

func TestStructuredScheduledRetrySuppressesDone(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{})
	_, session := h.create(t, structuredRequest(state.KindCodexAppServer))
	runner := h.launcher.Runner(session.Info().ID)
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnStarted{ConversationID: "conversation-1", TurnID: "turn-1"}))
	awaitCondition(t, func() bool { return session.Info().Working })
	runner.SetRetry(&proto.ProviderRetry{Attempt: 1, Max: 5, NextAt: time.Now().Add(time.Minute).UnixMilli(), Kind: providerfault.KindUnavailable})
	awaitCondition(t, func() bool { return session.Info().Retry != nil })
	runner.AddCodexEvent(map[string]any{
		"type": "system", "subtype": "provider_fault", "provider": "codex",
		"kind": providerfault.KindUnavailable, "detail": "Codex API unavailable (503, overloaded)",
		"timestamp": time.Now().UnixMilli(),
	})
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnComplete{ConversationID: "conversation-1", TurnID: "turn-1", Status: "failed"}))
	awaitCondition(t, func() bool { return h.log.idleCount() == 1 })
	h.assertQuiet(t, 0, 1)
	if got := session.Info(); got.IdleReason != state.IdleReasonFailed || got.FailureKind != providerfault.KindUnavailable {
		t.Fatalf("retrying turn outcome = %q/%q", got.IdleReason, got.FailureKind)
	}
}

func TestIndependentProviderFaultPublishesOnce(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{})
	request := structuredRequest(state.KindCodexAppServer)
	request.OnIdle = h.hookScript("lane")
	_, session := h.create(t, request)
	runner := h.launcher.Runner(session.Info().ID)
	fault := map[string]any{
		"type": "system", "subtype": "provider_fault", "provider": "codex",
		"kind": providerfault.KindAuth, "detail": "Codex is not logged in",
		"timestamp": time.Now().UnixMilli(),
	}
	runner.AddCodexEvent(fault)
	awaitCondition(t, func() bool { return h.log.pushCount() == 1 })
	h.assertQuiet(t, 1, 1)
	push := h.log.pushAt(0)
	if push.payload.Title != "🟠 rich — Codex needs login" || push.idleReason != state.IdleReasonFailed || !push.sentinel {
		t.Fatalf("fault push = %#v", push)
	}
	h.awaitHookLines(t, "lane error 0")
	runner.AddCodexEvent(fault)
	runner.AddCodexEvent(map[string]any{"type": "system", "subtype": "status", "source": codexapp.HistorySource})
	h.assertQuiet(t, 1, 1)
	h.awaitHookLines(t, "lane error 0")
}

func TestStartEdgeCancelsPendingWaitingNotification(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{waitingDelay: 150 * time.Millisecond})
	_, session := h.create(t, structuredRequest(state.KindCodexAppServer))
	runner := h.launcher.Runner(session.Info().ID)
	request := func(id string) json.RawMessage {
		raw, err := codexapp.ApprovalRequestedEvent(id, codexapp.ApprovalRequest{
			Kind: codexapp.ApprovalCommand, ConversationID: "conversation-1", TurnID: "turn-1",
			Command: "make clean", CWD: h.root, Reason: "clean",
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	resolved := func(id string) json.RawMessage {
		raw, err := codexapp.ApprovalResolvedEvent("conversation-1", id, codexapp.ApprovalAllow, "test", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	runner.AddCodexEvent(codexEvent(t, codexapp.TurnStarted{ConversationID: "conversation-1", TurnID: "turn-1"}))
	runner.AddCodexEvent(request("approval-1"))
	runner.AddCodexEvent(resolved("approval-1"))
	awaitCondition(t, func() bool { return h.log.idleCount() == 1 && session.Info().Working })
	time.Sleep(300 * time.Millisecond)
	h.assertQuiet(t, 0, 1)

	runner.AddCodexEvent(request("approval-2"))
	awaitCondition(t, func() bool { return h.log.pushCount() == 1 })
	h.assertQuiet(t, 1, 2)
	if push := h.log.pushAt(0); push.payload.Title != "🟡 rich — needs you" || push.working {
		t.Fatalf("waiting push = %#v", push)
	}
}

func TestReconnectBootstrapPublishesNoEffects(t *testing.T) {
	for _, working := range []bool{false, true} {
		t.Run(map[bool]string{false: "hello idle", true: "hello working"}[working], func(t *testing.T) {
			h := newEffectHarness(t, harnessOptions{globalHook: true})
			id := "00000000-0000-4000-8000-000000000081"
			info := proto.RunnerInfo{
				ID: id, Cmd: "codex", Cwd: h.root, Cols: 120, Rows: 40, PID: 4242,
				ProtocolVersion: proto.ProtocolVersion, Turn: &proto.TurnState{Working: working},
			}
			runner := prototest.NewRunner(info)
			codexTurn(t, runner, "old-turn", "EARLIER", "completed")
			session, err := h.manager.registry.RegisterMetadata(context.Background(), runner, state.RunnerMetadata{
				Info: info, Kind: state.KindCodexAppServer, Name: "adopted",
			}, h.hookScript("lane"))
			if err != nil {
				t.Fatal(err)
			}
			h.manager.manage(session)
			if got := session.Info(); got.Working != working || (!working && got.IdleReason != state.IdleReasonCompleted) {
				t.Fatalf("bootstrapped state = working %v idle %q", got.Working, got.IdleReason)
			}
			h.assertQuiet(t, 0, 0)
			time.Sleep(50 * time.Millisecond)
			if lines := h.hookLines(); len(lines) != 0 || h.sentinelExists(id) {
				t.Fatalf("bootstrap ran hooks %q or wrote a sentinel", lines)
			}
		})
	}
}

func TestShellIdleEdgeKeepsHooksAndNotificationWithoutCompletion(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{})
	runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "/bin/bash", Name: "shell", OnIdle: h.hookScript("lane")})
	runtime.setWorking(true)
	runtime.setWorking(false)
	h.assertQuiet(t, 1, 1)
	h.awaitHookLines(t, "lane done")
	if push := h.log.pushAt(0); push.idleReason != "" || push.payload.Title != "🟢 shell — done" {
		t.Fatalf("shell push = %#v", push)
	}
	if got := session.Info(); got.IdleReason != "" || got.IdleSince != nil {
		t.Fatalf("shell idle = %q since %v, want no completion claim", got.IdleReason, got.IdleSince)
	}
}
