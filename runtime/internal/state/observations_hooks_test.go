package state

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/claudehooks"
	"github.com/somewhere-tech/sessions/runtime/internal/providerargs"
)

func hookSession(t *testing.T) (*Session, claudehooks.Launch, string) {
	t.Helper()
	session, _ := observedSession(t, "/usr/local/bin/claude", "")
	launch, ok := session.HookLaunch()
	if !ok {
		t.Fatalf("a managed Claude launch has no hooks: %q", session.Info().Args)
	}
	return session, launch, providerargs.ClaudeSessionID(session.Info().Args)
}

func hookLine(launch, event, conversation string, at int64, extra string) []byte {
	return []byte(fmt.Sprintf(`{"v":1,"launch":%q,"observed_at":%d,"event":%q,"session_id":%q%s}`, launch, at, event, conversation, extra))
}

func hooksOf(t *testing.T, session *Session) *ObservedHooks {
	t.Helper()
	hooks := session.Info().Observations.Hooks
	if hooks == nil {
		t.Fatal("no hooks block for an interactive Claude session")
	}
	return hooks
}

// Before any record, coverage is "configured", which is not evidence the
// hooks run; the capability names the source.
func TestHooksConfiguredIsNotReceiving(t *testing.T) {
	session, _, _ := hookSession(t)
	observations := session.Info().Observations
	if observations.Capabilities.Hooks != claudehooks.Source || observations.Hooks.Coverage != HookCoverageConfigured {
		encoded, _ := json.Marshal(observations)
		t.Fatalf("unexpected hooks view: %s", encoded)
	}
	if observations.Hooks.LastEvent != nil || observations.Hooks.Tools.OpenCount != 0 {
		t.Fatalf("facts without records: %+v", observations.Hooks)
	}
}

// Records from another launch, another conversation, or that fail validation
// are counted and change nothing.
func TestHooksRejectForeignAndMalformedRecords(t *testing.T) {
	session, launch, conversation := hookSession(t)
	session.ObserveHookLine(hookLine(launch.Launch, "UserPromptSubmit", conversation, 100, ""))
	session.ObserveHookLine(hookLine("ffffffffffffffffffffffffffffffff", "Stop", conversation, 200, ""))
	session.ObserveHookLine(hookLine(launch.Launch, "Stop", "another-conversation", 300, ""))
	session.ObserveHookLine([]byte(`{"v":1,"launch":"` + launch.Launch + `","observed_at":400,"event":"Stop","prompt":`))
	hooks := hooksOf(t, session)
	if hooks.Coverage != HookCoverageReceiving || hooks.LastEvent.Name != "UserPromptSubmit" || hooks.LastEvent.ObservedAt != 100 {
		t.Fatalf("a rejected record changed the view: %+v", hooks.LastEvent)
	}
	if hooks.Rejected != (ObservedHookRejections{ForeignLaunch: 1, ForeignConversation: 1, Malformed: 1}) {
		t.Fatalf("rejections = %+v", hooks.Rejected)
	}
	if len(hooks.Gaps) != 1 || hooks.Gaps[0] != claudehooks.GapMalformedLine || hooks.Tools.HistoryComplete {
		t.Fatalf("malformed record not reported as a gap: %+v", hooks)
	}
}

// Only Claude's own SessionStart from this launch moves the conversation, as
// /clear does; afterwards the old conversation's records are foreign.
func TestHooksSessionStartRebindsConversation(t *testing.T) {
	session, launch, conversation := hookSession(t)
	session.ObserveHookLine(hookLine(launch.Launch, "SessionStart", conversation, 100, `,"detail":"startup"`))
	session.ObserveHookLine(hookLine(launch.Launch, "SessionStart", "cleared-conversation", 200, `,"detail":"clear"`))
	session.ObserveHookLine(hookLine(launch.Launch, "Stop", conversation, 300, ""))
	hooks := hooksOf(t, session)
	if *hooks.ConversationID != "cleared-conversation" || hooks.Events["SessionStart"].Detail == nil || *hooks.Events["SessionStart"].Detail != "clear" {
		t.Fatalf("conversation not rebound: %+v", hooks)
	}
	if _, stopped := hooks.Events["Stop"]; stopped || hooks.Rejected.ForeignConversation != 1 {
		t.Fatalf("the old conversation's Stop was applied: %+v", hooks)
	}
}

// Concurrent tool hooks append in any order. An end read before its start
// closes the call for good, the newest end decides the last status, and
// reading the same records twice changes nothing.
func TestHooksToolEventsInAnyOrderAndDuplicates(t *testing.T) {
	session, launch, conversation := hookSession(t)
	lines := [][]byte{
		hookLine(launch.Launch, "PostToolUse", conversation, 210, `,"tool_use_id":"toolu_a","tool_name":"Bash"`),
		hookLine(launch.Launch, "PreToolUse", conversation, 200, `,"tool_use_id":"toolu_a","tool_name":"Bash"`),
		hookLine(launch.Launch, "PreToolUse", conversation, 205, `,"tool_use_id":"toolu_b","tool_name":"Read"`),
		hookLine(launch.Launch, "PreToolUse", conversation, 206, `,"tool_use_id":"toolu_c","tool_name":"Edit"`),
		hookLine(launch.Launch, "PostToolUseFailure", conversation, 230, `,"tool_use_id":"toolu_c","is_interrupt":true`),
		hookLine(launch.Launch, "PostToolUseFailure", conversation, 220, `,"tool_use_id":"toolu_d"`),
	}
	for _, line := range lines {
		session.ObserveHookLine(line)
	}
	first := hooksOf(t, session)
	for _, line := range lines {
		session.ObserveHookLine(line)
	}
	again := hooksOf(t, session)
	if !reflect.DeepEqual(first, again) {
		t.Fatalf("duplicate records changed the view:\n%+v\n%+v", first, again)
	}
	tools := again.Tools
	if tools.OpenCount != 1 || tools.Open[0].ID != "toolu_b" || *tools.Open[0].Name != "Read" {
		t.Fatalf("open tools = %+v", tools.Open)
	}
	if *tools.LastEndStatus != ToolEndCancelled || *tools.LastEndedAt != 230 || *tools.LastStartedAt != 206 {
		t.Fatalf("last end = %v at %v, last start %v", *tools.LastEndStatus, *tools.LastEndedAt, *tools.LastStartedAt)
	}
	if again.LastEvent.Name != "PostToolUseFailure" || again.LastEvent.ObservedAt != 230 {
		t.Fatalf("last event = %+v", again.LastEvent)
	}
}

// Stop, StopFailure and Notification are recorded as what they are. None of
// them ends the transcript turn, moves the working flag, or creates a
// brokered approval: they are the provider's report, not lifecycle authority.
func TestHooksStopAndNotificationAreObservationsOnly(t *testing.T) {
	session, launch, conversation := hookSession(t)
	session.SetWorking(true)
	before := session.Info().Observations
	session.ObserveHookLine(hookLine(launch.Launch, "Notification", conversation, 100, `,"detail":"permission_prompt"`))
	session.ObserveHookLine(hookLine(launch.Launch, "Stop", conversation, 200, `,"stop_hook_active":true`))
	session.ObserveHookLine(hookLine(launch.Launch, "StopFailure", conversation, 300, `,"detail":"rate_limit"`))
	info := session.Info()
	after := info.Observations
	if !info.Working || !reflect.DeepEqual(before.Turn, after.Turn) || after.Approval != nil || info.PendingApproval != nil {
		t.Fatalf("a hook acted on lifecycle: working=%v turn=%+v approval=%+v", info.Working, after.Turn, after.Approval)
	}
	stop := after.Hooks.Events["Stop"]
	if stop.StopHookActive == nil || !*stop.StopHookActive || *after.Hooks.Events["StopFailure"].Detail != "rate_limit" || *after.Hooks.Events["Notification"].Detail != "permission_prompt" {
		t.Fatalf("events = %+v", after.Hooks.Events)
	}
}

// Transcript records arriving after a hook fact never change the hook block,
// and an older hook record never replaces a newer one of the same name.
func TestHooksAreNotOverriddenByTranscriptOrOlderRecords(t *testing.T) {
	session, launch, conversation := hookSession(t)
	session.ObserveHookLine(hookLine(launch.Launch, "Stop", conversation, 500, ""))
	before := hooksOf(t, session)
	session.mu.Lock()
	session.observed.observeRecord(map[string]any{"type": "user", "timestamp": "2026-10-22T00:00:00Z",
		"message": map[string]any{"role": "user", "content": "hi"}}, fromWatcher, 900)
	session.mu.Unlock()
	session.ObserveHookLine(hookLine(launch.Launch, "Stop", conversation, 400, `,"stop_hook_active":true`))
	after := hooksOf(t, session)
	if !reflect.DeepEqual(before.Events, after.Events) || after.LastEvent.ObservedAt != 500 {
		t.Fatalf("hook facts overridden:\nbefore %+v\nafter  %+v", before.Events, after.Events)
	}
}

// A daemon that reads the same sink again from the start rebuilds the same
// hook facts.
func TestHooksReplayRebuildsTheSameFacts(t *testing.T) {
	session, launch, conversation := hookSession(t)
	lines := [][]byte{
		hookLine(launch.Launch, "SessionStart", conversation, 100, `,"detail":"startup"`),
		hookLine(launch.Launch, "UserPromptSubmit", conversation, 110, ""),
		hookLine(launch.Launch, "PreToolUse", conversation, 120, `,"tool_use_id":"toolu_1","tool_name":"Bash"`),
		hookLine(launch.Launch, "Stop", conversation, 130, ""),
	}
	for _, line := range lines {
		session.ObserveHookLine(line)
	}
	replayed := &Session{info: session.Info()}
	replayed.observed.hooks = resolveHooks(replayed.info)
	for _, line := range lines {
		replayed.ObserveHookLine(line)
	}
	if !reflect.DeepEqual(hooksOf(t, session), replayed.observed.hooks.project()) {
		t.Fatal("replaying the sink produced different facts")
	}
}

// A launch without hooks (an older runner) says so; other runtimes have no
// hooks block at all.
func TestHooksUnavailableForOlderLaunchesAndOtherRuntimes(t *testing.T) {
	old := resolveHooks(SessionInfo{Tool: ToolClaude, Cmd: "claude", Args: []string{"--session-id", "x", "--settings", `{"remoteControlAtStartup":false}`}})
	view := old.project()
	if view.Coverage != HookCoverageUnavailable || *view.UnavailableReason != claudehooks.SkipNotInstalled || view.Tools != nil {
		t.Fatalf("older launch view = %+v", view)
	}
	old.observeLine(hookLine("0123456789abcdef0123456789abcdef", "Stop", "x", 1, ""))
	if old.project().LastEvent != nil {
		t.Fatal("a launch without hooks applied a record")
	}
	for _, info := range []SessionInfo{{Tool: ToolCodex}, {Tool: ToolClaude, Kind: KindClaudeStructured}, {Tool: ToolTerminal}} {
		if tracker := resolveHooks(info); tracker.project() != nil {
			t.Fatalf("%+v has a hooks block", info)
		}
	}
	if RestoredObservations(SessionInfo{Tool: ToolClaude}).Hooks != nil {
		t.Fatal("a restored record claims hook facts")
	}
}
