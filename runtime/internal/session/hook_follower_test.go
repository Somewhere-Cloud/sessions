package session

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudehooks"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func captureInto(t *testing.T, launch claudehooks.Launch, input string) {
	t.Helper()
	record := claudehooks.Capture(strings.NewReader(input), launch.Launch, time.Now())
	if err := claudehooks.Append(launch.Sink, record); err != nil {
		t.Fatal(err)
	}
}

// A managed Claude launch's hook records, written by the real capture code,
// reach the session's observations through the running manager.
func TestManagerFollowsClaudeHookSink(t *testing.T) {
	_, session, _ := startTranscriptSession(t)
	launch, ok := session.HookLaunch()
	if !ok {
		t.Fatalf("managed launch without hooks: %q", session.Info().Args)
	}
	conversation := "aaaaaaaa-1111-2222-3333-666666666666"
	captureInto(t, launch, `{"session_id":"`+conversation+`","hook_event_name":"SessionStart","source":"startup"}`)
	captureInto(t, launch, `{"session_id":"`+conversation+`","hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"ls"}}`)
	observations := awaitObservations(t, session, "hook tool start", func(o *state.Observations) bool {
		return o.Hooks != nil && o.Hooks.Tools != nil && o.Hooks.Tools.OpenCount == 1
	})
	if observations.Hooks.Coverage != state.HookCoverageReceiving || observations.Capabilities.Hooks != claudehooks.Source {
		t.Fatalf("hooks view = %+v", observations.Hooks)
	}
	captureInto(t, launch, `{"session_id":"`+conversation+`","hook_event_name":"PostToolUse","tool_use_id":"toolu_1","tool_response":"ok"}`)
	awaitObservations(t, session, "hook tool end", func(o *state.Observations) bool {
		return o.Hooks.Tools.OpenCount == 0 && o.Hooks.Tools.LastEndStatus != nil
	})
}

func followerFor(t *testing.T) (*hookFollower, *state.Session, claudehooks.Launch) {
	t.Helper()
	root := t.TempDir()
	config := state.Config{
		DefaultShell: "/bin/bash", DefaultCwd: root, DefaultCols: 80, DefaultRows: 24,
		StateRoot: filepath.Join(root, "state"), RunnerStateDir: filepath.Join(root, "state", "runners"),
		UserStateRoot: filepath.Join(root, "user"), LaunchAgentsDir: filepath.Join(root, "agents"),
	}
	manager := NewManager(config, prototest.NewLauncher(), ManagerOptions{DisableWatchers: true})
	t.Cleanup(manager.Close)
	created, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "claude", Args: []string{"--session-id", "aaaaaaaa-1111-2222-3333-666666666666"}, Cwd: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := manager.Get(created.ID)
	launch, ok := session.HookLaunch()
	if !ok {
		t.Fatalf("managed launch without hooks: %q", session.Info().Args)
	}
	return newHookFollower(session), session, launch
}

func validLine(launch claudehooks.Launch, event string, at int64) string {
	return `{"v":1,"launch":"` + launch.Launch + `","observed_at":` + strconv.FormatInt(at, 10) + `,"event":"` + event + `","session_id":"aaaaaaaa-1111-2222-3333-666666666666"}`
}

// A record split across polls is applied once it is whole; an overlong line
// is discarded through its newline and reported; a sink that shrinks keeps
// what was read and says the history is no longer whole.
func TestHookFollowerPartialOversizedAndShrunkSinks(t *testing.T) {
	follower, session, launch := followerFor(t)
	follower.poll() // no sink yet: nothing happens
	line := validLine(launch, "Stop", 100) + "\n"
	writeSink(t, launch.Sink, line[:20])
	follower.poll()
	if session.Info().Observations.Hooks.LastEvent != nil {
		t.Fatal("a partial record was applied")
	}
	appendSink(t, launch.Sink, line[20:])
	appendSink(t, launch.Sink, strings.Repeat("x", claudehooks.MaxRecordBytes+10)+"\n"+validLine(launch, "Notification", 200)+"\n")
	follower.poll()
	hooks := session.Info().Observations.Hooks
	if hooks.Events["Stop"].ObservedAt != 100 || hooks.Events["Notification"].ObservedAt != 200 {
		t.Fatalf("events = %+v", hooks.Events)
	}
	if len(hooks.Gaps) != 1 || hooks.Gaps[0] != claudehooks.GapMalformedLine {
		t.Fatalf("overlong line not reported: %v", hooks.Gaps)
	}
	writeSink(t, launch.Sink, validLine(launch, "UserPromptSubmit", 300)+"\n")
	follower.poll()
	hooks = session.Info().Observations.Hooks
	if hooks.Events["Stop"].ObservedAt != 100 || hooks.Events["UserPromptSubmit"].ObservedAt != 300 || !contains(hooks.Gaps, claudehooks.GapSinkUnreadable) {
		t.Fatalf("after shrink: events %+v gaps %v", hooks.Events, hooks.Gaps)
	}
}

// A full sink means later hook events were dropped by the capture; the view
// says so instead of implying the last event is the newest.
func TestHookFollowerReportsFullSink(t *testing.T) {
	follower, session, launch := followerFor(t)
	writeSink(t, launch.Sink, validLine(launch, "Stop", 100)+"\n"+strings.Repeat("\n", claudehooks.MaxSinkBytes))
	follower.poll()
	hooks := session.Info().Observations.Hooks
	if !contains(hooks.Gaps, claudehooks.GapSinkFull) || hooks.Tools.HistoryComplete {
		t.Fatalf("full sink not reported: %+v", hooks)
	}
}

func writeSink(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendSink(t *testing.T, path, data string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(data); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
