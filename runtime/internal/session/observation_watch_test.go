package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/watch"
)

// startTranscriptSession launches a PTY Claude session whose real transcript
// watcher follows a scratch JSONL, and returns the session and that path.
func startTranscriptSession(t *testing.T) (*Manager, *state.Session, string) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	conversation := "aaaaaaaa-1111-2222-3333-666666666666"
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	config := state.Config{
		DefaultShell: "/bin/bash", DefaultCwd: cwd, DefaultCols: 300, DefaultRows: 50,
		StateRoot: filepath.Join(root, "state"), RunnerStateDir: filepath.Join(root, "state", "runners"),
		UserStateRoot: filepath.Join(root, "user"), LaunchAgentsDir: filepath.Join(root, "agents"),
	}
	manager := NewManager(config, prototest.NewLauncher())
	t.Cleanup(manager.Close)
	created, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "claude", Args: []string{"--session-id", conversation}, Cwd: cwd, Profile: "observe",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(created.ConfigDir, "projects", watch.EncodeClaudeCWD(cwd))
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	session, _ := manager.Get(created.ID)
	return manager, session, filepath.Join(projectDir, conversation+".jsonl")
}

func awaitObservations(t *testing.T, session *state.Session, what string, condition func(*state.Observations) bool) *state.Observations {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if observations := session.Info().Observations; observations != nil && condition(observations) {
			return observations
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %+v", what, session.Info().Observations)
	return nil
}

func transcriptEventAt(observations *state.Observations, at int64) bool {
	return observations.Last != nil && observations.Last.TranscriptRecordEventAt != nil && *observations.Last.TranscriptRecordEventAt == at
}

func observerState(observations *state.Observations, name string) string {
	for _, observer := range observations.Observers {
		if observer.Name == name {
			return observer.State
		}
	}
	return ""
}

// A transcript record the watcher could not read is a gap: a tool whose end
// was in it would otherwise stay open with an exact count. The gap stays
// after later records arrive and the observer is following again.
func TestUnreadableTranscriptRecordMarksToolHistoryIncomplete(t *testing.T) {
	_, session, transcript := startTranscriptSession(t)
	appendRecord(t, transcript, `{"type":"assistant","uuid":"a1","timestamp":"2026-10-16T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash"}]}}`)
	appendRecord(t, transcript, `{"type":"user","uuid":"u1","timestamp":"2026-10-16T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1"`)
	appendRecord(t, transcript, `{"type":"assistant","uuid":"a2","timestamp":"2026-10-16T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`)
	observations := awaitObservations(t, session, "the record after the unreadable one", func(o *state.Observations) bool {
		return transcriptEventAt(o, time.Date(2026, 10, 16, 10, 0, 2, 0, time.UTC).UnixMilli())
	})
	if tools := observations.Tools; tools == nil || tools.OpenCount != 1 || tools.HistoryComplete || tools.OpenCountExact {
		t.Fatalf("a skipped record left tool history complete: %+v", observations.Tools)
	}
	if state := observerState(observations, observerClaudeTranscript); state != "following" {
		t.Fatalf("observer = %q after a later record was read", state)
	}
}

// Losing the bound transcript is reported as soon as it happens, and what was
// missed while it was gone keeps tool history incomplete after the file is
// back and followed again.
func TestLostTranscriptIsReportedAndLeavesAGap(t *testing.T) {
	_, session, transcript := startTranscriptSession(t)
	appendRecord(t, transcript, `{"type":"assistant","uuid":"b1","timestamp":"2026-10-16T11:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`)
	awaitObservations(t, session, "the first record", func(o *state.Observations) bool {
		return observerState(o, observerClaudeTranscript) == "following" && transcriptEventAt(o, time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC).UnixMilli())
	})
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	lost := awaitObservations(t, session, "the lost file to be reported", func(o *state.Observations) bool {
		return observerState(o, observerClaudeTranscript) != "following"
	})
	if lost.Tools == nil || lost.Tools.HistoryComplete {
		t.Fatalf("a lost transcript left tool history complete: %+v", lost.Tools)
	}
	appendRecord(t, transcript, `{"type":"assistant","uuid":"b2","timestamp":"2026-10-16T11:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"back"}]}}`)
	back := awaitObservations(t, session, "the transcript to be followed again", func(o *state.Observations) bool {
		return observerState(o, observerClaudeTranscript) == "following" && transcriptEventAt(o, time.Date(2026, 10, 16, 11, 0, 5, 0, time.UTC).UnixMilli())
	})
	if back.Tools == nil || back.Tools.HistoryComplete {
		t.Fatalf("following again erased the gap: %+v", back.Tools)
	}
}

// An unreadable screen with a little new output -- below the byte threshold
// -- is no evidence the working indicator cleared either.
func TestUnreadableScreenWithLowOutputKeepsPTYClaudeWorking(t *testing.T) {
	root := t.TempDir()
	launcher := prototest.NewLauncher()
	manager := NewManager(testConfig(root), launcher, ManagerOptions{DisableWatchers: true, ActivityInterval: time.Hour, Notify: func(PushPayload) {}})
	defer manager.Close()
	created, err := manager.Create(context.Background(), state.CreateSessionRequest{Cmd: "/usr/local/bin/claude", Cwd: root, ProviderTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.runtimes[created.ID]
	manager.mu.Unlock()
	runtime.tick()
	runner := launcher.Runner(created.ID)
	runner.AddOutput("Reading files\r\n✻ Thinking… · esc to interrupt\r\n" + strings.Repeat(" ", 120))
	<-runtime.outputObserved
	runtime.tick()
	session, _ := manager.Get(created.ID)
	if !session.Info().Working {
		t.Fatal("the working screen was not observed as working")
	}
	runtime.readScreen = func() (string, error) { return "", state.ErrNoTerminalMirror }
	runner.AddOutput(".")
	<-runtime.outputObserved
	runtime.tick()
	info := session.Info()
	if !info.Working || info.IdleReason == state.IdleReasonCompleted {
		t.Fatalf("an unreadable screen with low output published an ending: working=%v idleReason=%q", info.Working, info.IdleReason)
	}
	runtime.mu.Lock()
	done := runtime.turn.terminalDone
	runtime.mu.Unlock()
	if done {
		t.Fatal("an unreadable screen marked the terminal turn done")
	}
}

// Following means the bound file was read, not that a path was found or that
// a record arrived: an empty transcript that reads cleanly is followed.
func TestReadableEmptyTranscriptIsFollowed(t *testing.T) {
	_, session, transcript := startTranscriptSession(t)
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	observations := awaitObservations(t, session, "the empty transcript to be followed", func(o *state.Observations) bool {
		return observerState(o, observerClaudeTranscript) == state.ObserverFollowing
	})
	if observations.Tools == nil || !observations.Tools.HistoryComplete {
		t.Fatalf("a clean read of an empty file marked a gap: %+v", observations.Tools)
	}
}
